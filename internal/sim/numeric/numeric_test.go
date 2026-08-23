package numeric

import "testing"

// TestFixedTruncatesTowardZero locks INVARIANTS I3: narrowing follows retail's
// __ftol, which truncates toward zero — not floor, not round. Go's integer
// division already does this; the test exists so a future "fix" that reaches
// for math.Floor fails loudly.
func TestFixedTruncatesTowardZero(t *testing.T) {
	half := Fixed(FractionOne / 2)
	three := FixedFromInt(3)

	if got := half.Mul(three); got != Fixed(FractionOne+FractionOne/2) {
		t.Fatalf("0.5 * 3 = %d raw, want %d", got, FractionOne+FractionOne/2)
	}
	// -7/2 truncates to -3.5 exactly in 16.16; the interesting case is the
	// sub-unit remainder, which must vanish toward zero on both signs.
	negative := FixedFromInt(-7)
	if quotient, ok := negative.Div(FixedFromInt(2)); !ok || quotient != Fixed(-7*FractionOne/2) {
		t.Fatalf("-7/2 = %d raw, want %d", quotient, -7*FractionOne/2)
	}
	if got := Fixed(-1).Int(); got != 0 {
		t.Fatalf("Fixed(-1).Int() = %d, want 0 (truncation toward zero)", got)
	}
	if _, ok := FixedFromInt(1).Div(0); ok {
		t.Fatal("division by zero reported success")
	}
}
