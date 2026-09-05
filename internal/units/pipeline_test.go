package units

// Test-only per-unit pipeline stages [04 §1.1][GAP T15] C17.
//
// These helpers were the production per-unit pipeline behind the removed
// package-wide World.Tick sweep. The authoritative sweep is the session's
// phase-2 entry point: VisitActiveSlots with StepPreUpdate at the front and
// FinalizeDeath at slot-end, with weapon/COB/orders/movement work between
// those boundaries [01 §4.4][04 "unit sweep"]. Package tests drive the same
// traversal shape through runPhase2Sweep, which composes the stage helpers
// below in the established per-unit order. They live in this test-only file
// so no production code can bypass the authoritative entry point.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// runPhase2Sweep drives the per-unit pipeline through the authoritative
// traversal entry point: one visit per active slot in players-ascending then
// slots-ascending order [01 §6.2] C2 [P0-16], with the established per-unit
// stage order inside each visit [04 §1.1][GAP T15] C17 (I7):
//
//  1. pre-update/status (StepPreUpdate)
//  2. water damage and timed work (stub, no invented damage)
//  3. weapon-slot update (reload decrement; Aim can block)
//  4. normal COB drain delta 1
//  5. build/order work deferred (construction pump owns Remaining)
//  6. movement callbacks preserved for the movement window
//  7. slot-end death latch, then NeedsDeathFinalization → FinalizeDeath,
//     mirroring the session's phase-2 slot-end handling
//
// It never frees slots other than via FinalizeDeath at slot-end, and never
// mutates Remaining [05 "Construction target state"].
func runPhase2Sweep(w *World, tick uint32) {
	w.VisitActiveSlots(func(v SlotVisit) {
		w.StepPreUpdate(v.Handle, tick)
		w.unitWaterDamage(v.Unit, tick)
		w.weaponSlotUpdate(v.Unit, tick)
		w.cobDrain(v.Unit, tick)
		// Steps 5 (orders/build) and 6 (movement) are other windows' work:
		// the orders pump runs in phase 5, movement integration in the
		// movement window [01 §4.4][GAP T15] C17-C18.
		w.slotEndDeathHandling(v.Unit, tick)
		if w.NeedsDeathFinalization(v.Handle) {
			w.FinalizeDeath(v.Handle, tick)
		}
	})
}

// tickUnit executes the per-unit stage sequence for one unit visit. Kept for
// tests that assert the per-unit stages directly; runPhase2Sweep is the
// traversal-shaped entry point.
func (w *World) tickUnit(u *Unit, tick uint32) {
	if u == nil || !u.Alive {
		return
	}
	// 1. per-unit pre-update/status work [04 §2.4][04 §4.2].
	//
	// The marker that stood here said the pre-update's contents were "not
	// closed for this slice". They are closed, exhaustively and in order:
	// [04 R-MOV-03 §1] lists ten numbered acts per unit visit, of which the
	// pre-update slice is 2 (the general unit update, carrying the
	// wind-generator notifier of [05 R-PROD-01 §3]), 5 (the minimap blink byte
	// decremented as a SIGNED byte while nonzero, [06 R-WPN-04 §2]), 6 (the
	// post-capture countdown decremented by one), 7 (selection maintenance: a
	// unit carrying the selected bit loses it when it stops being ready) and 8
	// (the tick%30 health-percentage roll). Nothing about Remaining ticks,
	// health regen or activation toggles appears in the list, so the old
	// marker's caution was aimed at the right hazard for the wrong reason.
	//
	// Steps 5, 6, 7 and 8 are all in unitPreUpdate now (WU-19-188). Step 5's
	// byte is Unit.BlinkSuppress, written 240 (signed -16) by the damage
	// dispatcher before the reaction step and decremented as a signed byte here
	// [06 R-WPN-04 §2]; publication copies it onto the radar contact and the
	// contacts pass blinks the blip while it is nonzero [03 §3.9]. Step 7 clears
	// the selected bit (0x10, on the unit record — the HUD never owned it) from
	// a unit that stops being ready [07 R-WGT-01 §9]. Step 6's counter stays
	// fieldless: its only writer is the ownership transfer's remote-peer branch,
	// which is multiplayer transport, so single-player holds it at zero
	// [08 R-TRIG-01 §3]. Step 2's notifier is bound in internal/ai.
	w.unitPreUpdate(u, tick)

	// 2. water damage and unit-level timed work [04 §9.2].
	w.unitWaterDamage(u, tick)

	// 3. weapon-slot update and target/Aim scheduling [06 §1.2][06 §3.3][GAP T15].
	w.weaponSlotUpdate(u, tick)

	// 4. normal COB drain using the unit's actual VM and piece state
	// [04 §4.2][04 §4.6][GAP T15] C17: delta 1, eight thread slots then one
	// piece pass.
	w.cobDrain(u, tick)

	// 5./6. build/order and movement windows are deferred by design [GAP T15].

	// 7. slot-end death handling [04 §2.4] C2 [GAP T15] C17.
	w.slotEndDeathHandling(u, tick)
}

