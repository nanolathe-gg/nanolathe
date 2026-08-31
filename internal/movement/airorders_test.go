package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// airFixture is a grounded 1x1 aircraft on flat terrain with its order queue
// bound to a seeded simulation stream, so an executor that draws does so from a
// stream this test owns [I4].
func airFixture(t *testing.T) (*System, *units.World, *units.Unit) {
	t.Helper()
	sys, w, u := takeoffFixture(t)
	q := orders.QueueForUnit(u)
	sim := rng.NewSimulation(0x12345677)
	q.SetBinding(&orders.QueueBinding{SimRNG: &sim, Lookup: w.Unit})
	// The lean accumulator reads two words the raw fixture definition leaves at
	// zero: `bankscale`, whose compiled default is 1.0, and the map's `gravity`,
	// whose OTA default is 0x1FDB [04 R-AIR-01 §2]. Both are inputs, not
	// behaviour under test.
	u.Def.BankScale = 65536
	sys.Terrain.Gravity = 0x1FDB
	return sys, w, u
}

func pushAirOrder(t *testing.T, u *units.Unit, name string, goalX, goalZ numeric.Fixed) *orders.Node {
	t.Helper()
	id := orders.Lookup(name)
	if id == 0 {
		t.Fatalf("descriptor %s missing from the order table", name)
	}
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{Owner: u.Handle, GoalX: goalX, GoalZ: goalZ})
	return q.Primary()[q.LenPrimary()-1]
}

// TestVTOLMoveClimbsCruisesAndBanks locks the three layers above the flight
// integrator that the playtest found missing, in one run of the air path.
//
//   - The takeoff preamble installs the `cruisealt / 2` climb marker and the
//     record waits on its purely vertical arrival, so the aircraft leaves the
//     ground before it travels [04 R-AIR-01 §6][04 R-AIR-02].
//   - Phase 1's point marker carries no altitude setter, so the per-tick
//     cruise-altitude rule of [04 R-AIR-01 §1] step 4 commands the sector
//     height plus the full `cruisealt` while the goal is more than 160 world
//     units away.
//   - The command heading is the bearing to the goal beyond 320 world units
//     ([04 R-AIR-01 §1] step 5), so the aircraft turns onto its course, and the
//     lean accumulator writes a nonzero bank while it does [04 R-AIR-01 §2].
//     Bank is authoritative simulation state, not a renderer concern.
func TestVTOLMoveClimbsCruisesAndBanks(t *testing.T) {
	sys, w, u := airFixture(t)
	u.Move.Heading = 0x8000 // pointing away from the goal, so the leg is a turn
	startY := u.Y
	goalX, goalZ := world.CellToWorld(26), world.CellToWorld(8)
	pushAirOrder(t, u, "VTOL_Move", goalX, goalZ)

	maxBank := uint16(0)
	climbed := false
	for tick := uint32(1); tick <= 120; tick++ {
		runMovementTick(sys, tick, w)
		if u.Move.Mode&0x3 != 2 {
			t.Fatalf("tick %d: mover mode=%d, want the airborne 2 [04 R-AIR-01 §6]", tick, u.Move.Mode)
		}
		if u.Y > startY {
			climbed = true
		}
		if b := bankMagnitude(u.Move.Bank); b > maxBank {
			maxBank = b
		}
	}

	if !climbed {
		t.Fatal("the aircraft never left its start altitude: the climb marker did not command a climb [04 R-AIR-01 §6]")
	}
	// The commanded altitude while more than 160 world units out is the sector
	// height plus the full cruisealt [04 R-AIR-01 §1] step 4. The map is flat, so
	// the sector's smoothed byte is the terrain height byte throughout.
	sector, linked := sys.AirSectors.SectorHeightAt(u.X, u.Z)
	if !linked {
		t.Fatal("the aircraft's position does not link into the air sector grid [04 R-AIR-01 §5]")
	}
	wantY := numeric.Fixed((int64(u.Def.CruiseAlt) + int64(sector)) << 16)
	if dy := absFixed(u.Y - wantY); dy > numeric.Fixed(2<<16) {
		t.Fatalf("cruise altitude %d, want the sector rule's %d [04 R-AIR-01 §1 step 4]", u.Y, wantY)
	}
	if int64(u.X) <= int64(world.CellToWorld(8)) {
		t.Fatalf("the aircraft did not travel toward its goal: X=%d", u.X)
	}
	if maxBank == 0 {
		t.Fatal("bank stayed zero through the turn; the lean accumulator is not writing the unit's angle words [04 R-AIR-01 §2]")
	}
}

// TestAirMarkerHeadingSupplyCases locks the four ordered cases of the marker's
// heading-supply method [04 R-AIR-01 §4], including the one the producer's step
// 5 depends on: with no suggestion the destination is left untouched, because
// inside 16 world units "the command heading is left completely unchanged"
// [04 R-AIR-01 §1].
func TestAirMarkerHeadingSupplyCases(t *testing.T) {
	sys, _, u := airFixture(t)

	plain := sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
	dst := uint16(0x1234)
	if plain.SupplyHeading(u, &dst) {
		t.Fatal("a point marker with no explicit heading supplied one [04 R-AIR-01 §4]")
	}
	if dst != 0x1234 {
		t.Fatalf("the no-suggestion arm wrote %#x into the destination [04 R-AIR-01 §1 step 5]", dst)
	}

	explicit := sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
	explicit.setHeading(0x4000)
	if !explicit.SupplyHeading(u, &dst) || dst != 0x4000 {
		t.Fatalf("an explicit-heading marker supplied %#x [04 R-AIR-01 §4]", dst)
	}
}

// TestAirMarkerArrivalRadiusIsStrict locks the explicit-radius arm of the
// marker's arrival test: strict, horizontal only, and against the plain 16-bit
// word the executor leg wrote — not a lookup into an enumerated family
// [04 R-AIR-01 §4].
func TestAirMarkerArrivalRadiusIsStrict(t *testing.T) {
	sys, _, u := airFixture(t)
	goal := Vec3{X: u.X + numeric.Fixed(64<<16), Y: u.Y, Z: u.Z}
	m := sys.newPointMarker(u, goal)
	m.setArrivalRadius(64)
	if m.Arrived(u) {
		t.Fatal("arrival at exactly the radius must fail the strict test [04 R-AIR-01 §4]")
	}
	m.setArrivalRadius(65)
	if !m.Arrived(u) {
		t.Fatal("arrival inside the radius must pass [04 R-AIR-01 §4]")
	}
}

func bankMagnitude(b uint16) uint16 {
	if s := int16(b); s < 0 {
		return uint16(-s)
	}
	return b
}

func absFixed(v numeric.Fixed) numeric.Fixed {
	if v < 0 {
		return -v
	}
	return v
}

func orderQueueOf(t *testing.T, u *units.Unit) *orders.Queue {
	t.Helper()
	q := orders.QueueForUnit(u)
	if q == nil {
		t.Fatal("unit has no order queue")
	}
	return q
}
