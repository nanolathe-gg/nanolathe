package render

import "testing"

// The reveal runs five stages as the remaining fraction falls from one to
// zero: two sweeps over an empty body, a solid green fill, a texture fill, and
// a final sweep over the finished unit [03 §5.2].
func TestNanoframeRevealStages(t *testing.T) {
	const band, outline = 0xa3, 0xa9
	for _, c := range []struct {
		name                 string
		remaining            float32
		below, inBand, above int16
	}{
		{"first sweep", 0.98, NanoframeErase, band, NanoframeErase},
		{"second sweep", 0.85, NanoframeErase, band, NanoframeErase},
		{"green fill", 0.60, band, outline, NanoframeErase},
		{"texture fill", 0.30, NanoframeKeep, outline, band},
		{"final sweep", 0.05, NanoframeKeep, band, NanoframeKeep},
	} {
		r := BuildNanoframeReveal(c.remaining, band, outline)
		if r.Below != c.below || r.Band != c.inBand || r.Above != c.above {
			t.Fatalf("%s: verdicts = %d/%d/%d, want %d/%d/%d",
				c.name, r.Below, r.Band, r.Above, c.below, c.inBand, c.above)
		}
	}
}

// The sweep band is four height-key units deep and never underflows.
func TestNanoframeRevealBandDepth(t *testing.T) {
	r := BuildNanoframeReveal(0.60, 0xa3, 0xa9)
	if int(r.Line)-int(r.Floor) != 4 {
		t.Fatalf("band depth = %d, want 4", int(r.Line)-int(r.Floor))
	}
	if r.Verdict(r.Floor) != r.Band || r.Verdict(r.Line) != r.Above || r.Verdict(r.Floor-1) != r.Below {
		t.Fatalf("band boundaries are not half-open at [%d,%d)", r.Floor, r.Line)
	}
	// A line inside the first four units clamps the floor rather than wrapping.
	if got := BuildNanoframeReveal(1.0, 0xa3, 0xa9); got.Line < 4 && got.Floor != 0 {
		t.Fatalf("floor = %d for line %d, want 0", got.Floor, got.Line)
	}
}

// Both pulses reflect off the ends of the sixteen-entry green ramp.
func TestNanoframePulseReflectsAcrossTheRamp(t *testing.T) {
	seen := map[uint8]bool{}
	for tick := uint32(0); tick < 200; tick++ {
		a, b := NanoframePulse(0, tick)
		for _, v := range []uint8{a, b} {
			if v < 0xa0 || v > 0xaf {
				t.Fatalf("tick %d: pulse colour %#x escaped the 0xa0..0xaf ramp", tick, v)
			}
			seen[v] = true
		}
	}
	if len(seen) != 16 {
		t.Fatalf("pulse visited %d of the ramp's 16 entries", len(seen))
	}
	// Two units with different identifiers do not pulse in lockstep.
	if a0, _ := NanoframePulse(0, 40); a0 == mustPulse(t, 3, 40) {
		t.Fatalf("two units pulsed identically at tick 40")
	}
}

func mustPulse(t *testing.T, id uint16, tick uint32) uint8 {
	t.Helper()
	a, _ := NanoframePulse(id, tick)
	return a
}

// The height key is half the model-relative height, biased into byte range.
func TestNanoframeHeightKey(t *testing.T) {
	if got := NanoframeHeightKey(0); got != NanoframeHeightBias {
		t.Fatalf("ground key = %d, want %d", got, NanoframeHeightBias)
	}
	if got := NanoframeHeightKey(40); got != NanoframeHeightBias+20 {
		t.Fatalf("key at 40 units = %d, want %d", got, NanoframeHeightBias+20)
	}
}
