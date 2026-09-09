package cob

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// floorDiv is the reference the retail sequence reduces to: the 64-bit sum is
// shifted right arithmetically, which floors. It is written out here rather
// than reusing the helper under test so the test can disagree with it.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// TestTrigScalarFloorsAfterAddingHalf locks the rounding [04 §5.3]
// [04 R-MOV-01 §4] closed on 2026-09-04, retiring the open-question marker that
// stood on trigScalar. The two shared component routines RockUnit and
// HitByWeapon call form a signed full-width product of the signed 16-bit table
// entry and the magnitude, add half the 8192 scale to the 64-bit sum, and
// return the low word of an ARITHMETIC right shift by 13. There is no divide
// and no float-to-integer conversion in either body, so the result FLOORS —
// a negative product rounds toward negative infinity, not toward zero.
//
// The sweep is over the real table so the assertion is about the values the
// callbacks actually pass, and the discriminating counter is what makes it a
// test rather than a tautology: it fails if the sweep never visits a case
// where flooring and truncation disagree.
func TestTrigScalarFloorsAfterAddingHalf(t *testing.T) {
	discriminating := 0
	for _, scalar := range []int32{800, 400} { // RockUnit, HitByWeapon [04 §5.3]
		for step := 0; step < 512; step++ {
			a := numeric.Angle(uint16(step * 128))
			for _, v := range []int32{numeric.Sin(a), numeric.Cos(a)} {
				sum := int64(v)*int64(scalar) + 4096
				want := floorDiv(sum, 8192)
				if got := int64(trigScalar(v, scalar)); got != want {
					t.Fatalf("trigScalar(%d, %d) = %d, want %d (add half, then floor) [04 §5.3]", v, scalar, got, want)
				}
				if trunc := sum / 8192; trunc != want {
					discriminating++
				}
			}
		}
	}
	if discriminating == 0 {
		t.Fatal("the sweep never reached a product where flooring and truncating toward zero disagree; it proves nothing")
	}
}

// TestTrigScalarNegativeCaseIsNotTruncation pins one explicit value so the
// contract is readable without running the sweep: an exact table extreme times
// three, plus half the scale, lands on -2.5 scale units. Retail's arithmetic
// shift gives -3; a truncating conversion would give -2. The old marker
// treated the "+0.5 bias on negatives" as a risk to be validated away; it is
// the behavior being cloned [04 §5.3].
func TestTrigScalarNegativeCaseIsNotTruncation(t *testing.T) {
	if got := trigScalar(-8192, 3); got != -3 {
		t.Fatalf("trigScalar(-8192, 3) = %d, want -3 (floor of -2.5), not -2 [04 §5.3]", got)
	}
	if got := trigScalar(8192, 3); got != 3 {
		t.Fatalf("trigScalar(8192, 3) = %d, want 3 (floor of 3.5) [04 §5.3]", got)
	}
}
