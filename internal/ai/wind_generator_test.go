package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestWindGeneratorZeroingComparesMaxWindStrictlyBelow2500 locks the third
// zeroing branch of [08 R-P0-05 §9]: zero the first coefficient when the
// definition's `windgenerator` compares not equal to floating zero AND the
// map's maximum wind word is strictly less than the wind divisor divided by
// two — a signed integer divide of the compiled-in 5000 [05 R-PROD-01 §3], so
// the threshold is 2500. The comparison is strict and integer, and the operand
// is the map's maximum wind word, not the live wind scalar.
func TestWindGeneratorZeroingComparesMaxWindStrictlyBelow2500(t *testing.T) {
	generator := &content.UnitDef{UnitName: "windgen", WindGenerator: 25}
	plain := &content.UnitDef{UnitName: "solar"}
	for _, tc := range []struct {
		name    string
		def     *content.UnitDef
		maxWind int32
		bound   bool
		want    bool
	}{
		{"unbound word never fires", generator, 0, false, false},
		{"canonical fallback is above the threshold", generator, 2000, true, true},
		{"exactly the threshold does not fire", generator, 2500, true, false},
		{"one below the threshold fires", generator, 2499, true, true},
		{"above the threshold does not fire", generator, 5000, true, false},
		{"authored zero maximum fires", generator, 0, true, true},
		{"a non-generator never fires", plain, 0, true, false},
	} {
		var s Strategic
		if tc.bound {
			s.SetMaxWind(tc.maxWind)
		}
		if got := s.windGeneratorSuppressed(tc.def); got != tc.want {
			t.Fatalf("%s: maxWind %d windgenerator %v -> %v, want %v", tc.name, tc.maxWind, tc.def.WindGenerator, got, tc.want)
		}
	}

	// A negative argument is a setup error and leaves the word unbound, the
	// same shape SetUnitLimit uses.
	var unbound Strategic
	unbound.SetMaxWind(-1)
	if _, ok := unbound.MaxWind(); ok {
		t.Fatal("a negative maximum wind must not bind")
	}
}

// TestWindGeneratorZeroingReachesTheOtherMixCoefficient locks that the branch
// is applied to the class routine's first coefficient, after the multipliers
// and the half-capacity addend and before the clamp [08 R-P0-05 §9].
func TestWindGeneratorZeroingReachesTheOtherMixCoefficient(t *testing.T) {
	newDef := func() *content.UnitDef {
		return &content.UnitDef{
			UnitName:        "windgen",
			BuildCostMetal:  100,
			BuildCostEnergy: 100,
			WindGenerator:   25,
			Builder:         true, // the +30 that makes the other-mix accumulator non-zero
			MinWaterDepth:   -1,   // negative: no multiply-by-three [08 R-AI-03 §6]
		}
	}
	key := content.CanonicalKey("windgen")

	windy := halfCapacityStrategic(t, newDef())
	windy.SetMaxWind(2500)
	windy.recomputeClassVectors()
	if windy.ClassVectors[key].C0 == 0 {
		t.Fatal("fixture sanity: the other-mix coefficient must be non-zero above the threshold")
	}

	calm := halfCapacityStrategic(t, newDef())
	calm.SetMaxWind(2499)
	calm.recomputeClassVectors()
	if got := calm.ClassVectors[key].C0; got != 0 {
		t.Fatalf("wind generator below the threshold kept coefficient %d, want 0 [08 R-P0-05 §9]", got)
	}
}
