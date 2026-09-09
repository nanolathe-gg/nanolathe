package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// TestRestingSpritesLeaveTheArenaFree is R05's fixture regression. The arena of
// [05 R-FEAT-01 §2] is 2048 slots for 3D instances and ACTIVE sprite event
// records; a resting sprite anchor takes none [05 R-FEAT-01 §3 step 5]. So a
// forest denser than the arena must still leave every slot available.
func TestRestingSpritesLeaveTheArenaFree(t *testing.T) {
	const side = 64 // 4096 cells, twice the arena
	terrain := newTestTerrainP1(side, side)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	tree := defP1("tree", 1, 1, "", "trees") // sprite: flag bit 0 set
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")

	planted := 0
	for cz := 0; cz < side; cz++ {
		for cx := 0; cx < side; cx++ {
			if cz == side-1 && cx == side-1 {
				continue // keep one cell clear for the wreck
			}
			if svc.spawnFeatureAt(cx, cz, tree) == nil {
				t.Fatalf("resting sprite refused at (%d,%d) after %d plantings", cx, cz, planted)
			}
			planted++
		}
	}
	if planted <= FeatureAnimSlots {
		t.Fatalf("fixture planted only %d sprites, which does not exceed the %d-slot arena", planted, FeatureAnimSlots)
	}
	if occupants := svc.arenaOccupants(); occupants != 0 {
		t.Fatalf("resting sprites charged %d arena slots, want 0", occupants)
	}
	if svc.spawnFeatureAt(side-1, side-1, wreck) == nil {
		t.Fatalf("3D wreck refused behind %d resting sprites", planted)
	}
	if occupants := svc.arenaOccupants(); occupants != 1 {
		t.Fatalf("arena occupants after one wreck = %d, want 1", occupants)
	}
	// An ignition is the sprite side's own allocation, so it does charge a slot
	// [05 R-FEAT-01 §2][§9].
	inst := svc.InstanceAt(0, 0)
	if inst == nil {
		t.Fatal("no instance at (0,0)")
	}
	startBurning(svc, inst, longBurn(), 1000)
	if occupants := svc.arenaOccupants(); occupants != 2 {
		t.Fatalf("arena occupants with one burning sprite = %d, want 2", occupants)
	}
}
