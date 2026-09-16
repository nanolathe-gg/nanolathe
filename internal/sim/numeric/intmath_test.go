package numeric

import (
	"math"
	"testing"
)

// TestFloorDivRoundsTowardNegativeInfinity locks the sign correction that
// distinguishes FloorDiv from Go's truncating `/` [03 §2.1] [I3].
func TestFloorDivRoundsTowardNegativeInfinity(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{7, 2, 3}, {-7, 2, -4}, {7, -2, -4}, {-7, -2, 3},
		{6, 3, 2}, {-6, 3, -2}, {-1, 1 << 20, -1}, {0, 5, 0},
	}
	for _, c := range cases {
		if got := FloorDiv(c.a, c.b); got != c.want {
			t.Errorf("FloorDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := FloorDiv(int32(c.a), int32(c.b)); got != int32(c.want) {
			t.Errorf("FloorDiv[int32](%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// Exhaustive agreement with the definition over a small window.
	for a := int64(-40); a <= 40; a++ {
		for b := int64(-9); b <= 9; b++ {
			if b == 0 {
				continue
			}
			want := int64(math.Floor(float64(a) / float64(b)))
			if got := FloorDiv(a, b); got != want {
				t.Fatalf("FloorDiv(%d, %d) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestAbs(t *testing.T) {
	if Abs(int32(-5)) != 5 || Abs(int32(5)) != 5 || Abs(int64(0)) != 0 || Abs(-3) != 3 {
		t.Fatal("Abs disagrees with its definition")
	}
	if Abs(Fixed(-FixedOne)) != FixedOne {
		t.Fatal("Abs must accept named integer types")
	}
}

// TestISqrt64 locks the floor root against the exact definition, including
// the overflow-prone top of the range the pre-shift exists for.
func TestISqrt64(t *testing.T) {
	for v := int64(0); v <= 70000; v++ {
		r := ISqrt64(v)
		if r*r > v || (r+1)*(r+1) <= v {
			t.Fatalf("ISqrt64(%d) = %d", v, r)
		}
	}
	// Above 2^62 the naive first digit overflows root+digit; the pre-shift
	// keeps the search exact. (root+1)^2 overflows int64 near the top, so the
	// upper bound is checked against the known roots instead.
	if got := ISqrt64(1 << 62); got != 1<<31 {
		t.Fatalf("ISqrt64(2^62) = %d, want 2^31", got)
	}
	if got := ISqrt64((1 << 62) + 1); got != 1<<31 {
		t.Fatalf("ISqrt64(2^62+1) = %d, want 2^31", got)
	}
	if got := ISqrt64(math.MaxInt64); got != 3037000499 {
		t.Fatalf("ISqrt64(MaxInt64) = %d, want 3037000499", got)
	}
	if ISqrt64(-4) != 0 {
		t.Fatal("negative radicand must yield zero")
	}
}
