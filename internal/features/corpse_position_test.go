package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// R13's regression. The corpse stamper receives the dying unit's EXACT position
// triple [05 R-FEAT-01 §13 "The corpse creator's chain and stamp"], and step 4
// of [05 R-FEAT-01 §3] stores a supplied triple verbatim rather than computing
// the footprint centre and the snapped terrain floor. So a ship destroyed on
// the surface leaves a wreck at the surface, which then descends at the
// constant rate to the coarse floor.
func TestCorpseKeepsTheVictimsPositionAndSinks(t *testing.T) {
	const (
		floorByte = 10
		seaByte   = 20
	)
	terrain := newTestTerrainP1(8, 8)
	terrain.SeaLevel = seaByte
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	corpse := defP1("shipwreck", 2, 2, "shipwreck.3do", "")
	// A victim standing off both the cell origin and the footprint centre, at
	// the water plane rather than on the seabed.
	victim := [3]numeric.Fixed{
		world.CellToWorld(3).Add(numeric.Fixed(5 * 65536)),
		numeric.Fixed(seaByte * 65536),
		world.CellToWorld(4).Add(numeric.Fixed(7 * 65536)),
	}
	inst := svc.PlaceCorpse(victim, corpse, false)
	if inst == nil {
		t.Fatal("corpse refused")
	}
	if inst.X != victim[0] || inst.Y != victim[1] || inst.Z != victim[2] {
		t.Fatalf("corpse position (%d,%d,%d), want the victim's triple (%d,%d,%d)",
			inst.X.Raw(), inst.Y.Raw(), inst.Z.Raw(), victim[0].Raw(), victim[1].Raw(), victim[2].Raw())
	}
	// The plot anchor is the victim's cell, derived separately from the stored
	// position and unaffected by the footprint centring.
	if inst.CX != 3 || inst.CZ != 4 {
		t.Fatalf("corpse anchored at (%d,%d), want the victim's plot cell (3,4)", inst.CX, inst.CZ)
	}
	if !inst.IsSinking || inst.Settled {
		t.Fatalf("corpse over water is IsSinking=%v Settled=%v at birth, want sinking", inst.IsSinking, inst.Settled)
	}

	floor := terrain.CoarseHeightAt(3, 4)
	if floor.Raw() != floorByte*65536 {
		t.Fatalf("fixture floor %d, want %d", floor.Raw(), int64(floorByte)*65536)
	}

	// It must actually descend, for many ticks, before it lands.
	previous := inst.Y
	descending := 0
	settledAt := -1
	for tick := 1; tick <= 400; tick++ {
		svc.TickLifecycle(uint32(tick))
		if inst.Settled {
			settledAt = tick
			break
		}
		if inst.Y.Raw() >= previous.Raw() {
			t.Fatalf("tick %d: Y did not descend (%d then %d)", tick, previous.Raw(), inst.Y.Raw())
		}
		previous = inst.Y
		descending++
	}
	if settledAt < 0 {
		t.Fatalf("corpse never settled; Y is %d after 400 ticks", inst.Y.Raw())
	}
	if descending < 30 {
		t.Fatalf("corpse settled after only %d descending ticks; it started on the seabed", descending)
	}
	if inst.Y != floor {
		t.Fatalf("settled Y %d, want the coarse floor %d", inst.Y.Raw(), floor.Raw())
	}
	if inst.Vy != 0 {
		t.Fatalf("settled velocity %d, want 0", inst.Vy.Raw())
	}
	// Horizontal position is untouched by the descent: nothing in the corpse
	// path writes a horizontal velocity [05 R-FEAT-01 §13].
	if inst.X != victim[0] || inst.Z != victim[2] {
		t.Fatalf("horizontal position moved during the descent")
	}
}

// A stamp with no supplied position keeps the snapped footprint centre of
// [05 R-FEAT-01 §3] step 4 — the corpse override must not leak into the
// terrain, mission, successor, reproduction or reload paths.
func TestNullPositionStampKeepsTheSnappedCentre(t *testing.T) {
	terrain := newTestTerrainP1(8, 8)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	def := defP1("rock", 2, 2, "rock.3do", "")
	inst := svc.spawnFeatureAt(3, 4, def)
	if inst == nil {
		t.Fatal("stamp refused")
	}
	wantX := world.CellToWorld(3).Add(numeric.Fixed(2 * 1048576 / 2))
	wantZ := world.CellToWorld(4).Add(numeric.Fixed(2 * 1048576 / 2))
	if inst.X != wantX || inst.Z != wantZ {
		t.Fatalf("centre (%d,%d), want (%d,%d)", inst.X.Raw(), inst.Z.Raw(), wantX.Raw(), wantZ.Raw())
	}
	if inst.Y != terrain.CoarseHeightAt(3, 4) {
		t.Fatalf("Y %d, want the snapped floor %d", inst.Y.Raw(), terrain.CoarseHeightAt(3, 4).Raw())
	}
}
