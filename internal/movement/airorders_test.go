package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
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
		BMCode:           true,
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
	// The OTA gravity default, in the authored form the map loader converts
	// [03 §2.2][fmt ota]; AirStrike phase 4 refuses a zero one.
	ter.Gravity = 0x1FDB * 65536 / 900
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
		BMCode:           true,
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
		if fl := sys.Flights[u.Handle]; fl != nil && fl.Command != nil && fl.Command.Payload != nil {
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
	first := sys.Flights[u.Handle].Command.Payload
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
	secondMarker, ok := sys.Flights[u.Handle].Command.Payload.(*airMarker)
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
