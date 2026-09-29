// Command spike bridges a SIP call to two host audio devices and measures how far
// the host audio clock drifts from the 20 ms RTP cadence. It is throwaway code: no
// tray, no registrar, no tests, no abstraction worth keeping.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/gen2brain/malgo"
)

const (
	sampleRate = 8000
	channels   = 1

	// One RTP packet carries 20 ms of narrowband audio.
	samplesPerPacket = sampleRate / 50

	// Host callbacks run at 10 ms so the ring always has a partial packet ready.
	framesPerCallback = sampleRate / 100

	// Two seconds of slack. Large enough that a healthy run never touches the ends,
	// so any overrun or underrun means the clocks actually diverged.
	ringSamples = sampleRate * 2
)

func main() {
	var (
		listDevices = flag.Bool("devices", false, "list audio devices and exit")
		callURI     = flag.String("call", "sip:bridge@127.0.0.1:5080", "SIP URI to ring")
		callerID    = flag.String("caller-id", "Standup · Meet", "From display name")
		captureName = flag.String("capture", "BlackHole 2ch", "capture device, substring match")
		playbackNam = flag.String("playback", "BlackHole 16ch", "playback device, substring match")
		bindPort    = flag.Int("port", 15060, "local SIP port")
		answerPort  = flag.Int("answer", 0, "also run an in-process echo answerer on this port")
		duration    = flag.Duration("dur", 10*time.Minute, "how long to run")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	mctx, err := malgo.InitContext([]malgo.Backend{malgo.BackendCoreaudio}, malgo.ContextConfig{}, nil)
	if err != nil {
		log.Fatalf("init audio context: %v", err)
	}
	defer func() {
		_ = mctx.Uninit()
		mctx.Free()
	}()

	if *listDevices {
		printDevices(mctx.Context)
		return
	}

	if err := run(ctx, mctx.Context, opts{
		callURI:      *callURI,
		callerID:     *callerID,
		captureName:  *captureName,
		playbackName: *playbackNam,
		bindPort:     *bindPort,
		answerPort:   *answerPort,
		duration:     *duration,
	}); err != nil {
		log.Fatal(err)
	}
}

type opts struct {
	callURI      string
	callerID     string
	captureName  string
	playbackName string
	bindPort     int
	answerPort   int
	duration     time.Duration
}

// counters is the whole point of the spike. Every number is in samples.
type counters struct {
	hostCaptured atomic.Int64 // read out of the capture device
	sentToRTP    atomic.Int64 // handed to the encoder on the 20 ms tick
	recvFromRTP  atomic.Int64 // decoded out of the dialog
	hostPlayed   atomic.Int64 // written into the playback device
}

func run(ctx context.Context, mctx malgo.Context, o opts) error {
	captureDev, err := findDevice(mctx, malgo.Capture, o.captureName)
	if err != nil {
		return err
	}
	playbackDev, err := findDevice(mctx, malgo.Playback, o.playbackName)
	if err != nil {
		return err
	}
	log.Printf("capture %q, playback %q", captureDev.Name(), playbackDev.Name())

	var c counters
	fromHost := newRing(ringSamples) // meeting audio on its way to the phone
	toHost := newRing(ringSamples)   // phone audio on its way to the meeting

	capture, err := openCapture(mctx, captureDev, fromHost, &c)
	if err != nil {
		return fmt.Errorf("open capture: %w", err)
	}
	defer capture.Uninit()

	playback, err := openPlayback(mctx, playbackDev, toHost, &c)
	if err != nil {
		return fmt.Errorf("open playback: %w", err)
	}
	defer playback.Uninit()

	// An in-process answerer keeps the loop self-contained when no softphone is
	// installed. The drift being measured is the host clock against the 20 ms tick,
	// which does not care who is on the other end of the RTP.
	if o.answerPort > 0 {
		if err := serveEcho(ctx, o.answerPort); err != nil {
			return fmt.Errorf("start echo answerer: %w", err)
		}
		log.Printf("echo answerer listening on port %d", o.answerPort)
	}

	// The call comes second. If TCC blocks microphone access, the audio devices fail
	// first and we learn that without waiting for a phone to answer.
	uri := sip.Uri{}
	if err := sip.ParseUri(o.callURI, &uri); err != nil {
		return fmt.Errorf("parse %q: %w", o.callURI, err)
	}

	ua, err := sipgo.NewUA()
	if err != nil {
		return fmt.Errorf("new user agent: %w", err)
	}
	dg := diago.NewDiago(ua,
		diago.WithTransport(diago.Transport{
			Transport: "udp",
			BindHost:  "127.0.0.1",
			BindPort:  o.bindPort,
		}),
		diago.WithMediaConfig(diago.MediaConfig{
			Codecs: []media.Codec{media.CodecAudioUlaw},
		}),
	)

	// Passing a From header here is the open question: sipgo builds its own, so a
	// second one may make the request invalid rather than setting caller ID.
	headers := []sip.Header{}
	if o.callerID != "" {
		headers = append(headers, &sip.FromHeader{
			DisplayName: o.callerID,
			Address:     sip.Uri{User: "sirius", Host: "127.0.0.1", Port: o.bindPort},
		})
	}
	log.Printf("ringing %s with caller id %q", uri.String(), o.callerID)

	dialog, med, err := dg.Invite(ctx, uri, diago.InviteOptions{
		Headers: headers,
	})
	if err != nil {
		return fmt.Errorf("invite: %w", err)
	}
	defer dialog.Close()
	defer med.Close()

	props := diago.MediaProps{}
	rtpIn, err := med.AudioReader(diago.WithAudioReaderMediaProps(&props))
	if err != nil {
		return fmt.Errorf("audio reader: %w", err)
	}
	rtpOut, err := med.AudioWriter()
	if err != nil {
		return fmt.Errorf("audio writer: %w", err)
	}
	log.Printf("answered, codec %s payload type %d", props.Codec.Name, props.Codec.PayloadType)

	decoder, err := audio.NewPCMDecoderReader(props.Codec.PayloadType, rtpIn)
	if err != nil {
		return fmt.Errorf("decoder: %w", err)
	}
	encoder, err := audio.NewPCMEncoderWriter(props.Codec.PayloadType, rtpOut)
	if err != nil {
		return fmt.Errorf("encoder: %w", err)
	}

	if err := capture.Start(); err != nil {
		return fmt.Errorf("start capture: %w", err)
	}
	if err := playback.Start(); err != nil {
		return fmt.Errorf("start playback: %w", err)
	}

	runCtx, stop := context.WithTimeout(ctx, o.duration)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pumpToRTP(runCtx, fromHost, encoder, &c) }()
	go func() { defer wg.Done(); pumpFromRTP(runCtx, decoder, toHost, &c) }()

	report(runCtx, &c, fromHost, toHost)
	wg.Wait()

	hangupCtx, cancelHangup := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelHangup()
	return dialog.Hangup(hangupCtx)
}

