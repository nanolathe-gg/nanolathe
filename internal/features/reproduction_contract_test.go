package features

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"testing"
)

func TestReproductionRectangularAxisAndSmallBounds(t *testing.T) {
	for _, area := range []int32{0, 1, 256, 257} {
		terrain := newEmptyTerrain(6, 4)
		def := featureDef("rectangular-growth", 0, 0, 10)
		def.Reproduce = 100
		def.ReproduceArea = area
		sim := rng.SimulationFromState(42)
		svc := NewService(terrain, &sim, nil, nil)
		source := svc.spawnFeatureAt(2, 1, def) // index 8; 8/height=2, 8/width=1
		if source == nil {
			t.Fatal("source placement failed")
		}
		// A convenience record's legacy status is not a plot attachment bit.
		source.Status = 1
		svc.SetCursor(9)
		before := sim.Draws()
		svc.reproduceTick()
		if sim.Draws()-before != 1 || terrain.PlotAt(2, 2).IsEmpty() || len(svc.Instances()) != 2 {
			t.Fatalf("area=%d draws=%d offspring=%v", area, sim.Draws()-before, !terrain.PlotAt(2, 2).IsEmpty())
		}
	}
}

func TestReproductionSourceGroundGateAfterDraws(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		terrain := newEmptyTerrain(6, 4)
		def := featureDef("occupied-growth", 0, 0, 10)
		def.Reproduce = -1 // stored byte 255, so the bound-100 roll always passes
		def.ReproduceArea = 2
		sim := rng.SimulationFromState(42)
		svc := NewService(terrain, &sim, nil, nil)
		svc.spawnFeatureAt(2, 1, def)
		if occupied {
			terrain.PlotAt(2, 1).SetOccupantA(7)
		} else {
			terrain.PlotAt(2, 1).SetOccupantB(7)
		}
		expected := sim
		expected.Uint32n(100)
		tx := 2 + int(expected.Uint32n(2)) - 1
		tz := 2 + int(expected.Uint32n(2)) - 1
		// If the authored seed selected the source, the fixture would not distinguish the gate.
		if tx == 2 && tz == 1 {
			t.Fatal("fixture selected source")
		}
		svc.SetCursor(9)
		svc.reproduceTick()
		if sim.State != expected.State || sim.Draws() != expected.Draws() {
			t.Fatal("occupancy changed draw order")
		}
		if got := !terrain.PlotAt(int32(tx), int32(tz)).IsEmpty(); got == occupied {
			t.Fatalf("ground occupied=%t offspring=%t", occupied, got)
		}
	}
}

func TestReproductionRejectsAttachedAndNonemptyTargets(t *testing.T) {
	for _, attached := range []bool{false, true} {
		terrain := newEmptyTerrain(6, 4)
		def := featureDef("gated-growth", 0, 0, 10)
		def.Reproduce = 100
		def.ReproduceArea = 0
		sim := rng.SimulationFromState(42)
		svc := NewService(terrain, &sim, nil, nil)
		svc.spawnFeatureAt(2, 1, def)
		if attached {
			terrain.PlotAt(2, 1).SetFlagByte(terrain.PlotAt(2, 1).FlagByte() | 1)
		} else {
			svc.spawnFeatureAt(2, 2, def)
		}
		before := len(svc.Instances())
		draws := sim.Draws()
		svc.SetCursor(9)
		svc.reproduceTick()
		wantDraws := uint64(1)
		if attached {
			wantDraws = 0
		}
		if len(svc.Instances()) != before || sim.Draws()-draws != wantDraws {
			t.Fatalf("attached=%t instances=%d draws=%d", attached, len(svc.Instances()), sim.Draws()-draws)
		}
	}
}

func TestZeroGravityFeatureKeepsAboveWaterVelocity(t *testing.T) {
	terrain := newEmptyTerrain(3, 3)
	terrain.Gravity = 0
	svc := NewService(terrain, nil, nil, nil)
	inst := &Instance{X: numeric.Fixed(8 * 65536), Y: numeric.Fixed(30 * 65536), Z: numeric.Fixed(8 * 65536), Vy: -65536}
	svc.integrateSink(inst)
	if inst.Y != numeric.Fixed(29*65536) || inst.Vy != -65536 {
		t.Fatalf("zero gravity: y=%d vy=%d", inst.Y, inst.Vy)
	}
}

