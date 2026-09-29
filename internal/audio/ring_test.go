package audio

import "testing"

func TestRingRoundTrip(t *testing.T) {
	r := NewRing(8)
	r.Write([]int16{1, 2, 3})

	got := make([]int16, 3)
	r.Read(got)
	for i, want := range []int16{1, 2, 3} {
		if got[i] != want {
			t.Fatalf("sample %d = %d, want %d", i, got[i], want)
		}
	}
	if over, under := r.Stats(); over != 0 || under != 0 {
		t.Fatalf("overrun %d, underrun %d, want both zero", over, under)
	}
}

func TestRingWrapsAround(t *testing.T) {
	r := NewRing(4)
	for round := range 5 {
		r.Write([]int16{1, 2, 3})
		got := make([]int16, 3)
		r.Read(got)
		if got[0] != 1 || got[2] != 3 {
			t.Fatalf("round %d: got %v, want [1 2 3]", round, got)
		}
	}
	if over, under := r.Stats(); over != 0 || under != 0 {
		t.Fatalf("overrun %d, underrun %d, want both zero", over, under)
	}
}

func TestRingDropsWhenFull(t *testing.T) {
	r := NewRing(4)
	r.Write([]int16{1, 2, 3, 4, 5, 6})

	if over, _ := r.Stats(); over != 2 {
		t.Fatalf("overrun = %d, want 2", over)
	}
	got := make([]int16, 4)
	r.Read(got)
	if got[3] != 4 {
		t.Fatalf("kept %v, want the first four samples", got)
	}
}

func TestRingPadsWhenEmpty(t *testing.T) {
	r := NewRing(4)
	r.Write([]int16{7})

	got := make([]int16, 3)
	r.Read(got)
	if got[0] != 7 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("got %v, want [7 0 0]", got)
	}
	if _, under := r.Stats(); under != 2 {
		t.Fatalf("underrun = %d, want 2", under)
	}
}

// A producer and a consumer at the same rate should leave both counters at zero, which
// is what the spike measured over ten minutes of real audio.
func TestRingMatchedRatesStayClean(t *testing.T) {
	r := NewRing(Samples(2000))
	block := make([]int16, Samples(10))
	out := make([]int16, Samples(10))

	for range 10 * 60 * 100 { // ten minutes of 10 ms blocks
		r.Write(block)
		r.Read(out)
	}
	if over, under := r.Stats(); over != 0 || under != 0 {
		t.Fatalf("overrun %d, underrun %d, want both zero", over, under)
	}
	if r.Len() != 0 {
		t.Fatalf("ring holds %d samples, want 0", r.Len())
	}
}
