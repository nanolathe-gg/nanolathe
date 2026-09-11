package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
		runLandingTick(sys, tick, w)
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
	pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)

	for tick := uint32(61); tick <= 400; tick++ {
		runLandingTick(sys, tick, w)
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

// TestLandSearchTakesANewMoveOrderAtOnce locks the contract behind the
// play-test report "a plane over water hunting for a landing spot refuses new
// move orders until after it finds one".
//
// The aircraft is over an all-water map, so the landing-legality test of
// [04 R-AIR-01 §6a] refuses every candidate and `VTOL_LandIfCan` phase 1 loops
// its twelve-candidate search forever. A plain (non-queued) move order is then
// issued the way the session's command boundary issues one — the Replace purge
// of [04 R-MOV-03 §6] plus the leading-auto drop, then the producer insertion
// [04 §3.1] — and the aircraft must abandon the search and steer at the new
// goal, not finish the search first.
//
// The regression this locks is not in the queue: the purge always removed the
// record. It is that `ActivateMove` submitted a GROUND path request for the
// `VTOL_Move` record, and "aircraft never enter this scheduler … no A* runs for
// it" [04 R-PATH-01 §8]. Over water that search fails, the publisher raises the
// "cannot get there" bit `0x40` on the record that owns the goal
// [04 R-ORD-01 §0], and `VTOL_Move`'s gate is exactly `0xE0` — so the brand-new
// order completed on the tick it was issued and the pump refilled the standby
// chain. Asserting the pending word stays clear of the five movement bits is
// what catches a re-introduced ground search; asserting the commanded goal
// moves is what catches the symptom.
func TestLandSearchTakesANewMoveOrderAtOnce(t *testing.T) {
	sys, w, u := waterAirFixture(t)
	pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
	tick := uint32(0)
	for i := 0; i < 120; i++ {
		tick++
		runLandingTick(sys, tick, w)
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("mode=%d: the aircraft never got airborne, so it is not in the land search", u.Move.Mode)
	}
	if name := headDescriptorName(u); name != "VTOL_LandIfCan" {
		t.Fatalf("head is %q, want the still-running VTOL_LandIfCan: an all-water map has no landable cell [04 R-AIR-01 §6a]", name)
	}

	// The session's non-queued issue, verbatim [04 §3.1][04 R-MOV-03 §6].
	q := orderQueueOf(t, u)
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	id := orders.Lookup("VTOL_Move")
	goalX, goalZ := world.CellToWorld(56), world.CellToWorld(56)
	q.Push(id, orders.NewNodeForOrder(id, 0, goalX, 0, goalZ, tick, u.Handle, false))
	if q.LenPrimary() != 1 || headDescriptorName(u) != "VTOL_Move" {
		t.Fatalf("after the Replace issue the queue is %d records headed by %q, want one VTOL_Move [04 §3.1]", q.LenPrimary(), headDescriptorName(u))
	}
	node := q.Primary()[0]

	// The session's move boundary reaches ActivateMove for every `VTOL_Move`
	// head, so the test drives the same seam rather than asserting about a call
	// it never makes.
	if sys.ActivateMove(u, node) {
		t.Fatal("ActivateMove bound an aircraft into the ground path scheduler [04 R-PATH-01 §8]")
	}

	// Two ticks with the path scheduler running, as the session loop runs it:
	// the executor's phase 0 is the takeoff preamble, which builds no climb
	// marker for an aircraft already airborne, and phase 1 installs the
	// destination marker [04 R-ORD-02 §2].
	for i := 0; i < 2; i++ {
		tick++
		runLandingTick(sys, tick, w)
		sys.Scheduler.Tick(tick)
	}
	if node.Satisfied&goalPendingMask&^0x20 != 0 {
		t.Fatalf("the air move record carries movement pending bits %#x: a ground path search ran for a can-fly mover [04 R-PATH-01 §8]", node.Satisfied&goalPendingMask)
	}
	if sys.HasPathRequest(u.Handle) {
		t.Fatal("a ground path request is outstanding for an aircraft [04 R-PATH-01 §8]")
	}
	fl := handleRow(sys.Flights, u.Handle)
	if fl == nil || fl.Command == nil || fl.Command.Payload == nil {
		t.Fatal("the air move installed no goal payload, so the aircraft has no command to fly [04 R-AIR-01 §1]")
	}
	// The commanded position is the new goal, snapped onto the unit's own
	// footprint; the search leg's own marker is gone.
	if dx := absFixed(fl.Command.Pos.X - goalX); dx > numeric.Fixed(16<<16) {
		t.Fatalf("commanded X %d is not the new goal %d: the plane is still flying the land search", fl.Command.Pos.X, goalX)
	}
	if dz := absFixed(fl.Command.Pos.Z - goalZ); dz > numeric.Fixed(16<<16) {
		t.Fatalf("commanded Z %d is not the new goal %d: the plane is still flying the land search", fl.Command.Pos.Z, goalZ)
	}
}

// headDescriptorName names the record the air executor dispatches on.
func headDescriptorName(u *units.Unit) string {
	h := airHeadFor(u)
	if h == nil {
		return ""
	}
	return orders.DescriptorFor(h.ID).Name
}

// waterAirFixture is airFixture's aircraft over an all-water map: every cell's
// derived height is below the sea-level byte, so the landing-legality test of
// [04 R-AIR-01 §6a] refuses the whole map for a non-amphibious can-fly
// definition and the ground-landing search never terminates.
func waterAirFixture(t *testing.T) (*System, *units.World, *units.Unit) {
	t.Helper()
	// A plain aircraft: `canfly`, not `amphibious`, and the maximum water depth
	// an aircraft that authors only the one spelling compiles to.
	return waterAirFixtureFor(t, false, 0)
}

// TestSeaplaneLandsOnWater is the second half of the play-test report
// "seaplanes don't land on or in the water".
//
// A seaplane is a `canfly` definition that also authors `amphibious` — all
// eight stock ones do, and no `canfly` definition in the install authors
// `floater`, `waterline`, `upright` or `canhover`. It also authors two
// spellings of its maximum water depth inside one `[UNITINFO]`, and retail
// reads the second: 255, not 0 [fmt tdf "Duplicate keys"]. Those two facts
// together are the whole mechanism. The landing-legality predicate forms its
// water floor as `seaLevel − maxWaterDepth` and then raises it back to sea
// level for a `canfly` definition that is NOT `amphibious` [04 R-AIR-01 §6a]:
// a seaplane keeps a floor 255 below and water is landable ground, while every
// other aircraft is raised back and it is not.
//
// The regression this locks is the pair, not either half. With a maximum depth
// of 0 the floor already IS sea level, the `amphibious` arm can never fire, and
// no aircraft at all can set down on water — which is what this build did.
//
// Where a landed seaplane comes to rest is the seabed, locked separately by
// TestLandedSeaplaneRestsOnTheSeabed [04 R-AIR-01 §6 "Touchdown"]. This test
// asserts that it lands and that a non-amphibious aircraft still does not,
// never the resting altitude.
func TestSeaplaneLandsOnWater(t *testing.T) {
	// What [fmt tdf "Duplicate keys"] resolves `maxwaterdepth` to for the twelve
	// aircraft records that author both spellings.
	const retailAircraftWaterDepth = 255

	seaplane, w, u := waterAirFixtureFor(t, true, retailAircraftWaterDepth)
	if !seaplane.landable(u, u.X, u.Z) {
		t.Fatal("an amphibious aircraft over water reports its own position unlandable: the water floor must stay " +
			"at seaLevel-maxWaterDepth for an amphibious can-fly definition [04 R-AIR-01 §6a]")
	}
	if !flyThenLand(t, seaplane, w, u) {
		t.Fatalf("the seaplane never returned to grounded mode 1 (mode=%d): it refuses to land on water", u.Move.Mode&0x3)
	}
	if h, sea, ok := seaplane.terrainAndSea(u.X, u.Z); !ok || h > sea {
		t.Fatalf("the seaplane came down on a cell whose terrain %d is above the sea level %d, but the fixture is all water", h, sea)
	}

	// The same definition without `amphibious` is refused every cell on the map,
	// which is the other half of the rule.
	plain, w2, u2 := waterAirFixtureFor(t, false, retailAircraftWaterDepth)
	if plain.landable(u2, u2.X, u2.Z) {
		t.Fatal("a non-amphibious aircraft was allowed to land on water: the aircraft water rule must raise its " +
			"floor back to sea level whatever the authored depth allows [04 R-AIR-01 §6a]")
	}
	if flyThenLand(t, plain, w2, u2) {
		t.Fatal("a non-amphibious aircraft landed on water [04 R-AIR-01 §6a]")
	}
}

// flyThenLand lifts the aircraft with a `VTOL_Move` to its own position, then
// hands it `VTOL_LandIfCan` and reports whether it reached the grounded mode 1
// inside the search's budget [04 R-AIR-01 §6].
func flyThenLand(t *testing.T, sys *System, w *units.World, u *units.Unit) bool {
	t.Helper()
	pushAirOrder(t, u, "VTOL_Move", u.X, u.Z)
	tick := uint32(0)
	for i := 0; i < 60; i++ {
		tick++
		runLandingTick(sys, tick, w)
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("mode=%d after the climb, want the airborne 2", u.Move.Mode&0x3)
	}
	q := orderQueueOf(t, u)
	q.SetPrimary(nil)
	pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
	for i := 0; i < 900; i++ {
		tick++
		runLandingTick(sys, tick, w)
		if u.Move.Mode&0x3 == 1 {
			return true
		}
	}
	return false
}

func waterAirFixtureFor(t *testing.T, amphibious bool, maxWaterDepth int32) (*System, *units.World, *units.Unit) {
	t.Helper()
	const sea = 40
	ter := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: sea, Gravity: 0x1FDB, Plot: make([]world.PlotCell, 64*64)}
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
		ter.Plot[i].SetHeight(sea - 5)
		ter.Plot[i].SetMinHeight(sea - 5)
		ter.Plot[i].SetMaxHeight(sea - 5)
	}
	// The aircraft movement record every stock aircraft compiles to: no
	// movementclass, so the unit-local scratch record carries the FBI's own
	// keys [02 §5 "Movement class record"].
	prof := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: maxWaterDepth, MaxSlope: 10, BadSlope: 5, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, prof, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	def := setScratchMovement(&content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("waterscout")},
		UnitName:         "waterscout",
		CanFly:           true,
		CanMove:          true,
		Amphibious:       amphibious,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		CruiseAlt:        80,
		BankScale:        65536,
		MaxVelocity:      4 * 65536,
		Acceleration:     65536 / 4,
		BrakeRate:        65536 / 8,
		TurnRate:         500,
	}, prof)
	x, z := world.CellToWorld(8), world.CellToWorld(8)
	h, err := w.Create(def, 0, x, ter.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	q := orders.QueueForUnit(u)
	sim := rng.NewSimulation(0x12345677)
	q.SetBinding(&orders.QueueBinding{SimRNG: &sim, Lookup: w.Unit})
	return sys, w, u
}
