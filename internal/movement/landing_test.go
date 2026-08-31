package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestVTOLLandIfCanDescendsAndGrounds locks the ground-landing machine of
// [04 R-AIR-01 §6] end to end, and with it the occupancy consequence of
// [04 R-COLL-01 §4].
//
// The aircraft starts grounded and holding its cell. Its `VTOL_Move` leg lifts
// it — the mode write to 2 is what releases the ground cells, which is the same
// event a factory's next product waits on [04 R-FAC-02 §6]. A `VTOL_LandIfCan`
// record then runs its three phases: phase 0 caches the goal, draws the search
// bearing and runs the takeoff preamble; phase 1 finds the current position
// landable, installs a point marker whose commanded Y is the terrain height and
// lowers the activation edge (`Deactivate`, the landing script hook); phase 2
// calls the mode setter with mode 1 on arrival, which zeroes the velocity and
// the scalar speed, levels bank and pitch, and re-stamps the ground plane.
func TestVTOLLandIfCanDescendsAndGrounds(t *testing.T) {
	sys, w, u := airFixture(t)
	cell := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	if _, held := sys.Grid.OccupantAt(cell); !held {
		t.Fatal("a grounded aircraft must hold its ground cell [04 R-COLL-01 §4]")
	}

	// Take off and climb clear of the ground through the ordinary air path.
	pushAirOrder(t, u, "VTOL_Move", u.X, u.Z)
	groundY := u.Y
	for tick := uint32(1); tick <= 60; tick++ {
		runMovementTick(sys, tick, w)
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("mover mode=%d after the climb, want the airborne 2 [04 R-AIR-01 §6]", u.Move.Mode)
	}
	if u.Y <= groundY {
		t.Fatalf("the aircraft did not climb: Y=%d, started at %d", u.Y, groundY)
	}
	if _, held := sys.Grid.OccupantAt(cell); held {
		t.Fatal("an airborne aircraft still holds its ground cell [04 R-COLL-01 §4]")
	}
	airborneX, airborneZ, cruiseY := u.X, u.Z, u.Y

	// Clear the move record and hand the unit a landing order at the head.
	q := orderQueueOf(t, u)
	q.SetPrimary(nil)
	sys.airOrders = nil
	pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)

	for tick := uint32(61); tick <= 400; tick++ {
		runMovementTick(sys, tick, w)
		if u.Move.Mode&0x3 == 1 {
			break
		}
	}

	if u.Move.Mode&0x3 != 1 {
		t.Fatalf("mover mode=%d after VTOL_LandIfCan, want the grounded 1 [04 R-AIR-01 §6 phase 2]", u.Move.Mode)
	}
	if u.Activated {
		t.Fatal("the activation edge is still raised: phase 1 must lower it, raising `Deactivate` [04 R-AIR-01 §6]")
	}
	if u.Move.Speed != 0 {
		t.Fatalf("scalar speed=%d after touchdown, want the setter's zero [04 R-AIR-01 §3]", u.Move.Speed)
	}
	// It lands over its own position: phase 1 found the current position
	// landable, so the marker's goal was the unit's own X/Z.
	if dx := absFixed(u.X - airborneX); dx > 4<<16 {
		t.Fatalf("landed %d off its own X [04 R-AIR-01 §6 phase 1]", dx)
	}
	if dz := absFixed(u.Z - airborneZ); dz > 4<<16 {
		t.Fatalf("landed %d off its own Z [04 R-AIR-01 §6 phase 1]", dz)
	}
	// The descent is the integrator's, against the marker's commanded Y. The
	// exact settling altitude is the open question recorded at the phase-1 site:
	// [04 R-AIR-01 §6] glosses it as the terrain height, which does not follow
	// from [04 R-AIR-01 §4]'s setter expression. What is not in question is that
	// the aircraft comes down out of its cruise and settles on the surface, so
	// that is what this asserts.
	if u.Y >= cruiseY {
		t.Fatalf("the aircraft never descended: Y=%d, cruised at %d [04 R-AIR-01 §6 phase 1]", u.Y, cruiseY)
	}
	if dy := absFixed(u.Y - sys.Terrain.HeightAt(u.X, u.Z)); dy > numeric.Fixed(8<<16) {
		t.Fatalf("landed %d above the surface, which is no landing [04 R-AIR-01 §6 phase 1]", dy)
	}
	landed := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	if _, held := sys.Grid.OccupantAt(landed); !held {
		t.Fatal("a landed aircraft does not hold its ground cell: the mode-1 write must re-stamp the ground plane [04 R-COLL-01 §4]")
	}
}