// unitWaterDamage is a placeholder for the sweep's step-9 water damage
// [04 §9.2][04 R-MOV-03 §1 step 9], and it stays a placeholder.
//
// The marker that stood here asked for the mission waterdoesdamage/waterdamage
// keys and the player class check to be wired "when terrain/mission state is
// available in this package". That will never happen and does not need to:
// terrain and the damage funnel sit above internal/units in the import graph,
// and the applicator is already written on the correct side of that line as
// combat.TickWaterDamage (its cadence gate, canhover exemption, height test,
// veterancy scaling and kind-0xB packet are all implemented and tested).
//
// Closed (WU-19-175): the gap this marker named — no caller — is wired. The
// session's phase-2 visit now runs the step at its researched place, inside the
// controller-1-or-2 block before the order pumps (Session.stepWaterDamage), and
// the two mission words reach it on the terrain record beside the sea-level byte
// they are compared against (world.Terrain.WaterDoesDamage/WaterDamage). This
// placeholder stays a placeholder for the reason above it.
func (w *World) unitWaterDamage(u *Unit, tick uint32) {
	_ = u
	_ = tick
	// Deliberately no damage and no RNG: a stub that drew would desynchronise
	// the shared simulation stream against the wired applicator.
}

// weaponSlotUpdate updates the three weapon slots in numeric order [06 §1.2] C1.
// Each populated slot decrements nonzero reload before target resolve
// [06 §1.2][06 §4.1] P0-10. Aim dispatch and readiness gate [GAP T15] C16
// [06 §3.3] can block firing; Reload and AimReady are handled here without
// reaching projectile allocation.
func (w *World) weaponSlotUpdate(u *Unit, tick uint32) {
	_ = tick
	for idx := 0; idx < NumSlots; idx++ {
		slot := &u.Slots[idx]
		if !slot.IsPopulated() {
			continue
		}
		// Decrement nonzero reload countdown at the per-slot reload word
		// [06 §1.2][06 §4.1] P0-10. This is the sole weapon-driven mutation in
		// the pre-update window; it does not imply a firing and does not touch
		// resources or stockpile.
		if slot.Reload > 0 {
			slot.Reload-- // [06 §1.2][06 §4.1] P0-10 decrement nonzero reload
		}
		// Target validation and Aim scheduling are stubbed for the P0-I02
		// wiring. Retail clears a stale/dead target here, clearing latch 0x01
		// [06 §1.2] P0-10 and firing TargetCleared when a heading/pitch was
		// commanded [04 §5.1]. Aim dispatch would start here on demand: a
		// ballistic-solver sentinel suppresses Aim [06 §3.3] P0-10, otherwise
		// issue and block firing while Aim Ready is false [GAP T15] C16.
		// The target resolve and Aim dispatch this marker used to ask for are
		// wired, in the production sweep rather than here: the session's
		// phase-2 visit calls combat.Service.StepWeaponsForUnit with the
		// world, visibility, terrain, economy, catalog and both RNG streams
		// bound, and that is the authoritative path [04 R-MOV-03 §1 step 3].
		// This helper cannot reach it — internal/combat imports internal/units,
		// so the arrow cannot be reversed — and it is not meant to: it exists
		// to hold the traversal SHAPE for package-local tests, not to be a
		// second weapon step. Intentionally no target resolve, no spawner, no
		// resources, no projectile allocation and no movement callbacks.
		_ = pool.Handle(idx) // keep import used for future target mapping
	}
}

