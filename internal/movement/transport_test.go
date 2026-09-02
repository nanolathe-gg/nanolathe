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

// transportFixture builds the smallest world the two air transport executors of
// [04 §10.2] need: a flat map, an air carrier with a cruise altitude, a ground
// cargo, one shared queue binding whose presentation adapter records every
// status kind raised, and the movement runner bound so the descriptor handlers
// reach their legs.
func transportFixture(t *testing.T) (*System, *units.World, *units.Unit, *units.Unit, *rng.Simulation, *[]uint8) {
	t.Helper()
	ter := syntheticFlat(64, 64)
	sys := NewSystem(ter, Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)

	carrierDef := &content.UnitDef{
		DefinitionHeader:  content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armatlas")},
		UnitName:          "armatlas",
		CanFly:            true,
		CanLoad:           true,
		CanMove:           true,
		BMCode:            true,
		FootprintX:        1,
		FootprintZ:        1,
		MaxDamage:         100,
		CruiseAlt:         60,
		TransportSize:     4,
		TransportCapacity: 1,
		MaxVelocity:       6 * 65536,
		Acceleration:      65536,
		BrakeRate:         65536 / 2,
		TurnRate:          2000,
		BankScale:         65536,
		MinWaterDepth:     -10000,
	}
	cargoDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armpw")},
		UnitName:         "armpw",
		CanMove:          true,
		BMCode:           true,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		ModelTop:         6,
		ModelTopFixed:    6 * 65536,
		MinWaterDepth:    -10000,
		// `upright` with `canhover` clear selects the first of the four
		// post-move Y branches, `Y = terrainHeight(unitXZ) << 16`
		// [04 R-MOV-01 §5], so the released cargo's height has one settled
		// value to assert rather than the four-corner conform's "writes
		// nothing without a selection primitive" arm.
		Upright: true,
	}
	cx, cz := world.CellToWorld(8), world.CellToWorld(8)
	ch, err := w.Create(carrierDef, 0, cx, ter.HeightAt(cx, cz), cz)
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	gx, gz := world.CellToWorld(30), world.CellToWorld(8)
	gh, err := w.Create(cargoDef, 0, gx, ter.HeightAt(gx, gz), gz)
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	carrier, cargo := w.Unit(ch), w.Unit(gh)
	sys.EnsureUnit(carrier)
	sys.EnsureUnit(cargo)

	sim := rng.NewSimulation(0x12345677)
	kinds := &[]uint8{}
	binding := &orders.QueueBinding{
		SimRNG: &sim,
		Lookup: w.Unit,
		Presentation: &orders.PresentationAdapter{
			Ready: func() bool { return true },
			Status: func(_ *units.Unit, kind uint8, _ string) bool {
				*kinds = append(*kinds, kind)
				return true
			},
		},
	}
	orders.QueueForUnit(carrier).SetBinding(binding)
	orders.QueueForUnit(cargo).SetBinding(binding)
	sys.BindAirOrderLegs()
	return sys, w, carrier, cargo, &sim, kinds
}

func transportCountKind(kinds []uint8, want uint8) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

func transportHeadPhase(q *orders.Queue) int {
	if q == nil || q.LenPrimary() == 0 {
		return -1
	}
	return int(q.Primary()[0].Phase)
}

