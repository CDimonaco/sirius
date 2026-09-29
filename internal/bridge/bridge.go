// Package bridge couples one phone call to a pair of host audio devices. It is the only
// place that knows the order in which things start and stop.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cdimonaco/sirius/internal/audio"
)

const (
	// One RTP packet carries 20 ms of narrowband audio.
	packet = 20 * time.Millisecond

	// Two seconds of slack in each direction. The spike never came near the ends, so a
	// ring that touches them means the clocks genuinely diverged.
	ringDepth = 2000 * time.Millisecond

	// A gap longer than three packets is no longer jitter.
	stallThreshold = 3 * packet
)

// Phone is the far side of the bridge. It exists as an interface because every test in
// this package has to run without a phone, a network or a meeting.
type Phone interface {
	// ReadSamples blocks until the next packet arrives. The bool is the RTP marker bit.
	ReadSamples() (pcm []int16, marker bool, err error)
	WriteSamples(pcm []int16) error
	Done() <-chan struct{}
}

// Counters are what makes the two failure shapes we measured visible. Stalls and
// suppressed silence look identical in the audio and mean different things: a stall is
// something going wrong, suppressed silence is a phone behaving correctly.
type Counters struct {
	Stalls           atomic.Int64
	StalledMillis    atomic.Int64
	SilencePauses    atomic.Int64
	SilentMillis     atomic.Int64
	PacketsToPhone   atomic.Int64
	PacketsFromPhone atomic.Int64
}

func (c *Counters) String() string {
	return fmt.Sprintf("to phone %d packets, from phone %d packets, %d stalls totalling %dms, %d silence pauses totalling %dms",
		c.PacketsToPhone.Load(), c.PacketsFromPhone.Load(),
		c.Stalls.Load(), c.StalledMillis.Load(),
		c.SilencePauses.Load(), c.SilentMillis.Load())
}

// Run bridges call to the two named host devices and returns when the call ends, a
// device disappears, or ctx is cancelled.
//
// capture is the device a meeting client plays into, so its audio goes to the phone.
// playback is the device a meeting client records from, so the phone's audio goes there.
func Run(ctx context.Context, host *audio.Host, call Phone, captureName, playbackName string, c *Counters) error {
	toPhone := audio.NewRing(depth())
	fromPhone := audio.NewRing(depth())

	capture, err := host.Capture(captureName, toPhone)
	if err != nil {
		return err
	}
	defer capture.Close()

	playback, err := host.Playback(playbackName, fromPhone)
	if err != nil {
		return err
	}
	defer playback.Close()

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
			// The phone hanging up ends the read, which is not a failure.
			return nil
		}
		r.Write(pcm)
		c.PacketsFromPhone.Add(1)

		arrived := now()
		if gap := arrived.Sub(last); !last.IsZero() && gap > stallThreshold {
			millis := gap.Milliseconds()
			if marker {
				c.SilencePauses.Add(1)
				c.SilentMillis.Add(millis)
			} else {
				c.Stalls.Add(1)
				c.StalledMillis.Add(millis)
			}
		}
		last = arrived
	}
	return nil
}
