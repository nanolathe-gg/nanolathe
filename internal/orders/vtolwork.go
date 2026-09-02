// Package orders — the VTOL work twins [04 R-ORD-01 §7]: `VTOL_HelpBuild`,
// `VTOL_RepairUnit`, `VTOL_Reclaim` and `VTOL_RepairPatrol`.
//
// The air forms of the work orders are separate handler bodies, not the ground
// bodies behind a flight flag. They share the vocabulary of [04 R-ORD-01 §1]
// with their ground twins in work.go — the deadline setter, the slot helpers,
// the status emitter, the goal installers — and diverge in ways no
// implementation could derive from the ground bodies: no `INBUILDSTANCE` wait
// anywhere, no `StopBuilding` on an out-of-reach arm, different reach tests,
// different work constants, fewer captions. Each divergence is named at its
// site.
//
// The fifth twin of §7, `VTOL_ReclaimUnit`, is deliberately absent: WU-17-14
// established that internal/construction drives it from its own per-unit step
// (handlerlessButDriven, pump.go) and the pump leaves its phase, gate and
// deadline alone. Registering a descriptor handler for it would write over a
// live state machine.
//
// Everything work.go's header records applies here unchanged and is not
// repeated per site: the status emitter, the nanolathe spray and its stamp, and
// "refresh the builder interface" remain presentation-facing details documented
// there. Two facts of this file
// are worth stating once:
//
//   - installWorkGoal's `canfly` arm IS the air side of [04 R-ORD-01 §1]: all
//     four installers "skip the install entirely — release only — when the
//     owner's definition has the `canfly` bit", so every twin below gets the
//     release-and-clear half for free and none of them writes a ground goal
//     payload. The air marker replacement is wired below through the adapter.
//
// Air marker installation is routed through the session-owned movement
// adapter. The movement package retains ownership of marker construction and
// release; this package supplies only the node identity and scalar goal data
// [04 R-AIR-01 §4][P0-00 B].
package orders

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Gate combinations these rows arm, beyond work.go's named set
// [04 R-ORD-01 §0][04 R-ORD-01 §7].
const (
	// gateWorkRetry is the `0xA` the build and repair work phases OR in: the
	// cancel-current notification plus the target-removed bit.
	gateWorkRetry = gateCancelCurrent | pendTargetRemoved
	// gatePatrolInterrupt is `VTOL_RepairPatrol`'s pre-check mask `0x48`: the
	// target-removed bit and the "cannot get there" bit.
	gatePatrolInterrupt = pendTargetRemoved | gateNoRoute
	// patrolChainMember is bit 15 of the record's static-mask COPY — the
	// runtime patrol-chain member flag, "set only here and read only by this
	// setup" [04 R-ORD-01 §4].
	patrolChainMember uint32 = 0x8000
)

// moverMode reads the committed mover mode's low two bits: 0 none, 1 grounded,
// 2 airborne [04 R-MOV-01 §8].
func moverMode(u *units.Unit) uint8 {
	if u == nil {
		return 0
	}
	return u.Move.Mode & 0x3
}

// health16 reads a unit's health as the 16-bit field retail stores it in,
// widened for an unsigned comparison against a definition word. [04 R-ORD-01 §7]
// specifies `VTOL_RepairUnit`'s health test as "unsigned compare of the 16-bit
// health against the definition word", so an overkilled unit's negative health
// reads as a very large value and leaves the work state at once.
func health16(u *units.Unit) uint32 {
	if u == nil {
		return 0
	}
	return uint32(uint16(u.Health))
}

// dropFromCarrier is the preamble's "when the unit is carried, drop it from its
// carrier" [04 R-ORD-01 §7]. It severs both ends of the link that
// internal/orders owns: the carried unit's carrier reference and the carrier's
// cargo list, in list order (I1).
//
// TODO(T25): the section routes this through the attach commit of
// [R-COB-03 §5] "with the third value 2 (the only engine sites that pass a
// nonzero third value)". This build's AttachmentState has no field for that
// third value and no attach-commit entry point that takes one, so the value has
// nowhere to go; what survives is the link severance itself, which is the half
// every later phase reads.
func dropFromCarrier(u *units.Unit) {
	if u == nil || u.Attachment.Carrier == 0 {
		return
	}
	carrier := lookupTarget(u, u.Attachment.Carrier)
	u.Attachment.Carrier = 0
	u.Attachment.AttachPiece = -1
	if carrier == nil {
		return
	}
	kept := make([]pool.Handle, 0, len(carrier.Attachment.Cargo))
	for _, h := range carrier.Attachment.Cargo {
		if h != u.Handle {
			kept = append(kept, h)
		}
	}
	carrier.Attachment.Cargo = kept
}

