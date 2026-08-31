package orders

// The move family beyond `Move_Ground`: the queued-move pair `QMove` and
// `QPatrol` of [04 R-ORD-01 §2], the ground `Patrol` and `RepairPatrol` of
// [04 R-ORD-01 §4], and the two air forms `VTOL_Move` and `VTOL_Patrol` of
// [04 R-ORD-02 §2].
//
// Why this file exists (WU-18-8). pump.go's ensureMoveHandlers used to install
// `moveGroundHandler` — which is `Move_Ground`'s row and only that row — on all
// eight names of the move family. The four sections above give each of the
// other seven its own body, and none of them is `Move_Ground`'s:
//
//   - `QMove` / `QPatrol` are one row in [04 R-ORD-01 §2]: deadline 60, then
//     *rotate*. They own no goal and read no target — a queued-move record is a
//     rally marker its owner cycles, which is why "the factory itself never
//     moves" while its rally records sit in its queue [04 §3.8]. Under the move
//     body they instead walked their goal and completed, which is the behaviour
//     of the record the rally walk COPIES onto the product, not of the marker.
//   - `Patrol` and `RepairPatrol` have three- and two-phase bodies built around
//     the patrol-chain setup [04 R-ORD-01 §4]. Under the move body a patrol
//     completed at its first waypoint and the unit stopped: no chain, no
//     return-to-start record, no cycle.
//   - `VTOL_Patrol` and `VTOL_Move` are air bodies [04 R-ORD-02 §2]; the air
//     move in particular "never returns 9", where the ground move's phase 1
//     re-arms on exactly the two release bits that are its normal end.
//   - `VTOL_RepairPatrol` already had a written body in vtolwork.go
//     [04 R-ORD-01 §7] that could never install, because the family installers
//     assign only where a descriptor's handler is still nil and the move list
//     had already claimed the name.
//
// The vocabulary is [04 R-ORD-01 §1]'s throughout, and the helpers are the ones
// the earlier families already wrote: armDeadline and drawBelow (standing.go),
// installPointGoal and the slot helpers (combat.go), workStatus and hasMover
// (work.go), patrolChainSetup and airWorkPreamble (vtolwork.go).
//
// Two facts about gate writes govern several rows below and are stated once. A
// row that ASSIGNS its gate after arming a deadline ("deadline 15; gate =
// 0xE0") destroys the deadline setter's bit 0, so the record then waits on the
// movement outcomes alone with no timer; a row that ORs it ("gate |= 0xE0")
// keeps both. That is not a reading of the listing but its corroborated
// meaning: [R-ORDER-02 §1]'s outcome table gives an unpublished route for
// `Move_Ground` and `Patrol` as a "blocked-head stall at gate 0xE0 — no timer,
// no draw", which is exactly what the assignment produces.
//
// TODO(T25): the air marker family of [04 R-AIR-01 §4] and the takeoff preamble
// of [04 R-AIR-01 §6] live in internal/movement, which imports this package and
// therefore cannot be imported back. `VTOL_Move`'s executor there already owns
// the preamble, the footprint snap and the point marker for the record's goal,
// and publishes the arrival bit this file's phase 2 completes on; `VTOL_Patrol`
// has no executor there yet. Placeholder: the rows below run their
// record-visible half — the phase machine, the slot writes, the goal release
// and the gates — and leave the marker geometry to that seam, exactly as
// vtolwork.go's twins do.

import "github.com/nanolathe/nanolathe/internal/units"

// statusArrived is status kind 6 (`arrived`), whose default display text is
// `Arrived` [04 R-ORD-01 §1].
const statusArrived uint8 = 6