// TestAtlasLoadsCarriesAndUnloadsAPeewee is this unit's end-to-end proof, and
// it locks the relationships §10.2 states rather than a census of the run:
//
//   - the load flies: phase 0's takeoff preamble puts the carrier airborne and
//     phase 1's follow marker carries it to the cargo, so the attach happens
//     within the follow leg's horizontal arrival radius 0x30 rather than across
//     the map;
//   - the attach is request mode 0, which writes no occupancy word
//     [04 R-COLL-01 §4], and the carried branch slaves the cargo to the carrier
//     every tick;
//   - notification event 12 is published exactly once [04 §10.2];
//   - the unload validates the site and releases with request mode 1, and the
//     release writes NO position: the cargo holds the X and Z of the hang point
//     it had before the release tick, not the drop point's footprint centre
//     [04 R-AIR-01 §10] item 2;
//   - the cargo's OWN mover tick — which the sweep runs for it in the same
//     visit pass, with NO order of its own [04 R-MOV-03 §1] step 9 — commits at
//     that actual X/Z, stamps the ground word there, and lets [04 R-MOV-01 §5]'s
//     `upright`-without-`canhover` branch write `terrainHeight(XZ) << 16`;
//   - notification event 13 is published exactly once;
//   - neither executor draws from the simulation stream: §10.2's two phase
//     tables name no random value anywhere (I4).
func TestAtlasLoadsCarriesAndUnloadsAPeewee(t *testing.T) {
	sys, w, carrier, cargo, sim, kinds := transportFixture(t)
	q := orders.QueueForUnit(carrier)
	pickup := orders.Lookup("VTOL_Pickup")
	if pickup == 0 {
		t.Fatal("VTOL_Pickup missing from the order table")
	}
	q.Push(pickup, orders.Node{Owner: carrier.Handle, Target: cargo.Handle, Deadline: -1})

	tick := uint32(1)
	for ; tick <= 900 && cargo.Attachment.Carrier == 0; tick++ {
		q.Pump(carrier, tick)
		runMovementTick(sys, tick, w)
	}
	if cargo.Attachment.Carrier != carrier.Handle {
		t.Fatalf("the Atlas never attached its cargo; record phase %d, carrier at %d,%d cargo at %d,%d",
			transportHeadPhase(q), carrier.X.Raw()>>16, carrier.Z.Raw()>>16, cargo.X.Raw()>>16, cargo.Z.Raw()>>16)
	}
	if carrier.Move.Mode&0x3 != 2 {
		t.Fatalf("carrier mover mode %d at the attach, want the airborne 2 — the takeoff preamble did not run [04 R-AIR-01 §6]", carrier.Move.Mode)
	}
	// The follow leg's horizontal arrival radius is 0x30 world units
	// [04 §10.2], so the attach cannot happen further out than that. Measured
	// before the carried branch has slaved the cargo onto the carrier.
	if d := airPlanarDistance(carrier.X, carrier.Z, cargo.X, cargo.Z); d > 0x30<<16 {
		t.Fatalf("attach distance %d world units exceeds the follow leg's 0x30 arrival radius [04 §10.2]", d>>16)
	}
	if cargo.Move.Mode&0x3 != 0 {
		t.Fatalf("cargo mover mode %d after the attach, want the attached mode 0 [04 R-AIR-01 §9]", cargo.Move.Mode)
	}
	if coll := sys.Collisions[cargo.Handle]; coll != nil {
		if _, present := sys.Grid.OccupantAtPlane(PlaneGround, coll.CachedAnchor); present {
			t.Fatal("attached cargo still holds a ground occupancy word; mode 0 writes none [04 R-COLL-01 §4]")
		}
	}
	if got := transportCountKind(*kinds, TransportEventAttach); got != 1 {
		t.Fatalf("notification event 12 published %d times, want exactly one [04 §10.2]", got)
	}

	// The carry: the cargo follows the carrier's motion every tick. The attach
	// piece is the root fallback, so the hang point is the carrier's origin.
	for i := 0; i < 20; i++ {
		q.Pump(carrier, tick)
		runMovementTick(sys, tick, w)
		tick++
		if cargo.X != carrier.X || cargo.Z != carrier.Z {
			t.Fatalf("carried cargo at %d,%d, carrier at %d,%d: the carried branch is not slaving it [04 §10.2]",
				cargo.X.Raw()>>16, cargo.Z.Raw()>>16, carrier.X.Raw()>>16, carrier.Z.Raw()>>16)
		}
	}

	// The unload, at a drop point the carrier must fly to.
	dropX, dropZ := world.CellToWorld(44), world.CellToWorld(20)
	unload := orders.Lookup("VTOL_Unload")
	if unload == 0 {
		t.Fatal("VTOL_Unload missing from the order table")
	}
	q.Push(unload, orders.Node{Owner: carrier.Handle, GoalX: dropX, GoalY: carrier.Y, GoalZ: dropZ, Deadline: -1})
	// The hang point the cargo holds going INTO the tick that releases it. The
	// carried branch slaves the cargo every tick while it is aboard, so this is
	// what "the cargo keeps its hang position" has to mean at the release.
	// Only X and Z survive the release tick: the cargo's own mover tick runs
	// later in the same sweep and [04 R-MOV-01 §5] owns its Y.
	var hangX, hangZ, hangY numeric.Fixed
	for ; tick <= 2400 && cargo.Attachment.Carrier != 0; tick++ {
		hangX, hangY, hangZ = cargo.X, cargo.Y, cargo.Z
		q.Pump(carrier, tick)
		runMovementTick(sys, tick, w)
	}
	if cargo.Attachment.Carrier != 0 {
		t.Fatalf("the cargo was never released; record phase %d, carrier at %d,%d",
			transportHeadPhase(q), carrier.X.Raw()>>16, carrier.Z.Raw()>>16)
	}
	// The release writes NO position [04 R-AIR-01 §10] item 2: the detach's
	// apply step writes linkage and the mover mode only.
	if cargo.X != hangX || cargo.Z != hangZ {
		t.Fatalf("the release moved the cargo from its hang point %d,%d,%d to %d,%d,%d; the detach writes no X, Y or Z [04 R-AIR-01 §10]",
			hangX.Raw()>>16, hangY.Raw()>>16, hangZ.Raw()>>16, cargo.X.Raw()>>16, cargo.Y.Raw()>>16, cargo.Z.Raw()>>16)
	}
	// And specifically not the snap the build used to perform: the cargo is NOT
	// re-centred onto the footprint anchor the executor validated.
	anchorX, anchorZ := world.PlacementAnchor(dropX, dropZ, 1, 1)
	centreX, centreZ := world.PlacementCenter(anchorX, anchorZ, 1, 1)
	if cargo.X == centreX && cargo.Z == centreZ {
		t.Fatalf("released cargo sits exactly on the validated footprint centre %d,%d; retail leaves it at its hang point [04 R-AIR-01 §10]",
			centreX.Raw()>>16, centreZ.Raw()>>16)
	}
	if cargo.Move.Mode&0x3 != 1 {
		t.Fatalf("released cargo mover mode %d, want the grounded 1 [04 R-AIR-01 §9]", cargo.Move.Mode)
	}
	coll := sys.Collisions[cargo.Handle]
	if coll == nil {
		t.Fatal("released cargo has no collision record")
	}
	// The cargo's OWN commit is what settles the rest, with NO order of its own:
	// the sweep runs the mover tick for every live unit that has a mover
	// [04 R-MOV-03 §1] step 9, and the cargo's slot follows the carrier's in the
	// same sweep, so the release tick already carries it. The commit runs at the
	// cargo's actual X/Z — which is why the mover record holds the hang point and
	// not the validated anchor's centre — and [04 R-MOV-01 §5] writes Y. Before
	// WU-19-29 this needed a `Move_Ground` order pushed onto the cargo to reach
	// the ground branch at all.
	if coll.X != int32(cargo.X.Raw()) || coll.Z != int32(cargo.Z.Raw()) {
		t.Fatalf("mover record at %d,%d, unit at %d,%d: the commit must run at the cargo's actual position [04 R-AIR-01 §10]",
			coll.X>>16, coll.Z>>16, cargo.X.Raw()>>16, cargo.Z.Raw()>>16)
	}
	// The `BeCarried` the attach armed retires here: its carrier link is null.
	orders.QueueForUnit(cargo).Pump(cargo, tick)
	if cq := orders.QueueOfUnit(cargo); cq != nil && cq.Head() != nil {
		t.Fatalf("the released cargo holds order %q; this assertion is about an ORDERLESS mover [04 R-MOV-03 §1]",
			orders.DescriptorFor(cq.Head().ID).Name)
	}
	if want := sys.Terrain.HeightAt(cargo.X, cargo.Z); cargo.Y != want {
		t.Fatalf("released cargo Y=%d after its first commit, want the `upright` branch's terrain height %d [04 R-MOV-01 §5]",
			cargo.Y.Raw()>>16, want.Raw()>>16)
	}
	if got, present := sys.Grid.OccupantAtPlane(PlaneGround, coll.CachedAnchor); !present || got != coll.ID {
		t.Fatalf("released cargo ground occupancy = (%d,%t), want its own id %d [04 R-COLL-01 §4]", got, present, coll.ID)
	}
	// Phase 3's event follows the phase-2 release once the climb-away marker
	// the release constructs has been reached.
	for ; tick <= 3200 && transportCountKind(*kinds, TransportEventDetach) == 0; tick++ {
		q.Pump(carrier, tick)
		runMovementTick(sys, tick, w)
	}
	if got := transportCountKind(*kinds, TransportEventDetach); got != 1 {
		t.Fatalf("notification event 13 published %d times, want exactly one [04 §10.2]", got)
	}
	if sim.Draws() != 0 {
		t.Fatalf("the transport executors drew %d simulation values; §10.2's phase tables name none (I4)", sim.Draws())
	}
}

