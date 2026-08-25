package main

import "testing"

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// neither locale- nor Unicode-aware.
func TestRetailStricmpOrdersMapNames(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"Canal Crossing", "Cavedog Links CC", -1},
		{"Cavedog Links CC", "Checker Ponds", -1},
		{"Core Prime Industrial Area", "Coremageddon", -1},
		{"crystal isles", "Crystal Maze", -1},
		{"Acid Pools", "acid pools", 0},
		{"Acid", "Acid Pools", -1},
		{"Zeta", "Acid", 1},
	}
	for _, c := range cases {
		if got := retailStricmp(c.a, c.b); got != c.want {
			t.Errorf("retailStricmp(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// round(visible/total * (barLength-3)), clamped up to ten pixels.
func TestRetailScrollbarKnobSize(t *testing.T) {
	// SELMAP's SLIDER is 203 tall and its list shows 12 of 99 maps.
	if got, want := retailScrollbarKnobSize(12, 99, 203), 24; got != want {
		t.Errorf("knob = %d, want %d", got, want)
	}
	// A list that fits entirely fills the bar.
	if got, want := retailScrollbarKnobSize(12, 12, 203), 200; got != want {
		t.Errorf("knob = %d, want %d", got, want)
	}
	// The ten-pixel floor holds for a very long list.
	if got, want := retailScrollbarKnobSize(12, 4000, 203), 10; got != want {
		t.Errorf("knob = %d, want %d", got, want)
	}
}
