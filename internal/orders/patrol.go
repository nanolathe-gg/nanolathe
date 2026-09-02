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
// The air marker family of [04 R-AIR-01 §4] lives in internal/movement, which
// imports this package and therefore cannot be imported back. That is not a
// barrier: `MovementGoalAdapter.InstallAir` is the seam the session composes for
// exactly this, and it carries the absolute marker position and the arrival
// radius. `VTOL_Patrol` computes its displaced waypoint here and installs
// through that seam; the takeoff preamble it needs is `airWorkPreamble`, which
// this package already owns.
//
// Corrected 2026-08-31: this header carried an accepted-blocked marker saying `VTOL_Patrol`
// "has no executor there yet" and left the marker geometry unbuilt, so an air
// patrol installed no payload at all — it armed gate 0xE0 and waited for an
// arrival bit that nothing could raise. Since the point installer now takes the
// canfly release-only arm faithfully, that left an air patrol with no goal
// whatsoever.

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// statusArrived is status kind 6 (`arrived`), whose default display text is
// `Arrived` [04 R-ORD-01 §1].
const statusArrived uint8 = 6

// The two ground patrol rows' point-goal radii [04 R-ORD-01 §4]: `Patrol`
// phase 1 binds "point goal at the goal with radius 0", `RepairPatrol` phase 1
// "point goal at the goal radius 16". They are named here, beside the handlers
// that bind them, because the movement layer needs the same two values for the
// goal handle's threshold and must not re-derive them from a descriptor name.
//
// What the pair means at the handle: the threshold is floor(radius/16)²
// [R-P0-01 corrected], so a patrol leg retires only on its waypoint's own cell
// while a repair-patrol leg retires anywhere within one cell of it.
const (
	patrolGoalRadius       int32 = 0
	repairPatrolGoalRadius int32 = 16
)

// PatrolGoalRadius reports the arrival radius the ground patrol family binds
// with its point goal, and whether n is one of those rows.
//
// It is the same read-back seam as ParkGoalRect: the handler authors the goal
// geometry and the movement layer reads it back, so the value is stated once
// and the movement layer never guesses a radius from a name it does not own.
// Both rows bind in phase 1 and the bound radius does not vary with the phase
// the record is later left in, so this is a per-record constant.
//
// The two air rows are deliberately absent. `VTOL_Patrol` installs an air
// marker with the arrival radius of [04 R-ORD-02 §2] through the air seam
// (airPatrolArrivalRadius below), not a ground point goal, and
// `VTOL_RepairPatrol` is vtolwork.go's row [04 R-ORD-01 §7]; neither binds the
// ground goal handle this accessor describes.
func PatrolGoalRadius(n *Node) (int32, bool) {
	if n == nil {
		return 0, false
	}
	switch DescriptorFor(n.ID).Name {
	case "Patrol":
		return patrolGoalRadius, true
	case "RepairPatrol":
		return repairPatrolGoalRadius, true
	}
	return 0, false
}

