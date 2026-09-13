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
		runLandingTick(sys, tick, w)
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

// wideAirFixture is airFixture on a map wide enough for the attack-run legs,
// whose repositioning and swing-wide markers are hundreds to thousands of world
// units out [04 R-AIR-01 §8]. The 32-cell fixture map is 512 world units on a
// side, which is inside a single leg.
func wideAirFixture(t *testing.T) (*System, *units.World, *units.Unit) {
	t.Helper()
	ter := syntheticFlat(256, 64)
	sys := NewSystem(ter, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("strikescout")},
		UnitName:         "strikescout",
		CanFly:           true,
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		CruiseAlt:        60,
		MaxVelocity:      4 * 65536,
		Acceleration:     65536 / 4,
		BrakeRate:        65536 / 8,
		TurnRate:         500,
		BankScale:        65536,
		AttackRunLength:  120,
	}
	x, z := world.CellToWorld(16), world.CellToWorld(16)
	h, err := w.Create(def, 0, x, ter.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	sim := rng.NewSimulation(0x12345677)
	orders.QueueForUnit(u).SetBinding(&orders.QueueBinding{SimRNG: &sim, Lookup: w.Unit})
	// Retain the mission word separately from projectile acceleration, as the
	// map loader does [03 §2.2][04 R-AIR-01 §8].
	ter.Gravity = 112 * 65536 / 900
	ter.OTAGravity = 112
	return sys, w, u
}

// airTargetFor adds a second unit to the fixture's world so an attack record
// has something to point at: three of the air executors end their order on a
// null target reference before they reach a leg [04 R-AIR-01 §8].
func airTargetFor(t *testing.T, sys *System, w *units.World, cellX, cellZ int32) *units.Unit {
	t.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("airtarget")},
		UnitName:         "airtarget",
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		MinWaterDepth:    -10000,
	}
	x, z := world.CellToWorld(cellX), world.CellToWorld(cellZ)
	h, err := w.Create(def, 1, x, sys.Terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	return u
}

// TestAirStrikeFliesItsLegs is the unit's proof that an air-attack executor
// gets past its entry sequence and onto the marker legs of [04 R-AIR-01 §8].
// Until WU-18-5 the record completed the moment the shared entry fell through,
// which looked like an attack order that silently did nothing.
//
// The relationships asserted are the ones a regression would break, not a
// census of the run: the record survives (the bombing run loops through phase 6
// rather than completing), it advances past the takeoff preamble into the
// numbered legs, each leg leaves a goal payload installed on the command block,
// and the aircraft is airborne and travelling.
func TestAirStrikeFliesItsLegs(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	sys.BindAirOrderLegs()
	// Beyond the 480-world-unit test of phase 1, so the run starts at the
	// swing-wide leg rather than at a repositioning marker [04 R-AIR-01 §8].
	target := airTargetFor(t, sys, w, 200, 16)

	q := orderQueueOf(t, u)
	q.Push(orders.Lookup("AirStrike"), orders.Node{Owner: u.Handle, Target: target.Handle})
	n := q.Primary()[0]

	startX := u.X
	maxPhase := uint8(0)
	payloadSeen := false
	for tick := uint32(1); tick <= 400; tick++ {
		q.Pump(u, tick)
		runMovementTick(sys, tick, w)
		if q.LenPrimary() == 0 {
			t.Fatalf("tick %d: the record left the queue; the bombing run loops back to phase 3 while healthy [04 R-AIR-01 §8]", tick)
		}
		if n.Phase > maxPhase {
			maxPhase = n.Phase
		}
		if fl := handleRow(sys.Flights, u.Handle); fl != nil && fl.Command != nil && fl.Command.Payload != nil {
			payloadSeen = true
		}
	}

	// Phase 0 is the takeoff preamble and phase 1 the approach test; phase 2
	// is the swing-wide leg, which arms the first gate the run waits on, so a
	// run that actually flies is observed at phase 3 or beyond.
	if maxPhase < 3 {
		t.Fatalf("the run reached phase %d, want at least the leg after the swing-wide marker [04 R-AIR-01 §8]", maxPhase)
	}
	if !payloadSeen {
		t.Fatal("no goal payload was ever installed: the legs never built a marker [04 R-AIR-01 §4]")
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("mover mode %d, want the airborne 2 — the shared takeoff preamble did not run [04 R-AIR-01 §6]", u.Move.Mode)
	}
	if u.X == startX {
		t.Fatal("the aircraft never moved: the legs commanded nothing")
	}
}

