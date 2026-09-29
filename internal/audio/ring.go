// Package audio moves PCM between the host's sound devices and the rest of Sirius.
// Everything here is 8 kHz mono int16, which is what G.711 carries and what both
// virtual devices report as their native format.
package audio

import (
	"sync"
	"sync/atomic"
)

const (
	SampleRate = 8000
	Channels   = 1
)

// Samples returns how many samples cover d milliseconds.
func Samples(milliseconds int) int { return SampleRate * milliseconds / 1000 }

// Ring is the buffer between a real-time audio callback and an ordinary goroutine.
//
// The callback cannot block, so when the two sides fall out of step the Ring drops or
// pads with silence rather than waiting, and counts what it did. Those counters are
// the only way the failure shapes we measured in the spike are visible at all: a
// device that stalls for a fraction of a second, and a phone that stops sending while
// nobody is talking.
type Ring struct {
	// ponytail: one mutex for the whole ring. At 8 kHz with 10 ms blocks the lock is
	// held for microseconds a hundred times a second, which is nowhere near
	// contention. Move to atomic head and tail indices only if a profile says so.
	mu     sync.Mutex
	buf    []int16
	start  int // index of the oldest sample
	length int // how many samples are currently held

	overrun  atomic.Int64 // samples dropped because the reader fell behind
	underrun atomic.Int64 // samples of silence invented because the writer fell behind
}

func NewRing(samples int) *Ring {
	return &Ring{buf: make([]int16, samples)}
}

// Write copies p into the ring, dropping whatever does not fit.
func (r *Ring) Write(p []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	free := len(r.buf) - r.length
	if len(p) > free {
		r.overrun.Add(int64(len(p) - free))
		p = p[:free]
	}
	for _, s := range p {
		r.buf[(r.start+r.length)%len(r.buf)] = s
		r.length++
	}
}

// Read fills p, padding with silence when the ring runs dry. It always fills p
// completely, because an audio callback has to hand the device a full block.
func (r *Ring) Read(p []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for ; n < len(p) && r.length > 0; n++ {
		p[n] = r.buf[r.start]
		r.start = (r.start + 1) % len(r.buf)
		r.length--
	}
	if n == len(p) {
		return
	}
	r.underrun.Add(int64(len(p) - n))
	for i := n; i < len(p); i++ {
		p[i] = 0
	}
}

// Len reports how much audio is waiting, in samples.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.length
}

// Stats reports the running totals in samples.
func (r *Ring) Stats() (overrun, underrun int64) {
	return r.overrun.Load(), r.underrun.Load()
}