// hasSuccessor reports whether a record has another record behind it in the
// front segment. Two rows ask it: `Patrol` phase 2's "a next patrol record
// exists" and `VTOL_Move` phase 2's "when this record has no successor"
// [04 R-ORD-01 §4][04 R-ORD-02 §2]. The walk is over the ordered segment
// slice, never a map (I1).
//
// TODO(question): [04 R-ORD-01 §4] words `Patrol`'s test as "a next patrol
// record exists" while [04 R-ORD-02 §2] words the air move's as "no
// successor". Whether the patrol test additionally requires the successor to
// carry the chain-member bit — or to share the descriptor — is not stated, and
// the two readings differ only for a patrol with an unrelated order queued
// behind it. The successor test is used for both, because it is the one the
// record's own next link supports. A trace of the patrol phase-2 arm's read
// (the record's next pointer alone, or a walk filtered by the mask bit) would
// settle it [04 R-ORD-01 §4].
func hasSuccessor(u *units.Unit, n *Node) bool {
	q := QueueOfUnit(u)
	if q == nil || n == nil {
		return false
	}
	primary := q.Primary()
	for i, rec := range primary {
		if rec == n {
			return i+1 < len(primary)
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// QMove and QPatrol [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// queuedMoveHandler is the queued-move pair's whole row: "Deadline 60,
// *rotate*. No goal, no target use: a 60-tick delayed tail rotate"
// [04 R-ORD-01 §2][R-ORDER-02 §1].
//
// Every visit arms the deadline and returns 6, so the record moves to the tail
// of its segment and the walk continues with whatever was behind it. The gate
// bit the deadline setter ORs is what stops the rotation from spinning: the
// record is blocked at the head again until its 60 ticks are up.
//
// The record is a marker, not a move. Its goal triple is read by the rally walk
// of [04 §3.8], which enqueues the corresponding resolved move or patrol
// operation on the PRODUCT with that goal — the factory's own record never
// moves the factory.
func queuedMoveHandler(_ *units.Unit, n *Node, _ uint32, tick uint32) Code {
	armDeadline(n, tick, 60)
	return 6 // *rotate*: to the tail of the segment, walk continues [04 §3.3]
}

// ---------------------------------------------------------------------------
// Patrol [04 R-ORD-01 §4]
// ---------------------------------------------------------------------------

// patrolHandler is the ground patrol.
//
// Row: phase 0: dead -> cancel-all; the patrol-chain setup; deadline 1;
// advance. Phase 1: clear the three slot targets; point goal at the goal with
// radius 0; deadline 15; gate = 0xE0; advance. Phase 2: satisfied ∩ 0xE0 ->
// phase = 1, rotate; a next patrol record exists -> gate = 0, phase = 1, wait;
// else deadline 30 + RNG(30), phase = 1, return 4. Other: cancel-all.
//
// The cycle is the rotate: an arrived record goes to the segment tail with its
// phase set back to 1, so the record behind it — the next waypoint, or the
// return-to-start record the chain setup appended — becomes the head and arms
// its own leg. That is the whole of "patrol", and it is what the record could
// not do while it ran `Move_Ground`'s body, which completes on arrival.
func patrolHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7 // cancel-all
	}
	switch n.Phase {
	case 0:
		if !u.Alive {
			return 7 // cancel-all
		}
		patrolChainSetup(u, n)
		armDeadline(n, tick, 1)
		return 1 // advance
	case 1:
		clearWeaponTargetsUnconditional(u) // "clear the three slot targets" [R-ORDER-02 §2]
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, 0)
		armDeadline(n, tick, 15)
		n.DynamicGate = gateMoveOutcomes // ASSIGNED: the leg waits on movement alone (file header)
		return 1                         // advance
	case 2:
		if satisfied&gateMoveOutcomes != 0 {
			n.Phase = 1 // the next visit re-arms this waypoint's leg
			return 6    // *rotate*: the record behind takes the head
		}
		if hasSuccessor(u, n) {
			n.DynamicGate = 0
			n.Phase = 1
			return 3 // *wait*: the pump arms bit 0 and its own 30..44 deadline
		}
		armDeadline(n, tick, 30+drawBelow(u, 30)) // the row's one draw (I4)
		n.Phase = 1
		return 4 // continue walking unchanged [04 §3.3]
	default:
		return 7 // cancel-all: a phase outside the machine [R-ORDER-02 §1]
	}
}

