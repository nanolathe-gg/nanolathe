package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// newCostDef is a definition whose only class-routine inputs are the two build
// costs: the accumulator stays at one and the weapon budget at one, so the
// stored first-pass byte is the second truncation plus one.
func newCostDef(name string, metal, energy float32) *content.UnitDef {
	return &content.UnitDef{
		UnitName:        name,
		BuildCostMetal:  metal,
		BuildCostEnergy: energy,
		MinWaterDepth:   -1,
	}
}

// TestFirstPassHoldsRetailWorkingPrecision locks [08 "Arithmetic and clamping"]:
// retail accumulates the first pass at its 53-bit working precision with the two
// truncations as its only boundaries, and multiplies the costs by
// single-precision constants.
//
// The costs in the table are the discriminating ones. The single-precision
// hundredth is slightly BELOW 1/100, so a metal cost that is a non-zero multiple
// of 100 puts the exact product just under an integer: retail keeps the deficit
// and truncates down, while a float32 sum rounds up onto the integer and
// truncates one higher. Exactly five shipped definitions have such a cost, and
// these are their three distinct metal costs — each row is one byte lower than
// the float32 form produces, which is what this test exists to catch.
//
// The two-thousandth's error runs the other way (the single-precision constant
// is ABOVE 1/500), so an energy cost that is a multiple of 500 lands just above
// an integer and both forms truncate alike; the last two rows lock that the
// second truncation does not move.
func TestFirstPassHoldsRetailWorkingPrecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		metal  float32
		energy float32
		want   int8
	}{
		// acc starts at 1 and an unarmed definition's weapon budget is 1, so the
		// stored byte is 1 + t1.
		{"metal 100", 100, 985, 3},    // t0 = trunc(1.99999998) = 1, t1 = trunc(2.97) = 2
		{"metal 100 b", 100, 1757, 5}, // t0 = 1, t1 = trunc(4.514) = 4
		{"metal 300", 300, 5784, 15},  // t0 = trunc(3.99999993) = 3, t1 = trunc(14.568) = 14
		{"metal 600", 600, 750, 8},    // t0 = trunc(6.99999987) = 6, t1 = trunc(7.5) = 7
		{"metal 600 b", 600, 1100, 9}, // t0 = 6, t1 = trunc(8.2) = 8
		// Energy costs that are multiples of 500: the second truncation agrees
		// in both forms, so these rows must not move when the first pass widens.
		{"energy 500", 0, 500, 3},    // t0 = 1, t1 = trunc(2.0000000475) = 2
		{"energy 5000", 0, 5000, 12}, // t0 = 1, t1 = trunc(11.0000005) = 11
		// A metal cost that is not a multiple of 100 is unaffected either way.
		{"metal 250", 250, 0, 4}, // t0 = trunc(3.5) = 3
	} {
		t.Run(tc.name, func(t *testing.T) {
			single, _ := classVectorFor(t, newCostDef(tc.name, tc.metal, tc.energy))
			if single != tc.want {
				t.Fatalf("single coefficient = %d, want %d [08 \"Arithmetic and clamping\"]", single, tc.want)
			}
		})
	}
}

// TestFirstPassFloat32FormDiffersOnHundredMultiples records why the widening is
// required rather than cosmetic: the float32 first pass really does produce a
// different byte, and only for a metal cost that is a non-zero multiple of 100.
// The replica below is the form the routine used to carry; it is here to prove
// the table above discriminates, not as a second implementation.
func TestFirstPassFloat32FormDiffersOnHundredMultiples(t *testing.T) {
	firstPassFloat32 := func(metal, energy float32) int32 {
		t0 := ftol(float32(1) + metal*float32(0.01))
		return ftol(float32(t0) + energy*float32(0.002))
	}
	for _, tc := range []struct {
		name         string
		metal        float32
		energy       float32
		wantDiffered bool
	}{
		{"metal 100", 100, 985, true},
		{"metal 300", 300, 5784, true},
		{"metal 600", 600, 750, true},
		{"metal 250", 250, 0, false},
		{"energy 500", 0, 500, false},
		{"energy 5000", 0, 5000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			single, _ := classVectorFor(t, newCostDef(tc.name, tc.metal, tc.energy))
			narrow := int8(1 + firstPassFloat32(tc.metal, tc.energy))
			if differed := narrow != single; differed != tc.wantDiffered {
				t.Fatalf("float32 form = %d, working precision = %d; differed = %v, want %v [08 \"Arithmetic and clamping\"]",
					narrow, single, differed, tc.wantDiffered)
			}
			if tc.wantDiffered && narrow != single+1 {
				t.Fatalf("float32 form = %d, want exactly one above %d [08 \"Arithmetic and clamping\"]", narrow, single)
			}
		})
	}
}