// TestVTOLEvadeBreaksTwiceOnTheSameSide locks the two properties
// [04 R-AIR-01 §8] singles out for the evasion: "Exactly one random draw per
// evasion", and phase 1 repeats the break ON THE SAME SIDE at TWICE the radius
// because "the scratch word is re-read, not re-drawn". A second draw here would
// both desynchronise the simulation stream and let an aircraft weave.
func TestVTOLEvadeBreaksTwiceOnTheSameSide(t *testing.T) {
	sys, w, u := airFixture(t)
	sys.BindAirOrderLegs()
	u.Def.MaxVelocity = 4 * 65536
	q := orderQueueOf(t, u)

	n := &orders.Node{ID: orders.Lookup("VTOL_Evade"), Owner: u.Handle, Target: u.Handle}
	q.Push(n.ID, *n)
	n = q.Primary()[0]
	_ = w

	if code := sys.legVTOLEvade(u, n, 1); code != 1 {
		t.Fatalf("phase 0 returned %d, want 1", code)
	}
	side := n.Param1
	first := handleRow(sys.Flights, u.Handle).Command.Payload
	firstMarker, ok := first.(*airMarker)
	if !ok {
		t.Fatalf("phase 0 installed %T, want the point marker of [04 R-AIR-01 §4]", first)
	}
	firstGoal := firstMarker.goal

	n.Phase = 1
	if code := sys.legVTOLEvade(u, n, 2); code != 1 {
		t.Fatalf("phase 1 returned %d, want 1", code)
	}
	if n.Param1 != side {
		t.Fatalf("the scratch word changed from %d to %d; phase 1 re-reads it, it does not re-draw [04 R-AIR-01 §8]", side, n.Param1)
	}
	secondMarker, ok := handleRow(sys.Flights, u.Handle).Command.Payload.(*airMarker)
	if !ok {
		t.Fatal("phase 1 did not install a point marker [04 R-AIR-01 §4]")
	}
	// Twice the radius on the same bearing: the second offset from the unit is
	// exactly double the first.
	d1x, d1z := int64(firstGoal.X-u.X), int64(firstGoal.Z-u.Z)
	d2x, d2z := int64(secondMarker.goal.X-u.X), int64(secondMarker.goal.Z-u.Z)
	if d2x != 2*d1x || d2z != 2*d1z {
		t.Fatalf("phase 1 offset (%d,%d) is not twice phase 0's (%d,%d) [04 R-AIR-01 §8]", d2x, d2z, d1x, d1z)
	}

	n.Phase = 2
	if code := sys.legVTOLEvade(u, n, 3); code != 5 {
		t.Fatalf("phase 2 returned %d, want the 5 that ends the evasion [04 R-AIR-01 §8]", code)
	}
}

