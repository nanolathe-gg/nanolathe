package ebitenapp

import (
	"testing"
	"time"
)

// presentPattern feeds Draw arrivals, in milliseconds apart, through a 60 FPS
// cap and returns which ones presented.
func presentPattern(t *testing.T, gaps []float64) []bool {
	t.Helper()
	a := &app{presentInterval: time.Second / 60}
	at := time.Unix(1000, 0)
	out := make([]bool, 0, len(gaps)+1)
	out = append(out, a.presentDue(at))
	for _, g := range gaps {
		at = at.Add(time.Duration(g * float64(time.Millisecond)))
		out = append(out, a.presentDue(at))
	}
	return out
}

func repeatGaps(n int, gaps ...float64) []float64 {
	out := make([]float64, 0, n*len(gaps))
	for range n {
		out = append(out, gaps...)
	}
	return out
}

const refresh120 = 1000.0 / 120

// At 120 Hz the 60 cap presents exactly every other Draw.
func TestPresentCapAlternatesAt120Hz(t *testing.T) {
	got := presentPattern(t, repeatGaps(40, refresh120))
	for i := 12; i < len(got); i++ {
		if want := i%2 == 0; got[i] != want {
			t.Fatalf("draw %d presented=%v, want %v: %v", i, got[i], want, got)
		}
	}
}

// A heavy presented frame delays the odd Draw at 120 Hz, which then arrives
// long after the present and shortly before the next refresh. It is still the
// odd refresh and must not present: a wider allowance that presented it showed
// the heavy frame for one refresh and the next for three in the window trace.
func TestPresentCapSkipsDelayedOddDrawAt120Hz(t *testing.T) {
	got := presentPattern(t, repeatGaps(20, 12.6, 2*refresh120-12.6))
	for i := 12; i < len(got); i++ {
		if want := i%2 == 0; got[i] != want {
			t.Fatalf("draw %d presented=%v, want %v: %v", i, got[i], want, got[12:])
		}
	}
}

// At 60 Hz every Draw presents, including the early Draw that follows a late
// one: the eighth-interval test alone dropped it, so a 5 ms hitch cost two
// refreshes.
func TestPresentCapKeepsLateThenEarlyDrawAt60Hz(t *testing.T) {
	gaps := append(repeatGaps(12, 16.67), 21.9, 11.9, 16.4, 16.9, 19.6, 13.7, 17.4)
	got := presentPattern(t, gaps)
	for i, p := range got {
		if !p {
			t.Fatalf("draw %d skipped at 60 Hz: %v", i, got)
		}
	}
}

// A switch from 120 Hz to 60 Hz presents every Draw at once, before the
// refresh measurement has caught up.
func TestPresentCapSwitchTo60Hz(t *testing.T) {
	gaps := append(repeatGaps(20, refresh120), repeatGaps(12, 16.67)...)
	got := presentPattern(t, gaps)
	for i := 21; i < len(got); i++ {
		if !got[i] {
			t.Fatalf("draw %d skipped after the switch to 60 Hz: %v", i, got[20:])
		}
	}
}

// Back-to-back Draws do not present twice, and a long stall presents the late
// Draw and then resumes alternating instead of bursting to catch up.
func TestPresentCapBurstAndStall(t *testing.T) {
	gaps := append(repeatGaps(12, refresh120), 0.8, 5.8, refresh120)
	got := presentPattern(t, gaps)
	if !got[12] || got[13] || got[14] || !got[15] {
		t.Fatalf("burst presented %v, want only the Draw a cap interval later", got[12:])
	}
	gaps = append(repeatGaps(12, refresh120), 120)
	gaps = append(gaps, repeatGaps(6, refresh120)...)
	got = presentPattern(t, gaps)
	after := got[13:]
	if !after[0] || after[1] || !after[2] || after[3] || !after[4] {
		t.Fatalf("stall recovery presented %v, want alternating from the late Draw", after)
	}
}

// At 60 Hz a Draw arriving within half a refresh of the last present, one of a
// burst, is still skipped.
func TestPresentCapSkipsBurstAt60Hz(t *testing.T) {
	got := presentPattern(t, append(repeatGaps(12, 16.67), 0.8, 15.9, 16.67))
	if !got[12] || got[13] || !got[14] || !got[15] {
		t.Fatalf("60 Hz burst presented %v", got[12:])
	}
}

// A present whose Draw arrived late — here 5 ms into its refresh — still
// reached the screen at the refresh after its own, and the Draw after it
// arrives back on the display's refresh. The next present stays two refreshes
// after the late one's refresh instead of waiting a third, which is what the
// window trace of a heavy save under host load showed for half its late
// frames.
func TestPresentCapBooksLatePresentAtItsRefresh(t *testing.T) {
	gaps := append(repeatGaps(13, refresh120), refresh120+5, refresh120-5)
	gaps = append(gaps, repeatGaps(4, refresh120)...)
	got := presentPattern(t, gaps)
	want := map[int]bool{14: true, 15: false, 16: true, 17: false, 18: true}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("draw %d presented=%v, want %v: %v", i, got[i], w, got[12:])
		}
	}
}