// TestUnloadRefusesASiteThePlacementValidatorRejects locks §10.2's unload
// phase 1: the drop point becomes a footprint anchor from the cargo's packed
// footprint dimensions and the cargo definition is validated through the
// standard placement validator in mode 1; failure emits `Unable to unload unit`
// and returns 9. The record takes code 9's last-record arm — phase reset plus a
// 30..59-tick re-arm [04 §3.3] — rather than being freed, so the order the
// player still owns keeps retrying.
//
// The site is refused by a reserved feature sentinel on the drop cell, the
// first of mode 1's per-cell rejects [08 R-AI-03 §4].
func TestUnloadRefusesASiteThePlacementValidatorRejects(t *testing.T) {
	sys, w, carrier, cargo, _, kinds := transportFixture(t)
	if !AttachCargoMode(w, carrier.Handle, cargo.Handle, -1, 0) {
		t.Fatal("fixture attach failed")
	}
	carrier.Move.Mode = 2
	if fl := sys.Flights[carrier.Handle]; fl != nil {
		fl.Mode = 2
	}

	dropX, dropZ := world.CellToWorld(44), world.CellToWorld(20)
	cellX, cellZ := world.PlacementAnchor(dropX, dropZ, 1, 1)
	cell := sys.Terrain.PlotAt(cellX, cellZ)
	if cell == nil {
		t.Fatalf("drop cell %d,%d off the fixture map", cellX, cellZ)
	}
	cell.SetFeature(0xFFFB)
	if sys.ValidateUnloadSite(w, cargo.Handle, dropX, dropZ, sys.Terrain) {
		t.Fatal("the placement validator accepted a blocked drop cell [08 R-AI-03 §4]")
	}

	q := orders.QueueForUnit(carrier)
	unload := orders.Lookup("VTOL_Unload")
	q.Push(unload, orders.Node{Owner: carrier.Handle, GoalX: dropX, GoalY: carrier.Y, GoalZ: dropZ, Deadline: -1})
	head := q.Primary()[0]
	for tick := uint32(1); tick <= 2000 && transportCountKind(*kinds, 7) == 0; tick++ {
		q.Pump(carrier, tick)
		runMovementTick(sys, tick, w)
	}
	if transportCountKind(*kinds, 7) == 0 {
		t.Fatalf("the refused unload never raised the `cant` cue carrying `Unable to unload unit`; record phase %d [04 §10.2]", transportHeadPhase(q))
	}
	if cargo.Attachment.Carrier != carrier.Handle {
		t.Fatal("the cargo was released onto a site the validator refuses [04 §10.2]")
	}
	if q.LenPrimary() == 0 {
		t.Fatal("the refused unload record was freed; code 9's last-record arm re-arms it [04 §3.3]")
	}
	if head.Phase != 0 {
		t.Fatalf("refused record phase %d, want code 9's phase reset [04 §3.3]", head.Phase)
	}
}

