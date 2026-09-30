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

// Concealment covers a hole with the audio either side of it rather than with digital
// silence. A lost packet is gone whatever we do, but silence arrives as a click while a
// fading repeat of what came before mostly passes unnoticed.
var (
	concealHistory = Samples(20) // how much of the recent past gets repeated
	concealFade    = Samples(60) // how long the repeat takes to fall to silence
)

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

	// prime is how much has to pile up before the ring starts serving, both at the
	// start and again after it runs dry. It trades latency for room to absorb a
	// burst of late packets. Zero means serve whatever is there.
	prime   int
	filling bool

	history    []int16 // the last audio actually served, repeated to cover a hole
	historyLen int     // how much of history is real audio rather than the initial zeros
	concealed  int     // samples concealed since real audio last flowed

	overrun  atomic.Int64 // samples dropped because the reader fell behind
	underrun atomic.Int64 // samples of silence invented because the writer fell behind
}

// NewRing returns a ring of the given capacity that serves whatever it holds.
func NewRing(samples int) *Ring {
	return &Ring{buf: make([]int16, samples), history: make([]int16, concealHistory)}
}

// NewPrimedRing returns a ring that holds back until prime samples have piled up, and
// does so again every time it runs dry.
func NewPrimedRing(samples, prime int) *Ring {
	r := NewRing(samples)
	r.prime, r.filling = prime, prime > 0
	return r
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

	if r.filling {
		if r.length < r.prime {
			// Still filling. What goes out here is as much a hole as any other, so it
			// is concealed and counted the same way rather than hidden.
			r.underrun.Add(int64(len(p)))
			r.conceal(p)
			return
		}
		r.filling = false
	}

	n := 0
	for ; n < len(p) && r.length > 0; n++ {
		p[n] = r.buf[r.start]
		r.start = (r.start + 1) % len(r.buf)
		r.length--
	}
	if n > 0 {
		r.remember(p[:n])
	}
	if n == len(p) {
		r.concealed = 0
		return
	}
	r.underrun.Add(int64(len(p) - n))
	r.conceal(p[n:])
	r.filling = r.prime > 0
}

// conceal fills p by repeating recent audio, fading to silence so that a long gap goes
// quiet instead of buzzing. With nothing to repeat yet it writes silence.
func (r *Ring) conceal(p []int16) {
	if r.historyLen == 0 {
		for i := range p {
			p[i] = 0
		}
		r.concealed += len(p)
		return
	}
	recent := r.history[len(r.history)-r.historyLen:]
	for i := range p {
		gain := 1 - float32(r.concealed)/float32(concealFade)
		if gain <= 0 {
			p[i] = 0
		} else {
			p[i] = int16(float32(recent[r.concealed%len(recent)]) * gain)
		}
		r.concealed++
	}
}

// remember keeps the tail of what was just served, which is what conceal repeats.
func (r *Ring) remember(p []int16) {
	r.historyLen = min(len(r.history), r.historyLen+len(p))
	if len(p) >= len(r.history) {
		copy(r.history, p[len(p)-len(r.history):])
		return
	}
	copy(r.history, r.history[len(p):])
	copy(r.history[len(r.history)-len(p):], p)
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
