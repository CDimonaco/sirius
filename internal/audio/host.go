package audio

import (
	"fmt"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// Host is the machine's sound system. It owns the single miniaudio context that every
// device is opened from, so it must outlive its devices and be closed last.
type Host struct {
	ctx *malgo.AllocatedContext
}

func NewHost() (*Host, error) {
	// An empty backend list lets miniaudio pick: CoreAudio on macOS, ALSA or
	// PulseAudio on Linux. That is the whole reason this package needs no
	// per-platform file.
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	return &Host{ctx: ctx}, nil
}

func (h *Host) Close() error {
	err := h.ctx.Uninit()
	h.ctx.Free()
	return err
}

// Names lists what the host reports, which is the first thing to check when a device
// cannot be found. On macOS a freshly installed virtual driver shows up here only
// after coreaudiod has been restarted.
func (h *Host) Names() (capture, playback []string, err error) {
	for _, kind := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
		devices, err := h.ctx.Devices(kind)
		if err != nil {
			return nil, nil, fmt.Errorf("list %v devices: %w", kind, err)
		}
		for _, d := range devices {
			if kind == malgo.Capture {
				capture = append(capture, d.Name())
			} else {
				playback = append(playback, d.Name())
			}
		}
	}
	return capture, playback, nil
}

// Capture opens the input device whose name contains match and writes everything it
// delivers into r. The write happens on the real-time audio thread, which is why the
// other end is a Ring and not a channel.
func (h *Host) Capture(match string, r *Ring) (*Device, error) {
	info, err := h.find(malgo.Capture, match)
	if err != nil {
		return nil, err
	}
	cfg := h.config(malgo.Capture, info)
	d := &Device{name: info.Name(), lost: make(chan struct{})}

	dev, err := malgo.InitDevice(h.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, frames uint32) {
			r.Write(samples(in, int(frames)))
		},
		Stop: d.stopped,
	})
	if err != nil {
		return nil, fmt.Errorf("open capture %q: %w", info.Name(), err)
	}
	return d.start(dev)
}

// Playback opens the output device whose name contains match and feeds it from r.
func (h *Host) Playback(match string, r *Ring) (*Device, error) {
	info, err := h.find(malgo.Playback, match)
	if err != nil {
		return nil, err
	}
	cfg := h.config(malgo.Playback, info)
	d := &Device{name: info.Name(), lost: make(chan struct{})}

	dev, err := malgo.InitDevice(h.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, frames uint32) {
			r.Read(samples(out, int(frames)))
		},
		Stop: d.stopped,
	})
	if err != nil {
		return nil, fmt.Errorf("open playback %q: %w", info.Name(), err)
	}
	return d.start(dev)
}

func (h *Host) config(kind malgo.DeviceType, info malgo.DeviceInfo) malgo.DeviceConfig {
	cfg := malgo.DefaultDeviceConfig(kind)
	// Asking for 8 kHz from a device whose native rate is 44100 works: miniaudio
	// resamples internally. The spike measured exactly 8000 samples per second
	// coming out, so Sirius owns no resampler.
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInFrames = uint32(Samples(10))

	sub := &cfg.Capture
	if kind == malgo.Playback {
		sub = &cfg.Playback
	}
	sub.Format = malgo.FormatS16
	sub.Channels = Channels
	sub.DeviceID = info.ID.Pointer()
	return cfg
}

func (h *Host) find(kind malgo.DeviceType, match string) (malgo.DeviceInfo, error) {
	devices, err := h.ctx.Devices(kind)
	if err != nil {
		return malgo.DeviceInfo{}, fmt.Errorf("list %v devices: %w", kind, err)
	}
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name()), strings.ToLower(match)) {
			return d, nil
		}
	}
	return malgo.DeviceInfo{}, fmt.Errorf("no %v device whose name contains %q", kind, match)
}

// Device is one open sound device.
type Device struct {
	name    string
	dev     *malgo.Device
	closing atomic.Bool
	lost    chan struct{}
}

func (d *Device) start(dev *malgo.Device) (*Device, error) {
	d.dev = dev
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return nil, fmt.Errorf("start %q: %w", d.name, err)
	}
	return d, nil
}

func (d *Device) Name() string { return d.name }

// Lost closes when the device stops on its own, which is what happens when the audio
// daemon restarts or a driver is removed. The spike ignored this and spent the rest of
// the call writing into a buffer nobody drained.
func (d *Device) Lost() <-chan struct{} { return d.lost }

func (d *Device) stopped() {
	// miniaudio calls this for a deliberate stop too, so only an unexpected one counts.
	if d.closing.Load() {
		return
	}
	close(d.lost)
}

func (d *Device) Close() error {
	if d.closing.Swap(true) {
		return nil
	}
	d.dev.Uninit()
	return nil
}

// samples reinterprets a byte block from the device as int16 samples. No copy: the
// callback is on the audio thread and has no budget for one.
func samples(b []byte, frames int) []int16 {
	n := frames * Channels
	if n == 0 || len(b) < n*2 {
		return nil
	}
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), n)
}