// TestLoadEntryGatesRejectInOrder locks the four re-checks every phase of the
// load executor runs [04 §10.2]. Gate four is the one that distinguishes the
// air executor from general admission: it requires an EMPTY cargo list even
// though admission only compares the carried count against `transportcapacity`,
// and it returns result 8 with NO message.
func TestLoadEntryGatesRejectInOrder(t *testing.T) {
	sys, w, carrier, cargo, _, kinds := transportFixture(t)

	// Gate 3: a target whose Y plus its model total height is at or below sea
	// level is submerged and refused with `Transport mission failed`.
	cargo.Y = numeric.Fixed((int64(sys.Terrain.SeaLevel) << 16) - int64(cargo.Def.ModelTopFixed))
	n := &orders.Node{Owner: carrier.Handle, Target: cargo.Handle, Deadline: -1}
	if code := sys.legVTOLPickup(carrier, n, 0, 1); code != 8 {
		t.Fatalf("submerged target gave result %d, want 8 [04 §10.2]", code)
	}
	if got := transportCountKind(*kinds, 7); got != 1 {
		t.Fatalf("submerged-target gate raised %d `cant` cues, want one [04 §10.2]", got)
	}
	cargo.Y = sys.Terrain.HeightAt(cargo.X, cargo.Z)

	// Gate 2: the entry mask 0x10048 in the satisfied set.
	*kinds = (*kinds)[:0]
	if code := sys.legVTOLPickup(carrier, n, 0x40, 1); code != 8 {
		t.Fatalf("entry-mask bit 0x40 gave result %d, want 8 [04 §10.2]", code)
	}

	// Gate 4: a non-empty cargo list, result 8 with NO message.
	*kinds = (*kinds)[:0]
	ox, oz := world.CellToWorld(12), world.CellToWorld(12)
	other, err := w.Create(cargo.Def, 0, ox, sys.Terrain.HeightAt(ox, oz), oz)
	if err != nil {
		t.Fatalf("create second cargo: %v", err)
	}
	if !AttachCargoMode(w, carrier.Handle, other, -1, 0) {
		t.Fatal("fixture attach failed")
	}
	if code := sys.legVTOLPickup(carrier, n, 0, 1); code != 8 {
		t.Fatalf("loaded carrier gave result %d, want 8 [04 §10.2]", code)
	}
	if len(*kinds) != 0 {
		t.Fatalf("gate four raised %d status cues, want none — it returns code 8 with NO message [04 §10.2]", len(*kinds))
	}

	// The size gate is phase 0's, not an entry gate, and it carries the
	// `too heavy` message [04 §10.2] — one word different from the ground twin's
	// `too large` [04 R-AIR-01 §9].
	DetachCargoMode(w, other, 1)
	*kinds = (*kinds)[:0]
	sys.profiles[cargo.Handle] = Profile{FootPrintX: int16(carrier.Def.TransportSize) + 1, FootPrintZ: 1}
	if code := sys.legVTOLPickup(carrier, n, 0, 1); code != 8 {
		t.Fatalf("oversize cargo gave result %d, want 8 [04 §10.2]", code)
	}
	if got := transportCountKind(*kinds, 7); got != 1 {
		t.Fatalf("size gate raised %d `cant` cues, want one [04 §10.2]", got)
	}
}

