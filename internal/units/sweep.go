// Deterministic slot traversal and death finalization [01 §4.4][04 R-MOV-03 §1].
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
// slot-end death handling; explicit TeardownCleanup is teardown-only [01 §4.4].
//
// Per-unit micro-order within one visit [04 R-MOV-03 §1][01 §4.4]:
// general update, weapons, normal COB drain, StepPostCOBStatus, then water
// damage/self-repair/orders/movement, and finally FinalizeDeath at slot end.
// Allocated death-marked units still receive the normal drain and status
// refresh. Other stages retain their own eligibility gates; only the final
// slot-end call retires the unit. A freed slot cannot be stepped.
// Death hooks and pool free happen exactly once via FinalizeDeath; second
// call is a no-op [01 §4.4].

package units

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
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
			// No runtime-owner test. "Within the slot every unit record of the
			// player's slice is visited in ascending pool order; a record whose
			// definition index is zero is skipped" — the sweep visits the
			// physical slot, and the per-unit controller test that follows is
			// "re-evaluated per unit from the owner record, not from the slot
			// being swept" [04 R-MOV-03 §1 "the player gate"]. A record whose
			// owner moved without moving slices therefore stays visited; an
			// owner-equality filter here dropped it in both directions — never
			// stepped and never death-finalised — because no slice holds it
			// under its new owner either. Callers that care about the runtime
			// owner test it themselves, as ForEachPlayerSliceLive documents.
			fn(SlotVisit{Handle: pool.Handle(slot), Unit: u, Slot: uint16(slot)})
		}
	}
}

// StepPostCOBStatus refreshes status after the unit's one normal COB drain
// and before water damage, self-repair, orders and movement [04 R-MOV-03 §1].
// Allocated Dying units receive this refresh before slot-end finalization,
// including the health-sample roll consumed by local Killed [04 §5.1].
func (w *World) StepPostCOBStatus(handle pool.Handle, tick uint32) {
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
	w.unitPostCOBStatus(u, tick)
}

// NeedsDeathFinalization reports whether the handle's unit is latched Dying
// and still Alive, needing slot-end death handling finalization
// [01 §4.4][04 R-MOV-03 §1]. After FinalizeDeath it returns false because the
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
// for the given handle [01 §4.4][04 R-MOV-03 §1]. It fires OnDeath exactly
// once and frees the pool slot exactly once; a second call is a no-op [01
// §4.4]. The tick argument is the current global tick for hook context. Free
// retains the stored slot index stale [P0-16 §3.4] and decrements per-player
// live counters. Zero RNG draws. Dead unit cannot be stepped afterwards
// because the slot is freed and pool.Alive is false, so StepPostCOBStatus becomes
// a no-op.
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
	// Final pool free: clears the alive mask and classifier/status flags but
	// retains the stored slot index [P0-16 §3.4].
	player := int(u.Owner)
	u.Flags &^= ClassifierEligibleStatus
	u.Alive = false
	// The host keeps only packet-visible slot fields after finalization; this
	// releases the per-unit payload while leaving stale death reconstruction
	// able to reach its owner and kill word [P0-16][06 §12.1].
	w.rawUnits[idx] = retainedRawRecord(u)
	w.units[idx] = nil
	w.pool.Free(handle)
	if player >= 0 && player < 10 && w.liveCounters[player] > 0 {
		w.liveCounters[player]--
	}
	return DeathResult{Freed: true, HookFired: hookFired, Cause: cause}
}
