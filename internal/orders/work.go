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
// Movement goal installation is routed through the session-owned binding. The
// Standalone order fixtures must provide the same composed movement service as
// production queues; the movement payload adapter retains exact node identity
// [04 R-ORD-01 §1][P0-00 B].
//
// TODO(T25): "refresh the builder interface" is a presentation call [07]; this
// package cannot make one and does not.
package orders

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
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

// workStatus sends the semantic status request to the session-owned
// presentation adapter. Ordinary command feedback is not a diagnostic: the
// committed event is later arbitrated by the caption/audio queue and appended
// to the shared message-line ring [04 R-ORD-01 §1][07 R-HUD-03 §14].
func workStatus(u *units.Unit, kind uint8, text string) {
	if u == nil {
		return
	}
	if q := QueueForUnit(u); q != nil {
		if b := q.Binding(); b != nil {
			if b.Presentation != nil && b.Presentation.Status != nil {
				_ = b.Presentation.Status(u, kind, text)
			}
		}
	}
}

// NotifyStatus is the exported form of workStatus, for the mobile-build row
// driven from internal/construction and the session's walk boundary [04
// R-ORD-01 §1].
func NotifyStatus(u *units.Unit, kind uint8, text string) {
	workStatus(u, kind, text)
}

// emitNanolathe asks the committed presentation adapter to publish one
// accepted work-step emitter. Geometry remains owned by the concrete session
// adapter; this package supplies only the acting unit, order identity and
// tick [03 §5.5][04 R-ORD-01 §1].
func emitNanolathe(u *units.Unit, n *Node, tick uint32) {
	if u == nil || n == nil {
		return
	}
	if q := QueueForUnit(u); q != nil {
		if b := q.Binding(); b != nil && b.Presentation != nil && b.Presentation.Nanolathe != nil {
			_ = b.Presentation.Nanolathe(u, n, tick)
		}
	}
}