// TestUnloadGateWordsAndTheCannotGetThereInterrupt locks the three unload gate
// assignments and the interrupt bit [04 R-AIR-01 §10] item 4: phase 0 `= 0xE8`,
// phase 1 `= 0xE8`, phase 2 `= 0xE0` — written by assignment, not OR — and the
// phase-2 interrupt is satisfied bit `0x40`, the "cannot get there" outcome of
// [04 R-ORD-01 §0], tested BEFORE the second anchor recompute and validation.
//
// The ordering is what the interrupt case proves: the drop site is made
// invalid, so a revalidation would emit `Unable to unload unit`. With `0x40`
// set the executor returns 9 having raised no cue at all, which can only happen
// if the bit was tested first.
func TestUnloadGateWordsAndTheCannotGetThereInterrupt(t *testing.T) {
	sys, w, carrier, cargo, _, _ := transportFixture(t)
	if !AttachCargoMode(w, carrier.Handle, cargo.Handle, -1, 0) {
		t.Fatal("fixture attach failed")
	}
	carrier.Move.Mode = 2
	if fl := sys.Flights[carrier.Handle]; fl != nil {
		fl.Mode = 2
	}
	dropX, dropZ := world.CellToWorld(44), world.CellToWorld(20)
	n := &orders.Node{Owner: carrier.Handle, GoalX: dropX, GoalY: carrier.Y, GoalZ: dropZ, Deadline: -1}

	for _, row := range []struct {
		phase uint8
		gate  uint32
	}{{0, 0xE8}, {1, 0xE8}, {2, 0xE0}} {
		n.Phase = row.phase
		n.DynamicGate = 0x1 // a stale bit an assignment must drop and an OR would keep
		if code := sys.legVTOLUnload(carrier, n, 0, 1); code != 1 {
			t.Fatalf("unload phase %d gave result %d, want 1 [04 §10.2]", row.phase, code)
		}
		if n.DynamicGate != row.gate {
			t.Fatalf("unload phase %d gate = %#x, want the assignment %#x [04 R-AIR-01 §10]", row.phase, n.DynamicGate, row.gate)
		}
	}
	if cargo.Attachment.Carrier != 0 {
		t.Fatal("phase 2 did not release the cargo")
	}

	// The interrupt, on a fresh record whose site the validator would refuse.
	sys2, w2, carrier2, cargo2, _, kinds2 := transportFixture(t)
	if !AttachCargoMode(w2, carrier2.Handle, cargo2.Handle, -1, 0) {
		t.Fatal("fixture attach failed")
	}
	carrier2.Move.Mode = 2
	cellX, cellZ := world.PlacementAnchor(dropX, dropZ, 1, 1)
	cell := sys2.Terrain.PlotAt(cellX, cellZ)
	if cell == nil {
		t.Fatalf("drop cell %d,%d off the fixture map", cellX, cellZ)
	}
	cell.SetFeature(0xFFFB)
	n2 := &orders.Node{Owner: carrier2.Handle, Phase: 2, Param1: uint32(cargo2.Handle), GoalX: dropX, GoalY: carrier2.Y, GoalZ: dropZ, Deadline: -1}
	*kinds2 = (*kinds2)[:0]
	if code := sys2.legVTOLUnload(carrier2, n2, transportUnloadInterruptMask, 1); code != 9 {
		t.Fatalf("the `cannot get there` interrupt gave result %d, want 9 [04 R-AIR-01 §10]", code)
	}
	if len(*kinds2) != 0 {
		t.Fatalf("the interrupt raised %d status cues; it returns 9 BEFORE the revalidation that would emit one [04 R-AIR-01 §10]", len(*kinds2))
	}
	if cargo2.Attachment.Carrier != carrier2.Handle {
		t.Fatal("the interrupted unload released its cargo")
	}
	// Without the bit the same record reaches the revalidation and its message.
	if code := sys2.legVTOLUnload(carrier2, n2, 0, 1); code != 9 {
		t.Fatalf("the refused revalidation gave result %d, want 9 [04 §10.2]", code)
	}
	if transportCountKind(*kinds2, transportStatusCant) != 1 {
		t.Fatalf("the refused revalidation raised %d `cant` cues, want one [04 §10.2]", transportCountKind(*kinds2, transportStatusCant))
	}
}