// ---------------------------------------------------------------------------
// RepairPatrol [04 R-ORD-01 §4][05 R-WORK-01 §3]
// ---------------------------------------------------------------------------

// repairPatrolHandler is the ground repair patrol.
//
// Row: phase 0: with a target, goal = its position; the patrol-chain setup;
// advance. Phase 1: satisfied ∩ 0xE0 -> rotate. Point goal at the goal radius
// 16; deadline 60; gate |= 0xE0. Then, only when the player's energy is at
// least 20 % of energy storage, the repair-candidate scan; then, when both
// energy and metal are at least 20 % of their storages, hold; otherwise the
// feature pairing and its reclaim-spawn decision tree; none -> hold. Other
// phase: cancel-all.
//
// The gate here is ORed, not assigned, so the 60-tick deadline survives it and
// the row re-runs its scan every 60 ticks whether or not the leg has finished.
func repairPatrolHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7 // cancel-all
	}
	switch n.Phase {
	case 0:
		if target := lookupTarget(u, n.Target); target != nil {
			n.GoalX, n.GoalY, n.GoalZ = target.X, target.Y, target.Z
		}
		patrolChainSetup(u, n)
		return 1 // advance
	case 1:
		if satisfied&gateMoveOutcomes != 0 {
			return 6 // *rotate*: the leg is done, the next waypoint takes the head
		}
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, 16)
		armDeadline(n, tick, 60)
		n.DynamicGate |= gateMoveOutcomes // ORed: the 60-tick deadline survives
		if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) {
			candidates := scanRepairCandidates(u, u.Def.SightDistance)
			if target := pickRepairCandidate(u, candidates); target != nil && !scanHostile(bindingFor(u), u, target) && spawnPatrolRepair(u, target, tick) {
				n.DynamicGate = 0
				return 6 // rotate after the accepted repair issue
			}
		}
		if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) && resourceAtLeastTwenty(resources.Stock[0], resources.Capacity[0]) {
			return 2 // both stores are healthy: keep patrolling
		}
		if feature, ok := chooseReclaimFeature(u, u.Def.SightDistance); ok && spawnPatrolReclaim(u, feature, false, tick) {
			n.DynamicGate = 0
			return 3 // wait while the spawned reclaim runs at the head
		}
		return 2 // no repair/reclaim candidate
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// VTOL_Move [04 R-ORD-02 §2]
// ---------------------------------------------------------------------------

