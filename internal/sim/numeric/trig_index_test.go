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

// TestMulRoundAddsHalfThenFloors locks the trig component's rounding
// [04 R-MOV-01 §4] [04 §5.3]: the full-width product takes half the 8192 scale
// and is then shifted down arithmetically, so it FLOORS. A negative product
// rounds toward negative infinity and a tie rounds up; truncating toward zero
// — the shape a `/ 8192` would give — is wrong and disagrees on exactly the
// negative products the RockUnit and flight-shaping consumers produce.
//
// The discriminating counter is what makes this a test rather than a
// tautology: it fails if the sweep never reaches a product where flooring and
// truncation disagree.
func TestMulRoundAddsHalfThenFloors(t *testing.T) {
	floorDiv := func(a, b int64) int64 {
		q := a / b
		if a%b != 0 && (a < 0) != (b < 0) {
			q--
		}
		return q
	}
	discriminating := 0
	for _, magnitude := range []int32{1, 3, 400, 800, 65536} {
		for step := 0; step < 512; step++ {
			a := Angle(uint16(step * 128))
			for _, v := range []int32{Sin(a), Cos(a)} {
				sum := int64(v)*int64(magnitude) + 4096
				want := floorDiv(sum, 8192)
				if got := int64(MulRound(v, magnitude)); got != want {
					t.Fatalf("MulRound(%d, %d) = %d, want %d (add half, then floor) [04 R-MOV-01 §4]", v, magnitude, got, want)
				}
				if sum/8192 != want {
					discriminating++
				}
			}
		}
	}
	if discriminating == 0 {
		t.Fatal("the sweep never reached a product where flooring and truncating toward zero disagree; it proves nothing")
	}
	// One readable pin: the table's negative extreme times two, plus half the
	// scale, lands on -1.5 scale units. The arithmetic shift gives -2; a
	// truncating conversion would give -1.
	if got := MulRound(-8192, 2); got != -2 {
		t.Fatalf("MulRound(-8192, 2) = %d, want -2 [04 §5.3]", got)
	}
}