// TestLoadPhaseFourInstallsNoClimbAway locks [04 R-AIR-01 §10] item 3's
// correction to §10.2's phase-4 row: the phase builds a climb-away marker on
// the carrier's own position and NEVER installs it, so the record's payload
// stays the phase-3 follow marker on the cargo and a loaded transport climbs
// only when its next order commands it. It still ORs `0xE0` into the gate.
func TestLoadPhaseFourInstallsNoClimbAway(t *testing.T) {
	sys, _, carrier, cargo, _, kinds := transportFixture(t)
	carrier.Move.Mode = 2
	if fl := sys.Flights[carrier.Handle]; fl != nil {
		fl.Mode = 2
	}
	n := &orders.Node{Owner: carrier.Handle, Target: cargo.Handle, Phase: 3, Deadline: -1}
	if code := sys.legVTOLPickup(carrier, n, 0, 1); code != 1 {
		t.Fatalf("load phase 3 gave result %d, want 1 [04 §10.2]", code)
	}
	follow := sys.AirGoalPayload(carrier.Handle)
	if follow == nil {
		t.Fatal("load phase 3 installed no follow marker [04 §10.2]")
	}

	n.Phase = 4
	n.DynamicGate = 0
	if code := sys.legVTOLPickup(carrier, n, 0, 1); code != 1 {
		t.Fatalf("load phase 4 gave result %d, want 1 [04 §10.2]", code)
	}
	if cargo.Attachment.Carrier != carrier.Handle {
		t.Fatal("load phase 4 did not attach the cargo [04 §10.2]")
	}
	if transportCountKind(*kinds, TransportEventAttach) != 1 {
		t.Fatalf("event 12 published %d times, want one [04 §10.2]", transportCountKind(*kinds, TransportEventAttach))
	}
	if got := sys.AirGoalPayload(carrier.Handle); got != follow {
		t.Fatalf("load phase 4 replaced the record's payload; the climb-away it builds is never installed [04 R-AIR-01 §10]")
	}
	if n.DynamicGate != airLegGate {
		t.Fatalf("load phase 4 gate = %#x, want |= 0xE0 [04 §10.2]", n.DynamicGate)
	}
}

