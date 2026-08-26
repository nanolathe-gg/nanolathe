// Package units — deterministic slot traversal and death finalization [01 §4.4][04 "unit sweep"].
//
// This file provides the central loop one deterministic active-slot traversal API
// with explicit unit-local stages, plus exact once-only death finalization.
// No map iteration defines slot order (I1) [01 §4.4].
//
// The traversal order is retail: players 0..9 ascending, units in ascending
// pool order within each player's slice [01 §4.4][01 §6.2][P0-16]. Allocation
// and free during traversal follow same-tick visibility: newly created unit
// visible to later same-tick readers when player+slot lie ahead of current scan
// position, already-visited waits for next tick; freed slot immediately reusable
// via lowest-free scan, no generation counter [01 §4.4]. A unit killed during
// projectile processing retains state and final deletion is deferred to
// slot-end death handling or ledger cleanup [01 §4.4].
//
// Per-unit micro-order within one visit [04 "unit sweep"][01 §4.4]:
//  1. general unit update (StepPreUpdate) — exposed as boundary
//  2. weapon update (reload, target acquisition, Aim latch) — between boundaries
//  3. COB drain (delta 1, eight threads then one piece pass) — between
//  4. orders / construction pump — between
//  5. movement integration (immediate wake-flag starts) — between
//  6. slot-end death handling (FinalizeDeath) — exposed as boundary
//
// The central caller (built by another agent) should do:
//
//	w.VisitActiveSlots(func(v SlotVisit) {
//	    w.StepPreUpdate(v.Handle, tick)
//	    // --- weaponSlotUpdate, cobDrain, orders, movement between ---
//	    // e.g., w.weaponSlotUpdate(v.Unit, tick) is internal; external callers
//	    // run combat.Slots, cob.VM.Drain, orders pump, movement.StepUnit
//	    // directly on v.Unit. All between stages must respect Dying (skip).
//	    if w.NeedsDeathFinalization(v.Handle) {
//	        w.FinalizeDeath(v.Handle, tick)
//	    }
//	})
//
// Dead units cannot be stepped by later stages: StepPreUpdate is a no-op
// when NeedsDeathFinalization is true or slot not alive [04 "unit sweep"].
// Death hooks and pool free happen exactly once via FinalizeDeath; second
// call is a no-op [01 §4.4].
package units

import (
	"github.com/nanolathe/nanolathe/internal/pool"
)

// SlotVisit is one deterministic visit of an active slot [01 §4.4][01 §6.2].
// Handle is the pool handle (slot index, 0 null per I5), Unit is the live
// instance, Slot is the uint16 slot number (same value as Handle, typed per
// retail slot index).
type SlotVisit struct {
	Handle pool.Handle
	Unit   *Unit
	Slot   uint16
}

// DeathResult reports the outcome of a FinalizeDeath call [01 §4.4].
type DeathResult struct {
	Freed     bool       // true if this call freed the slot
	HookFired bool       // true if this call fired OnDeath
	Cause     DeathCause // death cause for the freed unit
}

// VisitActiveSlots traverses active unit slots in deterministic retail order:
// players 0..9 ascending, slots ascending within each player's slice
// [01 §4.4][01 §6.2][P0-16] (I1). One visit per active slot per traversal.
// The traversal is live: pool.Alive is checked at the moment each slot is
// reached, so allocation/free during the traversal follows same-tick
// visibility — newly created unit ahead of scan position is visited same tick,
// behind waits for next tick; freed slot immediately reusable via lowest-free
// scan, no generation counter [01 §4.4]; no double-visit.
//
// No map iteration defines order; iteration is over the slot-indexed array
// and pool alive flags (I1).
func (w *World) VisitActiveSlots(fn func(SlotVisit)) {
	if w == nil || w.pool == nil || fn == nil {
		return
	}
	// Deterministic, no map iteration (I1).
	if w.pool.IsSliced() {
		for player := 0; player < 10; player++ {
			start, end, ok := w.pool.SliceForPlayer(player)
			if !ok {
				continue
			}
			for slot := start; slot <= end; slot++ {
				if slot < 0 || slot >= len(w.units) {
					continue
				}
				// Live check at visit moment preserves same-tick free visibility [01 §4.4].
				if !w.pool.Alive(pool.Handle(slot)) {
					continue
				}
				u := w.units[slot]
				if u == nil || !u.Alive {
					continue
				}
				if int(u.Owner) != player {
					continue
				}
				fn(SlotVisit{Handle: pool.Handle(slot), Unit: u, Slot: uint16(slot)})
			}
		}
		return
	}
	// Unsliced path: players 0..9 outer, slots ascending inner with owner filter
	// [01 §6.2] (I1). Each slot visited at most once per traversal when owner
	// matches outer player.
	capTotal := w.pool.TotalRecords()
	if capTotal <= 0 || capTotal > len(w.units) {
		capTotal = len(w.units)
	}
	for player := 0; player < 10; player++ {
		for slot := 1; slot < capTotal; slot++ {
			if !w.pool.Alive(pool.Handle(slot)) {
				continue
			}
			u := w.units[slot]
			if u == nil || !u.Alive {
				continue
			}
			if int(u.Owner) != player {
				continue
			}
			fn(SlotVisit{Handle: pool.Handle(slot), Unit: u, Slot: uint16(slot)})
		}
	}
}

