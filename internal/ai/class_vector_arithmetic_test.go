package ai

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// classVectorFor runs the class routine over one definition and returns its
// first-pass coefficient and its class triple. The wind environment is bound to
// a still map so the net-energy query reads only the definition's own fields
// [05 R-PROD-01 §1].
func classVectorFor(t *testing.T, def *content.UnitDef) (int8, ClassVector) {
	t.Helper()
	key := content.CanonicalKey(def.UnitName)
	def.CanonicalKey = key
	s := &Strategic{Catalog: &content.Catalog{Units: map[string]*content.UnitDef{key: def}}}
	s.BindEnergyEnvironment(func() (float32, float32) { return 0, 0 })
	s.Init([]string{key})
	return s.SingleVectors[key], s.ClassVectors[key]
}

// TestFirstPassAddsTheTwoCostTerms locks [08 R-P0-05 §5]: retail multiplies each
// build cost by a negative constant and subtracts that product, so the net
// effect is +0.01 x metal cost and +0.002 x energy cost. Under the subtracting
// reading an expensive definition pins at -100 instead of +100, which is the
// defect this test exists to catch. Both directions: an expensive definition
// rails high, a cheap one stays low, and the ordering is monotone in cost.
func TestFirstPassAddsTheTwoCostTerms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		metal  float32
		energy float32
		want   int8
	}{
		// acc starts at 1; an unarmed definition's weapon budget is 1.
		{"free", 0, 0, 2},
		// 400 is a multiple of 100, so the exact product lands just under 4 and
		// the working-precision sum truncates to 4, not 5
		// [08 "Arithmetic and clamping"].
		{"cheap metal", 400, 0, 5},         // trunc(4.99999991) = 4, + 1
		{"cheap energy", 0, 2000, 6},       // 1 + 4.0000002 = 5, + 1
		{"both", 400, 2000, 9},             // trunc(4.99999991) = 4, trunc(8.0000002) = 8, + 1
		{"expensive", 100000, 0, 100},      // 1 + 1000 clamps at the upper bound
		{"very expensive", 0, 500000, 100}, // 1 then +1000 clamps at the upper bound
	} {
		t.Run(tc.name, func(t *testing.T) {
			single, _ := classVectorFor(t, &content.UnitDef{
				UnitName:        "cost",
				BuildCostMetal:  tc.metal,
				BuildCostEnergy: tc.energy,
				MinWaterDepth:   -1,
			})
			if single != tc.want {
				t.Fatalf("single coefficient = %d, want %d [08 R-P0-05 §5]", single, tc.want)
			}
		})
	}
}

// TestOtherMixAccumulatorStartsAtOneAndCanAttackReplacesIt locks [08 R-P0-05 §5]:
// the accumulator's initial value is 1, not 0, and can-attack overwrites it with
// 21 rather than adding to it. Both are visible through the zero-count times-four
// multiplier: 1*4 = 4 against 21*4 = 84.
func TestOtherMixAccumulatorStartsAtOneAndCanAttackReplacesIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canAttack bool
		want      int8
	}{
		{"plain definition starts at one", false, 4},
		{"can-attack replaces it with 21", true, 84},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cv := classVectorFor(t, &content.UnitDef{
				UnitName:      "startvalue",
				CanAttack:     tc.canAttack,
				MinWaterDepth: -1,
			})
			if cv.C0 != tc.want {
				t.Fatalf("other-mix = %d, want %d [08 R-P0-05 §5]", cv.C0, tc.want)
			}
		})
	}
}