// emitFeatureNanolathe publishes a feature-box work step only after the
// authoritative countdown has accepted it. The session adapter owns the
// six-word box and presentation event admission [03 §5.5][05 R-WORK-01 §8].
func emitFeatureNanolathe(u *units.Unit, n *Node, feature FeatureView, tick uint32) {
	if u == nil || n == nil {
		return
	}
	if q := QueueForUnit(u); q != nil {
		if b := q.Binding(); b != nil && b.Presentation != nil && b.Presentation.NanolatheFeature != nil {
			_ = b.Presentation.NanolatheFeature(u, n, feature, tick)
		}
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

// installWorkGoal selects the researched payload shape for each work row. The
// session-owned movement adapter is required for production work records.
func installWorkGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed) bool {
	return installWorkGoalWithRadius(u, n, x, y, z, 0)
}

func installWorkGoalWithRadius(u *units.Unit, n *Node, x, y, z numeric.Fixed, airRadius int32) bool {
	if n == nil {
		return false
	}
	if q := QueueOfUnit(u); q != nil {
		if b := q.Binding(); b != nil && b.Movement != nil {
			if u != nil && u.Def != nil && u.Def.CanFly {
				if b.Movement.InstallAir != nil {
					// Work rows install a point marker at the copied target
					// position. Target is retained as a separate request field for
					// follow markers, but these rows do not use that family.
					return b.Movement.InstallAir(AirGoalRequest{Owner: n.Owner, Node: n, X: x, Y: y, Z: z, Radius: airRadius})
				}
			} else {
				name := DescriptorFor(n.ID).Name
				switch name {
				case "HelpBuild":
					half := int32(0)
					if target := targetOf(u, n); target != nil && target.Def != nil {
						half = assistApproachHalf(target.Def.FootprintX, target.Def.FootprintZ)
					} else if u.Def != nil {
						half = assistApproachHalf(u.Def.FootprintX, u.Def.FootprintZ)
					}
					if b.Movement.InstallAnnulus != nil {
						return b.Movement.InstallAnnulus(AnnulusGoalRequest{Owner: n.Owner, Node: n, X: x, Y: y, Z: z, OuterRadius: u.Def.BuildDistance + half, InnerRadius: half})
					}
				case "RepairUnit":
					if target := targetOf(u, n); target != nil && target.Def != nil && b.Movement.InstallRectangle != nil {
						cellX := world.WorldToCell(target.X) - target.Def.FootprintX/2
						cellZ := world.WorldToCell(target.Z) - target.Def.FootprintZ/2
						return b.Movement.InstallRectangle(RectangleGoalRequest{Owner: n.Owner, Node: n, CellX: cellX, CellZ: cellZ, Width: target.Def.FootprintX, Depth: target.Def.FootprintZ})
					}
				}
				if b.Movement.InstallPoint != nil {
					return b.Movement.InstallPoint(PointGoalRequest{Owner: n.Owner, Node: n, X: x, Y: y, Z: z})
				}
			}
		}
	}
	return false
}

func boundAssist(q *Queue, builder *units.Unit, n *Node, tick uint32) (bool, bool) {
	if q == nil || n == nil {
		return false, false
	}
	if b := q.Binding(); b != nil && b.Work != nil && b.Work.Assist != nil {
		ok := b.Work.Assist(builder, n, tick)
		if ok {
			emitNanolathe(builder, n, tick)
		}
		return ok, true
	}
	return false, false
}

func boundRepair(q *Queue, builder, patient *units.Unit, n *Node, tick uint32) (bool, bool) {
	if q == nil || n == nil {
		return false, false
	}
	if b := q.Binding(); b != nil && b.Work != nil && b.Work.Repair != nil {
		ok := b.Work.Repair(builder, patient, n, tick)
		if ok {
			emitNanolathe(builder, n, tick)
		}
		return ok, true
	}
	return false, false
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
//
// TODO(T25): gate bit `0x4` has no producer in this build. [04 §3.1]'s gate-bit
// table names this wait and the wait/select family as the bit's consumers and
// names no producer for it; the COB engine-write port that carries the stance
// byte ([R-COB-03 §3]) writes the unit field and nothing else. The byte is set
// by the script's own `StartBuilding` body, which the emitter of [R-ORDER-02 §2]
// arranges as a DEFERRED callback, so it is never already set on the visit that
// first reaches this wait: with the gate armed and nothing able to satisfy it,
// every work record that reaches this helper would park at the head of its
// unit's queue permanently. Retail's stance byte does clear this wait, so a
// permanent park is not the behavior being cloned. Placeholder, the same shape
// airHandOff uses (vtolair.go): arm the shared deadline setter for one tick
// alongside the row's own gate, so the stance byte is re-polled on the next
// visit. The record keeps its place at the head and its phase, exactly as
// *hold* requires, and the added bit becomes inert the moment a producer for
// `0x4` exists.
// Decider: trace the INBUILDSTANCE engine-write port for a write of bit 2 into
// the unit's pending word.
func inBuildStanceWait(u *units.Unit, n *Node, extra uint32, tick uint32) Code {
	if u != nil && u.InBuildStance {
		return 1
	}
	if n != nil {
		n.DynamicGate = extra | gateBuildStance
		deadlineHold(n, tick, 1)
	}
	return 2
}

// workerQuantum is the construction quantum [05 R-WORK-01 §3]. Production
// handlers receive it through the session-owned Work adapter.
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
// The kind-10 packet is formed at combat's packet boundary. Packet ordering is
// still owned by the caller's normal combat window [06 §5.1][06 §9.1].
// repairTerms forms the helper's two terms exactly as [05 R-WORK-01 §3] gives
// them: widen, multiply by the worker, subtract one, divide by the build time,
// add one, truncate — then replace the term by exactly one whenever it is at
// least one, leaving zero and negative terms unchanged. A zero build time makes
// both terms zero, so the visit requests no energy, is admitted whenever energy
// carry is non-positive, and applies a zero-magnitude heal [01 §7].
func workerQuantum(u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 0
	}
	return u.Def.WorkerTime / 30
}

// The ordinary construction step is owned by internal/construction and reached
// through the session Work adapter [05 R-WORK-01 §1].
//
//	if (target.remaining == 0.0f)  return notCommitted   // exact float compare
//	if (worker >= 0.0f)            setWorkedThisTickFlag(target)
//	if (worker == 0.0f)            return notCommitted
//	new       = clamp(old - worker/buildtime, 0, 1)
//	delta     = old - new
//	energy    = buildcostenergy * delta
//	metal     = buildcostmetal  * delta
//	gain      = trunc(maxdamageF * old) - trunc(maxdamageF * new)
//	if (!admitTwoResource(BUILDER.subrecord, energy, metal)) return notCommitted
//	h = health + gain; if ((unsigned)h >= (unsigned)maxdamage) h = maxdamage
//	target.health = (int16)h ;  target.remaining = new
//
// Three properties of that body are the whole of build assistance, and none of
// them is a special case anywhere in retail:
//
//   - the quantum is the CALLER's `workertime/30` and the subrecord billed is
//     the CALLER's, so N builders visiting one target in one tick step the
//     fraction N times and each pays its own share of the drain;
//   - there is no owner check, no attach limit and no per-target rate cap: the
//     target carries a fraction, not a builder list;
//   - a shortfall is not this helper's business. Admission only records the
//     demand and gates on carry; the two-stage settlement of
//     [05 "Two-stage settlement algorithm"][05 R-ECO-01 §5] scales every
//     contributor's accepted work by the same per-resource ratio afterwards.
//
// `maxdamageF` is the definition word widened through a 64-bit integer load
// whose high word is zero, so a negative authored `maxdamage` reads as a large
// positive value [05 R-WORK-01 §1]; the maximum-health cap is UNSIGNED, so a
// `health + gain` that went negative is clamped UP to `maxdamage`.
//
// Malformed build times need no guard and get none [05 R-WORK-01 §1]: a zero
// `buildtime` makes the division an infinity, so the fraction goes to negative
// infinity and the lower clamp stores zero — the target finishes in one step
// and pays its whole remaining cost; a negative one drives the fraction the
// wrong way into the upper clamp. Neither faults, and Go's IEEE division
// reproduces both without a branch.
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
		// first compare in the construction repair helper is the signed one.
		if u.Def != nil && uint32(u.Health) >= uint32(u.Def.MaxDamage) {
			return 1 // advance
		}
		if _, bound := boundRepair(QueueForUnit(u), repairer, u, n, tick); !bound {
			return 7
			// The bound service owns admission; the order still re-arms exactly
			// as the ordinary work row does.
		}
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
	// The leash pre-check is `Attack_Chase`'s, which is combat.go's leashBroken
	// [R-STANCE-01 §4]. WU-18-8 folded this call over from resolve.go's second
	// leash test, which measured the same contract in 16.16 and abandoned early
	// on a diagonal; the retirement note stands at that helper's old site.
	if leashBroken(u, n) {
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
			if !installWorkGoal(u, n, target.X, target.Y, target.Z) {
				return 7
			}
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
		return inBuildStanceWait(u, n, pendTargetRemoved, tick)
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
		if _, bound := boundRepair(QueueForUnit(u), u, target, n, tick); !bound {
			return 7
		}
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
		if _, bound := boundRepair(QueueForUnit(u), u, target, n, tick); !bound {
			return 7
		}
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

// AssistApproachHalf is the exported form of the term above. internal/movement
// uses it to build the annulus payload that `HelpBuild` phase 0 installs —
// outer `builddistance + half`, inner `half` [04 R-ORD-01 §5].
func AssistApproachHalf(footX, footZ int32) int32 {
	return assistApproachHalf(footX, footZ)
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
		if !installWorkGoal(u, n, target.X, target.Y, target.Z) {
			return 7
		}
		if inBuildRangeOf(u, target) {
			// Already inside the annulus. Retail's route follower asks the
			// installed payload whether the unit has arrived on its very next
			// service and raises pending 0x20 straight away for a unit that is
			// already there [04 §10 "The follower's per-tick service"], so the
			// record reaches phase 1 without any motion. The bound movement
			// service publishes that result through the node's pending word; the
			// gate is therefore left clear and the pump cascades into phase 1 in
			// this same pass.
			return 1
		}
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
		return inBuildStanceWait(u, n, gateCancelCurrent|pendTargetRemoved, tick)
	case 3:
		// The construction service owns admission, the shared arithmetic, and
		// completion posture for the accepted step [05 R-WORK-01 §1].
		if _, bound := boundAssist(QueueForUnit(u), u, n, tick); !bound {
			return 7
		}
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
		if !installWorkGoal(u, n, target.X, target.Y, target.Z) {
			return 7
		}
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
		return inBuildStanceWait(u, n, pendTargetGone, tick)
	case 3:
		workStatus(u, statusWorking, "") // kind 11 has no default text
		return 1
	case 4:
		// The moving-target arm (target state bits 2-3) is unreachable for the
		// reason recorded in repairUnitHandler's phase 3.
		if int32(n.Param1) < int32(n.Param2) {
			// The completion test precedes the increment, so the number of
			// qualifying visits is ceil(timer/2) [05 R-WORK-01 §6].
			emitNanolathe(u, n, tick)
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

// queueEconomy is the concrete economy service behind the queue binding's
// narrow stockpile interface. The binding declares only the bucket accessor the
// stockpile handler needs; the feature-reclaim payout needs two more things
// that live on the same session-owned object — the terrain, which carries the
// feature grid [05 R-ECO-02 §2], and the unconditional production credit of
// [05 R-WORK-01 §5]. A binding built with a different implementation reports
// nothing and the executor abandons rather than half-completing.
func queueEconomy(q *Queue) *economy.Service {
	if q == nil {
		return nil
	}
	var svc *economy.Service
	if q != nil && q.Binding() != nil {
		svc, _ = q.Binding().Economy.(*economy.Service)
	}
	return svc
}

func queueTerrain(q *Queue) *world.Terrain {
	if svc := queueEconomy(q); svc != nil {
		return svc.Terrain
	}
	return nil
}

// featureAtGoal is the per-visit feature lookup every `Reclaim`, `VTOL_Reclaim`
// and `Resurrect` visit opens with [04 R-ORD-01 §5]: the record's goal position
// resolved through the feature grid to an anchor cell and a definition
// [05 R-ECO-02 §2].
func featureAtGoal(u *units.Unit, n *Node) (def *content.FeatureDef, cx, cz int, ok bool) {
	if u == nil || n == nil {
		return nil, 0, 0, false
	}
	t := queueTerrain(QueueForUnit(u))
	if t == nil {
		return nil, 0, 0, false
	}
	return features.FeatureAt(t, n.GoalX, n.GoalZ)
}

// featureViewAtGoal resolves the same anchored feature as featureAtGoal, then
// obtains its authored footprint and height through the composed world seam.
// Keeping both lookups on the same anchor prevents a presentation event from
// targeting a fringe cell after an authoritative transition [05 R-ECO-02 §2].
func featureViewAtGoal(u *units.Unit, n *Node) (FeatureView, bool) {
	if u == nil || n == nil {
		return FeatureView{}, false
	}
	q := QueueForUnit(u)
	if q == nil || q.Binding() == nil || q.Binding().World == nil || q.Binding().World.LookupFeature == nil {
		return FeatureView{}, false
	}
	_, cx, cz, ok := featureAtGoal(u, n)
	if !ok {
		return FeatureView{}, false
	}
	return q.Binding().LookupFeature(int32(cx), int32(cz))
}

// featureBoxCentre is the world centre of a feature's footprint rectangle —
// the point the rectangle goal and the spray target are built around
// [04 R-ORD-01 §5]. A footprint cell spans sixteen world units, so half a
// footprint is `foot * 16 * 65536 / 2` in 16.16.
func featureBoxCentre(cx, cz int, def *content.FeatureDef) (x, z numeric.Fixed) {
	footX, footZ := int32(1), int32(1)
	if def != nil {
		if def.FootprintX > 0 {
			footX = def.FootprintX
		}
		if def.FootprintZ > 0 {
			footZ = def.FootprintZ
		}
	}
	x = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
	z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
	return x, z
}

// featureWork is the reclaim countdown seed `trunc(k + (metal + energy) / 2)`
// [05 R-WORK-01 §5]. `k` is fifteen for the ground row and thirty for the air
// twin [04 R-ORD-01 §5, §7]; nothing else differs. Retail forms the sum,
// multiplies it by a stored -0.5f, subtracts that from k, and truncates the
// WHOLE expression toward zero (I3) — the halving is on the sum, and the
// truncation is not. A feature with empty pools still costs the fixed k.
//
// The equivalent integer form doubles the scale before dividing, so the single
// truncation lands in the same place: halving the pools first would round a
// negative authored pool the other way (`15 + (-3)/2` is 14, where the retail
// expression's 13.5 truncates to 13). Keeping it integer also keeps the
// countdown out of floating point, which I2 does not license here.
func featureWork(def *content.FeatureDef, k int32) int32 {
	if def == nil {
		return k
	}
	return (2*k + def.Metal + def.Energy) / 2 // Go's / truncates toward zero
}

// randBelow is the bounded simulation draw [01 §7.1]: a bound below two returns
// zero WITHOUT advancing the seed, which the stream helper already enforces.
// A queue with no simulation stream bound takes no draw at all — the fixtures
// that omit it are the ones with no determinism to preserve.
func (q *Queue) randBelow(bound uint32) uint32 {
	if q == nil || q.simForJitter() == nil {
		return 0
	}
	return q.simForJitter().Uint32n(bound)
}

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
// Correction (PT3-05). This body used to skip the per-visit feature lookup
// entirely, on the reading that the resolver "needs the plot grid, and the
// queue binding carries no terrain or feature service". The consequence was
// that a player could not reclaim anything: p1 was never seeded, so phase 3
// read a countdown of zero, fell straight through phases 4 and 5, and completed
// the order having credited nothing and removed nothing. The premise was wrong
// on both halves. The grid is reachable — the queue binding's economy service
// is the session's, and it carries the terrain for tidal strength already
// [05 "Tidal generation"] — and the transition is a grid operation, not this
// package's to invent: internal/features owns it as FeatureAt and
// ReclaimTransition, taking a terrain rather than a service receiver, and
// reconciles its own animation-instance map to the grid in the same tick's
// feature phase.
//
// The payout itself is [05 R-WORK-01 §5]'s: the definition's WHOLE energy and
// metal values are added to the builder's production accumulators, neither
// passing through an admission helper, and the feature is replaced by its
// `featurereclamate` successor or removed. It is a one-time completion event,
// not a per-tick drip.
func reclaimHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
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
		if !hasMover(u) || u.Def == nil || !u.Def.CanReclamate {
			return 7 // cancel-all
		}
		bx, bz := featureBoxCentre(cx, cz, def)
		if !installWorkGoal(u, n, bx, n.GoalY, bz) {
			return 7
		}
		if inBuildRange(u, bx, bz, def.FootprintX, def.FootprintZ) {
			// In reach the gate is left clear and the pump cascades into phase
			// 1 in this same pass: retail's follower raises arrival on its next
			// service for a unit already at its goal [04 §10 "The follower's
			// per-tick service"]. This build binds an arrival handle only for
			// the move-family descriptors (the file header's goal-installer
			// TODO(T25)), so arming 0xE0 here parked every reclaim a player
			// issued from inside nanolathe range on a bit nothing can raise —
			// half of PT3-05, and the same defect the assist row carried.
			return 1
		}
		// TODO(T25): out of reach the row installs its rectangle goal, arms
		// 0xE0 and advances, and phase 1 waits on arrival. Nothing in this
		// build approaches for a work descriptor (no arrival handle, and
		// internal/session activates movement only for the move-family names),
		// so advancing here would run the whole reclaim from wherever the
		// builder happens to stand. Placeholder: the record arms the row's gate
		// AND a plain thirty-tick re-poll — no random draw, so no stream
		// divergence (I4) — and HOLDS at phase 0, re-testing reach on every
		// wake. The order therefore never reclaims out of range and never
		// permanently jams the head; it starts the moment the builder is within
		// `builddistance` of the feature by any other means. Decider: bind an
		// arrival handle for the work descriptors, then restore the row's
		// advance.
		n.DynamicGate = gateMoveOutcomes
		n.MoveState = MoveEnRoute
		return deadlineHold(n, tick, 30)
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		n.Param1 = uint32(featureWork(def, 15))
		// The row's one simulation draw, bounded by the feature definition's
		// height byte, contributing only the vertical component of the spray
		// target [05 R-WORK-01 §5]. The spray is not emitted from this package
		// (the file header), but the draw is behavior: skipping it would shift
		// every later draw in the tick (I4).
		if q := QueueForUnit(u); q != nil {
			_ = q.randBelow(uint32(def.Height))
		}
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, 0, tick)
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
		if feature, ok := featureViewAtGoal(u, n); ok {
			emitFeatureNanolathe(u, n, feature, tick)
		}
		return code
	case 5:
		finishFeatureReclaim(u, cx, cz)
		return 5 // complete
	default:
		return 7 // cancel-all
	}
}

// finishFeatureReclaim is phase 5's payout, shared by the ground and air rows
// [05 R-WORK-01 §5]. The credit is unconditional — it lands in the production
// accumulators the settlement pass reads, with no admission helper between —
// and the grid transition is what removes the feature, so the two cannot come
// apart and pay twice.
//
// The order is the payout helper's: the pools are read and the grid rewritten
// by ReclaimTransition first, and only a transition that actually happened
// credits anything.
//
// The builder's OWNER is passed with the credit, not just its handle: step 4 of
// [05 R-WORK-01 §5] scales each addition for a computer player, and the gate is
// the builder's own player record — the slot must exist and its control byte
// must be 2 [05 R-ECO-01 §11]. The ledger holds the records, so the owner index
// is all it needs from here.
func finishFeatureReclaim(u *units.Unit, cx, cz int) {
	q := QueueForUnit(u)
	econ := queueEconomy(q)
	if econ == nil {
		return
	}
	metal, energy, ok := features.ReclaimTransition(econ.Terrain, cx, cz)
	if !ok {
		return
	}
	econ.CreditFeatureReclaim(u.Handle, u.Owner, metal, energy)
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
		if !installWorkGoal(u, n, n.GoalX, n.GoalY, n.GoalZ) {
			return 7
		}
		n.DynamicGate = gateMoveOutcomes
		return 1
	case 1:
		if satisfied&gateNoRoute != 0 {
			return 8 // abandon
		}
		EmitStartBuilding(u, n)
		return 1
	case 2:
		return inBuildStanceWait(u, n, 0, tick)
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
		if feature, ok := featureViewAtGoal(u, n); ok {
			emitFeatureNanolathe(u, n, feature, tick)
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
