// Package orders — the work family [04 R-ORD-01 §5]: `Capture`, `Reclaim`,
// `Resurrect`, `HelpBuild`, `RepairUnit`, `RepairUnitNoMove` and `SelfRepair`
// (the last is tabled with the trivial handlers, [04 R-ORD-01 §2], but its
// body is a work body and lives here with its siblings).
//
// This file owns the ORDER RECORDS only. `internal/construction` owns the work
// BODIES for factory build, mobile build and unit reclaim and drives those five
// records from its own per-unit step (handlerlessButDriven, pump.go); nothing
// here duplicates or touches them.
//
// Three shared facts govern every body below, all from [04 R-ORD-01 §1]:
//
//   - the record constructor zeroes the dynamic gate and the pending word, so a
//     fresh record reaches its handler on its very next pump visit with an empty
//     satisfied set — every phase 0 here is written for that first visit;
//   - "deadline n" always means "store current tick + n and OR bit 0 into the
//     dynamic gate", which is what makes the pump's expiry (step 1) wake the
//     record;
//   - the result codes are §3.3's: complete 5, abandon 8, cancel-all 7, wait 3,
//     re-arm 9, rotate 6, hold 2 or 4, advance 1, restart 0. Each return below
//     names which and why.
//
// Four capabilities the rows use do not exist in this build. They are recorded
// once here rather than at each of their forty-odd call sites:
//
// TODO(T25): the status emitter of [04 R-ORD-01 §1] (kind, optional text, the
// local-player/bit-28/bit-14 guard) has no counterpart. Placeholder: a caption
// carrying text is recorded on the queue's diagnostic sink, verbatim, so a
// failing order fails visibly (PLAN 18 gate 10); a caption with no text (kinds
// 5, 11, 16) is dropped, since the kind alone reaches presentation and there is
// no presentation to reach. The caption-clear helper's one-shot mask bit has no
// record field, so "caption clear" is the text emission alone.
//
// TODO(T25): the nanolathe spray of [04 R-ORD-01 §1] and its per-unit
// nanolathe-active stamp (tick + 150 / + 300 / + 900) are presentation state
// [03 §5.5] with no field on units.Unit and no emitter reachable from this
// package. Placeholder: neither is written. Nothing authoritative reads them
// [05 R-WORK-01 §8]: "nothing in the emission path is authoritative".
//
// TODO(T25): the four goal installers (point, annulus, rectangle, release) of
// [04 R-ORD-01 §1] do not exist, and internal/movement binds an arrival handle
// only for the move-family descriptor names, so nothing raises the movement
// outcome bits 0x20/0x40 into a work record. Placeholder: installWorkGoal below
// writes the record's goal triple and reproduces the one part of the installers
// that is record-local and observable — the release of the previous payload and
// the clearing of pending bits 0x20..0x200 [04 R-ORD-01 §0]. The rows' armed
// movement gates are still armed exactly as written, so a work record whose row
// waits on arrival waits until that seam exists; the arms that also carry a
// deadline (`RepairUnit` phase 1) keep moving on their own.
//
// TODO(T25): "refresh the builder interface" is a presentation call [07]; this
// package cannot make one and does not.
package orders

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Gate bits the work rows arm, named once [04 R-ORD-01 §0][04 R-ORD-01 §6].
const (
	gateDeadline      uint32 = 0x1  // the shared deadline setter's bit; the pump sets it on expiry
	gateCancelCurrent uint32 = 0x2  // the cancel-current notification [R-ORDER-02 §2]
	gateBuildStance   uint32 = 0x4  // the INBUILDSTANCE wait
	gateArrived       uint32 = 0x20 // the follower observed arrival
	gateNoRoute       uint32 = 0x40 // an empty route while not at the goal — "cannot get there"
	gateMoveOutcomes  uint32 = 0xE0 // arrival | no-route | payload-release
	// The two located interrupt bits are combat.go's pendTargetRemoved (0x8)
	// and pendTargetCloaked (0x10000) [04 R-ORD-01 §6]; the work rows arm them
	// alongside the movement outcomes.
	gateWorkApproach    uint32 = gateMoveOutcomes | pendTargetRemoved // 0xE8
	gateCaptureApproach uint32 = gateWorkApproach | pendTargetCloaked // 0x100E8
)

// Status kinds this family raises [04 R-ORD-01 §1]'s table.
const (
	statusOK       uint8 = 5  // `ok` — the caption clear's kind, no default text
	statusCant     uint8 = 7  // `cant` — every work rejection
	statusComplete uint8 = 8  // `unitcomplete`
	statusBuild    uint8 = 9  // `build`
	statusRepair   uint8 = 10 // `repair`
	statusWorking  uint8 = 11 // `working`, no default text
	statusCapture  uint8 = 16 // `capture`, no default text
)

// workStatus is the status emitter placeholder described in the file header.
// A kind with no text emits nothing here; a kind with text records it verbatim
// (including retail's trailing periods and its misspelling) so the failure is
// visible [04 R-ORD-01 §1].
func workStatus(u *units.Unit, kind uint8, text string) {
	if u == nil || text == "" {
		return
	}
	if q := QueueForUnit(u); q != nil {
		q.recordDiagnostic(fmt.Sprintf("orders: status %d %q", kind, text))
	}
}