// vtolMoveHandler is the air move's record machine.
//
// Row: phase 0: a live mover and `canfly` (else cancel-all); the takeoff
// preamble; advance. Phase 1: caption clear; inhibit all three slots; snap the
// record's goal X and Z onto the unit's own footprint; point marker there with
// no altitude or radius setter; install; gate = 0xE0; advance. Phase 2 (any of
// the three arrival bits): when this record has no successor, status 6
// (`Arrived`); complete. Other: cancel-all.
//
// The divergence from `Move_Ground` that matters is the ending: "There is no
// re-arm: the air move never returns 9". The ground row completes only on the
// arrival bit 0x20 and re-arms on the two release bits 0x40 and 0x80; the air
// row completes on any of the three, because the goal-release and
// payload-replaced bits end the air move exactly as arrival does. Under
// `Move_Ground`'s body a released air marker sent the record back to phase 0
// for another 30..59-tick approach cycle, and cost a simulation draw each time.
//
// The preamble, the footprint snap and the marker are internal/movement's air
// executor for this same record (file header). It applies the identical snap
// when it builds the marker and publishes the arrival bit phase 2 waits on, so
// nothing here re-derives that geometry.
func vtolMoveHandler(u *units.Unit, n *Node, _ uint32, _ uint32) Code {
	if u == nil || n == nil {
		return 7 // cancel-all
	}
	switch n.Phase {
	case 0:
		if !u.Alive || !hasMover(u) || u.Def == nil || !u.Def.CanFly {
			return 7 // cancel-all
		}
		return 1 // advance
	case 1:
		workStatus(u, statusOK, "") // the caption clear, no text
		inhibitSlot(u, slotAll)     // k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		n.DynamicGate = gateMoveOutcomes
		return 1 // advance
	case 2:
		// The pump dispatched this phase, so one of the three bits arrived.
		if !hasSuccessor(u, n) {
			workStatus(u, statusArrived, "Arrived") // only the last record captions
		}
		return 5 // *complete*
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// VTOL_Patrol [04 R-ORD-02 §2]
// ---------------------------------------------------------------------------

// vtolPatrolHandler is the air patrol.
//
// Row: phase 0: mover and `canfly` (else cancel-all); the patrol-chain setup;
// caption clear with `Patrolling`; the takeoff preamble; then inhibit all three
// slots; advance. Phase 1: clear the five movement pending bits 0x20-0x200 from
// the record's pending word; advance. Phase 2, in order: satisfied ∩ 0xE0 ->
// rotate (the phase is left at 2, so the next visit re-arms the leg); a point
// marker 320 world units short of the waypoint along the approach with
// horizontal arrival radius 0x150; install; gate |= 0xE0; then the low-health
// pad seek; then the fire-at-will opportunity scan; else deadline 30, hold.
// Other phase: cancel-all.
//
// Because the marker stops 320 units short of the waypoint and its arrival
// radius is 336, an air patrol leg is satisfied about 320 units before the
// authored waypoint, and a patrol whose waypoints are closer together than that
// rotates on every visit [04 R-ORD-02 §2].
func vtolPatrolHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7 // cancel-all
	}
	switch n.Phase {
	case 0:
		patrolChainSetup(u, n)
		// airWorkPreamble is the caption clear plus the takeoff preamble of
		// [04 R-AIR-01 §6], and carries this row's own mover/`canfly`
		// cancel-all [04 R-ORD-01 §7].
		if code := airWorkPreamble(u, n, "Patrolling"); code != 1 {
			return code
		}
		inhibitSlot(u, slotAll)
		return 1 // advance
	case 1:
		n.Satisfied &^= pendingMovement // the five movement bits 0x20..0x200
		return 1                        // advance
	case 2:
		if satisfied&gateMoveOutcomes != 0 {
			return 6 // *rotate*, phase left at 2: the next visit re-arms the leg
		}
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, 0x150)
		n.DynamicGate |= gateMoveOutcomes
		if target := opportunityScan(u); target != nil && autoEngage(u, target) {
			n.DynamicGate = 0
			return 3 // the spawned attack runs at the head
		}
		armDeadline(n, tick, 30)
		return 2 // *hold*
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// patrolHandlers pairs each descriptor with its handler in a fixed slice, not a
// map: registration order is source order (I1).
//
// `VTOL_RepairPatrol` is absent here on purpose — vtolwork.go owns that row
// [04 R-ORD-01 §7], and this unit's narrowing of ensureMoveHandlers is what
// finally lets its installer claim the descriptor.
var patrolHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"QMove", queuedMoveHandler},
	{"QPatrol", queuedMoveHandler},
	{"Patrol", patrolHandler},
	{"RepairPatrol", repairPatrolHandler},
	{"VTOL_Move", vtolMoveHandler},
	{"VTOL_Patrol", vtolPatrolHandler},
}

// ensurePatrolHandlers installs the family onto the descriptor table. It is
// idempotent, assigns only where the descriptor's handler is still nil, and
// tolerates a table that has not been built yet — the shape ensureStopHandler
// establishes.
func ensurePatrolHandlers() {
	if len(table) == 0 {
		return
	}
	for _, h := range patrolHandlers {
		id := Lookup(h.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = h.handler
		}
	}
}

func init() { ensurePatrolHandlers() }
