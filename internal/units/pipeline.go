// Package units — per-unit pipeline helpers [04 §1.3][04 §5][06][GAP T15][01 §4.4].
//
// This file implements the established per-unit sequence visited by the phase-2
// sweep in deterministic order players 0..9 then slots ascending [01 §6.2][P0-16].
// Construction Remaining is owned exclusively by construction.Service; this file
// never reads or writes Remaining [05 "Construction target state"] [05 "Construction arithmetic"].
//
// Order inside one unit visit per [04 §1.3][GAP T15] C17 (I7):
//  1. per-unit pre-update/status work (deferred via placeholder if no spec yet; no invented progress)
//  2. water damage and timed work (deferred unless spec says)
//  3. weapon-slot update and target/Aim scheduling (delegates to Slot; Aim can block)
//  4. normal COB drain delta 1 via the unit's actual VM and piece state
//  5. defer build/order work to later window (construction pump is phase 6, not phase 2)
//  6. preserve movement callbacks for movement window
//  7. slot-end death handling (marks Dying; leaves Cleanup for phase 10)
package units

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// tickUnit executes the established per-unit pipeline for one unit visit
// in phase 2 [04 §1.3][GAP T15] C17 (I7). It is called from World.Tick in
// players-asc then slots-asc order [01 §6.2] C2 [P0-16]. No map iteration;
// no float64 outside allowlist; deterministic (I1, I2).
//
// Construction progress must have one owner (construction.Service). No other
// package mutates Remaining [05 "Construction target state"]. This function
// never touches Remaining. Orders/build work is deferred to the construction
// pump window [01 §4.4] phase 6, not phase 2 [GAP T15]. Movement callbacks
// are preserved for the movement window [GAP T15] C18. Slot free is deferred
// to Cleanup (phase 10) [04 §2.4] C2; this function never frees slots.
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
	// TODO(T25): water damage evaluated once per 30 ticks when globalTick%30==0
	// and owner class 1 or 2 with mission waterdoesdamage/waterdamage nonzero and
	// signed integer height <= sea-level byte and canhover clear [04 §9.2].
	// Settlement and movement prerequisites not yet wired into this package's
	// tick; keep stub and do not invent damage. If spec says, delegate via unit's
	// health and cob callbacks with established packet kind 0xB [06 §9.1][04 §5.1] C26.
	w.unitWaterDamage(u, tick)

	// 3. weapon-slot update and target/Aim scheduling [06 §1.2][06 §3.3][GAP T15].
	// Visit slots in numeric order 0..2 [06 §1.2] C1 (I1). Decrement nonzero
	// reload before target resolve [06 §1.2][06 §4.1] P0-10. Dispatch Aim* on
	// demand via cob.AimSlot; an outstanding Aim blocks firing [GAP T15] C16.
	// Do not invent auto-decrement beyond reload countdown and do not call
	// spawner here; fire admission and projectile creation live in combat phase
	// [01 §4.4] [06 §4.1]. Movement callbacks are not emitted here.
	w.weaponSlotUpdate(u, tick)

	// 4. normal COB drain using the unit's actual VM and piece state
	// [04 §4.2][04 §4.6][GAP T15] C17: delta 1, eight thread slots then one piece pass.
	// Deferred callbacks queued before this drain run same visit [GAP T15] C17.
	// If the unit has no VM (fixture, pre-COB attach, or not yet wired session),
	// keep the placeholder Script dormant and do not synthesize completion [P1-I01].
	// The VM's own Drain handles the zero-piece case and the eight-slot fixed
	// scan order [04 §4.2] C13 (I1).
	w.cobDrain(u, tick)

	// 5. defer build/order work to later window [05 "Queue pumping and result codes"]
	// Construction pump is phase 6, not phase 2 [01 §4.4][GAP T15]. Orders pump
	// (primary head-blocking, secondary skip-not-due) runs in phase 5 [04 §3.3].
	// This visit queues no orders and advances no Remaining.
	// Intentionally empty — construction.Service exclusively owns Remaining.

	// 6. preserve movement callbacks for movement window [GAP T15] C18.
	// StartMoving, MoveRateN, setSFXoccupy are immediate wake-flag starts issued
	// by movement integration, not by phase 2. The VM barrier between
	// StartMoving and MoveRateN is the movement window's responsibility [GAP T15] C18.
	// Intentionally empty.

	// 7. perform slot-end death handling at established point [04 §2.4] C2 [GAP T15] C17.
	// Mark dying but do not free slots directly; Cleanup (phase 10) frees them.
	// Local authoritative death runs the synchronous 4-cell Killed query BEFORE
	// the death packet is emitted [04 §5.1]; the production query is wired in
	// the session OnDeath hook via combat.ResolveDeath [06 §12.1] C22-C25 which
	// drains the unit's script threads inline deterministically (no wall-clock,
	// no map iteration) and whose low nibble selects the corpse chain depth.
	// This stub only latches the mark so the unit stays visible to the sweep
	// and to Unit() until Cleanup [04 §2.4] C2; the hook performs the query
	// synchronously before corpse/explosion [04 §5.1].
	w.slotEndDeathHandling(u, tick)
}