// airWorkPreamble is phase 0 of all five twins [04 R-ORD-01 §7]:
//
//	requires a live mover and `canfly` (else cancel-all), then: caption clear
//	with the handler's state text; release all three weapon slots; when the
//	unit is carried, drop it from its carrier; raise the activation edge; and,
//	ONLY when the mover is grounded (mode 1), set it airborne (mode 2), build
//	an air marker on the unit's own position with altitude offset
//	`cruisealt / 2` (signed halving), install it as the goal payload, and OR
//	`0xE0` into the gate. The return is *advance* whether or not the marker was
//	built.
//
// The activation raise is unconditional here: the `onoffable` test belongs to
// the `Activate` / `Deactivate` handlers, not to this site [04 R-ORD-01 §2].
// An already-airborne unit skips straight to phase 1 with no goal armed and is
// re-dispatched on its next pump visit, because the preamble left the gate
// empty and the pump's walk restarts from the head after an *advance*.
func airWorkPreamble(u *units.Unit, n *Node, stateText string) Code {
	if !hasMover(u) || u.Def == nil || !u.Def.CanFly {
		return 7 // cancel-all
	}
	workStatus(u, statusOK, stateText) // caption clear, with the row's state text
	releaseSlot(u, slotAll)            // k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
	dropFromCarrier(u)
	u.SetActivationEdge(true) // edge bit 0 [04 R-UNIT-06 §2]
	if moverMode(u) == 1 {
		u.Move.Mode = (u.Move.Mode &^ 0x3) | 2 // grounded -> airborne [04 R-MOV-01 §8]
		// The takeoff marker sits over the unit's own position at half its
		// cruise altitude; `cruisealt / 2` is a signed halving of the 16-bit
		// definition word. Only the release half of the install is reachable
		// (see the file header), so the offset has nowhere to go.
		_ = u.Def.CruiseAlt / 2
		if !installWorkGoal(u, n, u.X, u.Y+numeric.Fixed(int64(u.Def.CruiseAlt/2)<<16), u.Z) {
			return 7
		}
		n.DynamicGate |= gateMoveOutcomes
	}
	return 1 // advance, marker or no marker
}

// emitStartBuildingAbsolute is the `StartBuilding` emitter of [R-ORDER-02 §2]
// with the one difference [04 R-CB-01 §3] records: `VTOL_HelpBuild` is the
// single site in the image that passes the RAW bearing without subtracting the
// builder's own heading, so an air builder's script receives a world heading
// rather than a relative one. Everything else is the shared emitter's —
// arity 1, the 65536-per-circle domain, and the StopBuilding-pending flag.
//
// The bearing is taken against the live target rather than the record goal,
// because the air twin never writes a ground goal payload (the file header's
// `canfly` note) and §7 states the value as "the absolute bearing from the
// builder to the target".
func emitStartBuildingAbsolute(u *units.Unit, n *Node, target *units.Unit) {
	if u == nil || n == nil || target == nil {
		return
	}
	bearing := startBuildingBearing(u.X, u.Z, target.X, target.Z)
	arrangeDeferred(callbackBridgeFor(u), "StartBuilding", []int32{int32(bearing)})
	n.Flags |= FlagStopBuildingPending
}

// The repair admission `VTOL_RepairUnit` phase 0 and the repair-patrol scan
// share is `nanoReach` in resolve.go. [04 R-ORD-02 §7] establishes that it is
// ONE function — the command resolver's codes 1, 2 and 8 call the same one —
// so the copy that stood here (`repairAdmission` plus `repairWaterClause`) is
// gone rather than kept in step by hand. Its terms, the water clause and the
// unbound-binding arm are all documented at that function.

// ---------------------------------------------------------------------------
// VTOL_HelpBuild [04 R-ORD-01 §7]
// ---------------------------------------------------------------------------