// TestOtherMixFoldsClampedEnergyMake locks the routine's ninth input
// [08 R-P0-05 §5]: the definition's passive `energymake`, clamped below at zero
// and above at THIRTY, added in floating point before the single truncation, so
// the zero-count times-four multiplier scales the folded sum. The upper bound is
// 30, not 100: a fusion plant authoring 1000 gains exactly 30.
func TestOtherMixFoldsClampedEnergyMake(t *testing.T) {
	for _, tc := range []struct {
		name       string
		energyMake float64
		want       int8
	}{
		{"absent", 0, 4},                   // trunc(1 + 0) * 4
		{"fractional truncates", 2.75, 12}, // trunc(1 + 2.75) = 3, * 4
		{"solar collector", 15, 64},        // trunc(1 + 15) = 16, * 4
		{"at the upper bound", 30, 100},    // trunc(1 + 30) = 31, * 4 = 124, clamped
		{"far above the bound", 1000, 100},
		{"negative clamps to zero", -50, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cv := classVectorFor(t, &content.UnitDef{
				UnitName:      "energymake",
				EnergyMake:    tc.energyMake,
				MinWaterDepth: -1,
			})
			if cv.C0 != tc.want {
				t.Fatalf("other-mix = %d, want %d [08 R-P0-05 §5]", cv.C0, tc.want)
			}
		})
	}

	// Below the upper bound the term is not saturated, so the coefficient must
	// still separate two producers. 20 and 25 differ; 30 and 1000 do not.
	_, twenty := classVectorFor(t, &content.UnitDef{UnitName: "e20", EnergyMake: 20, MinWaterDepth: -1})
	_, twentyFive := classVectorFor(t, &content.UnitDef{UnitName: "e25", EnergyMake: 25, MinWaterDepth: -1})
	if twenty.C0 == twentyFive.C0 {
		t.Fatalf("energymake 20 and 25 must differ, both %d [08 R-P0-05 §5]", twenty.C0)
	}
	_, thirty := classVectorFor(t, &content.UnitDef{UnitName: "e30", EnergyMake: 30, MinWaterDepth: -1})
	_, thousand := classVectorFor(t, &content.UnitDef{UnitName: "e1000", EnergyMake: 1000, MinWaterDepth: -1})
	if thirty.C0 != thousand.C0 {
		t.Fatalf("energymake 30 and 1000 must agree at the clamp: %d and %d [08 R-P0-05 §5]", thirty.C0, thousand.C0)
	}
}

// TestOtherMixHasNoLowerClamp locks [08 R-P0-05 §5]: `base` is clamped above at
// 100 and not below, so the half-capacity addend can leave it negative and a
// negative `base` does reach the candidate score.
//
// The absence of a lower bound at -100 is contract rather than observable
// arithmetic: the half-capacity addend's range is [-50, +50] and the value it
// joins is at least one, so the pre-store value can never fall below -100 and a
// spurious [-100, 100] clamp would agree here. What this test does forbid is
// the other plausible mistake — a lower bound at zero, matching the energy and
// metal coefficients — which would erase the negative value entirely.
func TestOtherMixHasNoLowerClamp(t *testing.T) {
	// The first-pass coefficient reaches -100 with a large negative authored
	// metal cost; half of it is -50, and the accumulator before it is 1*4 = 4.
	def := &content.UnitDef{UnitName: "nolower", BuildCostMetal: -100000, MinWaterDepth: -1}
	key := content.CanonicalKey(def.UnitName)
	def.CanonicalKey = key
	s := &Strategic{Catalog: &content.Catalog{Units: map[string]*content.UnitDef{key: def}}}
	s.Init([]string{key})
	if got := s.SingleVectors[key]; got != -100 {
		t.Fatalf("fixture: single coefficient = %d, want -100", got)
	}
	s.SetUnitLimit(10)
	s.liveUnitCount = 6
	s.recomputeClassVectors()
	if got := s.ClassVectors[key].C0; got != -46 {
		t.Fatalf("other-mix = %d, want -46 (4 + -100/2): there is no lower clamp [08 R-P0-05 §5]", got)
	}

	// The upper bound is still applied, at 100.
	_, cv := classVectorFor(t, &content.UnitDef{
		UnitName: "upper", CanAttack: true, CanFly: true, ExtractsMetal: 1,
		MakesMetal: 1, RadarDistance: 1, SonarDistance: 1, MinWaterDepth: -1,
	})
	if cv.C0 != 100 {
		t.Fatalf("other-mix = %d, want the upper clamp 100 [08 R-P0-05 §5]", cv.C0)
	}
}

// TestMetalCoefficientAddsTwentyFiveAndClampsAtZero locks [08 R-P0-05 §5]: the
// `makesmetal` term is PLUS 25 and the clamp is [0, 100] in floating point
// before the truncation, so a metal maker scores max(0, 25 - 0.02 x metal cost)
// and can never be negative. The old -25 with a [-100, 100] clamp produced
// deeply negative values, which is what this table forbids.
func TestMetalCoefficientAddsTwentyFiveAndClampsAtZero(t *testing.T) {
	for _, tc := range []struct {
		name          string
		extractsMetal float64
		makesMetal    int32
		metalCost     float32
		want          int8
	}{
		{"plain definition", 0, 0, 500, 0},         // -10 clamps up to 0
		{"maker, free", 0, 1, 0, 25},               // 25
		{"maker, cheap", 0, 1, 500, 15},            // 25 - 10
		{"maker, at the crossover", 0, 1, 1250, 0}, // 25 - 25
		{"maker, dear", 0, 1, 5000, 0},             // 25 - 100 clamps up to 0
		{"extractor, cheap", 1, 0, 500, 90},        // 100 - 10
		{"extractor, dear", 1, 0, 100000, 0},       // 100 - 2000 clamps up to 0
		{"extractor and maker", 1, 1, 0, 100},      // 125 clamps down to 100
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cv := classVectorFor(t, &content.UnitDef{
				UnitName:       "metal",
				ExtractsMetal:  tc.extractsMetal,
				MakesMetal:     tc.makesMetal,
				BuildCostMetal: tc.metalCost,
				MinWaterDepth:  -1,
			})
			if cv.C1 != tc.want {
				t.Fatalf("metal coefficient = %d, want %d [08 R-P0-05 §5]", cv.C1, tc.want)
			}
		})
	}
}

