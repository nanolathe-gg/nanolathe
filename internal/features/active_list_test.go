package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// activeOrder lists the active list from its head, as cell indices.
func activeOrder(svc *Service) []int {
	var out []int
	for inst := svc.activeHead; inst != nil; inst = inst.nextActive {
		out = append(out, inst.CZ*int(svc.Terrain.CellW)+inst.CX)
	}
	return out
}

// TestThreeDRecordsGoDormantAtZeroVelocity locks the 3D branch's dormant move
// [05 R-FEAT-01 §10] pass 3: every stamped 3D instance starts on the active
// list, a still one leaves it on its first visit and is never visited again,
// and a sinking one leaves it on the visit after it lands.
func TestThreeDRecordsGoDormantAtZeroVelocity(t *testing.T) {
	terrain := newTestTerrainP1(8, 8)
	terrain.SeaLevel = 20
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	rock := defP1("rock", 1, 1, "rock.3do", "")
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	still := svc.spawnFeatureAt(1, 1, rock)
	if still == nil || !still.onActive {
		t.Fatal("a stamped 3D instance did not join the active list")
	}
	sinking := svc.PlaceCorpse([3]numeric.Fixed{
		world.CellToWorld(3).Add(numeric.Fixed(5 * 65536)),
		numeric.Fixed(20 * 65536),
		world.CellToWorld(4).Add(numeric.Fixed(7 * 65536)),
	}, Orientation{}, wreck, false, 0)
	if sinking == nil || !sinking.onActive || sinking.Vy == 0 {
		t.Fatal("the corpse over water is not an active, moving record")
	}
	svc.TickLifecycle(1)
	if still.onActive || !still.Settled {
		t.Fatal("a still 3D instance was not retired on its first visit")
	}
	if !sinking.onActive {
		t.Fatal("a moving 3D instance was retired while it still had velocity")
	}
	floor := terrain.CoarseHeightAt(3, 4)
	landedAt := -1
	for tick := 2; tick <= 400 && landedAt < 0; tick++ {
		svc.TickLifecycle(uint32(tick))
		if sinking.Vy == 0 {
			landedAt = tick
		}
	}
	if landedAt < 0 || sinking.Y != floor {
		t.Fatalf("the wreck never landed (Y %d, floor %d)", sinking.Y.Raw(), floor.Raw())
	}
	// The landing visit snapped it; it is retired on the NEXT visit.
	if !sinking.onActive {
		t.Fatal("the wreck was retired on the visit that landed it, want the one after")
	}
	svc.TickLifecycle(uint32(landedAt + 1))
	if sinking.onActive || !sinking.Settled {
		t.Fatal("the landed wreck was not retired on the visit after it landed")
	}
	if got := activeOrder(svc); len(got) != 0 {
		t.Fatalf("active list %v, want empty", got)
	}
}
