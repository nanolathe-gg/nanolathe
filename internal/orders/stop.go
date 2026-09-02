package orders

import "github.com/nanolathe/nanolathe/internal/units"

// stopHandler is the `Stop` order [04 R-ORD-01 §2]. Its row is three steps and
// a completion:
//
//	caption clear; clear the three weapon-slot targets unconditionally
//	(the unconditional entry of [R-ORDER-02 §2]); if the unit's committed
//	mover mode is airborne (2, [04 R-MOV-01 §8]) and its definition has
//	`canfly`, spawn `VTOL_LandIfCan` (target none, goal = own position,
//	p1..p3 = 0) at the head. Complete.
//
// *Complete* is result code 5 [04 R-ORD-01 §1].
//
// The airborne arm is why a stopped aircraft lands: the landing machine of
// [04 R-AIR-01 §6] is `VTOL_LandIfCan`, and this is one of its two producers
// (the other is `VTOL_Standby`'s idle unloaded arm, [04 R-AIR-01 §7]).
//
// stopHandler is the row of [04 R-ORD-01 §2]. The spawned `VTOL_LandIfCan`
// record carries a true creation-tick snapshot [04 §3.2] because the tick is
// the handler's fourth argument (WU-18-7 retired the by-name
// `stopHandlerAtTick` special case the pump used to reach this body with).
func stopHandler(u *units.Unit, _ *Node, _ uint32, tick uint32) Code {
	captionClear(u)
	clearWeaponTargetsUnconditional(u)
	if u != nil && u.Def != nil && u.Def.CanFly && u.Move.Mode&0x3 == 2 {
		spawnLandIfCan(u, tick)
	}
	return Code(5) // *complete* [04 R-ORD-01 §1]
}

// spawnLandIfCan head-inserts the `VTOL_LandIfCan` record the `Stop` row
// names: no target, the goal is the unit's own position, and the three general
// parameters are zero [04 R-ORD-01 §2].
//
// The spawned record carries no dynamic gate: a freshly allocated record awaits
// nothing, and the spawn sites that do wait on something state their own gate
// ("gate = 0", "gate |= 0xE0") [04 R-ORD-01 §1][04 §3.2]. `Stop`'s row states
// none, so the record is dispatchable on its next visit.
//
// Retired (WU-19-4): this carried a TODO(T25) saying the spawned record was
// never freed, because `VTOL_LandIfCan`'s executor lives in internal/movement
// ([04 R-AIR-01 §6]) and its touchdown was not published back to the pump, so
// the record parked at the head until some non-queued order purged it. The
// descriptor now has a handler — vtolLandIfCanHandler in vtolair.go — which
// hands off to the movement system's air-leg runner; the runner reports the
// machine's outcome as an ordinary result code, *complete* on touchdown, and
// the pump frees the record the ordinary way [04 §3.3].
func spawnLandIfCan(u *units.Unit, tick uint32) {
	id := Lookup("VTOL_LandIfCan")
	if id == 0 {
		return
	}
	q := QueueOfUnit(u)
	if q == nil {
		return
	}
	q.PushHead(id, Node{
		Owner:        u.Handle,
		GoalX:        u.X,
		GoalY:        u.Y,
		GoalZ:        u.Z,
		CreationTick: tick,
	})
}

// clearWeaponTargetsUnconditional is the unconditional sibling of the
// weapon-target-clear helper [R-ORDER-02 §2]. That section gives three entry
// points to one walk over the three weapon slots in order: the cleanup-side
// form guarded by the slot's assigned bit and its clear latch
// (clearWeaponBuildTargets above), a mid-life form with the mirror guard, and
// this one, which handlers call with no guard at all. What survives in every
// form is the empty test and the notification: a slot whose target pair is
// already empty is skipped, and any other slot has its pair reset to the empty
// form and the owner's COB function `TargetCleared` arranged with the slot
// index as the first script argument. The event is script-only, and the
// arrange is a no-op when the unit's script defines no such function.
//
// TODO(question): [R-ORDER-02 §2] distinguishes the three entry points only by
// their latch guard ("one with the mirror guard (fires only while bit 4 is
// set, then clears bit 4), and one unconditional"), so whether the
// unconditional form also writes the latch bit — and whether it skips a slot
// whose assigned bit is clear — is not established. This form leaves the
// control byte alone, which is the reading that keeps "unconditional" meaning
// "no control-byte condition". A trace of the unconditional entry's writes to
// the slot control byte would settle it.
func clearWeaponTargetsUnconditional(u *units.Unit) {
	if u == nil {
		return
	}
	bridge := callbackBridgeFor(u)
	for slot := 0; slot < units.NumSlots; slot++ {
		s := u.SlotAt(slot)
		if s == nil {
			continue
		}
		if s.Target.Kind == units.TargetNone {
			continue // target words already empty: reset and signal are skipped
		}
		s.Target = units.Target{Kind: units.TargetNone}
		arrangeDeferred(bridge, "TargetCleared", []int32{int32(slot)})
	}
}

// ensureStopHandler installs the `Stop` handler onto the descriptor table.
// The table is built in table.go's init, so this file's init may run first;
// the pump re-runs the ensure the same way it does for the move, transport and
// park families.
func ensureStopHandler() {
	if len(table) == 0 {
		return
	}
	id := Lookup("Stop")
	if id == 0 || int(id) >= len(table) {
		return
	}
	if table[int(id)].Handler == nil {
		table[int(id)].Handler = stopHandler
	}
}

func init() { ensureStopHandler() }
