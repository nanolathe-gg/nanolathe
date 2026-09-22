package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Ordinary range keeps the authored range square in a signed 32-bit word.
// The last non-wrapping positive square is 46,340²; the next square is
// negative, so even a nearby point fails the signed comparison [06 §3.3].
func TestWithinRangeKeepsSignedRangeSquareWidth(t *testing.T) {
	zero := numeric.Fixed(0)
	if !WithinRange(zero, zero, fixed(100), zero, 46340) {
		t.Fatal("the last positive squared range rejected a nearby point")
	}
	if WithinRange(zero, zero, fixed(1), zero, 46341) {
		t.Fatal("a wrapped-negative squared range admitted a nearby point")
	}
	if WithinRange(zero, zero, fixed(100), zero, 50000) {
		t.Fatal("range 50,000 was compared after widening instead of signed 32-bit wrap")
	}
}

// There is no sign rejection before the established square. A negative range
// whose square stays positive therefore has the same reach as its magnitude
// [06 §3.3].
func TestWithinRangeSquaresNegativeRange(t *testing.T) {
	zero := numeric.Fixed(0)
	if !WithinRange(zero, zero, fixed(50), zero, -100) {
		t.Fatal("negative range was rejected before its signed 32-bit square")
	}
	if !WithinRange(zero, zero, fixed(100), zero, -100) {
		t.Fatal("negative range rejected the inclusive boundary of its square")
	}
	if WithinRange(zero, zero, fixed(101), zero, -100) {
		t.Fatal("negative range admitted a point beyond the squared magnitude")
	}
}

// Coordinate subtraction wraps before the wide square. Crossing the signed
// 32-bit boundary here leaves a two-world-unit delta, rather than a nearly
// 65,536-world-unit delta [06 §3.3].
func TestWithinRangeWrapsCoordinateSubtractionBeforeSquaring(t *testing.T) {
	shooter := numeric.FixedFromRaw(1<<31 - 1)
	candidate := numeric.FixedFromRaw(-1<<31 + 2*numeric.FractionOne - 1)
	if WithinRange(shooter, 0, candidate, 0, 1) {
		t.Fatal("wrapped two-unit coordinate delta admitted at range one")
	}
	if !WithinRange(shooter, 0, candidate, 0, 2) {
		t.Fatal("wrapped two-unit coordinate delta missed its inclusive range boundary")
	}
}

// Each maximum-magnitude axis contributes 1<<30 after the wide square and
// shift. Their signed 32-bit sum wraps negative before comparison [06 §3.3].
func TestWithinRangeWrapsAxisTermSum(t *testing.T) {
	extreme := numeric.FixedFromRaw(-1 << 31)
	if !WithinRange(0, 0, extreme, extreme, 0) {
		t.Fatal("axis terms were widened instead of summed as signed 32-bit values")
	}
}