// TestBeCarriedReArmPurgesTheFrontChainInKeepSurvivorsMode locks
// [04 R-AIR-01 §10] item 6: the re-arm purges the cargo's FRONT chain in
// keep-survivors mode — every record whose static-mask copy lacks bit `0x4` is
// unlinked, cleaned up and freed, while the nine bit-2 descriptors survive
// [04 R-MOV-03 §6] — and only then head-inserts a `BeCarried`, copying the
// displaced head's auto-operation flag onto it. The previous placeholder
// head-inserted without purging and kept orders retail discards.
func TestBeCarriedReArmPurgesTheFrontChainInKeepSurvivorsMode(t *testing.T) {
	sys, _, carrier, cargo, _, _ := transportFixture(t)
	q := orders.QueueForUnit(cargo)
	move := orders.Lookup("Move_Ground")
	survivor := orders.Lookup("BuildingBuild") // static gate bit 2 [04 §3.1]
	if move == 0 || survivor == 0 {
		t.Fatal("Move_Ground or BuildingBuild missing from the order table")
	}
	q.Push(survivor, orders.NewNodeForOrder(survivor, 0, 0, 0, 0, 0, cargo.Handle, false))
	// Queued, so the insertion itself does not run the Replace purge that would
	// remove the move before the re-arm gets to it [04 §3.3].
	q.Push(move, orders.NewNodeForOrder(move, 0, 1<<16, 0, 1<<16, 0, cargo.Handle, true))
	// The auto-operation flag goes on after both insertions: an insertion drops
	// a LEADING auto record before it adds [04 §3.3].
	q.Primary()[0].Flags |= orders.FlagAutoOp
	if q.LenPrimary() != 2 {
		t.Fatalf("fixture queue holds %d records, want 2", q.LenPrimary())
	}

	sys.armBeCarried(cargo, carrier.Handle)

	primary := q.Primary()
	if len(primary) != 2 {
		t.Fatalf("after the re-arm the front chain holds %d records, want the survivor plus `BeCarried`", len(primary))
	}
	if primary[0].ID != orders.Lookup("BeCarried") {
		t.Fatalf("front head is %q, want the head-inserted `BeCarried` [04 R-AIR-01 §10]", orders.DescriptorFor(primary[0].ID).Name)
	}
	if primary[0].Flags&orders.FlagAutoOp == 0 {
		t.Fatal("`BeCarried` did not inherit the displaced head's auto-operation flag [04 R-AIR-01 §10]")
	}
	if primary[1].ID != survivor {
		t.Fatalf("record behind the head is %q, want the surviving `BuildingBuild` [04 R-MOV-03 §6]", orders.DescriptorFor(primary[1].ID).Name)
	}
	for _, n := range primary {
		if n.ID == move {
			t.Fatal("the unprotected `Move_Ground` survived the keep-survivors purge [04 R-AIR-01 §10]")
		}
	}
}

// TestFactoryProductLinkPassesRequestModeOne locks [04 R-AIR-01 §10]'s closing
// aside: the factory product's builder link — the attachment helper's fourth
// caller — passes request mode 1, the grounded mode ([04 R-FAC-02 §1] item 4).
// The write is direct, so a product that somehow held another mode is put back
// on the ground plane by the link itself.
func TestFactoryProductLinkPassesRequestModeOne(t *testing.T) {
	_, w, carrier, cargo, _, _ := transportFixture(t)
	cargo.Move.Mode = 2
	if !AttachFactoryProduct(w, carrier.Handle, cargo.Handle, 0) {
		t.Fatal("factory product attach failed")
	}
	if cargo.Move.Mode&0x3 != 1 {
		t.Fatalf("product mover mode %d after the builder link, want the grounded 1 [04 R-FAC-02 §1]", cargo.Move.Mode)
	}
}