// cobDrain runs the normal COB drain for the unit's VM [04 §4.2][04 §4.6][GAP T15] C17.
// Delta is 1 tick of simulation time; the VM runs eight thread slots in fixed
// order 0..7 then one piece-interpolation pass [04 §4.2] C13. Deferred callbacks
// produced before this point run same visit [GAP T15] C17. An all-slot delta-0
// barrier inside a starter can also execute a pending deferred callback earlier
// [GAP T15] C17, but that path lives in cob.VM's D+wake starter [04 §4.2].
func (w *World) cobDrain(u *Unit, tick uint32) {
	_ = tick
	if u == nil {
		return
	}
	vm := u.GetScript()
	if vm == nil {
		// No VM bound yet (session not yet wired, fixture, or placeholder) —
		// keep dormant and do not synthesize completion ticks [04 §4.1][P1-I01].
		return
	}
	// Normal drain delta 1 [04 §4.6][GAP T15] C17. The VM runs all eight threads
	// in slot order 0..7 [04 §4.2] C13 then interpolates all piece axes with the
	// same delta [04 §4.6]. A sleep occupies its truncated tick count plus one
	// guard decrement so sleep 0 costs one tick [04 §4.6]. Signals/wake are
	// handled inside the run [04 §4.3].
	vm.Drain(1) // [04 §4.2][04 §4.6] delta 1, eight slots then one piece pass (I1)

	// The piece state after Drain is exposed via Unit.ScriptState.Pieces() for
	// snapshot composition [03 §2.4] C21.
}

// slotEndDeathHandling marks slot-end death readiness [04 §2.4] C2 [GAP T15] C17.
// If Health has reached zero or below and the unit is not yet Dying, latch the
// mark via w.Destroy so the unit stays visible to the sweep and to Unit() until
// the slot is finalized; OnDeath fires at that finalizer [08 "Evaluation"].
// Lethal special damage and paralyzer paths use their own Destroy
// callers; this handler is the generic health-exhausted at tick-end.
func (w *World) slotEndDeathHandling(u *Unit, tick uint32) {
	_ = tick
	if u == nil || !u.Alive || u.Dying {
		return
	}
	// Nanoframes have Remaining>0 and Health==0 initially [05 "Construction target state"].
	// They are not dead; construction will raise health via HealthGain [05 "Construction arithmetic"].
	// Exclude incomplete units from generic health-exhausted death.
	if u.Remaining != 0 {
		return
	}
	// Retail death latch: lethal damage against movement category 1/2 sets the
	// latch immediately with no HitByWeapon/TakeDamage callbacks [04 §5.1] C26;
	// that path already marked Dying via Destroy. Here we handle generic
	// health exhaustion observed at the end of the per-unit visit [04 §2.4].
	if u.Health <= 0 {
		// Use Destroy to defer exactly-once hook semantics to FinalizeDeath [08 "Evaluation"];
		// the slot itself is freed by FinalizeDeath at slot-end or by teardown cleanup
		// after the tick [04 §2.4] C2.
		w.Destroy(u.Handle, DeathKilled)
	}
}

