// Regression test for review finding R04: nanoIsqrt's old doubling search grew
// a candidate `r` by repeated `r*r` until the square overflowed signed 64-bit
// and wrapped to zero, at which point the loop's own `r*r <= v` condition
// could never again become false — an infinite loop for any radicand at or
// above 2^62. That radicand is reachable from isWithinNanoRange
// [05 R-WORK-01 §2][05 R-WORK-01 §12] on ordinary planar deltas: a builder and
// a construction site 25,000 world units apart on each axis already exceeds
// it. This file locks the floor-square-root contract with a big.Int reference
// and exercises the exact reachable radicand.
package construction

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
	"time"
)

// bigIsqrt is the reference floor(sqrt(v)) computed with math/big, independent
// of nanoIsqrt's own algorithm.
func bigIsqrt(v int64) int64 {
	if v <= 0 {
		return 0
	}
	b := new(big.Int).SetInt64(v)
	return new(big.Int).Sqrt(b).Int64()
}

func TestNanoIsqrtBoundaryTable(t *testing.T) {
	cases := []int64{
		0, 1, 2, 3, 4, 15, 16, 17,
		1<<32 - 1, 1 << 32, 1<<32 + 1,
		(1<<31 - 1) * (1<<31 - 1),
		(1<<31-1)*(1<<31-1) - 1,
		(1<<31-1)*(1<<31-1) + 1,
		1<<62 - 1, 1 << 62, 1<<62 + 1,
		math.MaxInt64,
	}
	for _, v := range cases {
		got := nanoIsqrt(v)
		want := bigIsqrt(v)
		if got != want {
			t.Errorf("nanoIsqrt(%d) = %d, want %d (floor sqrt)", v, got, want)
			continue
		}
		// floor(sqrt(v)): got*got <= v < (got+1)*(got+1), checked in big.Int to
		// avoid the very overflow this function exists to avoid.
		bv := new(big.Int).SetInt64(v)
		bg := new(big.Int).SetInt64(got)
		sq := new(big.Int).Mul(bg, bg)
		if sq.Cmp(bv) > 0 {
			t.Errorf("nanoIsqrt(%d) = %d, but %d*%d > %d", v, got, got, got, v)
		}
		next := new(big.Int).Add(bg, big.NewInt(1))
		nextSq := new(big.Int).Mul(next, next)
		if nextSq.Cmp(bv) <= 0 {
			t.Errorf("nanoIsqrt(%d) = %d, but (%d+1)^2 <= %d", v, got, got, v)
		}
	}
}

// TestNanoIsqrtRandomAgainstBigInt cross-checks a spread of random radicands,
// including values that straddle the old implementation's 2^62 failure point,
// against the math/big reference.
func TestNanoIsqrtRandomAgainstBigInt(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		var v int64
		switch i % 4 {
		case 0:
			v = rng.Int63()
		case 1:
			v = rng.Int63n(1 << 32)
		case 2:
			// straddle 2^62, the old implementation's hang threshold.
			v = (1 << 62) - 1000 + rng.Int63n(2000)
		default:
			v = rng.Int63n(1 << 16)
		}
		if got, want := nanoIsqrt(v), bigIsqrt(v); got != want {
			t.Fatalf("nanoIsqrt(%d) = %d, want %d", v, got, want)
		}
	}
}

// TestNanoIsqrtNonPositive checks the non-positive short-circuit is preserved.
func TestNanoIsqrtNonPositive(t *testing.T) {
	for _, v := range []int64{0, -1, -1000, math.MinInt64} {
		if got := nanoIsqrt(v); got != 0 {
			t.Errorf("nanoIsqrt(%d) = %d, want 0", v, got)
		}
	}
}

// TestNanoIsqrtReachableConstructionRadicand exercises the exact case that
// hung the old doubling search: a builder and a construction site separated
// by 25,000 world units on each of the X and Z axes, in 16.16 fixed point, as
// isWithinNanoRange forms via nanoRadicand(dx, dz) [05 R-WORK-01 §2]. The old
// implementation never returned from this call; the fix must return promptly
// and match the exact reference floor(sqrt(v)).
func TestNanoIsqrtReachableConstructionRadicand(t *testing.T) {
	const worldUnitsPerAxis = 25000
	const fixedShift = 16
	d := int64(worldUnitsPerAxis) << fixedShift
	radicand := nanoRadicand(d, d)
	const want = int64(5368709120000000000)
	if radicand != want {
		t.Fatalf("nanoRadicand(%d, %d) = %d, want %d (2*(25000<<16)^2)", d, d, radicand, want)
	}
	if radicand <= (1 << 62) {
		t.Fatalf("radicand %d does not exceed 2^62; test no longer exercises the hang threshold", radicand)
	}
	if radicand == math.MaxInt64 {
		t.Fatalf("radicand %d unexpectedly saturated at MaxInt64 for an in-range separation", radicand)
	}

	done := make(chan int64, 1)
	go func() { done <- nanoIsqrt(radicand) }()
	select {
	case got := <-done:
		want := bigIsqrt(radicand)
		if got != want {
			t.Fatalf("nanoIsqrt(%d) = %d, want %d", radicand, got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nanoIsqrt hung on the 25,000-world-unit-per-axis radicand (the R04 defect)")
	}
}

// TestNanoRadicandSaturatesWithoutWrapping exercises the caller-side overflow
// guard directly: inputs large enough that dx*dx or the sum would wrap signed
// 64-bit must saturate at math.MaxInt64 rather than produce a small or
// negative radicand. These magnitudes are far beyond any documented or
// loadable map extent (see the nanoRadicand doc comment) and stand in for a
// malformed or otherwise out-of-band coordinate.
func TestNanoRadicandSaturatesWithoutWrapping(t *testing.T) {
	const big63 = int64(1) << 32 // squared alone is 2^64, already past int64 range
	cases := []struct{ dx, dz int64 }{
		{big63, big63},
		{math.MaxInt64, 1},
		{math.MaxInt64, math.MaxInt64},
		{math.MinInt64, 0}, // MinInt64 has no positive negation in two's complement
		{math.MinInt64, math.MinInt64},
	}
	for _, c := range cases {
		got := nanoRadicand(c.dx, c.dz)
		if got != math.MaxInt64 {
			t.Errorf("nanoRadicand(%d, %d) = %d, want saturation at MaxInt64", c.dx, c.dz, got)
		}
		if got < 0 {
			t.Errorf("nanoRadicand(%d, %d) = %d, wrapped negative", c.dx, c.dz, got)
		}
	}
}
