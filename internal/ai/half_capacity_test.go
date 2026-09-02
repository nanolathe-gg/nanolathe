package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// halfCapacityStrategic builds a strategic state over one definition whose
// single coefficient is known, so the class routine's other-mix accumulator can
// be read with and without the half-capacity addend.
func halfCapacityStrategic(t *testing.T, def *content.UnitDef) *Strategic {
	t.Helper()
	key := content.CanonicalKey(def.UnitName)
	def.CanonicalKey = key
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{key: def}}
	s := &Strategic{Catalog: catalog}
	s.Init([]string{key})
	return s
}

// TestHalfCapacityComparisonIsUnsignedAtTheExactEdge locks [08 R-AI-01 §13]:
// the comparison is the session's per-player unit limit shifted right one,
// compared unsigned against the owning player's live unit count. It fires only
// strictly above half the cap, and a live count above 32767 must not read as
// negative.
func TestHalfCapacityComparisonIsUnsignedAtTheExactEdge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int32
		live  uint16
		bound bool
		want  bool
	}{
		{"unbound limit never fires", 0, 500, false, false},
		{"exactly half is not below", 200, 100, true, false},
		{"one above half fires", 200, 101, true, true},
		{"odd cap truncates the shift", 201, 100, true, false},
		{"odd cap one above", 201, 101, true, true},
		{"empty player", 200, 0, true, false},
		{"cap one, no units", 1, 0, true, false},
		{"cap one, one unit", 1, 1, true, true},
		{"high count is not negative", 2, 32768, true, true},
		{"full sixteen-bit cap", 65535, 32767, true, false},
		{"full sixteen-bit cap one above", 65535, 32768, true, true},
	} {
		s := &Strategic{}
		if tc.bound {
			s.SetUnitLimit(tc.limit)
		}
		s.liveUnitCount = tc.live
		if got := s.halfCapacity(); got != tc.want {
			t.Fatalf("%s: limit %d live %d -> %v, want %v", tc.name, tc.limit, tc.live, got, tc.want)
		}
	}

	// A negative argument is a setup error and leaves the limit unbound.
	var unbound Strategic
	unbound.SetUnitLimit(-1)
	if _, ok := unbound.UnitLimit(); ok {
		t.Fatal("a negative limit must not bind")
	}
}

// TestHalfCapacityAddsHalfTheSingleCoefficient locks the addend of
// [08 R-AI-01 §13] and [08 R-P0-05 §5]: half of the single coefficient — the
// signed byte divided by two, truncating toward zero — joins the other-mix
// accumulator after its multipliers and before the zeroing branches.
func TestHalfCapacityAddsHalfTheSingleCoefficient(t *testing.T) {
	// A definition with no weapons, no build list and no metal or energy role:
	// the other-mix accumulator is zero before the multipliers, so the whole
	// difference between the two runs is the addend. The metal build cost is
	// chosen so the single coefficient lands on an odd negative value, which is
	// where halving truncates toward zero rather than toward minus infinity.
	newDef := func() *content.UnitDef {
		return &content.UnitDef{
			UnitName:       "halfcap",
			BuildCostMetal: 1100,
			MinWaterDepth:  -1, // negative: no multiply-by-three [08 R-AI-03 §6]
		}
	}

	base := halfCapacityStrategic(t, newDef())
	key := content.CanonicalKey("halfcap")
	single := base.SingleVectors[key]
	if single >= 0 || single%2 == 0 {
		t.Fatalf("fixture sanity: the single coefficient must be odd and negative, got %d", single)
	}
	if got := base.ClassVectors[key].C0; got != 0 {
		t.Fatalf("fixture sanity: other-mix without the addend = %d, want 0", got)
	}

	// The count is zero on a fresh state, so the accumulator is multiplied by
	// four before the addend joins it; the accumulator is still zero.
	fired := halfCapacityStrategic(t, newDef())
	fired.SetUnitLimit(10)
	fired.liveUnitCount = 6 // 10>>1 = 5 < 6
	fired.recomputeClassVectors()
	want := int32(single) / 2 // truncation toward zero on a negative value [I3]
	if got := int32(fired.ClassVectors[key].C0); got != want {
		t.Fatalf("other-mix with the addend = %d, want %d (single %d halved, truncating)", got, want, single)
	}

	// At exactly half the cap the branch does not fire.
	edge := halfCapacityStrategic(t, newDef())
	edge.SetUnitLimit(10)
	edge.liveUnitCount = 5
	edge.recomputeClassVectors()
	if got := edge.ClassVectors[key].C0; got != 0 {
		t.Fatalf("other-mix at exactly half the cap = %d, want 0", got)
	}
}

// TestHalfCapacityIsZeroedByTheLaterBranches keeps the addend on the retail
// side of the zeroing branches: a can-load or is-feature definition still ends
// at zero however large the addend was [08 R-P0-05 §5].
func TestHalfCapacityIsZeroedByTheLaterBranches(t *testing.T) {
	key := content.CanonicalKey("halfcapload")
	s := halfCapacityStrategic(t, &content.UnitDef{
		UnitName:      "halfcapload",
		CanAttack:     true,
		CanLoad:       true,
		MinWaterDepth: -1,
	})
	s.SetUnitLimit(10)
	s.liveUnitCount = 9
	s.recomputeClassVectors()
	if got := s.ClassVectors[key].C0; got != 0 {
		t.Fatalf("a can-load definition must zero after the addend: %d, want 0", got)
	}
}

// TestClassRecomputeDrawsNothing keeps the class routine outside the draw
// ledger: it consumes no random numbers, with or without the addend (I4)
// [08 R-P0-05 §6].
func TestClassRecomputeDrawsNothing(t *testing.T) {
	stream := rng.NewSimulation(99)
	before := stream.Draws()
	s := halfCapacityStrategic(t, &content.UnitDef{UnitName: "halfcapdraw", MinWaterDepth: -1})
	s.SetUnitLimit(4)
	s.liveUnitCount = 4
	s.recomputeClassVectors()
	if got := stream.Draws(); got != before {
		t.Fatalf("class recompute consumed %d draws, want 0", got-before)
	}
}