// vtolHelpBuildHandler is the air build-assist executor.
//
// Row: pre-check target null or satisfied `0x8` -> status 7 `Construction
// terminated by hostile action`, refresh, abandon; satisfied `0x2` -> refresh,
// complete. Phase 0: the preamble with `Building`, plus the definition's
// builder-specific script slot. Phase 1: p3 = 0; air marker at the target with
// horizontal arrival radius `builddistance`; install; gate = 0xE0; advance.
// Phase 2: satisfied 0x40 -> abandon (no caption); target complete -> complete;
// else emit StartBuilding with the ABSOLUTE bearing, refresh, advance. Phase 3:
// every tick with `tick mod 150 == 0` rebuilds the orbit marker; then the work
// step; unfinished -> deadline 1, gate |= 0xA, hold; finished -> complete.
// Other: cancel-all.
//
// Three divergences from the ground `HelpBuild` in work.go:
//
//   - the caption census PLAN 17 recorded: the ground pair keeps a null target
//     handle (`Construction terminated`) and the interrupt bit separate, while
//     this row merges both under one caption naming hostile action;
//   - there is no `INBUILDSTANCE` phase between arrival and work, so the air
//     row's phases are shifted one below the ground row's;
//   - the finished arm completes straight out of the work phase: no phase 4, no
//     `Building complete` status, no `gate |= 0x2` on the way out, and no
//     nanolathe-active stamp.
func vtolHelpBuildHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil || satisfied&pendTargetRemoved != 0 {
		workStatus(u, statusCant, "Construction terminated by hostile action")
		return 8 // abandon
	}
	if satisfied&gateCancelCurrent != 0 {
		return 5 // complete: the cancel-current notification, refresh only
	}
	switch n.Phase {
	case 0:
		// TODO(question): [04 R-ORD-01 §7] adds "the definition's
		// builder-specific script slot must be present (else cancel-all)" to
		// this phase and does not say which slot that is — no other section
		// names a builder-specific script slot, and this build's UnitDef caches
		// no script-function indices at all. The clause is therefore not
		// evaluated: skipping an unevaluable EXTRA precondition admits records
		// retail would also admit, where a cancel-all on it would kill every
		// air assist. A trace naming that slot (which cached script function,
		// and the definition field holding it) settles it [04 R-ORD-01 §7].
		return airWorkPreamble(u, n, "Building")
	case 1:
		n.Param3 = 0
		// The marker's horizontal arrival radius is `builddistance` with the
		// radius-flag setter, so arrival is `dist < builddistance` in whole
		// units — strict, unlike the ground reach test's inclusive compare. See
		// the file header for why the radius has nowhere to go.
		_ = u.Def.BuildDistance
		if !installWorkGoalWithRadius(u, n, target.X, target.Y, target.Z, u.Def.BuildDistance) {
			return 7
		}
		if inBuildRangeOf(u, target) {
			// Already within reach: the ground twin's phase 0 records why the
			// approach gate is left clear in that case (work.go, PT3-04).
			return 1
		}
		n.DynamicGate = gateMoveOutcomes
		return 1
	case 2:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon, with no caption
		}
		if target.Remaining == 0 {
			return 5 // complete: nothing left to assist
		}
		emitStartBuildingAbsolute(u, n, target)
		return 1
	case 3:
		// TODO(T25): the 150-tick orbit-marker rebuild (bearing builder->target
		// plus 0xDB6E, radius `builddistance`, heading stored on the marker,
		// [04 §10.3]) is a marker-family call; see the file header.
		// The work step, quantum `workertime/30` [05 R-WORK-01 §1]. Corrected
		// with the ground twin (PT3-04): this arm used to admit no work at all,
		// which made an air builder's assistance a no-op.
		if ok, bound := boundAssist(QueueForUnit(u), u, n, tick); !bound {
			return 7
		} else if !ok {
			// Admission refusal leaves the order armed for the next visit.
		}
		if target.Remaining != 0 {
			n.DynamicGate |= gateWorkRetry
			return deadlineHold(n, tick, 1)
		}
		return 5 // complete — the air row has no phase 4 and no terminal caption
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// VTOL_RepairUnit [04 R-ORD-01 §7][05 R-WORK-01 §3]
// ---------------------------------------------------------------------------

