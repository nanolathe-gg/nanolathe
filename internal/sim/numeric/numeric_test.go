package numeric

import (
	"math"
	"testing"
)

// TestNarrowingTruncatesTowardZero locks the __ftol half of I3: narrowing a
// value to whole units truncates toward zero, not floor and not round
// [01 §8].
func TestNarrowingTruncatesTowardZero(t *testing.T) {
	if got := Fixed(-1).Int(); got != 0 {
		t.Fatalf("Fixed(-1).Int() = %d, want 0 (truncate toward zero)", got)
	}
	if got := Fixed(FractionOne + FractionOne/2).Int(); got != 1 {
		t.Fatalf("1.5 -> %d, want 1", got)
	}
	if got := Fixed(-FractionOne - FractionOne/2).Int(); got != -1 {
		t.Fatalf("-1.5 -> %d, want -1 (toward zero, not -2)", got)
	}
}

// TestFloorIsNotTruncation locks the other half of I3: the arithmetic-shift
// path floors, and the two disagree on exactly the negative fractions that sit
// on the map's west and north edges [03 §2.1].
func TestFloorIsNotTruncation(t *testing.T) {
	if got := Fixed(-1).Floor(); got != -1 {
		t.Fatalf("Fixed(-1).Floor() = %d, want -1", got)
	}
	if Fixed(-1).Int() == Fixed(-1).Floor() {
		t.Fatal("truncation and floor must disagree at -1; the distinction is I3")
	}
}

// TestMulFloors locks the R12 decision. A fixed-by-fixed multiply shifts the
// full-width product down arithmetically, so it floors. Using Go's `/` here
// would truncate toward zero and disagree on every negative product carrying a
// fraction. Established, not inference: [04 §7.2] forms the A* heuristic scale
// as "a full signed 64-bit multiplication ... arithmetically shifted", and
// [04 R-MOV-01 §3] writes the same shape per axis.
func TestMulFloors(t *testing.T) {
	half := Fixed(FractionOne / 2)
	if got := half.Mul(FixedFromInt(3)); got != Fixed(FractionOne+FractionOne/2) {
		t.Fatalf("0.5 * 3 = %d raw, want %d", got, FractionOne+FractionOne/2)
	}
	// The smallest negative fraction: -1 raw times 0.5 is -0.5 of a raw unit.
	// Flooring gives -1; truncating toward zero would give 0.
	if got := Fixed(-1).Mul(half); got != Fixed(-1) {
		t.Fatalf("(-1 raw) * 0.5 = %d, want -1 (floor, not 0)", got)
	}
	// Sign symmetry is deliberately absent: +1 raw times 0.5 floors to 0.
	if got := Fixed(1).Mul(half); got != 0 {
		t.Fatalf("(1 raw) * 0.5 = %d, want 0", got)
	}
}

// TestDivTruncates: division is an idiv, not a shift, so it truncates toward
// zero. The asymmetry with Mul is the hardware's.
func TestDivTruncates(t *testing.T) {
	if got, ok := FixedFromInt(-7).Div(FixedFromInt(2)); !ok || got != Fixed(-7*FractionOne/2) {
		t.Fatalf("-7/2 = %d raw, want %d", got, -7*FractionOne/2)
	}
	if _, ok := FixedFromInt(1).Div(0); ok {
		t.Fatal("division by zero reported success")
	}
}

// TestAngleWraps: angles are uint16 with 65,536 per circle, so arithmetic wraps
// rather than saturating [04 §5.1].
func TestAngleWraps(t *testing.T) {
	if got := Angle(65535).Add(Angle(2)); got != 1 {
		t.Fatalf("65535 + 2 = %d, want 1", got)
	}
	if got := Angle(1).Sub(Angle(2)); got != 65535 {
		t.Fatalf("1 - 2 = %d, want 65535", got)
	}
}

func TestAngleFromAtan2RetailTable(t *testing.T) {
	tests := []struct {
		name   string
		first  int64
		second int64
		want   Angle
	}{
		{"+Z", 0, 1, 0},
		{"+X", 1, 0, 16384},
		{"-Z", 0, -1, 32768},
		{"-X", -1, 0, 49152},
		{"+X+Z", 1, 1, 8192},
		{"+X-Z", 1, -1, 24576},
		{"-X-Z", -1, -1, 40960},
		{"-X+Z", -1, 1, 57344},
		{"negative wrap", -1, 2, 60700},
	}
	for _, test := range tests {
		if got := AngleFromAtan2(test.first, test.second); got != test.want {
			t.Errorf("AngleFromAtan2(%d, %d) = %d, want %d (%s)", test.first, test.second, got, test.want, test.name)
		}
	}

	// The narrowing instruction is round-to-nearest-even, including negative
	// values; these are the two tie parities in each direction [01 §8].
	for _, test := range []struct {
		in, want float64
	}{
		{0.5, 0}, {1.5, 2}, {-0.5, 0}, {-1.5, -2},
	} {
		if got := math.RoundToEven(test.in); got != test.want {
			t.Errorf("round-to-even(%v) = %v, want %v", test.in, got, test.want)
		}
	}
}