// StepPreUpdate executes the per-unit pre-update/status work for the given
// handle [04 "unit sweep"][01 §4.4]. It is the explicit first boundary call
// that the central loop runs inside VisitActiveSlots before weapon/COB/
// orders/movement stages. Dead units cannot be stepped: if the slot is not
// alive, already Dying, or NeedsDeathFinalization, this is a no-op [04 "unit
// sweep"].
func (w *World) StepPreUpdate(handle pool.Handle, tick uint32) {
	if w == nil || w.pool == nil || handle == 0 {
		return
	}
	if !w.pool.Alive(handle) {
		return
	}
	idx := int(handle)
	if idx < 0 || idx >= len(w.units) {
		return
	}
	u := w.units[idx]
	if u == nil || !u.Alive {
		return
	}
	if u.Dying {
		return
	}
	// Also respect death finalization pending: if NeedsDeathFinalization true,
	// the unit is already latched dying and must not be stepped again.
	// (Dying check above already covers it.)
	w.unitPreUpdate(u, tick)
}

// NeedsDeathFinalization reports whether the handle's unit is latched Dying
// and still Alive, needing slot-end death handling / ledger cleanup finalization
// [01 §4.4][04 "unit sweep"]. After FinalizeDeath it returns false because the
// slot is freed [P0-16 §3.4].
func (w *World) NeedsDeathFinalization(handle pool.Handle) bool {
	if w == nil || w.pool == nil || handle == 0 {
		return false
	}
	if !w.pool.Alive(handle) {
		return false
	}
	idx := int(handle)
	if idx < 0 || idx >= len(w.units) {
		return false
	}
	u := w.units[idx]
	if u == nil || !u.Alive {
		return false
	}
	return u.Dying
}

// FinalizeDeath performs slot-end death handling and ledger cleanup finalization
// for the given handle [01 §4.4][04 "unit sweep"]. It fires OnDeath exactly
// once and frees the pool slot exactly once; a second call is a no-op [01
// §4.4]. The tick argument is the current global tick for hook context. It
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// counters. Zero RNG draws. Dead unit cannot be stepped afterwards because the
// slot is freed and pool.Alive is false, so StepPreUpdate becomes a no-op.
func (w *World) FinalizeDeath(handle pool.Handle, tick uint32) DeathResult {
	_ = tick // retained for hook context / future tick-dependent corpse logic
	if w == nil || w.pool == nil || handle == 0 {
		return DeathResult{}
	}
	if !w.pool.Alive(handle) {
		return DeathResult{}
	}
	idx := int(handle)
	if idx <= 0 || idx >= len(w.units) {
		// No unit entry but pool alive (inconsistent); free pool and report.
		w.pool.Free(handle)
		return DeathResult{Freed: true}
	}
	u := w.units[idx]
	if u == nil || !u.Alive {
		// Pool says alive but world entry nil or not alive: free pool, no hook.
		w.pool.Free(handle)
		return DeathResult{Freed: true}
	}
	if !u.Dying {
		return DeathResult{}
	}
	cause := u.DeathCause
	hookFired := false
	if !u.deathHookFired && w.OnDeath != nil {
		w.OnDeath(handle, cause, u)
		u.deathHookFired = true
		hookFired = true
	} else if !u.deathHookFired {
		// Hook is nil but we still conceptually consider the hook as not fired;
		// leave flag false so a later call with hook installed could fire.
		// However the slot will be freed now, so later calls are no-ops anyway.
		// To preserve exactly-once semantics when hook later appears, do not set flag.
	}
	if !u.deathExtraHookFired && w.OnDeathExtra != nil {
		w.OnDeathExtra(handle, cause, u)
		u.deathExtraHookFired = true
		hookFired = true
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	player := int(u.Owner)
	u.Alive = false
	w.units[idx] = nil
	w.pool.Free(handle)
	if player >= 0 && player < 10 && w.liveCounters[player] > 0 {
		w.liveCounters[player]--
	}
	return DeathResult{Freed: true, HookFired: hookFired, Cause: cause}
}