// vtolRepairUnitHandler is the air repair executor.
//
// Row: target null -> status 7 `Repairs unsuccessful.`, ABANDON. Leash
// pre-check as `Attack_Chase`; over the leash -> complete. Target mover mode
// not grounded -> same caption, complete. Every visit then copies the target's
// position into the record goal. Phase 0: mover and `canfly`; the repair
// admission test fails -> status 7 `Repair mission failed`, abandon; preamble
// with `Repairing`. Phase 1: air marker at the goal with altitude offset the
// FULL `cruisealt`; install; gate = 0xE8; advance. Phase 2: satisfied 0x40 ->
// abandon; target mode not grounded -> abandon; target state bits 2-3 set ->
// deadline 15, restart; target health below `maxdamage` -> repair step, spray,
// deadline 1, gate |= 0x8, hold; else advance. Phase 3: status 10
// `Unit repaired`; complete. Other: cancel-all.
//
// Four divergences from the ground `RepairUnit` in work.go: a null target
// ABANDONS where the ground row completes; there is a `Repair mission failed`
// admission gate the ground row does not have; there is no reach test, no
// `StartBuilding` and no nanolathe stamp — an aircraft repairs from wherever
// its marker leaves it; and the mid-life arm waits 15 ticks without emitting
// `StopBuilding`, because none was ever emitted.
func vtolRepairUnitHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil {
		workStatus(u, statusCant, "Repairs unsuccessful.")
		return 8 // abandon — the ground twin completes here [04 R-ORD-01 §5]
	}
	// leashBroken is the whole-unit form [R-STANCE-01 §4] that [04 R-ORD-01 §3]
	// spells out for `Attack_Chase`, which this row cites.
	if leashBroken(u, n) {
		return 5 // complete
	}
	if moverMode(target) != 1 {
		workStatus(u, statusCant, "Repairs unsuccessful.")
		return 5 // complete
	}
	n.GoalX, n.GoalY, n.GoalZ = target.X, target.Y, target.Z // every visit
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.CanFly {
			return 7 // cancel-all
		}
		if !nanoReach(u, target) {
			workStatus(u, statusCant, "Repair mission failed")
			return 8 // abandon
		}
		return airWorkPreamble(u, n, "Repairing")
	case 1:
		_ = u.Def.CruiseAlt // the marker's full-cruise-altitude offset; see the file header
		if !installWorkGoal(u, n, n.GoalX, n.GoalY+numeric.Fixed(int64(u.Def.CruiseAlt)<<16), n.GoalZ) {
			return 7
		}
		n.DynamicGate = gateWorkApproach // 0xE8
		return 1
	case 2:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		if moverMode(target) != 1 {
			return 8 // abandon
		}
		// The "target state bits 2-3 set -> deadline 15, restart" arm is
		// unreachable for the reason work.go's repairUnitHandler records: this
		// build mirrors only the low two status bits, as Move.Mode, so bits 2
		// and 3 have no field. Left unreachable rather than invented.
		if health16(target) < uint32(target.Def.MaxDamage) {
			if _, bound := boundRepair(QueueForUnit(u), u, target, n, tick); !bound {
				return 7
			}
			n.DynamicGate |= pendTargetRemoved
			return deadlineHold(n, tick, 1)
		}
		return 1 // advance
	case 3:
		workStatus(u, statusRepair, "Unit repaired")
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// VTOL_Reclaim [04 R-ORD-01 §7][05 R-WORK-01 §5]
// ---------------------------------------------------------------------------

