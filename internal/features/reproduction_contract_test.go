package features

import (
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