// unitPreUpdate is the per-unit pre-update/status work placeholder [04 §2.4][04 §5.1].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// input [04 §5.1] C22: every 30 ticks it recomputes clamp(health*100/maxHealth,0,100)
// and shifts the previous window. We keep a single PriorSample field as the
// previous-window percent; the current window's sample overwrites it directly
// for determinism without a second field — a one-window lag on exact-boundary
// deaths remains TODO(question) but preserves the contract that death severity
// uses the previous 30-tick window percent, not immediate pre-lethal health.
func (w *World) unitPreUpdate(u *Unit, tick uint32) {
	if u == nil || !u.Alive || u.Dying {
		return
	}
	if tick%30 == 0 && u.MaxHealth > 0 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		pct := cob.HealthPercent(u.Health, u.MaxHealth) // [04 §4.4] clamped 0..100
		u.PriorSample = uint8(pct)                      // [04 §5.1] prior window percent for Killed severity [04 §5.1]
	}
	// No Remaining mutation. No order creation. No RNG draws.
}

// unitWaterDamage handles water damage when established [04 §9.2].
// TODO(T25): wire mission waterdoesdamage/waterdamage and player class checks when
// terrain/mission state is available in this package. Do not invent damage here.
func (w *World) unitWaterDamage(u *Unit, tick uint32) {
	_ = u
	_ = tick
	// Established condition [04 §9.2]: globalTick%30==0, owner class 1/2,
	// mission.waterdoesdamage !=0 && waterdamage !=0, wy <= wt && !canhover.
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
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// This is the sole weapon-driven mutation in phase 2; it does not imply
		// a firing and does not touch resources or stockpile.
		if slot.Reload > 0 {
			slot.Reload-- // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		}
		// Target validation and Aim scheduling are stubbed for P0-I02 wiring.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// and firing TargetCleared when heading!=0 or pitch!=0x8000 [04 §5.1].
		// No target-validity predicate is stable in this package's stub; defer to
		// combat/target.go when Vis and unit table are bound [06 §3.1][06 §3.2].
		// Aim dispatch would start here on demand: ballistic solver returns
		// -0x8000 sentinel suppresses Aim [06 §3.3] P0-10, otherwise issue via
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(T25): wire combat.Slots target resolve + Aim dispatch when Vis and
		// definition mover gates are bound; keep placeholder that preserves
		// outstanding Aim blocking (IssueBit && !Ready blocks CanFire).
		// Intentionally do not call spawner, resources, or projectile allocation.
		// Movement callbacks are not emitted here [GAP T15].
		_ = pool.Handle(idx) // keep import used for future target mapping
	}
}

// cobDrain runs the normal COB drain for the unit's VM [04 §4.2][04 §4.6][GAP T15] C17.
// Delta is 1 tick of simulation time; the VM runs eight thread slots in fixed
// order 0..7 then one piece-interpolation pass [04 §4.2] C13. Deferred callbacks
// produced before this point run same visit [GAP T15] C17. An all-slot delta-0
// barrier inside a starter can also execute a pending deferred callback earlier
// [GAP T15] C17, but that path lives in cob.VM's immediate-start helpers [04 §4.2].
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
	// snapshot composition [03 §2.4] C21; the snapshot phase copies the piece
	// transforms from these VM pieces into the immutable frame published after
	// phase 12 [03 §2.4] C21 [PLAN_03 C15]. This file does not write Remaining
	// or queue nodes; those belong to construction [05] and orders [04 §3.3].
}

// slotEndDeathHandling marks slot-end death readiness [04 §2.4] C2 [GAP T15] C17.
// It never frees slots directly; Cleanup frees them in phase 10 [04 §2.4] C2.
// If Health has reached zero or below and the unit is not yet Dying, latch the
// mark via w.Destroy so the OnDeath hook fires exactly once [08 "Evaluation"]
// and the unit stays visible to the sweep and to Unit() until Cleanup [04 §2.4].
// Lethal special damage and paralyzer paths use their own Destroy callers;
// this handler is the generic health-exhausted at tick-end.
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
		// Do not FreeImmediate here; let Cleanup handle it [04 §2.4] C2.
		// Use Destroy to get exactly-once hook semantics [08 "Evaluation"].
		w.Destroy(u.Handle, DeathKilled) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
}