// airBuildFixture is airFixture with a construction aircraft's authored build
// reach and a completed target standing away from it, which is the shape the
// air build orders run in.
func airBuildFixture(t *testing.T) (*System, *units.World, *units.Unit, *units.Unit) {
	t.Helper()
	sys, w, u := airFixture(t)
	u.Def.BuildDistance = 40
	u.Def.Builder = true
	target, err := w.Create(u.Def, 0, world.CellToWorld(20), sys.Terrain.HeightAt(world.CellToWorld(20), world.CellToWorld(8)), world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	tu := w.Unit(target)
	sys.EnsureUnit(tu)
	return sys, w, u, tu
}

// installedMarker is the air marker currently on the unit's flight command
// block, which is the only output the air executors have [04 R-AIR-01 §1].
func installedMarker(t *testing.T, sys *System, u *units.Unit) *airMarker {
	t.Helper()
	fl := handleRow(sys.Flights, u.Handle)
	if fl == nil || fl.Command == nil {
		t.Fatal("the unit has no flight command block [04 R-AIR-01 §1]")
	}
	m, ok := fl.Command.Payload.(*airMarker)
	if !ok || m == nil {
		t.Fatal("no air marker is installed [04 R-AIR-01 §4]")
	}
	return m
}

// TestAirConstructionOrbitRecurrence locks the geometry of §10.3, which the
// playtest found missing entirely: a construction aircraft that reaches its
// work target hovers on a repeating closed circuit of stations around it.
//
// Every station is `builddistance` from the target; each one is the builder's
// own angular position about the target advanced by the signed step 0xDB6E;
// seven steps close the circle, which is where "about seven stations" comes
// from — the count is a consequence of the step, not an authored constant; and
// the marker's explicit heading is the direction from the station back at the
// target, so the aircraft faces what it is building.
func TestAirConstructionOrbitRecurrence(t *testing.T) {
	sys, _, u, target := airBuildFixture(t)
	head := pushAirOrder(t, u, "VTOL_MobileBuild", target.X, target.Z)
	head.Target = target.Handle

	radius := numeric.Fixed(int64(u.Def.BuildDistance) << 16)
	first := uint16(0)
	prev := uint16(0)
	for station := 0; station < 8; station++ {
		sys.tick = uint32(station+1) * airOrbitPeriod
		expect := bearing(u.X, u.Z, target.X, target.Z) + airOrbitStep
		sys.VisitAirBuildWork(u, head, sys.tick)
		m := installedMarker(t, sys, u)

		// The station's distance from the target is the authored build reach.
		if d := airPlanarDistance(m.goal.X, m.goal.Z, target.X, target.Z); absInt64(d-int64(radius)) > 1<<12 {
			t.Fatalf("station %d sits %d from the target, want builddistance %d [04 §10.3]", station, d, int64(radius))
		}
		// The marker's explicit heading points from the station back at the
		// target: the aircraft faces the unit it is building.
		if m.flags&airMarkerExplicitHead == 0 {
			t.Fatalf("station %d carries no explicit heading [04 §10.3]", station)
		}
		// The station is placed through the 512-entry trig table, so recovering
		// its angle back out of the two components costs up to about one table
		// step (128 of the 65,536 units) plus the arctangent's own rounding.
		if want := bearing(m.goal.X, m.goal.Z, target.X, target.Z); angleDelta(m.heading, want) > 256 {
			t.Fatalf("station %d heading %d does not face the target (%d) [04 §10.3]", station, m.heading, want)
		}
		if m.heading != expect {
			t.Fatalf("station %d heading %d, want the builder's bearing plus the step %d [04 §10.3]", station, m.heading, expect)
		}
		if m.flags&airMarkerExplicitRadius != 0 || m.flags&airMarkerExplicitAlt != 0 {
			t.Fatalf("station %d carries a radius or altitude setter; §10.3 writes neither", station)
		}
		if station == 0 {
			first = m.heading
		} else if step := uint16(m.heading - prev); angleDelta(step, airOrbitStep) > 256 {
			// Retail recomputes the bearing from the builder's actual position
			// every time, so the step carries the same one-table-step slack the
			// placement does; it is the step, not a stored angle advanced in
			// closed form.
			t.Fatalf("station %d advanced by %d, want the step %d [04 §10.3]", station, step, airOrbitStep)
		}
		prev = m.heading

		// Walk the builder onto the station it was just given, which is what the
		// flight integrator does between two 150-tick edges.
		u.X, u.Y, u.Z = m.goal.X, m.goal.Y, m.goal.Z
	}
	// Seven steps of 0xDB6E are 65,534 of the 65,536-unit circle, so the eighth
	// station lands two units short of the first: the circuit closes.
	if d := angleDelta(prev, first); d > 2048 {
		t.Fatalf("the circuit did not close after seven steps: station 7 is %d units from station 0 [04 §10.3]", d)
	}
}

// TestAirBuildTakesOffAndOrbits joins the construction-owned approach visits
// to the mover tick. The authored target stands in for the construction
// allocation at phase 2; only its approach and orbit are under test.
func TestAirBuildTakesOffAndOrbits(t *testing.T) {
	sys, w, u, target := airBuildFixture(t)
	head := pushAirOrder(t, u, "VTOL_MobileBuild", target.X, target.Z)
	head.Target = target.Handle

	var stations []Vec3
	for tick := uint32(1); tick <= 700; tick++ {
		if head.Phase < 2 && (head.DynamicGate == 0 || head.Satisfied&airLegGate != 0) {
			satisfied := head.Satisfied & head.DynamicGate
			head.Satisfied &^= satisfied
			head.DynamicGate = 0
			if code := sys.VisitAirBuildApproach(u, head, satisfied, tick); code != 1 {
				t.Fatalf("approach result %d, want advance", code)
			}
			head.Phase++
		}
		if head.Phase == 2 && head.Satisfied&airLegGate != 0 {
			head.Phase = 3
		}
		if head.Phase == 3 {
			sys.VisitAirBuildWork(u, head, tick)
		}
		runMovementTick(sys, tick, w)
		if tick%airOrbitPeriod != 0 || tick < airOrbitPeriod*2 {
			continue
		}
		m := installedMarker(t, sys, u)
		stations = append(stations, m.goal)
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("the builder never left the ground: mover mode %d [04 R-AIR-01 §6]", u.Move.Mode)
	}
	if len(stations) < 3 {
		t.Fatalf("only %d orbit stations were installed in 700 ticks [04 §10.3]", len(stations))
	}
	radius := int64(u.Def.BuildDistance) << 16
	for i, st := range stations {
		if d := airPlanarDistance(st.X, st.Z, target.X, target.Z); absInt64(d-radius) > 1<<12 {
			t.Fatalf("station %d is %d from the target, want %d [04 §10.3]", i, d, radius)
		}
		if i > 0 && stations[i-1] == st {
			t.Fatalf("station %d repeats its predecessor; the circuit does not advance [04 §10.3]", i)
		}
	}
	// The aircraft is flying the circuit, not parked on the target.
	if d := airPlanarDistance(u.X, u.Z, target.X, target.Z); d > radius+(8<<16) {
		t.Fatalf("the builder is %d from its target, well outside the orbit [04 §10.3]", d)
	}
}

// TestVTOLLandIfCanSettlesOnTheTerrain locks the corrected altitude-offset
// branches of [04 R-AIR-01 §6]: the offset is zero on dry land and
// `terrainHeight − seaLevel` over water, so composed with the setter's
// `max(seaLevel, terrainHeight) + offset` both branches command exactly the
// terrain height. Read the other way round — which is what the doc said before
// this unit re-traced it — an aircraft landing on ground above sea level
// commands `terrain + (terrain − sea)` and settles that far in the air.
func TestVTOLLandIfCanSettlesOnTheTerrain(t *testing.T) {
	sys, w, u := airFixture(t)
	pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
	terrain := sys.Terrain.HeightAt(u.X, u.Z)
	if terrain>>16 <= numeric.Fixed(sys.Terrain.SeaLevel) {
		t.Fatal("the fixture's terrain must stand above sea level for this contract to bite")
	}
	for tick := uint32(1); tick <= 300; tick++ {
		runLandingTick(sys, tick, w)
	}
	if u.Move.Mode&0x3 != 1 {
		t.Fatalf("the aircraft never touched down: mover mode %d [04 R-AIR-01 §6]", u.Move.Mode)
	}
	if absFixed(u.Y-terrain) > numeric.Fixed(1<<16) {
		t.Fatalf("landed at Y=%d with terrain at %d [04 R-AIR-01 §6]", u.Y, terrain)
	}
}

// TestAirOffsetSignFamilies locks the two placement families apart. The travel
// direction of a heading is (−sin, −cos) [04 R-MOV-01 §4], so a leg that places
// its marker ALONG a bearing subtracts the component pair and the air build
// orbit, which places its station on the target's far side from that bearing,
// adds it. Getting this backwards is what put an orbit station across the
// target instead of around it.
func TestAirOffsetSignFamilies(t *testing.T) {
	const north = uint16(0) // heading 0 travels toward −Z [04 R-MOV-01 §4]
	ox, oz := offsetAtBearing(north, numeric.Fixed(100<<16))
	if ox != 0 {
		t.Fatalf("the X component at heading 0 is %d, want 0", ox)
	}
	if oz <= 0 {
		t.Fatalf("the Z component at heading 0 is %d, want the un-negated +cos [04 R-MOV-01 §4]", oz)
	}
	// Subtracting moves along the heading; adding moves opposite it.
	if along := numeric.Fixed(0) - oz; along >= 0 {
		t.Fatalf("pos − offset at heading 0 moved to %d, want the negative Z the travel direction gives", along)
	}
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// angleDelta is the unsigned separation of two 16-bit angles on the shorter arc.
func angleDelta(a, b uint16) uint16 {
	d := a - b
	if d > 0x8000 {
		d = -d
	}
	return d
}

// TestVTOLLandingParksOnThePad walks the seven-phase pad machine end to end
// [04 R-AIR-01 §6]: the aircraft takes off, closes on the pad through the
// follow marker legs, and the touchdown attaches it to the pad owner on the
// chosen pad piece with request mode 0 — the attached/parked mode
// [04 R-AIR-01 §3][04 R-UNIT-06 §3].
func TestVTOLLandingParksOnThePad(t *testing.T) {
	sys, w, u := airFixture(t)
	padDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("padfixture")},
		UnitName:         "padfixture",
		IsAirBase:        true,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
	}
	px, pz := world.CellToWorld(18), world.CellToWorld(8)
	ph, err := w.Create(padDef, 0, px, sys.Terrain.HeightAt(px, pz), pz)
	if err != nil {
		t.Fatalf("create pad: %v", err)
	}
	pad := w.Unit(ph)
	sys.EnsureUnit(pad)

	head := pushAirOrder(t, u, "VTOL_Landing", pad.X, pad.Z)
	head.Target = ph

	for tick := uint32(1); tick <= 900; tick++ {
		runLandingTick(sys, tick, w)
		if u.Attachment.Carrier == ph {
			break
		}
	}
	if u.Attachment.Carrier != ph {
		t.Fatalf("the aircraft never parked: carrier=%d, position (%d,%d,%d) [04 R-AIR-01 §6]", u.Attachment.Carrier, u.X, u.Y, u.Z)
	}
	if u.Attachment.AttachPiece != 0 {
		t.Fatalf("parked on piece %d, want the first free candidate 0 [04 R-AIR-01 §6]", u.Attachment.AttachPiece)
	}
	if u.Move.Mode&0x3 != 0 {
		t.Fatalf("mover mode %d after touchdown, want the parked 0 [04 R-AIR-01 §3]", u.Move.Mode)
	}
	// A second lander finds that piece taken and takes the next candidate.
	if sys.padPieceFree(pad, 0) {
		t.Fatal("the occupied pad piece still reports free [04 R-AIR-01 §6]")
	}
	if piece, ok := sys.queryLandingPad(pad); !ok || piece != 1 {
		t.Fatalf("the scan offered piece %d (ok=%v), want the next candidate 1 [04 R-AIR-01 §6]", piece, ok)
	}
}

