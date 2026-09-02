package numeric

import "testing"

// The shared table index is ((angle + 32) >> 7) & 511, not angle >> 7
// [04 §5.1][06 §3.3]: the +32 pre-add moves every entry boundary a quarter
// step early. This locks the boundary, which is easy to regress silently.
func TestSinIndexPreAdd(t *testing.T) {
	if Sin(95) != sineTable[0] {
		t.Fatalf("Sin(95) = %d, want entry 0 (%d)", Sin(95), sineTable[0])
	}
	if Sin(96) != sineTable[1] {
		t.Fatalf("Sin(96) = %d, want entry 1 (%d)", Sin(96), sineTable[1])
	}
	// The pre-add wraps: the last 32 angle units of the circle read entry 0.
	if Sin(65504) != sineTable[0] || Sin(65503) != sineTable[511] {
		t.Fatalf("wrap: Sin(65504)=%d Sin(65503)=%d, want entries 0 and 511", Sin(65504), Sin(65503))
	}
	// Cosine is the same helper a quarter turn ahead of the same pre-add.
	const quarterTurn Angle = 0x4000
	for _, a := range []Angle{0, 95, 96, 0x3fff, 0x4000, 0xffff} {
		q := a + quarterTurn
		if Cos(a) != Sin(q) {
			t.Fatalf("Cos(%d)=%d != Sin(%d)=%d", a, Cos(a), q, Sin(q))
		}
	}
}
