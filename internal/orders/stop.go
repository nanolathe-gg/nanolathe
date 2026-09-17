package orders

import "github.com/nanolathe-gg/nanolathe/internal/units"

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
func stopHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	captionClear(u, n)
	clearWeaponTargetsUnconditional(u)
	// "if the unit's **committed** mover mode is airborne (`2`, [R-MOV-01 §8])
	// and its definition has `canfly`" [04 §3.4]. Committed means the flags-word
	// mirror, which the position commit publishes, not `Move.Mode`, which is the
	// request byte the mover-mode setter writes [04 R-AIR-01 §3][04 R-COLL-01 §1].
	// A restored save can carry the two apart by design [08 R-SAVE-02 §6, §8].
	if u != nil && u.Def != nil && u.Def.CanFly && moverMode(u) == 2 {
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
// Retired (WU-19-4): this carried an accepted-blocked marker saying the spawned record was
// never freed, because `VTOL_LandIfCan`'s executor lives in internal/movement
// ([04 R-AIR-01 §6]) and its touchdown was not published back to the pump, so
// the record parked at the head until some non-queued order purged it. The
// descriptor now has a handler — vtolLandIfCanHandler in vtolair.go — which
// hands off to the movement system's air-leg runner; the runner reports the
// machine's outcome as an ordinary result code, *complete* on touchdown, and
// the pump frees the record the ordinary way [04 §3.3].
func spawnLandIfCan(u *units.Unit, tick uint32) {
	id := rowVTOLLandIfCan
	if id == 0 {
		return
	}
	q := QueueOfUnit(u)
	if q == nil {
		return
	}
	spawnAtSegmentHead(q, id, Node{
		Owner:        u.Handle,
		GoalX:        u.X,
		GoalY:        u.Y,
		GoalZ:        u.Z,
		CreationTick: tick,
		GoalSupplied: true, // "goal = own position" [04 R-ORD-01 §2]
	})
}

// spawnAtSegmentHead is the handler head insert of [04 R-ORD-01 §1], routed:
// "a handler that spawns a new record inserts it at the FRONT of the segment
// the record's rear-segment flag selects". That flag is the descriptor's static
// bit 18 [04 §3.1][04 §3.2], the same bit `Queue.appendTail` and the producer
// insertion's head-insert branch select on [04 R-ORD-01 §13].
//
// `Queue.PushHead` inserts into the primary segment unconditionally, so a
// caller that spawns a descriptor whose static mask carries bit 18 —
// `SelfDestruct` and `BuildWeapon` are the only two [R-ORDER-02 §2] — must make
// the choice itself. Every handler spawn in this package goes through here so
// that a later descriptor gaining the flag routes without a second site being
// remembered.
func spawnAtSegmentHead(q *Queue, id ID, n Node) *Node {
	if q == nil || id == 0 {
		return nil
	}
	if isSecondary(id) {
		q.PushSecondary(id, n)
		if rear := q.Secondary(); len(rear) > 0 {
			return rear[0]
		}
		return nil
	}
	return q.PushHead(id, n)
}

// clearWeaponTargetsUnconditional is the unconditional entry of the
// weapon-target clear [R-ORDER-02 §3]. The three entries share one tail and
// differ only at the top: the inhibit and release VERBS carry the control-byte
// guards ([04 R-ORD-01 §7]), and this one reads and writes no control byte at
// all. So it neither skips an unassigned slot nor touches the enabled bit or
// autonomy — which is what makes `Stop` "clear the three weapon-slot targets
// unconditionally" [04 R-ORD-01 §2] leave a slot's posture exactly as it was.
//
// The shared tail, identical in all three: a slot whose target pair is already
// empty is skipped entirely; any other slot has its pair reset to the empty
// form, `StartBuilding` is resolved in the owner's script and the result
// DISCARDED, and the owner's COB function `TargetCleared` is arranged with the
// slot index as its FIRST script argument (arity 1, [04 R-UNIT-06 §4]). The
// event is script-only, and the arrange is a no-op when the unit's script
// defines no such function.
//
// The verbs' "all slots" argument (k = 3) recurses over slots 0 and 1 and falls
// through to 2; this entry has no such form — it takes one slot, so a caller
// that wants all three walks them in index order, as here.
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
	id := rowStop
	if id == 0 || int(id) >= len(table) {
		return
	}
	if table[int(id)].Handler == nil {
		table[int(id)].Handler = stopHandler
	}
}

func init() { ensureStopHandler() }
