package bridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cdimonaco/sirius/internal/audio"
)

// fakeClock is hand-wound, so gap classification needs no wall time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// packets replays a scripted sequence of arrivals, advancing the clock by each one's
// gap before handing it over. It stands in for a phone.
type arrival struct {
	gap    time.Duration
	marker bool
	sample int16
}

func replay(clock *fakeClock, arrivals []arrival) func() ([]int16, bool, error) {
	i := 0
	return func() ([]int16, bool, error) {
		if i >= len(arrivals) {
			return nil, false, errors.New("hung up")
		}
		a := arrivals[i]
		i++
		clock.advance(a.gap)
		pcm := make([]int16, audio.Samples(20))
		for j := range pcm {
			pcm[j] = a.sample
		}
		return pcm, a.marker, nil
	}
}

func TestReceivedAudioReachesTheRing(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	r := audio.NewRing(depth())
	var c Counters

	arrivals := []arrival{{gap: packet, sample: 7}, {gap: packet, sample: 9}}
	if err := pumpFromPhone(context.Background(), replay(clock, arrivals), r, clock.now, &c); err != nil {
		t.Fatal(err)
	}

	if got := c.PacketsFromPhone.Load(); got != 2 {
		t.Fatalf("counted %d packets, want 2", got)
	}
	got := make([]int16, audio.Samples(20))
	r.Read(got)
	if got[0] != 7 {
		t.Fatalf("first packet arrived as %d, want 7", got[0])
	}
	r.Read(got)
	if got[0] != 9 {
		t.Fatalf("second packet arrived as %d, want 9", got[0])
	}
}

// A gap with no marker bit is the network being late, which is a stall.
func TestStallIsCountedAsStall(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	var c Counters

	arrivals := []arrival{
		{gap: packet},
		{gap: 200 * time.Millisecond}, // the phone kept sending, the packets were late
		{gap: packet},
	}
	if err := pumpFromPhone(context.Background(), replay(clock, arrivals), audio.NewRing(depth()), clock.now, &c); err != nil {
		t.Fatal(err)
	}

	if got := c.Gaps.Load(); got != 1 {
		t.Fatalf("gaps = %d, want 1", got)
	}
	if got := c.GapMillis.Load(); got != 200 {
		t.Fatalf("gap millis = %d, want 200", got)
	}
	if got := c.SilencePauses.Load(); got != 0 {
		t.Fatalf("silence pauses = %d, want 0", got)
	}
}

// The same gap with a marker bit is the phone suppressing silence, which is correct
// behaviour and must not be reported as a fault.
func TestSuppressedSilenceIsNotAStall(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	var c Counters

	arrivals := []arrival{
		{gap: packet},
		{gap: 3 * time.Second, marker: true}, // nobody was talking
		{gap: packet},
	}
	if err := pumpFromPhone(context.Background(), replay(clock, arrivals), audio.NewRing(depth()), clock.now, &c); err != nil {
		t.Fatal(err)
	}

	if got := c.SilencePauses.Load(); got != 1 {
		t.Fatalf("silence pauses = %d, want 1", got)
	}
	if got := c.SilentMillis.Load(); got != 3000 {
		t.Fatalf("silent millis = %d, want 3000", got)
	}
	if got := c.Gaps.Load(); got != 0 {
		t.Fatalf("gaps = %d, want 0", got)
	}
}

func TestJitterWithinThresholdIsNotCounted(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	var c Counters

	arrivals := []arrival{{gap: packet}, {gap: 50 * time.Millisecond}, {gap: packet}}
	if err := pumpFromPhone(context.Background(), replay(clock, arrivals), audio.NewRing(depth()), clock.now, &c); err != nil {
		t.Fatal(err)
	}
	if c.Gaps.Load() != 0 || c.SilencePauses.Load() != 0 {
		t.Fatalf("counted a gap of 50ms: %s", c.String())
	}
}

func TestSentAudioIsPacedByTheTicker(t *testing.T) {
	r := audio.NewRing(depth())
	block := make([]int16, audio.Samples(20))
	for i := range block {
		block[i] = 3
	}
	r.Write(block)
	r.Write(block)

	tick := make(chan time.Time, 3)
	for range 3 {
		tick <- time.Now()
	}
	close(tick)

	var sent [][]int16
	var c Counters
	err := pumpToPhone(context.Background(), r, func(pcm []int16) error {
		sent = append(sent, append([]int16(nil), pcm...))
		return nil
	}, tick, &c)
	if err != nil {
		t.Fatal(err)
	}

	if len(sent) != 3 {
		t.Fatalf("sent %d packets, want one per tick", len(sent))
	}
	if sent[0][0] != 3 || sent[1][0] != 3 {
		t.Fatalf("buffered audio did not reach the phone: %d, %d", sent[0][0], sent[1][0])
	}
	// The third tick had nothing buffered, so it must carry silence rather than wait.
	if sent[2][0] != 0 {
		t.Fatalf("third packet = %d, want silence", sent[2][0])
	}
	if _, under := r.Stats(); under != int64(audio.Samples(20)) {
		t.Fatalf("underrun = %d, want one packet worth", under)
	}
}

func TestSendErrorStopsThePump(t *testing.T) {
	tick := make(chan time.Time, 2)
	tick <- time.Now()
	tick <- time.Now()

	var c Counters
	err := pumpToPhone(context.Background(), audio.NewRing(depth()), func([]int16) error {
		return errors.New("socket closed")
	}, tick, &c)
	if err == nil {
		t.Fatal("a failing send did not stop the pump")
	}
}

// The report has to say whether audio was actually lost, which is the question the gap
// counts cannot answer on their own.
func TestCountersReportInsertedAndDroppedAudio(t *testing.T) {
	small := audio.NewRing(audio.Samples(20))
	block := make([]int16, audio.Samples(20))

	small.Write(block)
	small.Write(block) // one packet too many for the ring: 20ms dropped

	drained := make([]int16, audio.Samples(40))
	small.Read(drained) // only 20ms was there, so 20ms of silence is invented

	var c Counters
	c.record(small)

	if got := c.DroppedMillis.Load(); got != 20 {
		t.Errorf("dropped = %dms, want 20", got)
	}
	if got := c.InsertedMillis.Load(); got != 20 {
		t.Errorf("inserted = %dms, want 20", got)
	}
}
