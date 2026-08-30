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
	// TODO(question): established pre-update/status contents not closed for this
	// slice; placeholder preserves placement and does not invent progress. The
	// dedicated post-research T23/T25 items keep gaps explicit — do not invent
	// Remaining ticks, health regen, or activation toggles here.
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

// unitWaterDamage handles water damage when established [04 §9.2].
// TODO(T25): wire mission waterdoesdamage/waterdamage and player class checks when
// terrain/mission state is available in this package. Do not invent damage here.
func (w *World) unitWaterDamage(u *Unit, tick uint32) {
	_ = u
	_ = tick
	// Established condition [04 §9.2]: globalTick%30==0, owner class 1/2,
	// mission.waterdoesdamage !=0 && waterdamage !=0, depth at unit position
	// at or below the water line and !canhover.
	// When wired, route through standard damage funnel kind 0xB with no
	// HitByWeapon/TakeDamage callbacks and with veterancy-scaled reduction
	// [06 §9.1][04 §5.1] C26, then let death handling latch.
	// Stub: no damage, no RNG.
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
		// TODO(T25): wire combat.Slots target resolve + Aim dispatch when Vis
		// and definition mover gates are bound; keep the placeholder that
		// preserves outstanding Aim blocking (IssueBit && !Ready blocks
		// CanFire). Intentionally no spawner, resources, or projectile
		// allocation; movement callbacks are not emitted here [GAP T15].
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