// vtolReclaimHandler is the air feature-reclaim executor.
//
// Row: every visit resolves the feature at the goal (none -> status 7
// `Reclamation failed`, abandon; not reclaimable -> abandon). Phase 0: preamble
// with `Reclaiming`, plus `canreclamate`. Phase 1: air marker on the feature's
// goal position with no altitude or radius setter; install; gate = 0xE0;
// advance. Phase 2: satisfied 0x40 -> abandon; p1 = trunc(30 + (metal +
// energy)/2); status 11; advance. Phase 3: deadline 2; p1 -= 2; p1 > 0 -> stamp
// (p1 > 30 -> two spray segments) hold; p1 <= 0 -> advance. Phase 4: finish the
// reclaim; complete. Other: cancel-all.
//
// Four divergences from the ground `Reclaim` in work.go: the seed constant is
// 30 where the ground row uses 15, so an aircraft takes fifteen more ticks per
// feature and its two-segment spray gate moves to `p1 > 30` with it; the walk
// target's Y carries NO random draw, where the ground row draws
// `RNG(featureHeightByte)` — one fewer simulation draw per air reclaim (I4);
// there is no `StartBuilding` and no `INBUILDSTANCE` phase; and status 11 is
// emitted once, in the phase before the work, rather than on every visit the
// work phase stays in.
func vtolReclaimHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	// Correction (PT3-05), with the ground twin: the per-visit feature lookup
	// used to be skipped entirely, so phase 2's seed stayed zero and phase 4's
	// payout was empty — an air reclaim removed nothing and paid nothing. The
	// resolver is internal/features' FeatureAt over the terrain the queue's
	// economy service carries; work.go's reclaimHandler records why that is
	// reachable and why the earlier reading was wrong.
	def, cx, cz, found := featureAtGoal(u, n)
	if !found {
		workStatus(u, statusCant, "Reclamation failed")
		return 8 // abandon
	}
	if !def.Reclaimable {
		return 8 // abandon, silently
	}
	switch n.Phase {
	case 0:
		if u.Def == nil || !u.Def.CanReclamate {
			return 7 // cancel-all
		}
		return airWorkPreamble(u, n, "Reclaiming")
	case 1:
		// No altitude and no radius setter: arrival is the air marker family's
		// default `dist <= 0.5` world units at the goal's own Y
		// [04 R-AIR-01 §4]. See the file header.
		bx, bz := featureBoxCentre(cx, cz, def)
		if !installWorkGoal(u, n, bx, n.GoalY, bz) {
			return 7
		}
		if inBuildRange(u, bx, bz, def.FootprintX, def.FootprintZ) {
			// Already within reach: the ground twin's phase 0 records why the
			// approach gate is left clear in that case (work.go, PT3-05).
			return 1
		}
		// Out of reach: the ground twin's phase 0 records the placeholder and
		// its decider. The record holds at this phase, re-testing reach, rather
		// than advancing into a work phase that would reclaim from anywhere.
		n.DynamicGate = gateMoveOutcomes
		return deadlineHold(n, tick, 30)
	case 2:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		// p1 = trunc(30 + (energy + metal)/2) from the feature definition —
		// THIRTY, not the ground row's fifteen [04 R-ORD-01 §7]. The air twin
		// takes NO height draw here: the walk target's Y carries none, which is
		// one fewer simulation draw per air reclaim (I4).
		n.Param1 = uint32(featureWork(def, 30))
		workStatus(u, statusWorking, "") // kind 11 has no default text
		return 1
	case 3:
		code := deadlineHold(n, tick, 2)
		work := int32(n.Param1) - 2
		n.Param1 = uint32(work)
		if work <= 0 {
			return 1 // advance
		}
		// "p1 > 0 → stamp `tick + 300`" — the air feature-reclaim twin keeps
		// the ground row's nanolathe-active stamp, unlike `VTOL_RepairUnit`
		// and `VTOL_ReclaimUnit`, which emit none [04 R-ORD-01 §7].
		stampNanolatheActive(u, tick, nanolatheStampBuild)
		// work > 30 draws the spray twice, where the ground row's gate is 15.
		return code
	case 4:
		finishFeatureReclaim(u, cx, cz)
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// VTOL_RepairPatrol [04 R-ORD-01 §7][04 R-ORD-01 §4]
// ---------------------------------------------------------------------------

// patrolChainSetup is [04 R-ORD-01 §4]'s patrol-chain setup, shared by `Patrol`
// and both repair patrols: walk the front segment for a record whose
// static-mask COPY carries bit 15; when none does, allocate a record of this
// same descriptor with goal = the unit's current position and append it at the
// tail (the return-to-start waypoint); in every case set bit 15 on this record.
func patrolChainSetup(u *units.Unit, n *Node) {
	q := QueueOfUnit(u)
	if q == nil || n == nil {
		return
	}
	segment := q.Primary()
	if n.StaticGate&0x40000 != 0 {
		segment = q.Secondary()
	}
	found := false
	for _, rec := range segment {
		if rec.StaticGate&patrolChainMember != 0 {
			found = true
			break
		}
	}
	if !found {
		q.appendTail(n.ID, Node{
			Owner:        u.Handle,
			GoalX:        u.X,
			GoalY:        u.Y,
			GoalZ:        u.Z,
			CreationTick: n.CreationTick,
		})
	}
	n.StaticGate |= patrolChainMember
}

// vtolRepairPatrolHandler is the air repair patrol.
//
// Row: pre-check satisfied ∩ 0x48 -> deadline 30, restart. Phase 0: mover,
// `canfly`, and the `canreclamate` mirror bit; with a target, goal = its
// position; the patrol-chain setup; preamble with `Patrolling`; advance.
// Phase 1, in order: (1) satisfied ∩ 0xE0 -> rotate; (2) air marker at the goal
// with the full `cruisealt`, install, deadline 45, gate |= 0xE0; (3) a
// low-health pad seek; (4) an energy-gated repair-candidate scan; (5) a feature
// pairing over a FIXED ±120-unit square sampled every 48 units, then the ground
// twin's decision tree with every spawn a `VTOL_Reclaim`; none -> hold. Other
// phase: cancel-all.
//
// The divergences from the ground `RepairPatrol` are steps 3 to 5: the pad seek
// exists only here, step 4 uses the shared scanner-owner-to-candidate-owner
// diplomacy test once (the ground twin repeats that check) and its
// unfinished-target arm spawns `VTOL_HelpBuild` rather than issuing code 8, and
// step 5's search radius is a fixed ±120 world units where the ground twin
// passes `sightdistance`. Draws occur only at reached sites: the pad pick, the
// unit pick, and the conditional feature tournaments.
func vtolRepairPatrolHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	if satisfied&gatePatrolInterrupt != 0 {
		armDeadline(n, tick, 30)
		return 0 // restart
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.CanFly || !u.Def.CanReclamate {
			return 7 // cancel-all
		}
		if target := lookupTarget(u, n.Target); target != nil {
			n.GoalX, n.GoalY, n.GoalZ = target.X, target.Y, target.Z
		}
		patrolChainSetup(u, n)
		return airWorkPreamble(u, n, "Patrolling")
	case 1:
		if satisfied&gateMoveOutcomes != 0 {
			return 6 // rotate: the leg is done, the next waypoint takes the head
		}
		_ = u.Def.CruiseAlt // the marker's full-cruise-altitude offset; see the file header
		if !installWorkGoal(u, n, n.GoalX, n.GoalY, n.GoalZ) {
			return 7
		}
		armDeadline(n, tick, 45)
		n.DynamicGate |= gateMoveOutcomes
		// The low-health pad seek [04 R-ORD-01 §7]: the one health expression
		// of [04 R-AIR-01 §11] over the target registry's third list at its
		// last rebuild, filtered by combat.ScanAirBaseList.
		if combat.AirBelowThreeQuarters(u) {
			pads := airBasePads(u)
			if pad := pickCandidate(u, pads); pad != nil {
				releaseGoalPayload(u, n)
				if spawnPatrolLanding(u, pad, tick) {
					n.DynamicGate = 0
					return 0 // restart with the landing record at the head
				}
			}
		}
		if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) {
			candidates := scanRepairCandidates(u, u.Def.SightDistance)
			if target := pickRepairCandidate(u, candidates); target != nil && spawnPatrolRepair(u, target, tick) {
				n.DynamicGate = 0
				if target.Remaining != 0 {
					return 3 // unfinished targets explicitly spawn VTOL_HelpBuild
				}
				return 6 // accepted complete target repair rotates
			}
		}
		if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) && resourceAtLeastTwenty(resources.Stock[0], resources.Capacity[0]) {
			return 2
		}
		if feature, ok := chooseReclaimFeature(u, 240); ok && spawnPatrolReclaim(u, feature, true, tick) {
			n.DynamicGate = 0
			return 3 // wait while the spawned VTOL reclaim runs at the head
		}
		return 2 // no repair/reclaim candidate
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// vtolWorkHandlers pairs each descriptor with its handler in a fixed slice, not
// a map: registration order is source order (I1).
//
// `VTOL_ReclaimUnit` is absent by design (see the file header).
var vtolWorkHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"VTOL_HelpBuild", vtolHelpBuildHandler},
	{"VTOL_Reclaim", vtolReclaimHandler},
	{"VTOL_RepairPatrol", vtolRepairPatrolHandler},
	{"VTOL_RepairUnit", vtolRepairUnitHandler},
}

// ensureVTOLWorkHandlers installs the VTOL work twins onto the descriptor
// table. It is idempotent, assigns only where the descriptor's handler is still
// nil, and tolerates a table that has not been built yet — the shape
// ensureStopHandler establishes.
func ensureVTOLWorkHandlers() {
	if len(table) == 0 {
		return
	}
	for _, h := range vtolWorkHandlers {
		id := Lookup(h.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = h.handler
		}
	}
}

func init() { ensureVTOLWorkHandlers() }
