package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/world"
)

// WU-19-226 locks, from the play-test round: a gunship that would not hold
// its target, and fighters that would not come back to theirs.

// TestFrozenTerrainMarkerIsBoundToItsTarget locks the corrected constructor
// [04 R-AIR-01 §4]: the frozen terrain-point marker takes the target it is
// frozen about, and the two methods the freeze bit does not touch read it —
// heading supply writes `bearing(unit, target)` (flag 0x02 with a live
// target) and persistence keeps the marker through arrival (flag 0x01 with
// a live target). Without a target the same flags supply nothing and release
// on arrival, which is what a gunship that faced its line of flight was built
// from.
func TestFrozenTerrainMarkerIsBoundToItsTarget(t *testing.T) {
	sys, w, u := airFixture(t)
	target := airTargetFor(t, sys, w, 20, 10)
	goal := Vec3{X: u.X, Y: u.Y, Z: u.Z}

	bound := sys.newFrozenTerrainPointMarker(u, target.Handle, goal)
	if bound.flags != 0xA3 {
		t.Fatalf("frozen marker flags %#x, want 0xA3 [04 R-AIR-01 §4]", bound.flags)
	}
	dst := uint16(0x1234)
	if !bound.SupplyHeading(u, &dst) {
		t.Fatal("a frozen marker bound to a live target supplied no heading [04 R-AIR-01 §4]")
	}
	if want := bearing(u.X, u.Z, target.X, target.Z); dst != want {
		t.Fatalf("supplied heading %#x, want bearing(unit, target) %#x [04 R-AIR-01 §4]", dst, want)
	}
	if !bound.Persistent() {
		t.Fatal("a frozen marker bound to a live target must persist through arrival [04 R-AIR-01 §4]")
	}

	unbound := sys.newFrozenTerrainPointMarker(u, 0, goal)
	dst = 0x1234
	if unbound.SupplyHeading(u, &dst) || dst != 0x1234 {
		t.Fatalf("an unbound frozen marker supplied a heading (%#x) [04 R-AIR-01 §4]", dst)
	}
	if unbound.Persistent() {
		t.Fatal("an unbound frozen marker persisted [04 R-AIR-01 §4]")
	}
}

// TestAirToGroundHoverStandoffFacesTheTarget drives the `hoverattack` orbit
// to its phase-3 standoff and locks the relationship the play-test found
// missing: the standoff marker is frozen ABOUT THE RECORD'S TARGET, so the
// heading it supplies to the flight command is the bearing to that target
// [04 R-AIR-01 §8][04 R-AIR-01 §4]. The gunship therefore faces what it is
// shooting while it slides between its two standoff points.
func TestAirToGroundHoverStandoffFacesTheTarget(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	sys.BindAirOrderLegs()
	u.Def.HoverAttack = true
	u.InstallWeapon(0, &content.WeaponDef{Range: 240})
	target := airTargetFor(t, sys, w, 40, 16)

	q := orderQueueOf(t, u)
	q.Push(orders.Lookup("AirToGroundHover"), orders.Node{Owner: u.Handle, Target: target.Handle})
	n := q.Primary()[0]

	var standoff *airMarker
	for tick := uint32(1); tick <= 600 && standoff == nil; tick++ {
		q.Pump(u, tick)
		runMovementTick(sys, tick, w)
		if n.Phase != 3 {
			continue
		}
		fl := sys.Flights[u.Handle]
		if fl == nil || fl.Command == nil {
			continue
		}
		if m, ok := fl.Command.Payload.(*airMarker); ok && m.flags&airMarkerFreeze != 0 {
			standoff = m
		}
	}
	if standoff == nil {
		t.Fatalf("the orbit never installed its frozen standoff marker (phase %d) [04 R-AIR-01 §8]", n.Phase)
	}
	if standoff.target != target.Handle {
		t.Fatalf("standoff marker bound to %d, want the record's target %d [04 R-AIR-01 §4]", standoff.target, target.Handle)
	}
	dst := uint16(0)
	if !standoff.SupplyHeading(u, &dst) || dst != bearing(u.X, u.Z, target.X, target.Z) {
		t.Fatalf("standoff heading %#x, want the bearing to the target %#x [04 R-AIR-01 §4]", dst, bearing(u.X, u.Z, target.X, target.Z))
	}
	if r := int32(int16(standoff.radius)); r != 0x10 {
		t.Fatalf("standoff arrival radius %d, want 0x10 [04 R-AIR-01 §8]", r)
	}
}

// TestAirToGroundOffMapForcesPhaseTwo locks the strafing run's own step-4 arm
// [04 R-AIR-01 §5][04 R-AIR-01 §8]: off the map, `AirToGround` builds NO
// recovery marker — it sets the deadline to tick + 30, forces the phase to 2
// and falls through, so phase 2's Range-radius marker on the cached goal is
// what gets installed and the fighter turns straight back onto its target.
// The recovery leg the other executors take is a hold behind gate 0xE0 on a
// marker toward the map centre.
func TestAirToGroundOffMapForcesPhaseTwo(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	sys.BindAirOrderLegs()
	u.InstallWeapon(0, &content.WeaponDef{Range: 200})
	target := airTargetFor(t, sys, w, 40, 16)

	q := orderQueueOf(t, u)
	q.Push(orders.Lookup("AirToGround"), orders.Node{Owner: u.Handle, Target: target.Handle, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z})
	n := q.Primary()[0]
	n.Phase = 4 // the break leg, where a fly-through of three ranges leaves the map

	// Beyond the east edge: the fixture map is 256 cells wide.
	u.X = world.CellToWorld(300)
	u.Move.Mode = 2
	// The executor reads the completed stamp's retained sector, not these
	// coordinates directly. Commit and stamp this authored move before asking
	// the off-map gate.
	fl := sys.Flights[u.Handle]
	fl.X, fl.Y, fl.Z = int32(u.X), int32(u.Y), int32(u.Z)
	sys.commitFlightState(u, fl)
	sys.syncMoverStamp(u)
	if !sys.airOffMap(u) {
		t.Fatal("fixture: the unit is not off the map")
	}

	const tick = uint32(1000)
	code := sys.legAirToGround(u, n, tick)
	if code != 1 {
		t.Fatalf("result code %d, want 1: phase 2 advances [04 R-AIR-01 §8]", code)
	}
	if n.Phase != 2 {
		t.Fatalf("phase %d, want the forced 2 [04 R-AIR-01 §5]", n.Phase)
	}
	if n.Deadline != int32(tick+30) {
		t.Fatalf("deadline %d, want tick + 30 [04 R-AIR-01 §5]", n.Deadline)
	}
	if n.DynamicGate != airLegGateStrike {
		t.Fatalf("gate %#x, want phase 2's %#x — not the recovery leg's 0xE0 [04 R-AIR-01 §8]", n.DynamicGate, airLegGateStrike)
	}
	fl = sys.Flights[u.Handle]
	if fl == nil || fl.Command == nil {
		t.Fatal("no flight command block")
	}
	m, ok := fl.Command.Payload.(*airMarker)
	if !ok || m == nil {
		t.Fatal("phase 2 installed no marker")
	}
	if m.goal.X != target.X || m.goal.Z != target.Z {
		t.Fatalf("marker at (%v,%v), want the cached goal (%v,%v): a recovery marker toward the map centre was built instead [04 R-AIR-01 §5]",
			m.goal.X, m.goal.Z, target.X, target.Z)
	}
	if r := int32(int16(m.radius)); r != 200 {
		t.Fatalf("marker radius %d, want the first weapon slot's Range 200 [04 R-AIR-01 §8]", r)
	}
}