// hasMover reports whether the unit owns a mover reference — the "mover
// required" clause every work phase 0 opens with. The allocator constructs a
// mover only for a bmcode-1 definition [04 R-FAC-02 §5], which is the same
// reading `Park`'s phase 0 uses.
func hasMover(u *units.Unit) bool {
	return u != nil && u.Def != nil && u.Def.BMCode
}

// isqrt64 is the floor of the square root of a non-negative value. Retail forms
// these magnitudes on the double-precision stack and truncates toward zero
// [05 R-WORK-01 §2]; for an exact integer radicand the two agree, and the
// integer form keeps authoritative state out of floating point (I2).
func isqrt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	r := int64(1)
	for r*r <= v {
		r <<= 1
	}
	x := int64(0)
	for b := r; b > 0; b >>= 1 {
		t := x + b
		if t*t <= v {
			x = t
		}
	}
	return x
}

// footprintPad is one end's half-footprint diagonal in whole world units:
// trunc(8 · hypot(footX, footZ)) [05 R-WORK-01 §2]. Eight is half of the
// sixteen world units a footprint cell spans, and 8·sqrt(n) is sqrt(64n).
func footprintPad(footX, footZ int32) int32 {
	r := int64(footX)*int64(footX) + int64(footZ)*int64(footZ)
	return int32(isqrt64(64 * r))
}

// inBuildRange is the reach test shared by `MobileBuild`, `RepairUnit`,
// `Capture` and the reclaim family [04 R-ORD-01 §1][05 R-WORK-01 §2]:
//
//	distWorld  = (int16)(trunc(hypot(dx, dz)) >> 16)   // the signed high word
//	builderPad = trunc( 8 · hypot(myFootX,  myFootZ))
//	targetPad  = trunc(-8 · hypot(itsFootX, itsFootZ))
//	inRange    = (distWorld - builderPad + targetPad) <= (uint16)builddistance
//
// Both pads subtract because the target's is formed with a negative eight, and
// the comparison is inclusive. The target end is passed by value so a
// construction site can substitute the product definition's footprint, which is
// what §2 says the mobile builder's approach does.
func inBuildRange(builder *units.Unit, targetX, targetZ numeric.Fixed, targetFootX, targetFootZ int32) bool {
	if builder == nil || builder.Def == nil {
		return false
	}
	dx := int64(builder.X) - int64(targetX)
	dz := int64(builder.Z) - int64(targetZ)
	distFixed := isqrt64(dx*dx + dz*dz)
	// The high word is read as a signed 16-bit quantity out of a 32-bit
	// register, not as a shift of the whole value: a separation of 32768 world
	// units or more reads negative [05 R-WORK-01 §2].
	distWorld := int32(int16(uint32(distFixed) >> 16))
	builderPad := footprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)
	targetPad := footprintPad(targetFootX, targetFootZ)
	return distWorld-builderPad-targetPad <= int32(uint16(builder.Def.BuildDistance))
}

// inBuildRangeOf is inBuildRange against a live unit target.
func inBuildRangeOf(builder, target *units.Unit) bool {
	if target == nil || target.Def == nil {
		return false
	}
	return inBuildRange(builder, target.X, target.Z, target.Def.FootprintX, target.Def.FootprintZ)
}

// installWorkGoal is the rectangle and annulus goals these rows install. The
// four installers of [04 R-ORD-01 §1] differ only in the shape of the payload
// they publish — a point with an arrival radius, an annulus with two radii, a
// rectangle from a footprint cell origin and size — and share every effect the
// record can observe: release the previous payload, skip the install entirely
// for an owner whose definition has `canfly`, and clear pending bits
// 0x20..0x200 last so the record cannot see a stale outcome. This build carries
// no payload at all (see the file header), so all four collapse onto that
// shared half, which combat.go's installPointGoal already implements; the
// rectangle's origin and size and the annulus's two radii have nowhere to go.
func installWorkGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed) {
	if n == nil {
		return
	}
	installPointGoal(u, n, x, y, z, 0)
}

// deadlineHold is the rows' "deadline n; hold": store current tick + n, OR gate
// bit 0 [04 R-ORD-01 §1], and return *hold* (2) so the walk continues without
// touching the phase [04 §3.3].
//
// WU-18-2 could not write the exact deadline: the primary walk passed no tick
// to a descriptor handler, so this returned the pump's own bounded wait
// (code 3) instead, which waited 30..44 ticks instead of n, cost one
// simulation draw the row does not have (I4), and ASSIGNED gate bit 0 over
// whatever the row had armed. WU-18-7 put the tick in the handler signature,
// so every row's deadline is now formed exactly as written.
func deadlineHold(n *Node, tick uint32, ticks int32) Code {
	if n == nil {
		return 2
	}
	n.Deadline = int32(tick + uint32(ticks))
	n.DynamicGate |= gateDeadline
	return 2
}