// hasSuccessor reports whether a record has another record behind it in the
// front segment. ONE row asks it: `VTOL_Move` phase 2's "when this record has
// no successor", which decides whether the air move captions `Arrived`
// [04 R-ORD-02 §2]. The walk is over the ordered segment slice, never a map
// (I1).
//
// The marker that stood here asked whether `Patrol` phase 2's arm — worded in
// [04 R-ORD-01 §4] as "a next patrol record exists" — needed the successor to
// carry the chain-member bit. [04 R-ORD-01 §9] retires the question by
// retiring the caller: the handler body has no read of the record chain at
// that point at all, and the arm is the standing-fire scan (see patrolHandler
// below). §4's label for it was wrong. The air move's wording is unaffected.
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
// phase = 1, rotate; the standing-fire scan finds a target the auto-engage
// issuer accepts -> gate = 0, phase = 1, wait; else deadline 30 + RNG(30),
// phase = 1, return 4. Other: cancel-all.
//
// Corrected by [04 R-ORD-01 §9] (WU-19-68). [04 R-ORD-01 §4]'s row worded the
// middle arm as "a next patrol record exists -> gate = 0, phase = 1, wait",
// and this handler implemented it as a successor test. The handler body has no
// read of the record chain at that point: the arm is the idle-arm target scan
// of [04 R-STANCE-01 §3] — which searches only when the standing FIRE field
// reads exactly 2, fire at will, over `sightdistance` — handed to the
// auto-engage issuer of [04 R-STANCE-01 §4] with `force = 0`, and the wait is
// taken only when the issuer ACCEPTS. That scan-and-engage is the "idle/loiter
// arm of `Patrol`" §3 already lists among the scan's callers; §4's label for
// it was wrong. `VTOL_Patrol` phase 2 already ran the same pair in the same
// position, which is what the ground row was missing.
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
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, patrolGoalRadius)
		armDeadline(n, tick, 15)
		n.DynamicGate = gateMoveOutcomes // ASSIGNED: the leg waits on movement alone (file header)
		return 1                         // advance
	case 2:
		if satisfied&gateMoveOutcomes != 0 {
			n.Phase = 1 // the next visit re-arms this waypoint's leg
			return 6    // *rotate*: the record behind takes the head
		}
		// The standing-fire scan and its issuer, in the section's own order and
		// with the issuer's own acceptance as the condition [04 R-ORD-01 §9]
		// [04 R-STANCE-01 §3][04 R-STANCE-01 §4]. Both gates live inside the
		// pair: opportunityScan returns nothing unless the standing fire field
		// is exactly 2, and autoEngage with force = 0 refuses a hold-fire or
		// hold-position stance and refuses a target the resolver will not turn
		// into an attack.
		if target := opportunityScan(u); target != nil && autoEngage(u, target, false) {
			n.DynamicGate = 0
			n.Phase = 1
			return 3 // *wait*: the spawned attack runs at the head
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
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, repairPatrolGoalRadius)
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
		// Phase 0 IS the takeoff preamble, and the preamble ORs 0xE0 into the
		// record's gate [04 R-ORD-02 §2][04 R-AIR-01 §6]. The preamble's
		// flight-block half runs in movement's executor off this same record,
		// but the gate is the record's half and has to be armed here.
		//
		// Without it the pump's advance re-dispatches from the head in the same
		// visit (a code-1 advance sets cursor 0), so phase 1 armed the gate and
		// phase 2 completed the order before the aircraft had left the ground —
		// and the arrival that completed it was the preamble's own climb marker
		// at the unit's own X/Z, cruisealt/2 up. A grounded aircraft therefore
		// rose about 55 units, reported Arrived, and never flew anywhere. With
		// the gate armed the walk stalls until the climb reports arrival, and
		// phase 1 installs the destination marker on the next visit.
		//
		// The gate is armed only when the preamble will actually build that
		// marker: its step 4 is grounded-only, and an already-airborne aircraft
		// gets no climb marker and so must not wait for one. That asymmetry is
		// why a second move issued in flight always worked.
		if u.Move.Mode&0x3 == 1 {
			n.DynamicGate |= gateMoveOutcomes
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
// marker at the waypoint displaced 320 world units along `bearing(me -> goal)`
// with horizontal arrival radius 0x150; install; gate |= 0xE0; then the
// low-health pad seek; then the fire-at-will opportunity scan; else deadline
// 30, hold. Other phase: cancel-all.
//
// Corrected 2026-08-31: this comment said the marker stops "320 world units
// short of the waypoint along the approach", and concluded that a leg is
// satisfied about 320 units early and that closer waypoints rotate every visit.
// That is the reading [04 R-ORD-02 §2] retracted on 2026-08-30. The leg forms
// both components at `bearing(me -> goal)`, negates each and ADDS them to the
// goal, so the marker sits 320 units BEYOND the waypoint. With the strict 336
// arrival radius the leg is satisfied about 16 world units before the waypoint,
// not 656 before it: the aircraft flies essentially the whole leg and aims
// through the corner rather than braking into it. Waypoints closer together
// than about 336 units still rotate every visit.
//
// Corrected (WU-19-4): this header carried an accepted-blocked marker saying the low-health
// pad seek "is not implemented here", because its candidate list was thought to
// be an air-base enumeration internal/movement owns and its draw had to travel
// with that list. The list is the binding's own live-unit enumerator walked
// through the shared pad filter of [04 R-AIR-01 §7] — the same one
// `VTOL_RepairPatrol` step 3 already uses — so the row can take its own draw in
// the enumerator's pool order without diverging the stream (I4).
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
		installAirPatrolMarker(u, n)
		n.DynamicGate |= gateMoveOutcomes
		// The low-health pad seek, in the section's own order: it runs after
		// the leg's marker is armed and before the opportunity scan
		// [04 R-ORD-02 §2]. The health test is the one expression of
		// [04 R-AIR-01 §11], the candidate set is the target registry's third
		// list at its last rebuild, and the draw is one RNG(count) over the
		// admitted vector — taken only when that vector is non-empty, so an
		// aircraft with no pad in reach consumes no random state
		// [04 R-ORD-01 §1][I4].
		if combat.AirBelowThreeQuarters(u) {
			if pads := airBasePads(u); len(pads) > 0 {
				releaseGoalPayload(u, n)
				if pad := pickCandidate(u, pads); pad != nil && spawnPatrolLanding(u, pad, tick) {
					n.DynamicGate = 0
					return 0 // *restart*: the landing record now holds the head
				}
			}
		}
		if target := opportunityScan(u); target != nil && autoEngage(u, target, false) {
			n.DynamicGate = 0
			return 3 // the spawned attack runs at the head
		}
		armDeadline(n, tick, 30)
		return 2 // *hold*
	default:
		return 7 // cancel-all
	}
}