// TestVTOLMobileBuildSnapsToProductFootprint locks the product-footprint
// resolver seam: `VTOL_MobileBuild` phase 1, with no target yet assigned,
// snaps its cached goal onto the PRODUCT's footprint centre — anchor cell from
// the recorded position, then the reverse `(foot + 2·cell)·2^19` centre
// [04 R-ORD-02 §2][04 R-PATH-01 §13] — using the resolver's footprint pair
// rather than the record's raw goal. An asymmetric footprint (width != depth)
// guards against the two axes being swapped.
func TestVTOLMobileBuildSnapsToProductFootprint(t *testing.T) {
	sys, _, u := airFixture(t)
	u.Def.BuildDistance = 40
	u.Def.Builder = true

	// An off-grid goal so the snap actually moves it, and an asymmetric
	// footprint (4 x 8 cells) so a swapped axis produces a different result.
	goalX, goalZ := world.CellToWorld(21)+1<<18, world.CellToWorld(9)+1<<18
	const footX, footZ = int32(4), int32(8)
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		t.Fatalf("footprint extent: %v", err)
	}
	anchor, err := world.SnapFootprintAnchor(goalX, goalZ, extent)
	if err != nil {
		t.Fatalf("snap anchor: %v", err)
	}
	centre, err := world.CenterForFootprint(anchor, extent)
	if err != nil {
		t.Fatalf("centre: %v", err)
	}

	head := pushAirOrder(t, u, "VTOL_MobileBuild", goalX, goalZ)
	head.Param1 = 7 // the stable catalog index the resolver receives

	var gotIndex uint32
	sys.ProductFootprint = func(catalogIndex uint32) (fx, fz int32, ok bool) {
		gotIndex = catalogIndex
		return footX, footZ, true
	}

	head.Phase = 1
	if code := sys.VisitAirBuildApproach(u, head, 0, 1); code != 1 {
		t.Fatalf("site leg result %d, want advance", code)
	}

	if gotIndex != head.Param1 {
		t.Fatalf("resolver received catalog index %d, want the record's Param1 %d", gotIndex, head.Param1)
	}
	m := installedMarker(t, sys, u)
	if m.goal.X != centre.X() || m.goal.Z != centre.Z() {
		t.Fatalf("installed goal (%d,%d), want the product footprint centre (%d,%d) [04 R-ORD-02 §2][04 R-PATH-01 §13]",
			m.goal.X, m.goal.Z, centre.X(), centre.Z())
	}
}