// inBuildStanceWait is the shared `INBUILDSTANCE` wait [04 R-ORD-01 §1]: it
// returns *advance* (1) once the unit's build-stance byte is set, and otherwise
// writes the dynamic gate to `extra | 0x4` and returns *hold* (2). The gate is
// assigned, not ORed, exactly as the section states.
func inBuildStanceWait(u *units.Unit, n *Node, extra uint32) Code {
	if u != nil && u.InBuildStance {
		return 1
	}
	if n != nil {
		n.DynamicGate = extra | gateBuildStance
	}
	return 2
}

// repairStep is the repair helper of [05 R-WORK-01 §3], instruction-exact:
//
//	if ((int32)def.maxdamage <= (int32)(int16)target.health) return notCommitted
//	healTerm     = trunc(1 + (maxdamage       · worker - 1) / buildtime)
//	resourceTerm = trunc(1 + (buildcostenergy · worker - 1) / buildtime)
//	each term clamped DOWN to exactly 1 whenever it is at least 1
//	if (!admitOneResourceEnergy(builder, resourceTerm)) return notCommitted
//	damagePacket(builder, target, healTerm, kind 10)
//
// The admission is the one-resource helper against the BUILDER's buckets, and
// the heal is the kind-10 early healing path [06 §9.1]. The first compare reads
// the target's health as a signed 16-bit quantity, which is the retail field
// width; a target already at or above full health is refused.
//
// The two terms are repairTerms below rather than economy.RepairResourceTerm.
// That helper predates §3 and disagrees with it twice, in the direction that
// matters: it clamps a term UP to one when the term is below one and leaves a
// large term alone, where §3's compare is `>= 1` on the already-integerised
// term ("clamp to exactly 1 whenever positive ... 0 and negative values survive
// unchanged"), and it returns 1 for a zero `buildtime` where §3 gives 0 ("only
// eax of the conversion is consumed, so buildtime = 0 yields terms of 0 rather
// than a large magnitude"). Under the old reading a workertime-300 builder
// heals ten points a visit instead of §3's "one health point and one energy
// unit per accepted repair call". The helper has no other caller; WU-18-2
// reports it for correction or retirement rather than editing a package it does
// not own.
//
// The builder is the unit billed and the target the unit healed — `SelfRepair`
// passes them the other way round, which is the whole of its difference
// [05 R-WORK-01 §3].
//
// TODO(T25): retail applies the heal as a kind-10 damage packet so it competes
// with the same tick's other packets in slot order [06 §5.1]. This build has no
// packet queue reachable from the order pump, so the heal is applied through
// the same clamp directly. Ordering against other packets is the only
// observable difference, and only when two events hit one unit in one tick.
// repairTerms forms the helper's two terms exactly as [05 R-WORK-01 §3] gives
// them: widen, multiply by the worker, subtract one, divide by the build time,
// add one, truncate — then replace the term by exactly one whenever it is at
// least one, leaving zero and negative terms unchanged. A zero build time makes
// both terms zero, so the visit requests no energy, is admitted whenever energy
// carry is non-positive, and applies a zero-magnitude heal [01 §7].
func repairTerms(maxDamage, buildCostEnergy, worker, buildTime int32) (healTerm, resourceTerm int32) {
	if buildTime == 0 {
		return 0, 0
	}
	healTerm = int32(1 + (int64(maxDamage)*int64(worker)-1)/int64(buildTime))
	resourceTerm = int32(1 + (int64(buildCostEnergy)*int64(worker)-1)/int64(buildTime))
	if healTerm >= 1 {
		healTerm = 1
	}
	if resourceTerm >= 1 {
		resourceTerm = 1
	}
	return healTerm, resourceTerm
}

// The queue is the order's own — the economy service is session-wide and is
// reached through the queue the record is being pumped from, while the handle
// billed is the builder's. `SelfRepair` needs that separation: its billed unit
// is the order's target, which owns a different queue.
func repairStep(q *Queue, builder, target *units.Unit, worker int32) bool {
	if builder == nil || target == nil || target.Def == nil {
		return false
	}
	def := target.Def
	if def.MaxDamage <= int32(int16(target.Health)) {
		return false
	}
	healTerm, resourceTerm := repairTerms(def.MaxDamage, def.BuildCostEnergy, worker, def.BuildTime)
	if q == nil || q.StockpileEconomy == nil {
		// TODO(T25): the queue carries the economy service only under its
		// stockpile name (QueueBinding, pump.go). With none bound there is no
		// bucket to bill, and the helper's own contract is that unadmitted work
		// commits nothing — the notCommitted arm, which is a traced path rather
		// than an invented one.
		return false
	}
	buckets := q.StockpileEconomy.UnitBuckets(builder.Handle)
	if buckets == nil {
		return false
	}
	// The one-resource helper admits when energy carry is non-positive; that
	// verdict is the helper's own [05 "One-resource admission"][05 R-ECO-01 §7].
	admitted := buckets[economy.Energy].Carry <= 0
	economy.AdmitOneResource(buckets, float32(resourceTerm))
	if !admitted {
		return false
	}
	maxHealth := def.MaxDamage
	if maxHealth <= 0 {
		maxHealth = target.MaxHealth
	}
	if healTerm < 0 {
		healTerm = 0
	}
	target.Health = combat.ApplyHealing(target.Health, maxHealth, uint16(healTerm))
	return true
}