// airPatrolSetback is the 320 world units `VTOL_Patrol`'s leg displaces its
// marker by, and airPatrolArrivalRadius the strict horizontal arrival radius
// 0x150 (336) that goes with it — the fourth value of the radius-flag family
// [04 R-ORD-02 §2][04 R-AIR-01 §4].
const (
	airPatrolSetback       = 320
	airPatrolArrivalRadius = 0x150
)

// installAirPatrolMarker builds `VTOL_Patrol`'s point marker and installs it
// through the air seam.
//
// The geometry is the negate-then-add shape [04 R-ORD-02 §2] gives: both
// components are formed at `bearing(me -> goal)`, negated, and added to the
// goal, which places the marker 320 world units BEYOND the waypoint. Subtracting
// the offset pair is that negate-and-add, and it is the same sign the air legs
// in internal/movement use for this family.
//
// A record whose goal the patrol chain has not filled in yet, or a unit whose
// binding has no air seam, installs nothing and leaves the caller to arm its
// gate — the leg then holds on its deadline exactly as it does between waypoints.
func installAirPatrolMarker(u *units.Unit, n *Node) {
	if u == nil || n == nil {
		return
	}
	b := bindingOfUnit(u)
	if b == nil || b.Movement == nil || b.Movement.InstallAir == nil {
		return
	}
	heading := startBuildingBearing(u.X, u.Z, n.GoalX, n.GoalZ)
	ox, oz := bearingOffset(heading, numeric.Fixed(int64(airPatrolSetback)<<16))
	b.Movement.InstallAir(AirGoalRequest{
		Owner:  n.Owner,
		Node:   n,
		X:      n.GoalX - ox,
		Y:      n.GoalY,
		Z:      n.GoalZ - oz,
		Radius: airPatrolArrivalRadius,
	})
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
