package sipsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
	"unsafe"

	"github.com/emiago/diago"
	diagoaudio "github.com/emiago/diago/audio"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/CDimonaco/sirius/internal/audio"
)

// RingTimeout is how long Sirius lets the phone ring before giving up.
const RingTimeout = 30 * time.Second

// Server is the SIP endpoint: it listens on the network, answers the phone's REGISTER,
// and places calls to whatever phone is registered.
type Server struct {
	Registrar *Registrar

	dg   *diago.Diago
	host string
	port int
}

// NewServer builds the endpoint. host must be an address the phone can actually reach,
// so not 127.0.0.1, because the same address is put in the SDP as the media address.
func NewServer(account Account, host string, port int) (*Server, error) {
	ua, err := sipgo.NewUA()
	if err != nil {
		return nil, fmt.Errorf("new user agent: %w", err)
	}

	reg := NewRegistrar(account, time.Now)
	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	// diago registers handlers for INVITE, BYE and the rest. REGISTER is ours, and the
	// two coexist because they are different methods.
	srv.OnRegister(reg.Handle)

	dg := diago.NewDiago(ua,
		diago.WithServer(srv),
		diago.WithTransport(diago.Transport{Transport: "udp", BindHost: host, BindPort: port}),
		// PCMU only. Every phone speaks it, it is the only realistic codec behind an
		// analog adapter, and diago refuses a call it cannot negotiate down to this.
		diago.WithMediaConfig(diago.MediaConfig{Codecs: []media.Codec{media.CodecAudioUlaw}}),
	)
	return &Server{Registrar: reg, dg: dg, host: host, port: port}, nil
}

// Serve starts listening and returns. Incoming calls are declined by doing nothing with
// them: this iteration only ever calls the phone, never the other way round.
func (s *Server) Serve(ctx context.Context) error {
	return s.dg.ServeBackground(ctx, func(*diago.DialogServerSession) {})
}

func (s *Server) Address() string { return fmt.Sprintf("%s:%d", s.host, s.port) }

// Ring calls the registered phone. callerID becomes the display name the phone shows,
// which is the whole trick behind saying which meeting is ringing: it travels in the
// From header, so no platform has to cooperate.
//
// jitterPackets is how many packets of playout delay to hold, which is also the window
// the buffer has to put reordered packets back in sequence. Zero turns the buffer off
// and hands packets over in arrival order.
func (s *Server) Ring(ctx context.Context, callerID string, jitterPackets int) (*Call, error) {
	contact, ok := s.Registrar.Contact()
	if !ok {
		return nil, errors.New("no phone is registered")
	}

	ctx, cancel := context.WithTimeout(ctx, RingTimeout)
	defer cancel()

	// The last response code tells declined apart from never answered.
	var lastStatus int
	dialog, med, err := s.dg.Invite(ctx, contact, diago.InviteOptions{
		Headers: []sip.Header{&sip.FromHeader{
			DisplayName: callerID,
			Address:     sip.Uri{Scheme: "sip", User: "sirius", Host: s.host, Port: s.port},
			Params:      sip.NewParams(),
		}},
		OnResponse: func(res *sip.Response) error {
			lastStatus = res.StatusCode
			return nil
		},
	})
	if err != nil {
		return nil, inviteError(err, lastStatus)
	}

	props := diago.MediaProps{}
	readerOpts := []diago.AudioReaderOption{diago.WithAudioReaderMediaProps(&props)}
	if jitterPackets > 0 {
		// Wifi delivers packets out of order, and PCM samples carry no sequence
		// number, so by the time audio reaches the bridge it is too late to put it
		// back in order. This buffer does it here, where the numbers still exist.
		readerOpts = append(readerOpts, diago.WithAudioReaderJitterBuffer(media.RTPJitterBufferOptions{
			DelayPackets: jitterPackets,
			MaxPackets:   jitterPackets * 4,
		}))
	}
	payload, err := med.AudioReader(readerOpts...)
	if err != nil {
		return nil, fmt.Errorf("audio reader: %w", err)
	}
	decoder, err := diagoaudio.NewPCMDecoderReader(props.Codec.PayloadType, payload)
	if err != nil {
		return nil, fmt.Errorf("decoder for payload type %d: %w", props.Codec.PayloadType, err)
	}
	out, err := med.AudioWriter()
	if err != nil {
		return nil, fmt.Errorf("audio writer: %w", err)
	}
	encoder, err := diagoaudio.NewPCMEncoderWriter(props.Codec.PayloadType, out)
	if err != nil {
		return nil, fmt.Errorf("encoder for payload type %d: %w", props.Codec.PayloadType, err)
	}

	return &Call{
		dialog:  dialog,
		med:     med,
		decoder: decoder,
		encoder: encoder,
		Codec:   props.Codec.Name,
		in:      make([]byte, media.RTPBufSize),
		out:     make([]byte, audio.Samples(20)*2),
	}, nil
}

// Call is one answered call to the phone. Its audio is 8 kHz mono, one RTP packet per
// 20 milliseconds in each direction.
type Call struct {
	Codec string

	dialog  *diago.DialogClientSession
	med     *diago.DialogMedia
	decoder io.Reader
	encoder io.Writer

	in  []byte
	out []byte
}

// ReadSamples waits for the next packet from the phone and decodes it.
//
// marker is the RTP marker bit, which a phone sets on the first packet after a pause.
// That is how silence suppression announces itself: the phone stops sending while
// nobody is talking, then marks the packet that starts the next talkspurt. Without it,
// a pause and a network stall look identical from here.
//
// The returned slice is reused on the next call.
func (c *Call) ReadSamples() (pcm []int16, marker bool, err error) {
	n, err := c.decoder.Read(c.in)
	if err != nil {
		return nil, false, err
	}
	// Safe to read in this goroutine, which is the one that just called Read.
	return bytesToSamples(c.in[:n]), c.med.RTPPacketReader.PacketHeader.Marker, nil
}

// WriteSamples sends pcm to the phone. Hand it 20 milliseconds at a time.
func (c *Call) WriteSamples(pcm []int16) error {
	need := len(pcm) * 2
	if cap(c.out) < need {
		c.out = make([]byte, need)
	}
	buf := c.out[:need]
	copy(buf, samplesToBytes(pcm))
	_, err := c.encoder.Write(buf)
	return err
}

// Hangup ends the call. It is safe to call after the phone has already hung up.
func (c *Call) Hangup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// The BYE is the part worth reporting. Closing the media session and the dialog
	// cannot fail in a way the caller could act on.
	err := c.dialog.Hangup(ctx)
	_ = c.med.Close()
	_ = c.dialog.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Done closes when the call ends for any reason, including a BYE from the phone.
func (c *Call) Done() <-chan struct{} { return c.dialog.Context().Done() }

func bytesToSamples(b []byte) []int16 {
	if len(b) < 2 {
		return nil
	}
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}

func samplesToBytes(s []int16) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2)
}

// inviteError says why the phone is not on the line. The three cases look the same to
// diago and mean different things to a person: the phone refused, nobody picked up, or
// the call never got that far.
func inviteError(err error, lastStatus int) error {
	switch {
	case lastStatus >= 400:
		return fmt.Errorf("phone declined with %d: %w", lastStatus, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("phone did not answer within %s: %w", RingTimeout, err)
	default:
		return fmt.Errorf("invite: %w", err)
	}
}