// pumpToRTP is the clock the host audio has to keep up with: one packet every 20 ms,
// whatever the capture device produced in the meantime.
func pumpToRTP(ctx context.Context, r *ring, w io.Writer, c *counters) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	pcm := make([]int16, samplesPerPacket)
	raw := make([]byte, samplesPerPacket*2)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.read(pcm)
			int16sToBytes(pcm, raw)
			if _, err := w.Write(raw); err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("write rtp: %v", err)
				}
				return
			}
			c.sentToRTP.Add(int64(len(pcm)))
		}
	}
}

func pumpFromRTP(ctx context.Context, r io.Reader, out *ring, c *counters) {
	raw := make([]byte, media.RTPBufSize)
	for ctx.Err() == nil {
		n, err := r.Read(raw)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("read rtp: %v", err)
			}
			return
		}
		pcm := bytesToInt16s(raw[:n])
		out.write(pcm)
		c.recvFromRTP.Add(int64(len(pcm)))
	}
}

func report(ctx context.Context, c *counters, fromHost, toHost *ring) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			printReport(time.Since(start), c, fromHost, toHost)
			return
		case <-ticker.C:
			printReport(time.Since(start), c, fromHost, toHost)
		}
	}
}

func printReport(elapsed time.Duration, c *counters, fromHost, toHost *ring) {
	mins := elapsed.Minutes()
	captured, sent := c.hostCaptured.Load(), c.sentToRTP.Load()
	recv, played := c.recvFromRTP.Load(), c.hostPlayed.Load()

	outDrift, inDrift := float64(captured-sent), float64(recv-played)
	if mins > 0 {
		outDrift /= mins
		inDrift /= mins
	}
	fo, uo := fromHost.stats()
	fi, ui := toHost.stats()

	log.Printf("t=%4.0fs host->rtp captured=%d sent=%d drift=%+.0f smp/min (over=%d under=%d) | rtp->host recv=%d played=%d drift=%+.0f smp/min (over=%d under=%d)",
		elapsed.Seconds(), captured, sent, outDrift, fo, uo, recv, played, inDrift, fi, ui)
}