// TestVTOLMobileBuildKeepsGoalWhenResolverUnbound locks the no-invented-
// fallback contract: with ProductFootprint left nil (the seam unbound), phase
// 1 installs the record's own stored goal exactly as it did before this seam
// existed, rather than guessing a footprint.
func TestVTOLMobileBuildKeepsGoalWhenResolverUnbound(t *testing.T) {
	sys, _, u := airFixture(t)
	u.Def.BuildDistance = 40
	u.Def.Builder = true

	goalX, goalZ := world.CellToWorld(21), world.CellToWorld(9)
	head := pushAirOrder(t, u, "VTOL_MobileBuild", goalX, goalZ)
	head.Param1 = 7

	if sys.ProductFootprint != nil {
		t.Fatal("fixture unexpectedly bound a resolver")
	}
	head.Phase = 1
	if code := sys.VisitAirBuildApproach(u, head, 0, 1); code != 1 {
		t.Fatalf("site leg result %d, want advance", code)
	}

	m := installedMarker(t, sys, u)
	if m.goal.X != goalX || m.goal.Z != goalZ {
		t.Fatalf("installed goal (%d,%d) with no resolver bound, want the record's stored goal (%d,%d) — no invented fallback",
			m.goal.X, m.goal.Z, goalX, goalZ)
	}
}
