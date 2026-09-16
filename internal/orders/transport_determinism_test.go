package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestTransportHandlersWired(t *testing.T) {
	ensureTransportHandlers()
	for _, name := range []string{"Ground_Pickup", "VTOL_Pickup", "Ground_Unload", "VTOL_Unload", "VTOL_Landing", "BeCarried"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("Lookup %q failed", name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("handler for %q is nil, should be wired [04 §10.2]", name)
		}
	}
	// Capture/Resurrect remain nil by design (arithmetic in construction, not order handlers) – verify they are still nil or document.
	// We leave Capture nil to avoid inventing handler; check that we explicitly left it nil.
	if id := Lookup("Capture"); id != 0 && DescriptorFor(id).Handler != nil {
		t.Logf("Capture handler is wired (unexpected, but not required)")
	}
	if id := Lookup("Resurrect"); id != 0 && DescriptorFor(id).Handler != nil {
		t.Logf("Resurrect handler is wired (unexpected)")
	}
}

func TestBeCarriedUsesExactTenTickWaitWithoutRNG(t *testing.T) {
	sim := rng.NewSimulation(12345)
	w := newOrdersFixtureWorld(8, &content.Catalog{})
	carrierDef := &content.UnitDef{UnitName: "armlab", MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armflash", MaxDamage: 100, BMCode: 1}
	carrierH, _ := w.Create(carrierDef, 0, 0, 0, 0)
	cargoH, _ := w.Create(cargoDef, 0, 0, 0, 0)
	carrier, cargo := w.Unit(carrierH), w.Unit(cargoH)
	cargo.Attachment.Carrier = carrierH
	cargo.Attachment.AttachPiece = 0
	carrier.Attachment.Cargo = []pool.Handle{cargoH}
	q := QueueForUnit(cargo)
	q.SetBinding(&QueueBinding{SimRNG: &sim})
	q.Push(Lookup("BeCarried"), Node{Target: carrierH})
	q.Pump(cargo, 0)
	head := q.Head()
	if head == nil || head.Phase != 1 || head.Deadline != 10 || head.DynamicGate != 1 {
		t.Fatalf("phase-1 carried wait=%+v, want deadline 10 gate 1", head)
	}
	if sim.Draws() != 0 {
		t.Fatalf("BeCarried consumed %d RNG draws, want 0", sim.Draws())
	}
	q.Pump(cargo, 9)
	if q.Head() != head || head.Deadline != 10 {
		t.Fatal("BeCarried woke before its exact ten-tick deadline")
	}
	cargo.Attachment.Carrier = 0
	carrier.Attachment.Cargo = nil
	q.Pump(cargo, 10)
	if q.LenPrimary() != 0 {
		t.Fatal("detached BeCarried record did not complete at its next wake")
	}
	if sim.Draws() != 0 {
		t.Fatalf("BeCarried detach consumed %d RNG draws, want 0", sim.Draws())
	}
}

// transportFixture builds a carrier and a cargo in one world with a binding
// that resolves handles and records every status kind the handlers raise.
func transportFixture(t *testing.T, carrierDef, cargoDef *content.UnitDef) (*units.World, *units.Unit, *units.Unit, *[]uint8) {
	t.Helper()
	w := newOrdersFixtureWorld(10, &content.Catalog{})
	hC, err := w.Create(carrierDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	hCargo, err := w.Create(cargoDef, 0, numeric.Fixed(5*65536), numeric.Fixed(10*65536), 0)
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	carrier, cargo := w.Unit(hC), w.Unit(hCargo)
	carrier.Move.Mode, cargo.Move.Mode = 1, 1
	kinds := &[]uint8{}
	binding := &QueueBinding{
		Lookup: w.Unit,
		Presentation: &PresentationAdapter{
			Ready: func() bool { return true },
			Status: func(_ *units.Unit, kind uint8, _ string) bool {
				*kinds = append(*kinds, kind)
				return true
			},
		},
	}
	QueueForUnit(carrier).SetBinding(binding)
	QueueForUnit(cargo).SetBinding(binding)
	return w, carrier, cargo, kinds
}

func countKind(kinds []uint8, want uint8) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

// TestAirTransportDescriptorsHandOffRatherThanAttachInPlace locks the boundary
// this unit moved. `VTOL_Pickup` and `VTOL_Unload` install air path markers
// [04 R-AIR-01 §4], a family internal/orders cannot reach, so their descriptor
// handlers hand the record to the movement runner and the legs do the work
// [04 §10.2].
//
// What this asserts is the arm taken with NO runner bound, which is the same
// one `airHandOff` gives every other air executor: the one-tick deadline hold
// of [04 R-ORD-01 §1]. The previous handlers attached the cargo on their fourth
// visit with the carrier still sitting on the ground, because nothing they did
// could fly.
func TestAirTransportDescriptorsHandOffRatherThanAttachInPlace(t *testing.T) {
	carrierDef := &content.UnitDef{UnitName: "armatlas", CanLoad: true, CanFly: true, CanMove: true, TransportSize: 10, TransportCapacity: 1, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armpw", CanMove: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	w, carrier, cargo, _ := transportFixture(t, carrierDef, cargoDef)
	q := QueueForUnit(carrier)
	id := Lookup("VTOL_Pickup")
	q.Push(id, NewNodeForOrder(id, cargo.Handle, 0, 0, 0, 0, carrier.Handle, false))
	pump := &Pump{World: w}
	startX, startZ := carrier.X, carrier.Z

	for tick := uint32(0); tick < 8; tick++ {
		pump.PumpUnit(carrier.Handle, tick)
	}
	if cargo.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0 {
		t.Fatal("the descriptor attached the cargo itself; the attach is the leg's, after the follow marker's 0x30 arrival [04 §10.2]")
	}
	if carrier.X != startX || carrier.Z != startZ {
		t.Fatal("the descriptor moved the carrier itself")
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("the load record was freed with no runner bound; want it held on the one-tick deadline, got %d records", q.LenPrimary())
	}
	if head := q.Head(); head == nil || head.DynamicGate&gateDeadline == 0 {
		t.Fatalf("hand-off did not arm the deadline gate: %+v", q.Head())
	}
}

// TestGroundPickupFiresTransportPickupAndEvent12 locks the ground carrier's
// load machine [04 R-AIR-01 §9]: it never moves the cargo itself, it fires
// `TransportPickup` on the CARRIER's script at phase 2 and emits notification
// event 12 right after, and phase 4 completes only once the script has done the
// attachment.
func TestGroundPickupFiresTransportPickupAndEvent12(t *testing.T) {
	carrierDef := &content.UnitDef{UnitName: "armmship", CanLoad: true, CanMove: true, TransportSize: 10, FootprintX: 3, FootprintZ: 3, MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armpw", CanMove: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	w, carrier, cargo, kinds := transportFixture(t, carrierDef, cargoDef)
	q := QueueForUnit(carrier)
	id := Lookup("Ground_Pickup")
	q.Push(id, NewNodeForOrder(id, cargo.Handle, 0, 0, 0, 0, carrier.Handle, false))
	pump := &Pump{World: w}
	head := q.Head()

	// Phases 0 through 2: the caption, the short move, then the callback and
	// the event. Phase 2's deadline stops the walk.
	for tick := uint32(0); tick < 3; tick++ {
		pump.PumpUnit(carrier.Handle, tick)
	}
	if got := countKind(*kinds, statusLoadEvent); got != 1 {
		t.Fatalf("notification event 12 published %d times, want exactly one [04 R-AIR-01 §9]", got)
	}
	if head.Param2 != 1 {
		t.Fatalf("attempt counter = %d after one callback, want 1 [04 R-AIR-01 §9]", head.Param2)
	}
	if cargo.Attachment.Carrier != 0 {
		t.Fatal("the ground executor attached the cargo itself; the SCRIPT performs the attach [04 R-COB-03 §5]")
	}
	// Phase 4 completes once the script has attached.
	cargo.Attachment.Carrier = carrier.Handle
	carrier.Attachment.Cargo = []pool.Handle{cargo.Handle}
	for tick := uint32(20); tick < 26 && q.LenPrimary() > 0; tick++ {
		pump.PumpUnit(carrier.Handle, tick)
	}
	if q.LenPrimary() != 0 {
		t.Fatalf("the load record survived the script's attach, %d left at phase %d", q.LenPrimary(), head.Phase)
	}
	if got := countKind(*kinds, statusLoadEvent); got != 1 {
		t.Fatalf("event 12 published %d times over the whole load, want exactly one", got)
	}
}

// TestGroundPickupSizeGateAndEntryGate locks the two rejections
// [04 R-AIR-01 §9] quotes verbatim for the ground load: the size gate's
// `Unit is too large to transport` with result 8 — one word different from the
// air twin's `too heavy`, and that difference is the contract — and the entry
// gate's `Transport mission failed` on a null target.
func TestGroundPickupSizeGateAndEntryGate(t *testing.T) {
	carrierDef := &content.UnitDef{UnitName: "armmship", CanLoad: true, CanMove: true, TransportSize: 1, FootprintX: 3, FootprintZ: 3, MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armbull", CanMove: true, FootprintX: 5, FootprintZ: 5, MaxDamage: 50}
	w, carrier, cargo, kinds := transportFixture(t, carrierDef, cargoDef)
	q := QueueForUnit(carrier)
	id := Lookup("Ground_Pickup")
	q.Push(id, NewNodeForOrder(id, cargo.Handle, 0, 0, 0, 0, carrier.Handle, false))
	pump := &Pump{World: w}
	pump.PumpUnit(carrier.Handle, 0)
	if q.LenPrimary() != 0 {
		t.Fatalf("the oversize load survived its size gate, %d records left", q.LenPrimary())
	}
	if got := countKind(*kinds, statusCant); got != 1 {
		t.Fatalf("size-gate rejection raised %d `cant` cues, want one [04 R-AIR-01 §9]", got)
	}

	*kinds = (*kinds)[:0]
	q.Push(id, NewNodeForOrder(id, 0, 0, 0, 0, 0, carrier.Handle, false))
	pump.PumpUnit(carrier.Handle, 1)
	if q.LenPrimary() != 0 {
		t.Fatal("a null-target load survived its entry gate [04 R-AIR-01 §9]")
	}
	if got := countKind(*kinds, statusCant); got != 1 {
		t.Fatalf("null-target entry raised %d `cant` cues, want one", got)
	}
}

// TestGroundUnloadPacksTheDropPointAndEmitsEvent13 locks `Ground_Unload`'s two
// established payloads [04 R-AIR-01 §9][04 R-UNIT-06 §3]: `TransportDrop`'s
// cell 1 is the record's goal X in the high half and its goal Z in the low
// half, both truncated to whole world units; and phase 2 emits notification
// event 13 as soon as the cargo's carrier reference is no longer this carrier.
func TestGroundUnloadPacksTheDropPointAndEmitsEvent13(t *testing.T) {
	carrierDef := &content.UnitDef{UnitName: "armmship", CanLoad: true, CanMove: true, TransportSize: 10, FootprintX: 3, FootprintZ: 3, MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armpw", CanMove: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	w, carrier, cargo, kinds := transportFixture(t, carrierDef, cargoDef)
	cargo.Attachment.Carrier = carrier.Handle
	carrier.Attachment.Cargo = []pool.Handle{cargo.Handle}
	q := QueueForUnit(carrier)
	id := Lookup("Ground_Unload")
	n := NewNodeForOrder(id, 0, numeric.Fixed(300*65536), 0, numeric.Fixed(72*65536), 0, carrier.Handle, false)
	n.GoalX, n.GoalZ = numeric.Fixed(300*65536), numeric.Fixed(72*65536)
	q.Push(id, n)
	head := q.Head()
	if got, want := packedDropPoint(head), int32(300<<16|72); got != want {
		t.Fatalf("packed drop point = %#x, want %#x [04 R-UNIT-06 §3]", got, want)
	}

	pump := &Pump{World: w}
	for tick := uint32(0); tick < 3; tick++ {
		pump.PumpUnit(carrier.Handle, tick)
	}
	if head.Target != cargo.Handle {
		t.Fatalf("phase 0 did not bind the record's target to the cargo-list head, got %d", head.Target)
	}
	if got := countKind(*kinds, statusUnloadEvent); got != 0 {
		t.Fatalf("event 13 published %d times while the cargo was still aboard [04 R-AIR-01 §9]", got)
	}
	// The script performs the drop.
	cargo.Attachment.Carrier = 0
	carrier.Attachment.Cargo = nil
	for tick := uint32(20); tick < 26 && q.LenPrimary() > 0; tick++ {
		pump.PumpUnit(carrier.Handle, tick)
	}
	if got := countKind(*kinds, statusUnloadEvent); got != 1 {
		t.Fatalf("notification event 13 published %d times, want exactly one [04 R-AIR-01 §9]", got)
	}
	if q.LenPrimary() != 0 {
		t.Fatalf("the unload record survived the script's drop, %d left", q.LenPrimary())
	}
}

func TestTransportLanding(t *testing.T) {
	// This test previously asserted the placeholder it was written against: that
	// one pump visit put the aircraft at the pad's exact X/Z, set mode 1 and
	// emptied the queue. That was the teleport, not a landing — the seven-phase
	// machine of [04 R-AIR-01 §6] flies a loiter, queries the pad and descends,
	// and it lives in internal/movement. The descriptor now hands off to the
	// runner, so what this asserts is the hand-off contract: with no movement
	// runner bound, one visit must NOT move the aircraft and must NOT free the
	// record — it holds it on the one-tick deadline of [04 R-ORD-01 §1] so the
	// order the player still owns survives to the next tick.
	rng.SeedGlobal(2, 0)
	w2 := newOrdersFixtureWorld(10, &content.Catalog{})
	defVTOL := &content.UnitDef{UnitName: "vtol", CanFly: true, CanMove: true, MaxDamage: 100}
	defPad := &content.UnitDef{UnitName: "airbase", IsAirBase: true, MaxDamage: 100}
	hVTOL, _ := w2.Create(defVTOL, 0, numeric.Fixed(0), numeric.Fixed(100*65536), numeric.Fixed(0))
	hPad, _ := w2.Create(defPad, 0, numeric.Fixed(10*65536), numeric.Fixed(0), numeric.Fixed(10*65536))
	uVTOL := w2.Unit(hVTOL)
	uPad := w2.Unit(hPad)
	uVTOL.Move.Mode = 2
	uPad.Move.Mode = 1
	startX, startY, startZ := uVTOL.X, uVTOL.Y, uVTOL.Z
	qVTOL := QueueForUnit(uVTOL)
	qVTOL.SetBinding(&QueueBinding{Lookup: func(h pool.Handle) *units.Unit { return w2.Unit(h) }})
	idLand := Lookup("VTOL_Landing")
	if idLand == 0 {
		t.Fatalf("VTOL_Landing lookup failed")
	}
	qVTOL.Push(idLand, NewNodeForOrder(idLand, hPad, 0, 0, 0, 0, hVTOL, false))
	pump := &Pump{World: w2}
	pump.PumpUnit(hVTOL, 0)

	if uVTOL.X != startX || uVTOL.Y != startY || uVTOL.Z != startZ {
		t.Fatalf("the descriptor moved the aircraft itself: %v,%v,%v -> %v,%v,%v; landing is flown by the movement executor",
			startX, startY, startZ, uVTOL.X, uVTOL.Y, uVTOL.Z)
	}
	if uVTOL.X == uPad.X && uVTOL.Z == uPad.Z {
		t.Fatal("the aircraft was teleported onto the pad")
	}
	if uVTOL.Move.Mode != 2 {
		t.Fatalf("mover mode changed to %d without the descent ever running", uVTOL.Move.Mode)
	}
	if len(qVTOL.Primary()) != 1 {
		t.Fatalf("landing record freed on its first visit; want it held for the machine, got %d records", len(qVTOL.Primary()))
	}
}

// TestGroundTransportShortMoveHoldsOnTheScriptsBusyLevel locks the shared
// short-move helper [04 R-AIR-01 §10] item 5. §9 named the tested word only as
// "the unit's movement-state byte"; §10 names it: the SECOND unit state byte,
// the one COB ports 5, 6 and 19 write, whose bit `0x2` is the level port 6
// (`BUSY`) sets from the low bit of its value. Set → gate `0x8 | 0x4` and
// *hold*; clear → *advance*. It is the engine half of the sea/hover transport
// handshake: the script raises `BUSY` while it animates the attach or the drop.
func TestGroundTransportShortMoveHoldsOnTheScriptsBusyLevel(t *testing.T) {
	carrierDef := &content.UnitDef{UnitName: "armmship", CanLoad: true, CanMove: true, TransportSize: 10, FootprintX: 3, FootprintZ: 3, MaxDamage: 100}
	cargoDef := &content.UnitDef{UnitName: "armpw", CanMove: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	_, carrier, cargo, _ := transportFixture(t, carrierDef, cargoDef)

	// `Ground_Pickup` phases 1 and 3 and `Ground_Unload` phase 1 are the
	// helper's only callers [04 R-AIR-01 §10].
	for _, phase := range []uint8{1, 3} {
		n := &Node{Owner: carrier.Handle, Target: cargo.Handle, Phase: phase, Deadline: -1}
		carrier.Busy = true
		if code := groundPickupHandler(carrier, n, 0, 1); code != 2 {
			t.Fatalf("Ground_Pickup phase %d with BUSY set gave %d, want the hold 2 [04 R-AIR-01 §10]", phase, code)
		}
		if n.DynamicGate != groundTransportBusyGate {
			t.Fatalf("Ground_Pickup phase %d hold gate = %#x, want %#x [04 R-AIR-01 §10]", phase, n.DynamicGate, groundTransportBusyGate)
		}
		carrier.Busy = false
		n.DynamicGate = 0
		if code := groundPickupHandler(carrier, n, 0, 1); code != 1 {
			t.Fatalf("Ground_Pickup phase %d with BUSY clear gave %d, want the advance 1 [04 R-AIR-01 §10]", phase, code)
		}
		if n.DynamicGate != 0 {
			t.Fatalf("the advance arm wrote gate %#x, want none [04 R-AIR-01 §10]", n.DynamicGate)
		}
	}

	cargo.Attachment.Carrier = carrier.Handle
	carrier.Attachment.Cargo = []pool.Handle{cargo.Handle}
	n := &Node{Owner: carrier.Handle, Target: cargo.Handle, Phase: 1, Deadline: -1}
	carrier.Busy = true
	if code := groundUnloadHandler(carrier, n, 0, 1); code != 2 {
		t.Fatalf("Ground_Unload phase 1 with BUSY set gave %d, want the hold 2 [04 R-AIR-01 §10]", code)
	}
	// It is NOT the work handlers' `INBUILDSTANCE` wait, which tests port 5 and
	// holds on the CLEAR level [04 R-ORD-01 §1].
	carrier.Busy = false
	carrier.InBuildStance = false
	n.DynamicGate = 0
	if code := groundUnloadHandler(carrier, n, 0, 1); code != 1 {
		t.Fatalf("Ground_Unload phase 1 with BUSY clear gave %d, want the advance 1 [04 R-AIR-01 §10]", code)
	}
}

// TestGroundUnloadHoverRadiusIsTwentyFourTimesFootprintZ locks
// [04 R-AIR-01 §10] item 5's correction: the radius parameter is
// `trunc(1.5 · zExtentInteger)` where the Z extent comes from the FOOTPRINT and
// not the model — `FootprintZ << 20` in 16.16, integer half `16 · FootprintZ`
// [02 R-CAT-01 §7] — so the radius is exactly `24 · FootprintZ` world units of
// the CARRIER's definition, and 0 without `canhover`.
func TestGroundUnloadHoverRadiusIsTwentyFourTimesFootprintZ(t *testing.T) {
	hover := &units.Unit{Def: &content.UnitDef{UnitName: "armhover", CanHover: true, FootprintX: 3, FootprintZ: 4}}
	if got := groundUnloadRadius(hover); got != 96 {
		t.Fatalf("hover carrier radius = %d, want 24·4 = 96 [04 R-AIR-01 §10]", got)
	}
	// The carrier's own footprint, not the cargo's, and the Z axis, not X.
	ship := &units.Unit{Def: &content.UnitDef{UnitName: "armmship", FootprintX: 3, FootprintZ: 4}}
	if got := groundUnloadRadius(ship); got != 0 {
		t.Fatalf("non-hover carrier radius = %d, want 0 [04 R-AIR-01 §10]", got)
	}
}

// TestPackedDropPointAddsRatherThanOrs locks [04 R-AIR-01 §10] item 1's
// expression for `TransportDrop`'s cell 1: `(goalX & 0xFFFF0000) + (goalZ >> 16)`
// — an ADDITION, so a negative Z integer part borrows from the X half instead
// of smearing into it the way an OR of truncated halves would.
func TestPackedDropPointAddsRatherThanOrs(t *testing.T) {
	n := &Node{GoalX: numeric.Fixed(300 * 65536), GoalZ: numeric.Fixed(72 * 65536), GoalSupplied: true}
	if got, want := packedDropPoint(n), int32(300<<16|72); got != want {
		t.Fatalf("packed drop point = %#x, want %#x [04 R-UNIT-06 §3]", got, want)
	}
	// Z = -1 borrows: 300<<16 plus -1 is (299<<16 | 0xFFFF), which an OR of
	// truncated halves would have written as 300<<16 | 0xFFFF.
	n.GoalZ = numeric.Fixed(-1 * 65536)
	if got, want := packedDropPoint(n), int32(300<<16)+int32(-1); got != want {
		t.Fatalf("packed drop point with negative Z = %#x, want the borrow %#x [04 R-AIR-01 §10]", got, want)
	}
}