func openCapture(mctx malgo.Context, dev malgo.DeviceInfo, out *ring, c *counters) (*malgo.Device, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = framesPerCallback
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = channels
	cfg.Capture.DeviceID = dev.ID.Pointer()

	return malgo.InitDevice(mctx, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, frames uint32) {
			pcm := bytesToInt16s(in[:int(frames)*2])
			out.write(pcm)
			c.hostCaptured.Add(int64(len(pcm)))
		},
	})
}

func openPlayback(mctx malgo.Context, dev malgo.DeviceInfo, in *ring, c *counters) (*malgo.Device, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = framesPerCallback
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = channels
	cfg.Playback.DeviceID = dev.ID.Pointer()

	scratch := make([]int16, framesPerCallback)
	return malgo.InitDevice(mctx, cfg, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, frames uint32) {
			n := int(frames)
			if n > len(scratch) {
				n = len(scratch)
			}
			in.read(scratch[:n])
			int16sToBytes(scratch[:n], out[:n*2])
			c.hostPlayed.Add(int64(n))
		},
	})
}

// serveEcho answers anything that rings it and sends the RTP straight back.
func serveEcho(ctx context.Context, port int) error {
	ua, err := sipgo.NewUA()
	if err != nil {
		return err
	}
	dg := diago.NewDiago(ua,
		diago.WithTransport(diago.Transport{
			Transport: "udp",
			BindHost:  "127.0.0.1",
			BindPort:  port,
		}),
		diago.WithMediaConfig(diago.MediaConfig{
			Codecs: []media.Codec{media.CodecAudioUlaw},
		}),
	)

	return dg.ServeBackground(ctx, func(in *diago.DialogServerSession) {
		med, err := in.Answer(diago.AnswerOptions{})
		if err != nil {
			log.Printf("echo answer: %v", err)
			return
		}
		r, _ := med.AudioReader()
		w, _ := med.AudioWriter()
		if _, err := media.Copy(r, w); err != nil && !errors.Is(err, io.EOF) {
			log.Printf("echo copy: %v", err)
		}
	})
}

func findDevice(mctx malgo.Context, kind malgo.DeviceType, name string) (malgo.DeviceInfo, error) {
	devices, err := mctx.Devices(kind)
	if err != nil {
		return malgo.DeviceInfo{}, fmt.Errorf("enumerate devices: %w", err)
	}
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name()), strings.ToLower(name)) {
			return d, nil
		}
	}
	return malgo.DeviceInfo{}, fmt.Errorf("no %v device matching %q", kind, name)
}

func printDevices(mctx malgo.Context) {
	for _, kind := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
		devices, err := mctx.Devices(kind)
		if err != nil {
			log.Printf("enumerate %v: %v", kind, err)
			continue
		}
		fmt.Printf("%v:\n", kind)
		for _, d := range devices {
			info, err := mctx.DeviceInfo(kind, d.ID, malgo.Shared)
			rates := "unknown native format"
			if err == nil && len(info.Formats) > 0 {
				f := info.Formats[0]
				rates = fmt.Sprintf("native %d Hz, %d ch, %v", f.SampleRate, f.Channels, f.Format)
			}
			fmt.Printf("  %-32s %s\n", d.Name(), rates)
		}
	}
}

// ring is a fixed-size sample buffer between a real-time audio callback and a
// goroutine. Overruns and underruns are the measurement, so both are counted
// instead of being smoothed away.
type ring struct {
	mu       sync.Mutex
	buf      []int16
	start    int
	length   int
	overrun  atomic.Int64
	underrun atomic.Int64
}

func newRing(n int) *ring {
	return &ring{buf: make([]int16, n)}
}

func (r *ring) write(p []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range p {
		if r.length == len(r.buf) {
			r.overrun.Add(int64(len(p)))
			return
		}
		r.buf[(r.start+r.length)%len(r.buf)] = s
		r.length++
	}
}

// read fills p, padding with silence when the producer has fallen behind.
func (r *ring) read(p []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for ; n < len(p) && r.length > 0; n++ {
		p[n] = r.buf[r.start]
		r.start = (r.start + 1) % len(r.buf)
		r.length--
	}
	if n < len(p) {
		r.underrun.Add(int64(len(p) - n))
		for i := n; i < len(p); i++ {
			p[i] = 0
		}
	}
}

func (r *ring) stats() (overrun, underrun int64) {
	return r.overrun.Load(), r.underrun.Load()
}

func bytesToInt16s(b []byte) []int16 {
	if len(b) < 2 {
		return nil
	}
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}

func int16sToBytes(s []int16, out []byte) {
	if len(s) == 0 {
		return
	}
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2))
}
