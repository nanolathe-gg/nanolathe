package combat

import (
	"math"
	"testing"
)

// The Go specification lets a compiler fuse `x*y + z` into one operation that
// rounds once instead of twice. Retail's x87 has no fused multiply-add: every
// product is rounded to the working precision before it is added. These tests
// pin the two-rounding result at inputs where the one-rounding result differs,
// so the contract holds on every backend rather than only on the ones that do
// not fuse today (default amd64 does not; arm64 and GOAMD64=v3 do).
//
// math.FMA is the one-rounding value by definition, so each case states both
// answers and asserts which one the engine computes.

func TestFalloffRoundsTheProductBeforeTheEdgeTerm(t *testing.T) {
	// Each row is a distance, a blast radius and an edge effectiveness for
	// which the separately rounded expression and the fused one land on
	// different float32 values. `[06 §9.3]`: the square is formed first, scaled
	// by one minus edge, added to edge, and narrowed by ONE store at the end.
	cases := []struct {
		d, r, edge float32
	}{
		{113, 565, 0.44421127},
		{9, 27, 0.27463117},
		{11, 14, 0.07164858},
		{5, 15, 0.13593307},
		{2, 6, 0.26469627},
		{1, 3, 0.2791802},
	}
	for _, c := range cases {
		f := float64(c.d)/float64(c.r) - 1
		edge := float64(c.edge)
		separate := float32(float64(f*f*(1-edge)) + edge)
		fused := float32(math.FMA(f*f, 1-edge, edge))
		if separate == fused {
			t.Fatalf("Falloff(%v, %v, %v): the two roundings agree here, so the case proves nothing", c.d, c.r, c.edge)
		}
		if got := Falloff(c.d, c.r, c.edge); got != separate {
			t.Errorf("Falloff(%v, %v, %v) = %v (%#08x), want the separately rounded %v (%#08x); the fused value is %v (%#08x)",
				c.d, c.r, c.edge, got, math.Float32bits(got),
				separate, math.Float32bits(separate), fused, math.Float32bits(fused))
		}
	}
}

func TestDistance3DRawRoundsEachSquareBeforeSumming(t *testing.T) {
	// Full-range raw 16.16 deltas square past 53 bits, so the squares are
	// inexact and the fused sum reaches the truncated integer distance that
	// divides the weapon velocity into the integer flight time `[06 §3.3]`.
	cases := []struct {
		dx, dy, dz int32
		want       int64
		fused      int64
	}{
		{-1038152790, -1624317820, 1605371681, -1786304739, -1786304738},
		{-2000947392, 1451208802, -178967995, -1816696327, -1816696328},
	}
	for _, c := range cases {
		if c.want == c.fused {
			t.Fatalf("distance3DRaw(%d, %d, %d): the two roundings agree here", c.dx, c.dy, c.dz)
		}
		if got := distance3DRaw(c.dx, c.dy, c.dz); got != c.want {
			t.Errorf("distance3DRaw(%d, %d, %d) = %d, want the separately rounded %d (the fused value is %d)",
				c.dx, c.dy, c.dz, got, c.want, c.fused)
		}
	}
}