// Phase-four reconciliation does not consume the reproduction draw. Phase-six
// reproduction sees the stream left by intervening player work, including for
// a zero-rate source [01 §4.4][05 R-FEAT-01 §10, §12].
func TestReproductionDrawBelongsToLifecycle(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	sim := rng.SimulationFromState(42)
	svc := NewService(terrain, &sim, nil, nil)
	def := featureDef("zero-rate", 0, 0, 100)
	if svc.PlaceAt(1, 1, def) == nil {
		t.Fatal("placement refused")
	}
	svc.SetCursor(6)
	expected := sim
	svc.TickMotion(1)
	if sim.State != expected.State || sim.Draws() != expected.Draws() {
		t.Fatal("phase four consumed reproduction draw")
	}
	if got, want := sim.Uint32n(100), expected.Uint32n(100); got != want {
		t.Fatalf("intervening player draw=%d, want %d", got, want)
	}
	expected.Uint32n(100)
	svc.TickLifecycle(1)
	if sim.State != expected.State || sim.Draws() != expected.Draws() || svc.LastReproIdx != 5 {
		t.Fatal("lifecycle did not consume exactly the following zero-rate draw")
	}
}

// A map-authored 3D anchor is stamped, and step 4 of the one stamp routine
// "sets its instance-attached bit" [05 R-FEAT-01 §3]. The reproduction sweep
// reads that bit and skips the anchor before its bounded draw, so a map tree
// modelled in 3DO must cost the shared stream nothing — the draw is taken even
// when the rate is zero, so a missing bit shifts the stream on every stock map
// (I4) [05 R-FEAT-01 §12].
func TestPopulateFromTerrainSets3DAnchorInstanceBit(t *testing.T) {
	cases := []struct {
		name      string
		object    string
		filename  string
		wantBit   bool
		wantDraws uint64
	}{
		// An authored model and no sprite source: the anchor carries the bit
		// and the sweep passes over it without drawing.
		{name: "3d", object: "tree.3do", wantBit: true, wantDraws: 0},
		// The sprite arm of step 5 leaves the bit clear, so the sweep still
		// draws. Without it the 3D case above would pass under any change that
		// merely stopped the sweep from drawing at all.
		{name: "sprite", filename: "trees", wantBit: false, wantDraws: 1},
	}
	for _, tc := range cases {
		terrain := newEmptyTerrain(6, 4)
		def := featureDef("map-authored-"+tc.name, 0, 0, 10)
		def.Object = tc.object
		def.Filename = tc.filename
		// Stock content: every definition's reproduction rate is zero, and the
		// draw happens anyway.
		def.Reproduce = 0
		terrain.FeatureDefs = []*content.FeatureDef{def}
		// The map loader writes the plot grid directly; the service builds the
		// animation side from it afterwards.
		terrain.PlotAt(2, 1).SetFeature(0)
		sim := rng.SimulationFromState(42)
		svc := NewService(terrain, &sim, nil, nil)
		if n := svc.PopulateFromTerrain(); n != 1 {
			t.Fatalf("%s: populate placed %d anchors, want 1", tc.name, n)
		}
		if got := terrain.PlotAt(2, 1).Occupied(); got != tc.wantBit {
			t.Fatalf("%s: anchor instance bit %t, want %t", tc.name, got, tc.wantBit)
		}
		svc.SetCursor(9) // the descending cursor lands on index 8, cell (2,1)
		before := sim.Draws()
		svc.reproduceTick()
		if svc.LastReproIdx != 8 {
			t.Fatalf("%s: sweep visited index %d, want 8", tc.name, svc.LastReproIdx)
		}
		if got := sim.Draws() - before; got != tc.wantDraws {
			t.Fatalf("%s: sweep took %d simulation draws, want %d", tc.name, got, tc.wantDraws)
		}
	}
}
