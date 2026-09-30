// Package bridge couples one phone call to a pair of host audio devices. It is the only
// place that knows the order in which things start and stop.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/CDimonaco/sirius/internal/audio"
)

const (
	// One RTP packet carries 20 ms of narrowband audio.
	packet = 20 * time.Millisecond

	// Two seconds of slack in each direction. The spike never came near the ends, so a
	// ring that touches them means the clocks genuinely diverged.
	ringDepth = 2000 * time.Millisecond

	// Three packets. Anything shorter is ordinary jitter not worth a line in the report.
	gapThreshold = 3 * packet
)

// Device is what the bridge needs from a host audio device.
type Device interface {
	Name() string
	// Lost closes when the device stops on its own, which is what happens when the
	// audio daemon restarts or a driver is removed.
	Lost() <-chan struct{}
	Close() error
}

// Open hands the bridge a device wired to a ring. The caller decides which device, so
// the bridge never has to know how they are named or found.
type Open func(*audio.Ring) (Device, error)

// Phone is the far side of the bridge. It exists as an interface because every test in
// this package has to run without a phone, a network or a meeting.
type Phone interface {
	// ReadSamples blocks until the next packet arrives. The bool is the RTP marker bit.
	ReadSamples() (pcm []int16, marker bool, err error)
	WriteSamples(pcm []int16) error
	Done() <-chan struct{}
}

// Counters are what makes the failure shapes visible. Read them in this order: the
// concealed and dropped milliseconds say how much audio went missing, and the gap
// counts say why.
//
// Gaps are measured between our own reads, so on wifi they count bursty delivery as
// well as real trouble: packets arrive in clumps, we read the clump back to back and
// then wait. Many gaps with nothing concealed means the buffer absorbed them.
type Counters struct {
	PacketsToPhone   atomic.Int64
	PacketsFromPhone atomic.Int64

	Gaps          atomic.Int64 // gaps with no marker bit: packets late or lost
	GapMillis     atomic.Int64
	SilencePauses atomic.Int64 // gaps the phone marked as deliberate silence
	SilentMillis  atomic.Int64

	// Filled in from the two rings when the call ends.
	ConcealedMillis atomic.Int64 // audio missing from the stream and covered over
	DroppedMillis   atomic.Int64 // audio thrown away because nobody drained it
}

func (c *Counters) String() string {
	return fmt.Sprintf("to phone %d packets, from phone %d packets, concealed %dms, dropped %dms, %d gaps totalling %dms, %d silence pauses totalling %dms",
		c.PacketsToPhone.Load(), c.PacketsFromPhone.Load(),
		c.ConcealedMillis.Load(), c.DroppedMillis.Load(),
		c.Gaps.Load(), c.GapMillis.Load(),
		c.SilencePauses.Load(), c.SilentMillis.Load())
}

// record folds both rings' totals into the counters. Called once, as the call ends.
func (c *Counters) record(rings ...*audio.Ring) {
	var over, under int64
	for _, r := range rings {
		o, u := r.Stats()
		over += o
		under += u
	}
	c.DroppedMillis.Store(millis(over))
	c.ConcealedMillis.Store(millis(under))
}

func millis(samples int64) int64 { return samples * 1000 / audio.SampleRate }

// Run bridges call to a pair of host devices and returns when the call ends, a device
// disappears, or ctx is cancelled. Both devices are closed on the way out, whatever
// ended the call.
//
// openCapture opens the device a meeting client plays into, so its audio goes to the
// phone. openPlayback opens the device a meeting client records from, so the phone's
// audio arrives there. prebuffer is how much of the phone's audio to pile up before
// playing any of it, which buys room to absorb a burst of late packets.
func Run(ctx context.Context, call Phone, openCapture, openPlayback Open, prebuffer time.Duration, c *Counters) error {
	toPhone := audio.NewRing(depth())
	fromPhone := audio.NewPrimedRing(depth(), audio.Samples(int(prebuffer.Milliseconds())))
	defer c.record(toPhone, fromPhone)

	capture, err := openCapture(toPhone)
	if err != nil {
		return err
	}
	defer func() { _ = capture.Close() }()

	playback, err := openPlayback(fromPhone)
	if err != nil {
		return err
	}
	defer func() { _ = playback.Close() }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The ticker is the clock the host audio has to keep up with: one packet every
	// 20 ms, whatever the capture device managed to deliver in the meantime.
	ticker := time.NewTicker(packet)
	defer ticker.Stop()

	sendErr := make(chan error, 1)
	go func() { sendErr <- pumpToPhone(ctx, toPhone, call.WriteSamples, ticker.C, c) }()

	recvErr := make(chan error, 1)
	go func() { recvErr <- pumpFromPhone(ctx, call.ReadSamples, fromPhone, time.Now, c) }()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.Done():
		return nil
	case <-capture.Lost():
		return fmt.Errorf("capture device %q disappeared", capture.Name())
	case <-playback.Lost():
		return fmt.Errorf("playback device %q disappeared", playback.Name())
	case err := <-sendErr:
		return err
	case err := <-recvErr:
		return err
	}
}

func depth() int { return audio.Samples(int(ringDepth / time.Millisecond)) }

// pumpToPhone hands the phone one packet on every tick, padding with silence when the
// capture device has not kept up. It never waits for audio: an RTP stream that pauses
// to wait is worse than one that carries a moment of silence.
func pumpToPhone(ctx context.Context, r *audio.Ring, send func([]int16) error, tick <-chan time.Time, c *Counters) error {
	pcm := make([]int16, audio.Samples(20))
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-tick:
			if !ok {
				return nil
			}
			r.Read(pcm)
			if err := send(pcm); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("send to phone: %w", err)
			}
			c.PacketsToPhone.Add(1)
		}
	}
}

// pumpFromPhone decodes what the phone sends into the playback ring, and classifies the
// gaps. The classification is the marker bit: a phone that paused because nobody was
// talking marks the packet that resumes the talkspurt, and a phone whose packets were
// simply late does not.
func pumpFromPhone(ctx context.Context, recv func() ([]int16, bool, error), r *audio.Ring, now func() time.Time, c *Counters) error {
	var last time.Time
	for ctx.Err() == nil {
		pcm, marker, err := recv()
		if err != nil {
			// A call that ended is a clean stop. Anything else, a decode failure or a
			// dead socket, has to be reported: otherwise a broken media path is
			// indistinguishable from the user hanging up.
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("receive from phone: %w", err)
		}
		r.Write(pcm)
		c.PacketsFromPhone.Add(1)

		arrived := now()
		if gap := arrived.Sub(last); !last.IsZero() && gap > gapThreshold {
			gapMillis := gap.Milliseconds()
			if marker {
				c.SilencePauses.Add(1)
				c.SilentMillis.Add(gapMillis)
			} else {
				c.Gaps.Add(1)
				c.GapMillis.Add(gapMillis)
			}
		}
		last = arrived
	}
	return nil
}