// workerQuantum is `workertime / 30`, the quantum every work executor passes
// [05 R-WORK-01 §3][05 "Construction arithmetic"].
func workerQuantum(u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 0
	}
	return u.Def.WorkerTime / 30
}

// ---------------------------------------------------------------------------
// SelfRepair [04 R-ORD-01 §2, the `SelfRepair` row][05 R-WORK-01 §3]
// ---------------------------------------------------------------------------

// selfRepairHandler runs the patient-side repair order. The record lives
// on the unit being repaired and its target is the REPAIRER, so the helper is
// called with the arguments reversed relative to `RepairUnit`: the repairer's
// energy is billed and the patient is healed [05 R-WORK-01 §3].
//
// Row [04 R-ORD-01 §2]: target null -> status 7 `Repair aborted.`, abandon (8).
// Phase 0: the target's definition must have `builder` (else cancel-all 7); the
// target must be complete and activated -> release all slots, advance (1); else
// abandon (8). Phase 1: own health >= own maxdamage -> advance (1); else the
// repair step, deadline 1, gate |= 0x8, hold (2). Phase 2: status 10
// `Unit repaired`, complete (5). Other: cancel-all (7).
func selfRepairHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	repairer := lookupTarget(u, n.Target)
	if n.Target == 0 || repairer == nil {
		workStatus(u, statusCant, "Repair aborted.")
		return 8 // abandon: the repairer handle went dead while the patient waited
	}
	switch n.Phase {
	case 0:
		if repairer.Def == nil || !repairer.Def.Builder {
			return 7 // cancel-all
		}
		// TODO(question): [04 R-ORD-01 §2] puts both clauses on the target —
		// "target must be complete (remaining fraction 0.0) and activated (edge
		// bit 0)" — while [05 R-WORK-01 §3] splits them, requiring the
		// REPAIRER's remaining fraction to be zero and "the patient carries an
		// instance permission byte's low bit". The handler row is followed here
		// because it owns the body; a trace of phase 0's two reads would settle
		// whose fields they are [04 R-ORD-01 §2][05 R-WORK-01 §3].
		if repairer.Remaining != 0 || !repairer.Activated {
			return 8 // abandon
		}
		releaseSlot(u, slotAll) // "release all slots": k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		return 1
	case 1:
		// The advance test is UNSIGNED — "(unsigned)maxdamage <= (unsigned)health"
		// [05 R-WORK-01 §3] — so an overkilled unit's negative health reads as a
		// very large value and leaves the work state at once. The helper's own
		// first compare, inside repairStep, is the signed one.
		if u.Def != nil && uint32(u.Health) >= uint32(u.Def.MaxDamage) {
			return 1 // advance
		}
		repairStep(QueueForUnit(u), repairer, u, workerQuantum(repairer))
		n.DynamicGate |= pendTargetRemoved
		return deadlineHold(n, tick, 1)
	case 2:
		workStatus(u, statusRepair, "Unit repaired")
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// RepairUnit and RepairUnitNoMove [04 R-ORD-01 §5][05 R-WORK-01 §3]
// ---------------------------------------------------------------------------

// repairUnitHandler is the builder-side moving repair executor.
//
// Row: target null -> status 7 `Repairs unsuccessful.`, complete (5). The leash
// pre-check is `Attack_Chase`'s (the record's third parameter and the anchor
// pair); over the leash -> complete. A target whose mover mode is not grounded
// (not 1) -> the same caption, complete. Phase 0: mover, `builder`, target
// complete (else cancel-all); caption clear with `Repairing`; advance. Phase 1:
// satisfied 0x40 -> abandon; the reach test; out of reach -> rectangle goal on
// the target's footprint, deadline 30 + RNG(30), gate |= 0xE8, hold, with the
// phase left at 1 so the goal is re-issued on every wake; in reach -> release
// all slots, StartBuilding, advance. Phase 2: INBUILDSTANCE wait, extra 0x8.
// Phase 3: target at or above full health -> advance; target state bits 2-3 set
// -> StopBuilding, deadline 15, restart; else the repair step, deadline 1,
// gate |= 0x8, hold. Phase 4: status 10 `Unit repaired`, complete. Other:
// cancel-all.
func repairUnitHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil {
		workStatus(u, statusCant, "Repairs unsuccessful.")
		return 5 // complete — this row ends the order rather than abandoning it
	}
	if leashExceeded(u, n) {
		return 5 // complete
	}
	if target.Move.Mode&0x3 != 1 {
		workStatus(u, statusCant, "Repairs unsuccessful.")
		return 5 // complete
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.Builder || target.Remaining != 0 {
			return 7 // cancel-all
		}
		workStatus(u, statusOK, "Repairing") // the caption clear, with a state text
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		if !inBuildRangeOf(u, target) {
			installWorkGoal(u, n, target.X, target.Y, target.Z)
			// The row's one simulation draw [05 R-WORK-01 §8]'s census counts
			// for this executor (I4).
			wait := int32(30)
			if q := QueueForUnit(u); q != nil && q.simForJitter() != nil {
				wait += int32(q.randBelow30())
			}
			n.DynamicGate |= gateWorkApproach
			return deadlineHold(n, tick, wait) // the phase stays 1: the goal is re-issued on every wake
		}
		releaseSlot(u, slotAll) // "release all slots": k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, pendTargetRemoved)
	case 3:
		// Unsigned compare [05 R-WORK-01 §3]; see selfRepairHandler.
		if target.Def != nil && uint32(target.Health) >= uint32(target.Def.MaxDamage) {
			return 1 // advance
		}
		// TODO(question): the "target state-word bits 2-3 set" arm
		// ([04 R-ORD-01 §5], `(target.status & 0xc) != 0` in
		// [05 R-WORK-01 §6]) has no field in this build: units.Unit mirrors
		// only the low two status bits, as Move.Mode. A trace naming bits 2 and
		// 3 of that word would settle what they mirror; until then this arm —
		// StopBuilding, deadline 15, restart — is left unreachable rather than
		// invented.
		repairStep(QueueForUnit(u), u, target, workerQuantum(u))
		n.DynamicGate |= pendTargetRemoved
		return deadlineHold(n, tick, 1)
	case 4:
		workStatus(u, statusRepair, "Unit repaired")
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// repairUnitNoMoveHandler is the stationary repair executor: the same
// work visit with no approach phases and only the null-target entry guard
// [04 R-ORD-01 §5][05 R-WORK-01 §3].
//
// Row: target null -> status 7 `Repairs unsuccessful.`, complete. Phase 0:
// `builder` required (else cancel-all); target complete AND THIS UNIT activated
// -> release all slots, advance; else abandon. Phase 1: target at or above full
// health -> advance; target bits 2-3 set -> advance; else the repair step,
// deadline 1, gate |= 0x8, hold. Phase 2: status 10 `Unit repaired`, complete.
// Other: cancel-all.
func repairUnitNoMoveHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil {
		workStatus(u, statusCant, "Repairs unsuccessful.")
		return 5 // complete
	}
	switch n.Phase {
	case 0:
		if u.Def == nil || !u.Def.Builder {
			return 7 // cancel-all
		}
		if target.Remaining != 0 || !u.Activated {
			return 8 // abandon
		}
		releaseSlot(u, slotAll) // "release all slots": k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		return 1
	case 1:
		// Unsigned compare [05 R-WORK-01 §3]; see selfRepairHandler.
		if target.Def != nil && uint32(target.Health) >= uint32(target.Def.MaxDamage) {
			return 1 // advance
		}
		// The bits 2-3 arm of this row advances rather than restarting; it is
		// unreachable for the reason given in repairUnitHandler.
		repairStep(QueueForUnit(u), u, target, workerQuantum(u))
		n.DynamicGate |= pendTargetRemoved
		return deadlineHold(n, tick, 1)
	case 2:
		workStatus(u, statusRepair, "Unit repaired")
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// HelpBuild [04 R-ORD-01 §5][05 R-WORK-01 §2][05 R-WORK-01 §8]
// ---------------------------------------------------------------------------

// assistApproachHalf is the assist approach radius term
// `half = trunc(16 · sqrt(footX² + footZ + footZ)) / 2`, the halving signed
// [04 R-ORD-01 §5][05 R-WORK-01 §2]. The radicand really does square only the
// X term and add Z twice; §2 reproduces it as the instructions compute it and
// leaves whether that is a retail defect Unknown.
//
// TODO(question): the two sections disagree on WHOSE footprint the radicand
// reads. [04 R-ORD-01 §5] says the term is taken "from **my own** footprint";
// [05 R-WORK-01 §2] says it is "taken from the **target's** definition". The
// arithmetic is identical in both. The handler row is followed here, because
// §5 owns this body; a trace of the assist phase 0's two footprint loads would
// settle it [04 R-ORD-01 §5][05 R-WORK-01 §2].
func assistApproachHalf(footX, footZ int32) int32 {
	r := int64(footX)*int64(footX) + int64(footZ) + int64(footZ)
	return int32(isqrt64(256*r)) / 2 // 16·sqrt(r) = sqrt(256r); the /2 is signed
}

// helpBuildHandler is the build-assist executor.
//
// Row: target null -> status 7 `Construction terminated`, abandon (8). Phase 0:
// mover and `builder` required (else cancel-all); annulus goal at the target's
// position with outer `builddistance + half` and inner `half`; gate = 0xE8;
// advance. Phase 1: satisfied 0x40 -> status 7 `I can't get there`, abandon;
// target complete -> complete; release all slots, StartBuilding, advance.
// Phase 2: INBUILDSTANCE wait, extra 0xA. Phase 3: the work step; unfinished ->
// deadline 1, gate |= 0xA, hold; else advance. Phase 4: status 8
// `Building complete`; gate |= 0x2 — the bit that makes cleanup deliver the
// cancel-current wake back through this handler — then complete.
func helpBuildHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil {
		workStatus(u, statusCant, "Construction terminated")
		return 8 // abandon
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.Builder {
			return 7 // cancel-all
		}
		_ = assistApproachHalf(u.Def.FootprintX, u.Def.FootprintZ) // the annulus radii; see the header's goal-installer TODO(T25)
		installWorkGoal(u, n, target.X, target.Y, target.Z)
		n.DynamicGate = gateWorkApproach
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			workStatus(u, statusCant, "I can't get there")
			return 8 // abandon
		}
		if target.Remaining == 0 {
			return 5 // complete: nothing left to assist
		}
		releaseSlot(u, slotAll) // "release all slots": k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, gateCancelCurrent|pendTargetRemoved)
	case 3:
		// TODO(T25): the work step is the shared construction helper with a
		// quantum of workertime/30 [05 R-WORK-01 §1][05 "Construction
		// arithmetic"], whose body internal/construction owns
		// (ConstructionStep, its two-resource admission and its health gain).
		// internal/construction imports this package, so no call into it can be
		// made from here and no seam of the SetGetBuiltHandler shape exists for
		// it. Placeholder: no work is admitted and none is applied, which is
		// the helper's own rejected-work arm — "no query and no segment is
		// emitted when the two-resource construction admission rejects the work
		// step" [05 R-P0-06 §1] — so the row's unfinished branch runs. A
		// builder assisting therefore adds no progress until the seam exists.
		if target.Remaining != 0 {
			n.DynamicGate |= gateCancelCurrent | pendTargetRemoved
			return deadlineHold(n, tick, 1)
		}
		return 1 // advance
	case 4:
		workStatus(u, statusComplete, "Building complete")
		n.DynamicGate |= gateCancelCurrent
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Capture [04 R-ORD-01 §5][05 R-WORK-01 §6]
// ---------------------------------------------------------------------------

// captureBudget is the capture timer of [05 R-WORK-01 §6], instruction-exact:
//
//	base_f = 0.015·buildcostenergy + 0.2142857142857·buildcostmetal + 150.0
//	base   = trunc(base_f); if (base >= 1800) base = 1800     // UPPER clamp only
//	healthScaled = (uint32)((int16)health + maxdamage) · base) / (uint32)(2·maxdamage)
//	killsFactor  = (uint16)kills / 5                          // signed, truncating
//	timer        = ((killsFactor + 10) · healthScaled · 10) / 100
//
// §6 corrects an earlier reading that clamped `base` below at zero: there is no
// lower clamp, and the division that follows is UNSIGNED, so a negative
// authored cost produces an enormous timer rather than a small one. There is no
// cap on the experience factor. At full health the middle step is the identity,
// and a damaged target captures faster in proportion to
// (health + maxdamage) / (2 · maxdamage).
//
// The last step's arithmetic is what is implemented. §6's closing gloss reads
// "a fresh, un-veteran target's timer is `base × 10 / 100`, i.e. one tenth of
// `base`", which its own instruction listing contradicts: with `killsFactor`
// zero the expression is (0 + 10) · base · 10 / 100 = base, not base/10. The
// arithmetic is followed here and the disagreement is reported rather than
// settled by choosing — one tenth of `base` would capture a stock unit in
// about half a second.
//
// The three constants are retail's float32 literals and the truncation is the
// ordinary one (I3). docs/INVARIANTS.md I2 carries no row for this transient;
// it is reported by WU-18-2 as a row the table needs, not as a new use of
// floating point for authoritative state — the stored budget is the integer.
//
// A zero `maxdamage` divides by zero here exactly as the executable does; §6
// states no guard, and inventing one would be inventing behavior (I11).
func captureBudget(energyCost, metalCost, health, maxDamage, kills int32) int32 {
	baseF := float32(0.015)*float32(energyCost) + float32(0.2142857142857)*float32(metalCost) + float32(150.0)
	base := int32(baseF)
	if base >= 1800 {
		base = 1800
	}
	healthScaled := uint32((int32(int16(health))+maxDamage)*base) / uint32(2*maxDamage)
	killsFactor := int32(uint16(kills)) / 5
	return (killsFactor + 10) * int32(healthScaled) * 10 / 100
}

// captureHandler is the capture executor.
//
// Pre-check: target null or satisfied ∩ 0x10008 -> status 7 `Capture failed`,
// abandon (8). Phase 0: mover and `cancapture` (else cancel-all); a target
// definition that ALSO has `cancapture` -> status 7 `That unit cannot be
// captured`, abandon — the same bit gates both ends, so anything that can
// capture cannot be captured; an unfinished target -> status 7 `That unit is a
// cloud of vapor and cannot be captured`, abandon; caption clear with
// `Capturing`; the budget into p2; release all slots; rectangle goal on the
// target footprint; gate = 0x100E8; advance. Phase 1: 0x40 -> abandon with no
// caption; the reach test; in reach -> StartBuilding, advance; else restart (0).
// Phase 2: INBUILDSTANCE wait, extra 0x10008. Phase 3: status 11, advance.
// Phase 4: a moving target -> StopBuilding, deadline 30, restart; p1 < p2 ->
// p1 += 2, deadline 2, hold; else advance. Phase 5: transfer the target,
// status 16, complete. Other: cancel-all. No resource cost and no decay.
func captureHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil || satisfied&pendTargetGone != 0 {
		workStatus(u, statusCant, "Capture failed")
		return 8 // abandon
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.CanCapture {
			return 7 // cancel-all
		}
		if target.Def == nil || target.Def.CanCapture {
			workStatus(u, statusCant, "That unit cannot be captured")
			return 8 // abandon
		}
		if target.Remaining != 0 {
			// "A cloud of vapor" is a target still under construction
			// [05 R-WORK-01 §6], not a mid-death or busy one.
			workStatus(u, statusCant, "That unit is a cloud of vapor and cannot be captured")
			return 8 // abandon
		}
		workStatus(u, statusOK, "Capturing") // caption clear with a state text
		n.Param2 = uint32(captureBudget(target.Def.BuildCostEnergy, target.Def.BuildCostMetal, target.Health, target.Def.MaxDamage, target.Kills))
		releaseSlot(u, slotAll) // "release all slots": k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		installWorkGoal(u, n, target.X, target.Y, target.Z)
		n.DynamicGate = gateCaptureApproach
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon, with no caption
		}
		if !inBuildRangeOf(u, target) {
			return 0 // restart: phase 0 re-arms the goal and the emitter
		}
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, pendTargetGone)
	case 3:
		workStatus(u, statusWorking, "") // kind 11 has no default text
		return 1
	case 4:
		// The moving-target arm (target state bits 2-3) is unreachable for the
		// reason recorded in repairUnitHandler's phase 3.
		if int32(n.Param1) < int32(n.Param2) {
			// The completion test precedes the increment, so the number of
			// qualifying visits is ceil(timer/2) [05 R-WORK-01 §6].
			n.Param1 += 2
			return deadlineHold(n, tick, 2)
		}
		return 1 // advance
	case 5:
		// TODO(T25): the central ownership-transfer path is
		// construction.Service.TransferOwnership [05 "Capture"]; that package
		// imports this one, so the call cannot be made from here and no
		// injection seam of the SetGetBuiltHandler shape exists for it.
		// Placeholder: the record completes and raises its cue exactly as the
		// row says, and the target keeps its owner. Reported upward by WU-18-2
		// as the one seam this family cannot reach.
		workStatus(u, statusCapture, "")
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Reclaim (feature) [04 R-ORD-01 §5][05 R-WORK-01 §5]
// ---------------------------------------------------------------------------

// reclaimHandler is the feature-reclaim executor. Its countdown lives on
// the order record, is seeded from the feature definition's energy and metal
// pools, and is decremented by a fixed two per visit, so the reclaimer's
// `workertime` has no effect on how long a feature takes [05 R-WORK-01 §5].
//
// Row: every visit resolves the feature at the goal cell; none -> status 7
// `Reclamation failed`, abandon; a feature that is not reclaimable -> abandon
// silently. Phase 0: mover and `canreclamate` -> rectangle goal on the
// feature's footprint, gate = 0xE0, advance; else cancel-all. Phase 1:
// satisfied 0x40 -> abandon; p1 = trunc(15 + (metal + energy)/2); the spray
// target's Y is the terrain height plus RNG(featureHeight); StartBuilding;
// advance. Phase 2: INBUILDSTANCE wait, extra 0. Phases 3 and 4 share one body,
// with phase 3 emitting status 11 on every visit it stays in: deadline 2;
// p1 -= 2; p1 > 0 -> (p1 > 15 -> two spray segments) hold; p1 <= 0 -> advance.
// Phase 5: finish the reclaim, complete. Other: cancel-all.
//
// TODO(T25): the feature resolver of [05 R-ECO-02 §2] needs the plot grid, and
// the queue binding carries no terrain or feature service. Placeholder: the
// per-visit feature lookup is skipped rather than answered "none" — answering
// "none" would abandon every reclaim a player issues, a failure retail does not
// produce. Two consequences are recorded rather than invented: phase 1 cannot
// seed p1 from the feature's pools and cannot take the one simulation draw the
// row makes there (a draw-count divergence, I4), and phase 5 cannot credit the
// pools or remove the feature (economy.Service.CreditFeatureReclaim is the
// credit half and is reachable; the feature half is not).
func reclaimHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.CanReclamate {
			return 7 // cancel-all
		}
		installWorkGoal(u, n, n.GoalX, n.GoalY, n.GoalZ)
		n.DynamicGate = gateMoveOutcomes
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		// p1 = trunc(15 + (energy + metal)/2) from the feature definition; see
		// the resolver TODO(T25) above for why the pools are not readable here.
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, 0)
	case 3, 4:
		if n.Phase == 3 {
			workStatus(u, statusWorking, "") // kind 11, no text, on every visit it stays in phase 3
		}
		code := deadlineHold(n, tick, 2)
		work := int32(n.Param1) - 2
		n.Param1 = uint32(work)
		if work <= 0 {
			return 1 // advance
		}
		return code
	case 5:
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Resurrect [04 R-ORD-01 §5][05 R-WORK-01 §7]
// ---------------------------------------------------------------------------

// resurrectionDelay is `trunc(0.3 · buildtime / (workertime / 30))` with the
// inner division integer [04 R-ORD-01 §5][05 R-WORK-01 §7]. The 0.3 belongs to
// this state alone. A builder whose `workertime` is below thirty makes the
// inner quotient zero; retail's conversion of the resulting infinity yields a
// zero low word, which is the only word the caller consumes, so the delay is
// zero and the resurrection completes immediately with no spray [01 §7].
func resurrectionDelay(buildTime, workerTime int32) int32 {
	q := workerTime / 30
	if q == 0 {
		return 0 // the out-of-range conversion's zero low word [05 R-WORK-01 §7]
	}
	return int32(float64(buildTime) * 0.3 / float64(q))
}

// resurrectHandler is the resurrection executor. Unlike every other work
// order it reschedules ONE tick, not two, and its spray runs in the ordinary
// construction direction [05 R-WORK-01 §7].
//
// Row: phases 0-5 begin with `Reclaim`'s feature lookup; none -> status 7
// `Resurrection failed`, abandon; not reclaimable -> abandon. Phase 0: mover
// and `canresurrect` -> rectangle goal on the footprint, gate = 0xE0, advance;
// else cancel-all. Phase 1: 0x40 -> abandon; StartBuilding toward the same
// random-height centre as `Reclaim`; advance. Phase 2: INBUILDSTANCE wait,
// extra 0. Phase 3: the feature's name up to its first `_` resolves a unit
// definition; found -> p1 = its index, p2 = the delay, status 11, advance; not
// found -> status 7 `Ressurection failed` (retail's spelling), abandon.
// Phase 4: p2 -= 1; when it was nonzero -> deadline 1, hold; else advance.
// Phase 5: create the unit and bind it as the target; null -> status 7
// `Unable to create any more units`, deadline 300, hold; re-read the feature,
// gone -> abandon; transplant, remaining 0 and health 1; advance. Phase 6:
// status 8 `Resurrection complete`; resolve command code 8 (repair) against the
// new unit and spawn it at the head; complete. Other: cancel-all.
//
// The feature resolver TODO(T25) recorded on reclaimHandler applies here
// too, and takes phases 3 and 5 with it: the corpse name that resolves the unit
// definition and the transplant both read the feature record.
func resurrectHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	switch n.Phase {
	case 0:
		if !hasMover(u) || u.Def == nil || !u.Def.CanResurrect {
			return 7 // cancel-all
		}
		installWorkGoal(u, n, n.GoalX, n.GoalY, n.GoalZ)
		n.DynamicGate = gateMoveOutcomes
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, 0)
	case 3:
		// TODO(T25): the corpse name, its truncation at the first underscore and
		// the catalog lookup all read the feature record; see the resolver
		// TODO(T25) on reclaimHandler. Placeholder: the delay is computed
		// from the builder alone, which is the half of phase 3 that does not
		// need the feature, and the definition index in p1 is left as the
		// producer set it. The `Ressurection failed` arm belongs to a failed
		// lookup and is therefore unreachable, not invented.
		n.Param2 = uint32(resurrectionDelay(0, u.Def.WorkerTime))
		workStatus(u, statusWorking, "")
		return 1
	case 4:
		v := int32(n.Param2)
		n.Param2 = uint32(v - 1)
		if v == 0 {
			return 1 // advance
		}
		return deadlineHold(n, tick, 1)
	case 5:
		// TODO(T25): the allocation, the transplant and the feature removal are
		// construction.Service.Resurrect's [05 R-WORK-01 §7]; that package
		// imports this one, so the call cannot be made from here. Placeholder:
		// the record advances with no unit created, which keeps the phase order
		// and the terminal caption intact. Reported upward by WU-18-2.
		return 1
	case 6:
		workStatus(u, statusComplete, "Resurrection complete")
		// The successor repair order is command code 8 resolved against the new
		// unit and head-inserted [04 R-ORD-01 §5][04 §3.4]; with no unit created
		// there is nothing to resolve it against.
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// workHandlers pairs each descriptor with its handler in a fixed slice, not a
// map: registration order is source order (I1).
var workHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"Capture", captureHandler},
	{"HelpBuild", helpBuildHandler},
	{"Reclaim", reclaimHandler},
	{"RepairUnit", repairUnitHandler},
	{"RepairUnitNoMove", repairUnitNoMoveHandler},
	{"Resurrect", resurrectHandler},
	{"SelfRepair", selfRepairHandler},
}

// ensureWorkHandlers installs the work family onto the descriptor table. It is
// idempotent, assigns only where the descriptor's handler is still nil, and
// tolerates a table that has not been built yet — the shape ensureStopHandler
// establishes.
func ensureWorkHandlers() {
	if len(table) == 0 {
		return
	}
	for _, h := range workHandlers {
		id := Lookup(h.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = h.handler
		}
	}
}

func init() { ensureWorkHandlers() }
