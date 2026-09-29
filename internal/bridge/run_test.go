package bridge

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cdimonaco/sirius/internal/audio"
)

// fakeDevice is a host device that can be made to disappear and remembers being closed.
type fakeDevice struct {
	name   string
	lost   chan struct{}
	closes atomic.Int64
}

func newFakeDevice(name string) *fakeDevice {
	return &fakeDevice{name: name, lost: make(chan struct{})}
}

func (d *fakeDevice) Name() string                     { return d.name }
func (d *fakeDevice) Lost() <-chan struct{}            { return d.lost }
func (d *fakeDevice) Close() error                     { d.closes.Add(1); return nil }
func (d *fakeDevice) disappear()                       { close(d.lost) }
func (d *fakeDevice) open(*audio.Ring) (Device, error) { return d, nil }

// fakePhone never delivers audio and hangs up when told.
type fakePhone struct {
	done chan struct{}
}

func newFakePhone() *fakePhone { return &fakePhone{done: make(chan struct{})} }

func (p *fakePhone) ReadSamples() ([]int16, bool, error) {
	<-p.done
	return nil, false, errors.New("call ended")
}
func (p *fakePhone) WriteSamples([]int16) error { return nil }
func (p *fakePhone) Done() <-chan struct{}      { return p.done }
func (p *fakePhone) hangup()                    { close(p.done) }

// run starts Run and returns a channel carrying its verdict, so each test can trigger
// one ending and wait for it without a sleep.
func run(t *testing.T, ctx context.Context, phone *fakePhone, capture, playback *fakeDevice) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, phone, capture.open, playback.open, &Counters{}) }()
	return done
}

func waitFor(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func assertClosed(t *testing.T, devices ...*fakeDevice) {
	t.Helper()
	for _, d := range devices {
		if got := d.closes.Load(); got != 1 {
			t.Errorf("%s closed %d times, want once", d.name, got)
		}
	}
}

func TestPhoneHangupEndsTheBridgeCleanly(t *testing.T) {
	capture, playback := newFakeDevice("capture"), newFakeDevice("playback")
	phone := newFakePhone()

	done := run(t, context.Background(), phone, capture, playback)
	phone.hangup()

	if err := waitFor(t, done); err != nil {
		t.Fatalf("a hangup returned an error: %v", err)
	}
	assertClosed(t, capture, playback)
}

func TestCancellingTheContextEndsTheBridge(t *testing.T) {
	capture, playback := newFakeDevice("capture"), newFakeDevice("playback")
	ctx, cancel := context.WithCancel(context.Background())

	done := run(t, ctx, newFakePhone(), capture, playback)
	cancel()

	if err := waitFor(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	assertClosed(t, capture, playback)
}

func TestLosingTheCaptureDeviceEndsTheCall(t *testing.T) {
	capture, playback := newFakeDevice("BlackHole 2ch"), newFakeDevice("BlackHole 16ch")

	done := run(t, context.Background(), newFakePhone(), capture, playback)
	capture.disappear()

	err := waitFor(t, done)
	if err == nil || !strings.Contains(err.Error(), "BlackHole 2ch") {
		t.Fatalf("got %v, want an error naming the lost capture device", err)
	}
	assertClosed(t, capture, playback)
}

func TestLosingThePlaybackDeviceEndsTheCall(t *testing.T) {
	capture, playback := newFakeDevice("BlackHole 2ch"), newFakeDevice("BlackHole 16ch")

	done := run(t, context.Background(), newFakePhone(), capture, playback)
	playback.disappear()

	err := waitFor(t, done)
	if err == nil || !strings.Contains(err.Error(), "BlackHole 16ch") {
		t.Fatalf("got %v, want an error naming the lost playback device", err)
	}
	assertClosed(t, capture, playback)
}

// A device that cannot be opened must not leave the other one open behind it.
func TestFailingToOpenPlaybackClosesCapture(t *testing.T) {
	capture := newFakeDevice("capture")
	failing := func(*audio.Ring) (Device, error) { return nil, errors.New("no such device") }

	err := Run(context.Background(), newFakePhone(), capture.open, failing, &Counters{})
	if err == nil {
		t.Fatal("opening a missing playback device succeeded")
	}
	assertClosed(t, capture)
}

// A second call has to work after the first, which means nothing survives teardown.
func TestASecondCallWorksAfterTheFirst(t *testing.T) {
	for round := range 2 {
		capture, playback := newFakeDevice("capture"), newFakeDevice("playback")
		phone := newFakePhone()

		done := run(t, context.Background(), phone, capture, playback)
		phone.hangup()

		if err := waitFor(t, done); err != nil {
			t.Fatalf("round %d returned %v", round, err)
		}
		assertClosed(t, capture, playback)
	}
}
