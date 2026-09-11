package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Two authored peaks separate the anchor's coarse height (40), the odd
// footprint centre's bilinear height (50), and its coarse floor (60).
func featureSlopeTerrain() *world.Terrain {
	const side = 8
	attrs := make([]formats.TNTAttribute, side*side)
	for i := range attrs {
		attrs[i].Feature = world.PlotFeatureNone
	}
	attrs[2*side+2].Height = 80
	attrs[3*side+3].Height = 120
	return &world.Terrain{CellW: side, CellH: side, Plot: world.ExpandPlot(attrs, side, side), SeaLevel: 10}
}

func TestFeatureStampSamplesOddFootprintCentre(t *testing.T) {
	terrain := featureSlopeTerrain()
	svc := NewService(terrain, nil, nil, nil)
	def := defP1("slope-rock", 3, 3, "rock", "")
	inst := svc.PlaceAt(1, 1, def)
	if inst == nil {
		t.Fatal("stamp refused")
	}
	want := [3]numeric.Fixed{40 << 16, 50 << 16, 40 << 16}
	check := func(label string, got *Instance) {
		t.Helper()
		if got == nil || [3]numeric.Fixed{got.X, got.Y, got.Z} != want {
			t.Fatalf("%s: %+v, want %v", label, got, want)
		}
	}
	check("stamp", inst)
	check("reconstructed stamp", svc.newInstanceAt(1, 1, def))
	restored := NewService(terrain, nil, nil, nil)
	restored.PopulateFromTerrain()
	check("map population", restored.InstanceAt(1, 1))
}

func TestFeatureAreaUsesAttachedRecordOnly(t *testing.T) {
	for _, model := range []bool{false, true} {
		terrain := featureSlopeTerrain()
		svc := NewService(terrain, nil, nil, nil)
		def := defP1("slope-candidate", 3, 3, "", "sprites")
		if model {
			def.Object, def.Filename = "rock", ""
		}
		inst := svc.PlaceAt(1, 1, def)
		if inst == nil {
			t.Fatal("stamp refused")
		}
		stored := [3]numeric.Fixed{41 << 16, 99 << 16, 42 << 16}
		inst.X, inst.Y, inst.Z = stored[0], stored[1], stored[2]
		cand, ok := svc.AreaCandidateAt(3, 3) // fringe resolves to anchor 1,1
		want := [3]numeric.Fixed{40 << 16, 50 << 16, 40 << 16}
		if model {
			want = stored
		}
		if !ok || cand.CX != 1 || cand.CZ != 1 || [3]numeric.Fixed{cand.X, cand.Y, cand.Z} != want {
			t.Fatalf("model=%t candidate=%+v, want %v", model, cand, want)
		}
		if !model {
			startBurning(svc, inst, longBurn(), 100)
			cand, ok = svc.AreaCandidateAt(3, 3)
			if !ok || [3]numeric.Fixed{cand.X, cand.Y, cand.Z} != stored {
				t.Fatalf("attached sprite lost stored position: %+v", cand)
			}
		}
	}
}

func TestCorpseSinkingAdmissionUsesExactVictimPoint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		point   int64
		sea     uint8
		sinking bool
	}{
		{"wet point, dry anchor average", 18, 10, true},
		{"dry point, wet anchor average", 31, 45, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terrain := featureSlopeTerrain()
			terrain.SeaLevel = tc.sea
			svc := NewService(terrain, nil, nil, nil)
			def := defP1("slope-corpse", 1, 1, "wreck", "")
			pos := [3]numeric.Fixed{numeric.FixedFromInt(tc.point), 90 << 16, numeric.FixedFromInt(tc.point)}
			inst := svc.PlaceCorpse(pos, Orientation{}, def, false, 0)
			if inst == nil || inst.IsSinking != tc.sinking {
				t.Fatalf("corpse=%+v, want sinking=%t", inst, tc.sinking)
			}
			if [3]numeric.Fixed{inst.X, inst.Y, inst.Z} != pos {
				t.Fatal("corpse override was resampled")
			}
		})
	}
}

func TestSinkingKeepsCoarseFloorUnderStoredPoint(t *testing.T) {
	terrain := featureSlopeTerrain()
	svc := NewService(terrain, nil, nil, nil)
	inst := &Instance{CX: 1, CZ: 1, X: 40 << 16, Z: 40 << 16, Y: 55 << 16, Vy: -1}
	svc.integrateSink(inst)
	// The current position's coarse floor is 60, versus centre interpolation
	// 50 or anchor coarse 40. Landing snaps to that coarse value [05 R-FEAT-01 §13].
	if inst.Y != 60<<16 || inst.Vy != 0 {
		t.Fatalf("sink floor = %d, velocity=%d", inst.Y, inst.Vy)
	}
}

func TestBurnSmokeSamplesCentreBeforeJitter(t *testing.T) {
	terrain := featureSlopeTerrain()
	crt := rng.CRTFromState(3)
	svc := NewService(terrain, nil, &crt, nil)
	inst := svc.PlaceAt(1, 1, defP1("slope-smoke", 3, 3, "", "sprites"))
	if inst == nil {
		t.Fatal("stamp refused")
	}
	var got [3]numeric.Fixed
	svc.BurnSmoke = func(pos [3]numeric.Fixed) { got = pos; crt.Rand() }
	svc.emitBurnSmoke(inst)
	if got != [3]numeric.Fixed{40 << 16, 50 << 16, 40 << 16} || crt.Draws() != 3 {
		t.Fatalf("smoke=%v, CRT draws=%d", got, crt.Draws())
	}
}