// TestEnergyCoefficientClampsToZeroAndHundred locks [08 R-P0-05 §5]: the energy
// coefficient's clamp bounds are 0 and 100, applied in floating point before the
// single truncation, not [-100, 100] after it. A consumer therefore reads zero
// rather than a negative value.
func TestEnergyCoefficientClampsToZeroAndHundred(t *testing.T) {
	for _, tc := range []struct {
		name       string
		energyUse  float64
		energyCost float32
		want       int8
	}{
		{"neutral", 0, 0, 0},
		{"consumer clamps up to zero", 3, 514, 0}, // -1.285 - 15
		{"producer", -10, 400, 49},                // -1.0 + 50
		{"strong producer clamps at 100", -100, 0, 100},
		{"expensive definition clamps up to zero", 0, 100000, 0}, // -250
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cv := classVectorFor(t, &content.UnitDef{
				UnitName:        "energy",
				EnergyUse:       tc.energyUse,
				BuildCostEnergy: tc.energyCost,
				MinWaterDepth:   -1,
			})
			if cv.C2 != tc.want {
				t.Fatalf("energy coefficient = %d, want %d [08 R-P0-05 §5]", cv.C2, tc.want)
			}
		})
	}
}

// TestNaNCoefficientsTakeTheUpperArm locks the unordered path of
// [08 R-P0-05 §5]: retail's upper-bound comparison is unordered-sensitive and
// NaN sets the bit that branch selects, so a NaN energy or metal coefficient
// becomes 100, not 0. Only reachable with NaN authored costs.
func TestNaNCoefficientsTakeTheUpperArm(t *testing.T) {
	nan := float32(math.NaN())
	_, cv := classVectorFor(t, &content.UnitDef{
		UnitName:        "nan",
		BuildCostMetal:  nan,
		BuildCostEnergy: nan,
		MinWaterDepth:   -1,
	})
	if cv.C1 != 100 {
		t.Fatalf("metal coefficient on a NaN cost = %d, want 100 [08 R-P0-05 §5]", cv.C1)
	}
	if cv.C2 != 100 {
		t.Fatalf("energy coefficient on a NaN cost = %d, want 100 [08 R-P0-05 §5]", cv.C2)
	}

	// A NaN `energymake` takes the other-mix clamp's lower arm and contributes
	// nothing, matching retail's fall-through to the literal zero.
	_, plain := classVectorFor(t, &content.UnitDef{UnitName: "plain", MinWaterDepth: -1})
	_, nanMake := classVectorFor(t, &content.UnitDef{
		UnitName: "nanmake", EnergyMake: math.NaN(), MinWaterDepth: -1,
	})
	if nanMake.C0 != plain.C0 {
		t.Fatalf("other-mix on a NaN energymake = %d, want %d [08 R-P0-05 §5]", nanMake.C0, plain.C0)
	}
}

// TestBuildOptionTermsTestListPresence locks [08 R-P0-05 §9] and
// [08 R-ENTRY-02 §2]: the `+20` initialization term and the refresh's
// build-capable count test whether the compiled build-option list EXISTS, which
// is exactly the authored `builder` flag. Retail allocates that list for every
// builder, including one whose `CANBUILD` section is absent, so a builder with
// no resolvable entries still qualifies — the case the old non-emptiness test
// dropped.
func TestBuildOptionTermsTestListPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		builder bool
		buttons []string
		want    int8
	}{
		{"non-builder with no menu", false, nil, 40},
		{"non-builder with a menu", false, []string{"x"}, 40},
		{"builder with entries", true, []string{"x"}, 60},
		{"builder with an empty menu", true, nil, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := content.CanonicalKey("bo")
			def := &content.UnitDef{
				DefinitionHeader: content.DefinitionHeader{CanonicalKey: key},
				UnitName:         "bo",
				Builder:          tc.builder,
			}
			cat := &content.Catalog{
				Units:      map[string]*content.UnitDef{key: def},
				BuildMenus: map[string]*content.BuildMenuPage{key: {Buttons: tc.buttons}},
			}
			s := &Strategic{Catalog: cat}
			s.Init([]string{key})
			if got := s.InitVectors[key]; got != tc.want {
				t.Fatalf("initialization byte = %d, want %d [08 R-P0-05 §9]", got, tc.want)
			}
		})
	}
}