// TestBlinkByteReachesZeroInSixteenVisits locks the direction and the length of
// the pre-update's step-5 countdown [04 R-MOV-03 §1 step 5][06 R-WPN-04 §2]. The
// damage dispatcher writes 240; the sweep reads that byte SIGNED as -16 and
// steps it one toward zero per visit, so the blink lasts exactly sixteen visits.
// Subtracting one instead would walk away from zero and never end; an unsigned
// decrement of 240 would blink for eight seconds instead of half of one.
func TestBlinkByteReachesZeroInSixteenVisits(t *testing.T) {
	world := newFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "blink-unit", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := world.Unit(h)
	u.Health = 100
	if u.BlinkSuppress != 0 {
		t.Fatalf("spawn blink byte = %d, want 0 [06 R-WPN-04 §2]", u.BlinkSuppress)
	}
	u.BlinkSuppress = int8(-16) // the byte retail writes as 240
	for visit := 1; visit <= 16; visit++ {
		world.StepPreUpdate(h, uint32(visit))
		want := int8(-16 + visit) // one step toward zero per visit
		if u.BlinkSuppress != want {
			t.Fatalf("visit %d: blink byte = %d, want %d", visit, u.BlinkSuppress, want)
		}
	}
	if u.BlinkSuppress != 0 {
		t.Fatalf("blink byte after sixteen visits = %d, want 0", u.BlinkSuppress)
	}
	// Zero is a floor, not a wrap: a seventeenth visit must not restart the
	// countdown at -1 (which unsigned would read as another 255-visit blink).
	world.StepPreUpdate(h, 17)
	if u.BlinkSuppress != 0 {
		t.Fatalf("blink byte after the zero floor = %d, want 0", u.BlinkSuppress)
	}
}

// TestSelectionMaintenanceClearsUnreadyUnits locks step 7 of the per-unit visit
// [04 R-MOV-03 §1 step 7]: the selected bit survives only while the unit is
// ready, where ready is the shared eligibility predicate [07 R-WGT-01 §9] — the
// selectable bit, remaining-build exactly 0.0, and either no carrier or a
// carrier whose status word carries the cargo-selectable bit.
func TestSelectionMaintenanceClearsUnreadyUnits(t *testing.T) {
	def := &content.UnitDef{UnitName: "select-unit", MaxDamage: 100, Limit: -1}
	base := &content.UnitDef{UnitName: "select-base", MaxDamage: 100, Limit: -1, IsAirBase: true}
	transport := &content.UnitDef{UnitName: "select-transport", MaxDamage: 100, Limit: -1}

	newSelected := func(t *testing.T, w *World) (pool.Handle, *Unit) {
		t.Helper()
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		u := w.Unit(h)
		u.Health = 100
		u.Flags |= SelectedStatus
		return h, u
	}

	t.Run("ready unit keeps the bit", func(t *testing.T) {
		w := newFixtureWorld(4, nil)
		h, u := newSelected(t, w)
		w.StepPreUpdate(h, 1)
		if u.Flags&SelectedStatus == 0 {
			t.Fatal("a ready unit lost the selected bit")
		}
	})

	t.Run("incomplete unit loses the bit", func(t *testing.T) {
		w := newFixtureWorld(4, nil)
		h, u := newSelected(t, w)
		u.Remaining = 0.5 // the remaining-build fraction is not exactly 0.0
		w.StepPreUpdate(h, 1)
		if u.Flags&SelectedStatus != 0 {
			t.Fatal("an incomplete unit kept the selected bit")
		}
	})

	t.Run("unselectable unit loses the bit", func(t *testing.T) {
		w := newFixtureWorld(4, nil)
		h, u := newSelected(t, w)
		u.ClearClassifierEligibility()
		w.StepPreUpdate(h, 1)
		if u.Flags&SelectedStatus != 0 {
			t.Fatal("a unit without the selectable bit kept the selected bit")
		}
	})

	t.Run("cargo aboard an ordinary transport loses the bit", func(t *testing.T) {
		w := newFixtureWorld(4, nil)
		carrier, err := w.Create(transport, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		h, u := newSelected(t, w)
		u.Attachment.Carrier = carrier
		w.StepPreUpdate(h, 1)
		if u.Flags&SelectedStatus != 0 {
			t.Fatal("cargo aboard a non-airbase carrier kept the selected bit")
		}
	})

	t.Run("cargo aboard an airbase keeps the bit", func(t *testing.T) {
		w := newFixtureWorld(4, nil)
		carrier, err := w.Create(base, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if w.Unit(carrier).Flags&CargoSelectableStatus == 0 {
			t.Fatal("fixture airbase did not seed the cargo-selectable bit")
		}
		h, u := newSelected(t, w)
		u.Attachment.Carrier = carrier
		w.StepPreUpdate(h, 1)
		if u.Flags&SelectedStatus == 0 {
			t.Fatal("cargo aboard an airbase lost the selected bit")
		}
	})
}
