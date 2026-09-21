// The attack-chase and guard state machines [04 §3.5][04 R-ORD-01 §3]
// [04 R-ORD-01 §8][04 R-UNIT-06 §1], and the air twins `VTOL_Follow` and
// `VTOL_SeekGuard` [04 R-ORD-02 §3]. Moved out of resolve.go by CL-5, which
// split that file into command resolution and the handlers it resolves to; the
// code is unchanged.

package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ---------------------------------------------------------------------------
// Attack-chase state machine [04 §3.5]
// ---------------------------------------------------------------------------

// Retired 2026-08-31: `chaseAbandonMask` (0x01), `chaseDisengageMask` (0x06)
// and `stubStandoffWorld` (64) stood here, each carrying an open-question marker
// saying its value was "not located". None of the three exists in retail.
// [04 R-ORD-01 §3] gives the handler's real pre-checks — pending `0x800`, a
// null target, pending `0x10008`, and the leash — and the standoff is the
// slot's engagement distance, which [06 R-WPN-05 §1] closes as the weapon's
// authored `range`. The invented masks additionally aliased real bits: `0x01`
// is the deadline bit and `0x02`/`0x04` are movement bits, so an ordinary
// deadline expiry or a path verdict completed the order outright.

// Retired (WU-18-8): `leashExceeded` stood here. It was this package's second
// pursuit-leash test, cited to the same contract as combat.go's `leashBroken`
// and disagreeing with it below one world unit, so which one an order used
// decided whether it abandoned. What it did: it promoted the record's 16-bit
// anchor pair to 16.16 (`anchor * 65536`), subtracted it from the unit's
// FRACTIONAL 16.16 position, promoted the leash the same way, and compared
// `dx² + dz² >= leash²` entirely in 16.16 — carrying two open-question markers
// that said the units of the anchor and of the leash were not established.
//
// [R-STANCE-01 §4] settles both questions and the arithmetic with them: the
// deltas are taken between WHOLE world units on both sides (the unit's
// position's high half against the sign-extended 16-bit anchor), the distance
// is `trunc(hypot(dx, dz))` — truncated toward zero before the compare — and
// the test is the inclusive `leash <= d`. Truncating the distance is what the
// retired form got wrong: keeping the fractional parts inside the hypot
// abandons early on any diagonal. With an anchor at the origin and a leash of
// 5, a unit at (3.9, 3.9) whole units is at `trunc(hypot(3, 3)) = 4` by the
// contract and continues; the 16.16 form measured 5.51 and abandoned. The
// contract's form is combat.go's `leashBroken`, which is now the package's only
// leash test — used by `Attack_Chase` (resolve.go), the ground `RepairUnit`
// (work.go), the air executors, and `VTOL_RepairUnit` (vtolwork.go).

// Retired 2026-09-01 (WU-19-6): `setBandedGoalAroundWard` stood here. It wrote
// the record's goal to `ward + (standoff, 0, 0)` — a due-east point at a
// standoff its two callers took from p1 with a fallback of 20 world units —
// under an open-question marker saying the banded geometry was not located. It is
// located. [04 R-ORD-01 §8] gives the whole closure: the radius is computed by
// the handler from the two footprints and never read from the issuer, the
// direction is one RNG draw taken once at admit, the record's goal triple
// holds an OFFSET rather than a position, and the payload is a point goal —
// never an annulus and never a rectangle. The fallback 20 has no retail
// counterpart at all: "a guard record never carries a caller-supplied radius,
// so 'the radius when p1 is zero' is not a case retail has". Both callers are
// below; nothing replaces the literal.

// guardFollowRadius is `p1` of [04 R-ORD-01 §8 point 1], in whole world units:
//
//	s  = FootPrintX(me) + FootPrintX(ward) + 2      (whole cells)
//	p1 = s · 16                                     (whole world units)
//
// Both terms are the X word of the unit's copied footprint SIZE pair — the
// same word `Park` and the transport size gate read [04 R-ORD-01 §1] — so a
// Z-asymmetric footprint contributes only its X size. The handler writes p1
// unconditionally at admit; there is no issuer input and no zero case.
func guardFollowRadius(me, ward *units.Unit) int32 {
	return (footprintXOf(me) + footprintXOf(ward) + 2) * 16
}

func footprintXOf(u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 0
	}
	return u.Def.FootprintX
}

// GuardFollowPoint is the world point and arrival radius a `Follow_Ground`
// record's follow-maintenance leg installs [04 R-ORD-01 §8 point 3]: the
// ward's position plus the record's stored anchor offset, with
//
//	radius = p1 / 2 = (FootPrintX(me) + FootPrintX(ward) + 2) · 8
//
// The division is signed and toward zero (I3); p1 is 16·s and therefore even
// and positive, so it is exact. The Y sum is formed here and ignored by the
// installer, which takes X and Z only.
//
// It is exported because the movement side derives the same goal for a record
// whose payload is not currently bound (internal/movement/goals.go), and the
// two must not carry separate arithmetic.
func GuardFollowPoint(n *Node, wardX, wardY, wardZ numeric.Fixed) (x, y, z numeric.Fixed, radius int32) {
	if n == nil {
		return wardX, wardY, wardZ, 0
	}
	return wardX + n.GoalX, wardY + n.GoalY, wardZ + n.GoalZ, int32(n.Param1) / 2
}

// chaseVerticalJump is the substate 1-4 test of [04 R-ORD-01 §3]: the strafe
// arm jumps to substate 6 when the two units are separated by MORE than eight
// world units on Y. The compare is on the raw 16.16 difference against eight
// world units, so it is strict and exact, not a whole-unit truncation.
const chaseVerticalJump int64 = 8 << 16

// canEngageSlot is the shot-admission gate used before `Attack_Chase` binds
// a slot [04 R-ORD-01 §3] and before a guard retains a target
// [04 R-UNIT-06 §1]. It routes to the combat owner
// through the queue binding; a queue with no weapon adapter, or an adapter that
// does not supply the gate, refuses — which sends the handler down its own
// established "gate failed" arm rather than binding a slot the weapon layer
// would then refuse to fire.
func canEngageSlot(u *units.Unit, target pool.Handle, slot int) bool {
	b := bindingOfUnit(u)
	if b == nil || b.Weapons == nil || b.Weapons.CanEngage == nil {
		return false
	}
	return b.Weapons.CanEngage(u, target, slot)
}

// attackChaseHandler is the pursuing attack [04 R-ORD-01 §3].
//
//	Pre-checks, in order: satisfied 0x800 → complete; target null → complete;
//	satisfied ∩ 0x10008 → complete; the leash of [R-STANCE-01 §4] → complete.
//	Phase 0: requires a mover reference, no `canfly`, and state bit 31 (else
//	cancel-all); caption clear; goal = own position; p2 = 0; if p1 is 0 take
//	the default slot pick; advance. Phase 1: release the payload; satisfied ∩
//	0x3000 → advance; the shot-admission gate for slot p1 fails → advance; else
//	release slots 0 and 2, bind slot p1 to the target, gate = 0x13808; hold.
//	Phase 2 (maneuver): see below. Phase 3: satisfied ∩ 0x40E0 → phase = 1,
//	return 4; else if the shot gate passes: release slots 0 and 2, bind slot p1,
//	gate = 0x148E8, deadline 30, hold; else inhibit all, gate = 0x100E8,
//	deadline 30, hold. Other phase: cancel-all.
//
// Rewritten 2026-08-31. What stood here was written against [04 §3.5]'s prose
// summary and three invented constants, and [04 R-ORD-01 §3] carries an
// explicit correction to that summary. The differences that mattered in play:
// the standoff was a fixed 64 world units rather than the weapon's range, so an
// ordered unit orbited far outside its own reach and never fired; no phase ever
// bound a weapon slot at all (phases 1 and 3 were bare open-question advances);
// the goals were written straight into the record's goal fields instead of
// through the payload installers, so the mover was steered by whatever the
// movement side's own per-order fallback invented; and the pre-check masks
// aliased the deadline and movement bits, completing the order on an ordinary
// path verdict.
func attackChaseHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	// Pre-checks in the order [04 R-ORD-01 §3] fixes. 0x800 is the disengage
	// bit; 0x10008 is the target-removed/target-cloaked pair of §6.
	if satisfied&0x800 != 0 {
		return Code(5) // *complete*
	}
	if n.Target == 0 {
		return Code(5) // *complete*
	}
	if satisfied&pendTargetGone != 0 {
		return Code(5) // *complete*
	}
	// The leash is tested before the phase switch, on every dispatch, and only
	// when it is non-zero — leashBroken carries that guard itself
	// [R-STANCE-01 §4].
	if leashBroken(u, n) {
		return Code(5) // at or beyond the leash completes; the compare is inclusive
	}
	switch n.Phase {
	case 0:
		// "requires a mover reference, no canfly, and state-word bit 31".
		// hasLiveMover is this package's mover-reference test; bit 31 is the
		// armed bit, set when the definition resolved a weapon [R-ORD-01 §3].
		if !hasLiveMover(u) || u == nil || (u.Def != nil && u.Def.CanFly) || u.Flags&units.ArmedStatus == 0 {
			return Code(7) // *cancel-all*
		}
		captionClear(u, n)
		n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
		n.Param2 = 0
		if n.Param1 == 0 {
			n.Param1 = uint32(defaultAttackSlot(u))
		}
		return Code(1) // *advance*
	case 1:
		// "release the payload" is the installers' own release arm, reached
		// here without an install [04 R-ORD-01 §1].
		if b := bindingOfUnit(u); b != nil && b.Movement != nil && b.Movement.Release != nil {
			b.Movement.Release(n)
		}
		if satisfied&0x3000 != 0 {
			return Code(1) // *advance*
		}
		if !canEngageSlot(u, n.Target, int(n.Param1)) {
			return Code(1) // *advance* — out of reach, go maneuver
		}
		releaseSlot(u, 0)
		releaseSlot(u, 2)
		bindSlotToUnit(u, int(n.Param1), n.Target)
		n.DynamicGate = 0x13808
		return Code(2) // *hold*
	case 2:
		return chaseManeuver(u, n)
	case 3:
		if satisfied&0x40E0 != 0 {
			n.Phase = 1
			return Code(4) // *hold* with the phase already reset
		}
		if canEngageSlot(u, n.Target, int(n.Param1)) {
			releaseSlot(u, 0)
			releaseSlot(u, 2)
			bindSlotToUnit(u, int(n.Param1), n.Target)
			n.DynamicGate = 0x148E8
			armDeadline(n, tick, 30)
			return Code(2) // *hold*
		}
		inhibitSlot(u, slotAll)
		n.DynamicGate = 0x100E8
		armDeadline(n, tick, 30)
		return Code(2) // *hold*
	default:
		return Code(7) // *cancel-all*
	}
}

// chaseManeuver is `Attack_Chase` phase 2, the orbit [04 R-ORD-01 §3].
//
// Let `d` be the slot's engagement distance — the weapon's authored range
// [06 R-WPN-05 §1]. By p2: **0** → point goal at the target radius d, p2 = 1.
// **1-4** → if |myY − targetY| > 8 world units: point goal radius d/2, p2 = 6;
// else draw `a = bearing(me → target) − 0x4000 + RNG(0x8000)` and install a
// point goal at `target − d·(sin a, 0, cos a)` with radius d/4 — **p2
// unchanged**. **5** → point goal radius d/2, p2 = 6. **6** → point goal at
// the target radius 0, p2 = 7. **7** → annulus (outer d, inner d/2), p2 = 8.
// **8** → annulus (outer 2d, inner d), p2 = 0. **≥ 9** → cancel-all. Every arm
// advances.
//
// [04 R-ORD-01 §3]'s correction to §3.5 applies here: because the strafe arm
// never increments p2, the reachable cycle is 0 → 1 (repeated strafes) → 6 → 7
// → 8 → 0, entered at 6 only through the vertical jump. Substates 2, 3, 4 and 5
// are dead under this handler and are written out anyway, because p2 is a
// record field a save can restore into any of them.
func chaseManeuver(u *units.Unit, n *Node) Code {
	if n.Param2 >= 9 {
		return Code(7) // *cancel-all*
	}
	d := engagementDistance(u, n.Param1)
	tgt := targetOf(u, n)
	if tgt == nil {
		// Every arm below reads the target's position; the pre-check only
		// rejects a null handle, so an unresolvable one waits rather than
		// installing a goal at the origin.
		return Code(3)
	}
	switch n.Param2 {
	case 0:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, d)
		n.Param2++
	case 1, 2, 3, 4:
		if numeric.Abs(u.Y.Raw()-tgt.Y.Raw()) > chaseVerticalJump {
			installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, truncHalf(d))
			n.Param2 = 6
			break
		}
		sx, sy, sz, radius := chaseStrafePoint(u, tgt, d)
		installPointGoal(u, n, sx, sy, sz, radius)
		// p2 unchanged: the strafe arm is the one that repeats.
	case 5:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, truncHalf(d))
		n.Param2++
	case 6:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, 0)
		n.Param2++
	case 7:
		installAnnulusGoal(u, n, tgt.X, tgt.Y, tgt.Z, d, truncHalf(d))
		n.Param2++
	case 8:
		installAnnulusGoal(u, n, tgt.X, tgt.Y, tgt.Z, 2*d, d)
		n.Param2 = 0
	}
	return Code(1) // *advance*
}

// chaseStrafePoint is the strafe arm's goal: a point one standoff away from the
// target along a bearing drawn from the half-circle centred on the line from
// the target back toward me, with an arrival radius of a quarter standoff
// [04 R-ORD-01 §3].
//
// The bearing is `atan2q(target.X − my.X, target.Z − my.Z)`, less a quarter
// turn, plus one simulation draw below 0x8000 — a half turn — so the result
// sweeps the semicircle from 90 degrees left of the line to 90 degrees right of
// it, which is what makes the unit circle its target instead of walking at it.
// The offset is subtracted from the target on X and Z and the target's own Y is
// kept, so the goal stays in the target's horizontal plane.
//
// The scaled sine and cosine are numeric's shared table helpers, which are
// retail's: [06 §3.3] states the index arithmetic as `((int16)angle + 32) >> 6`
// over the even byte offsets of a 512-entry table, i.e. one entry per 128 angle
// units, and numeric.Sin and numeric.Cos apply that same pre-add before their
// shift — `((angle + 32) >> 7) & 511` over entries, with the quarter turn added
// first for the cosine [04 R-MOV-01 §4]. The component product is numeric's
// `(entry * magnitude + 0x1000) >> 13`. Nothing is compensated for here.
func chaseStrafePoint(u, tgt *units.Unit, d int32) (x, y, z numeric.Fixed, radius int32) {
	bearing := numeric.AngleFromAtan2(int64(tgt.X.Raw()-u.X.Raw()), int64(tgt.Z.Raw()-u.Z.Raw()))
	angle := numeric.Angle(uint16(bearing) - 0x4000 + uint16(drawBelow(u, 0x8000)))
	magnitude := int32(d << 16)
	offX := numeric.MulRound(numeric.Sin(angle), magnitude)
	offZ := numeric.MulRound(numeric.Cos(angle), magnitude)
	return tgt.X - numeric.Fixed(offX), tgt.Y, tgt.Z - numeric.Fixed(offZ), truncQuarter(d)
}

// truncHalf and truncQuarter divide toward zero, which is what retail's
// `cltd; sub; sar` sequences do for the d/2 and d/4 radii [04 R-ORD-01 §3].
// Go's `/` already truncates toward zero, so these only name the contract.
func truncHalf(d int32) int32    { return d / 2 }
func truncQuarter(d int32) int32 { return d / 4 }

// ---------------------------------------------------------------------------
// Guard assistance triggers [04 §3.5] Follow_Ground / VTOL_Follow / Guard_NoMove
// ---------------------------------------------------------------------------

// The guard's two gate bit values, named rather than spelled as literals
// [04 R-UNIT-06 §1][04 R-ORD-01 §8 point 4]. The follow-maintenance leg arms
// both; the combat join consumes the higher one out of the record's satisfied
// word.
//
// [04 R-UNIT-06 §5] closes both producers, superseding §1's "Unknown" paragraph:
// `0x08` is *target removed* [04 R-ORD-01 §6] and `0x10` is *my target took
// damage* — raised by part 1 of the damage reaction on every order record
// observing the victim, whoever owns it [04 R-MOV-03 §7][06 R-WPN-04 §2]. The
// guard's `0x18` therefore reads, exactly: wake when the ward is destroyed or
// when the ward takes damage, plus the 30-tick deadline.
//
// This build raises the join bit: ObserverNotice in react.go ORs it into the
// owning unit's pending word for every record whose target is the victim and
// whose descriptor carries the observer bit `0x200` — which both guard rows do.
// A pending bit the gate does not admit persists, so a guard waiting behind its
// spawned attack (gate 0) banks the hits its ward took and re-joins one visit
// after that attack ends [04 R-UNIT-06 §5 part 2].
const (
	guardRearmBits     uint32 = 0x18
	guardCombatJoinBit uint32 = 0x10
)

// liveUnitByHandle resolves a pool handle through the acting unit's queue
// binding — the same seam targetOf reads a record's target through [P0-I16] —
// and drops a handle whose slot is no longer live.
//
// The liveness test is required, not defensive. The recorded-attacker link has
// no per-tick clear and is not cleared when the attacker dies, so it routinely
// names a dead or reused slot; [04 R-UNIT-06 §5 part 1] says so outright and
// puts the burden on the reader.
func liveUnitByHandle(actor *units.Unit, h pool.Handle) *units.Unit {
	if actor == nil || h == 0 {
		return nil
	}
	q := QueueForUnit(actor)
	if q == nil {
		return nil
	}
	binding := q.Binding()
	if binding == nil || binding.Lookup == nil {
		return nil
	}
	tgt := binding.Lookup(h)
	if tgt == nil || !tgt.Alive || tgt.Dying {
		return nil
	}
	return tgt
}

func getLookupForWard(n *Node, u *units.Unit) *units.Unit {
	if n == nil || n.Target == 0 {
		return nil
	}
	if u != nil {
		if q := QueueForUnit(u); q != nil {
			if binding := q.Binding(); binding != nil && binding.Lookup != nil {
				if tgt := binding.Lookup(n.Target); tgt != nil {
					return tgt
				}
			}
		}
	}
	return nil
}

// attackerHostileToGuard is leg 1's diplomacy term as RWU-19-13 corrects it
// inline in [04 R-UNIT-06 §1]. The byte is row A of the ENGAGEMENT TARGET's
// owner — the recorded attacker's own alliance declaration — indexed by the
// GUARD's owner slot, and the leg proceeds when it reads zero: the attacker has
// not declared alliance toward the guard's side, so it is hostile to the guard.
// `attackerOwner.A[guardOwner] == 0`, the same shape as the retaliation site's
// "attacker not allied" test [04 R-STANCE-01 §3].
//
// Corrected 2026-09-01. WU-19-35 read this as "the WARD is allied to the
// guard", following §1's pre-correction sentence and its "(allied)" gloss. Both
// halves were wrong: the row belongs to the attacker, not the ward, and the
// sense is hostile, not allied. The old form joined the ward's fight whenever
// the ward was friendly — including against a target allied to the guard — and
// refused it whenever the ward was an enemy's unit, neither of which is the
// traced gate. The ward's owner's rows are never read here at all.
//
// The row is read one-directionally through the binding's DeclaresAlliance,
// which is economy's row-A read [05 R-SHARE-01 §1]. The symmetric `Hostile`
// predicate the command resolver uses would answer a different question — "has
// EITHER side declared" — and would decline the join whenever the guard's own
// side had declared alliance to the attacker without reciprocation, which §1's
// correction names as a case that still joins.
func attackerHostileToGuard(guard, attacker *units.Unit) bool {
	if guard == nil || attacker == nil {
		return false
	}
	if b := bindingOfUnit(guard); b != nil && b.World != nil && b.World.DeclaresAlliance != nil {
		return !b.World.DeclaresAlliance(attacker.Owner, guard.Owner)
	}
	// No rows to read: slot initialization leaves each player allied only to
	// itself [05 R-SHARE-01 §1], so a different owner is hostile.
	return attacker.Owner != guard.Owner
}

// guardWillChase is leg 1's no-chase term [04 R-UNIT-06 §1]: the ward's
// engagement target's definition must be absent from the GUARD's
// `nochasecategory` bit array. The same array the retaliation branch tests
// [08 R-AI-01 §11], read here off the guard rather than the victim.
func guardWillChase(guard, target *units.Unit) bool {
	if guard == nil || guard.Def == nil || target == nil || target.Def == nil {
		return false
	}
	return !target.Def.DefinitionMask().Intersects(guard.Def.NoChaseCategoryMask)
}

// guardCombatJoin is leg 1's issue [04 R-UNIT-06 §1] as corrected by
// [04 §3.3 "Follow_Ground"]: the shared auto-engage issuer of
// [04 R-STANCE-01 §3] entered with its FORCE flag set. Force bypasses that
// issuer's two stance gates — "the queued-mode attack bypasses the
// standing-order gates" is exactly this flag — leaving admission steps 1 and 4:
// the unit is not its own target, and command code 3 resolves to a name. The
// resolved record goes in through the head insert, so the guard record waits
// behind the attack and resumes when it is gone; the "queue tail" of §1's own
// wording is corrected there.
//
// The two guard handlers are the only caller family that sets force
// [04 R-STANCE-01 §3]. This is the one site that passes true.
func guardCombatJoin(u *units.Unit, target *units.Unit) bool {
	return autoEngage(u, target, true)
}

// guardSlotBadTargetMask is the per-slot bad-target category array leg 2 tests
// against: the three authored `wpri_`/`wsec_`/`wspe_badTargetCategory` keys in
// slot order [02 "Unit record"][06 §3.2].
func guardSlotBadTargetMask(def *content.UnitDef, idx int) content.CategoryMask {
	if def == nil {
		return content.CategoryMask{}
	}
	switch idx {
	case 0:
		return def.BadTargetCategoryWPRIMask
	case 1:
		return def.BadTargetCategoryWSECMask
	case 2:
		return def.BadTargetCategoryWSPEMask
	}
	return content.CategoryMask{}
}

// guardSlotKeepsTarget retains a slot target only when the full shot-admission
// predicate accepts it and its definition is absent from the slot's bad-target
// array [04 R-UNIT-06 §1]. Distance alone does not establish a legal shot.
//
// "Resolve the slot's stored target" is the read of [04 R-ORD-01 §1], which
// "yields the unit only while the companion carries the unit marker and the id
// is nonzero" — so a slot bound to a ground point resolves to nothing and reads
// as "no target".
func guardSlotKeepsTarget(u *units.Unit, s *units.Slot, idx int) bool {
	if u == nil || s == nil || s.Weapon == nil {
		return false
	}
	if s.Target.Kind != units.TargetUnit || s.Target.Unit == 0 {
		return false // "the slot has no target"
	}
	held := liveUnitByHandle(u, s.Target.Unit)
	if held == nil || held.Def == nil {
		return false
	}
	// Retention uses the combat owner's complete admission predicate, including
	// range, medium, air restrictions and ballistic feasibility [04 R-UNIT-06 §1].
	if !canEngageSlot(u, held.Handle, idx) {
		return false
	}
	// "the target's definition **is** in the guard's per-slot
	// bad-target-category bit array".
	return !held.Def.DefinitionMask().Intersects(guardSlotBadTargetMask(u.Def, idx))
}

// guardRetargetSlots is leg 2 of [04 R-UNIT-06 §1] — "auto-fire support" — as
// corrected by [04 R-STANCE-01 §3].
//
// It is NOT an acquisition and it keeps no latch: it walks slots 0..2 in
// numeric order and rebinds onto the ward's engagement target exactly those
// slots whose own target is missing, fails shot admission, or is in its
// bad-target category. The caller reaches this fallback only after the
// damage-join gates pass and forced attack insertion fails.
// It returns no result code; the handler falls through to legs 3, 4 and 5.
//
// The step is "skipped when the guard's standing fire field is zero" — the
// standing-MOVE field is not read anywhere in either guard handler, which is
// [04 R-STANCE-01 §3]'s correction to §1's own wording.
//
// The slot's own admission is "enabled AND autonomous". [04 R-UNIT-06 §5 part
// 3] closes §1's "tracking bit": it is bit 4 of the SAME control byte whose bit
// 1 is *slot enabled*, [04 R-ORD-01 §7]'s "inhibit latch" and [06 §1.2]'s
// "tracking flag" are one bit, and its only writers are that section's two slot
// verbs — whose names read inverted against their effect. Bit 4 SET means the
// slot belongs to autonomous acquisition; *release* takes the slot for an
// order's own target (clearing it) and *inhibit* hands it back (setting it).
//
// So the test is load bearing, not dead code: a slot an attack order currently
// holds must be left alone here, and becomes eligible again when the record
// destructor returns it. The guard's own admit phase runs the return verb on
// all three slots [04 R-UNIT-06 §1], so leg 2 is live from the first phase-1
// visit.
//
// WU-19-35 skipped this test, following the same reasoning the retaliation
// offer in internal/combat/damage.go still carries — that nothing sets the bit,
// so testing it would kill the leg. §5 supersedes that: our admit phase already
// sets it through clearWeaponBuildTargets, which is inhibitSlot over all three.
func guardRetargetSlots(u *units.Unit, wardTarget *units.Unit) {
	if u == nil || u.Def == nil || wardTarget == nil {
		return
	}
	if u.Flags>>stanceFireShift&stanceFieldMask == 0 {
		return
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		s := u.SlotAt(idx)
		if s == nil || !slotEnabled(s) {
			continue // the slot-enabled bit [04 R-ORD-01 §7]
		}
		if s.Flags&units.SlotFlagAutonomous == 0 {
			continue // the slot is held by an order, not autonomous [04 R-UNIT-06 §5]
		}
		if s.Weapon.CommandFire {
			continue // "a weapon whose command-fire-only definition bit is clear"
		}
		if guardSlotKeepsTarget(u, s, idx) {
			continue
		}
		bindSlotToUnit(u, idx, wardTarget.Handle)
	}
}

func canRepairGuard(actor *units.Unit) bool {
	// Leg 3 of [04 R-UNIT-06 §1]: "when the ward's health (signed word) compares
	// below its definition's maximum-damage word **and the guard's definition
	// has the builder bit**". It is the authored `builder` key alone; the
	// `canreclamate` disjunct that stood here admitted reclaimers with no
	// nanolathe to the repair leg.
	return actor != nil && actor.Def != nil && actor.Def.Builder
}
func wardIsDamaged(ward *units.Unit) bool {
	return ward != nil && ward.Health < ward.MaxHealth
}

// staticGoalObserver is bit 10 of a descriptor's static gate mask — "this
// record was issued with a goal position", the twin of react.go's
// staticTargetObserver. The record constructor clears it on the record's own
// static-mask copy when no goal was supplied [04 §3.1][04 R-MOV-03 §7]; newNode
// applies that clear from the producer's Node.GoalSupplied statement, never
// from the coordinates.
//
// Its readers are the two guard copy arms — the ground leg 4 below and the air
// row's own leg in vtolFollowCopyWork — which take the bit as "this record has
// a goal to copy". Both are reached only for a ward whose front record carries
// bit 20 (the nanolathe/build-site class), and of those rows only `MobileBuild`
// and `VTOL_MobileBuild` also carry bit 10. The ground leg takes `MobileBuild`
// in the arm above, leaving `VTOL_MobileBuild` as the row its test decides; the
// air leg takes both build rows in its help-build arm [04 R-ORD-02 §3].
const staticGoalObserver uint32 = 0x400

func wardHasBuildOrder(ward *units.Unit) bool {
	if ward == nil {
		return false
	}
	q := QueueForUnit(ward)
	if q == nil || len(q.primary) == 0 {
		return false
	}
	head := q.primary[0]
	// "0x100000 marks the nanolathe/build-site class, tested by the guard-assist
	// branch" — a named bit of the descriptor gate mask, Established [04 §3.1].
	return head.StaticGate&0x100000 != 0
}

// guardHandler is the follow guard: `Follow_Ground` and its air twin
// `VTOL_Follow` [04 R-ORD-01 §8][04 R-UNIT-06 §1]. `Guard_NoMove` no longer
// shares it — it is a different order, not a follow variant, and its body is
// guardNoMoveHandler in combat.go [04 R-ORD-01 §8 point 5].
//
// Entry gates, in order [04 R-UNIT-06 §1]: a missing ward completes the order
// (code 5); a guard that is itself carried cancels its whole queue (code 7); a
// ward whose definition can fly removes the order (code 8 — a ground guard
// follows only ground wards); a phase byte beyond 1 cancels all (code 7).
//
// Phase 0 is the admit of [04 R-ORD-01 §8 points 1 and 2]; the GROUND row's
// phase 1 is the assist evaluation of [04 R-UNIT-06 §1], whose fall-through is
// the follow maintenance of [04 R-ORD-01 §8 points 3 and 4].
//
// The AIR row has one more phase than the ground row [04 R-ORD-02 §3]: its
// phase 1 is a separate "inhibit all three slots; advance" step, so its leg
// evaluation is phase 2 and its cancel-all boundary is a phase byte beyond 2.
// The ground row's two-phase shape is unchanged. guardLegPhase below is that
// fork, named once so the phase gate and the dispatch cannot drift apart.
func guardHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if n == nil {
		return Code(5)
	}
	air := unitCanFly(u)
	// The air twin's first entry step, which the ground row does not have:
	// "target null or satisfied ∩ `0x48` → … complete either way"
	// [04 R-ORD-02 §3]. It subsumes the ground row's plain "a missing ward
	// completes the order", because for an air guard the hand-off has to run
	// before the completion.
	if air {
		if code, done := vtolFollowHandOff(u, n, satisfied); done {
			return code
		}
	} else if n.Target == 0 {
		return Code(5) // no ward → *complete* [04 R-UNIT-06 §1]
	}
	ward := getLookupForWard(n, u)
	if ward == nil {
		return Code(5)
	}
	if u != nil && u.Attachment.Carrier != 0 {
		if modernGuardOnRepairPad(u) {
			// Resume the retained orbit after pad work without re-running the
			// admission draw. The normal preamble releases the pad attachment.
			if code := airWorkPreamble(u, n, "Guarding"); code != Code(1) {
				return code
			}
		} else {
			return Code(7) // a carried guard cancels its whole queue [04 R-UNIT-06 §1]
		}
	}
	// The flying-ward gate is stated for the GROUND guard ("a ground guard
	// follows only ground wards"); [04 R-UNIT-06 §1]'s air paragraph lists the
	// air twin's additions and does not repeat it, so it is applied on the same
	// canfly fork the command resolver uses to pick between the two names
	// [04 R-ORD-02 §1] rather than to both.
	if !air && unitCanFly(ward) {
		return Code(8) // *abandon* — the order is removed [04 R-UNIT-06 §1]
	}
	// The air twin's second entry step, before the phase switch and returning
	// from it immediately: the off-map recovery of [04 R-AIR-01 §5], whose
	// marker is the movement package's, so it is reached through the air leg
	// seam [04 R-ORD-02 §3].
	if air {
		if code, done := vtolFollowOffMap(u, n, satisfied, tick); done {
			return code
		}
	}
	if int(n.Phase) > guardLegPhase(air) {
		return Code(7) // *cancel-all* [04 R-UNIT-06 §1][04 R-ORD-02 §3]
	}
	// The air twin's third entry step, which the ground row does not have:
	// "every visit then copies the target's position into the record goal"
	// [04 R-ORD-02 §3]. A `VTOL_Follow` record's goal triple is therefore the
	// ward's POSITION, where `Follow_Ground`'s is the anchor OFFSET
	// [04 R-ORD-01 §8 point 2] — the two rows keep different things in the same
	// three fields, which is why the orbit leg below reads the ward directly and
	// why GuardFollowPoint is never asked about an air record
	// (internal/movement/goals.go names `Follow_Ground` alone).
	if air {
		n.GoalX, n.GoalY, n.GoalZ = ward.X, ward.Y, ward.Z
	}
	if n.Phase == 0 {
		return guardAdmit(u, n, ward)
	}
	// The air row's own phase 1, which the ground row does not have: "inhibit
	// all three slots; advance" [04 R-ORD-02 §3]. It is the same verb over the
	// same three slots the ground row runs inside its ADMIT phase
	// [04 R-UNIT-06 §1], moved into a phase of its own — so an air guard hands
	// its slots back to autonomous acquisition one dispatch later than a ground
	// guard does, and its leg evaluation starts at phase 2.
	if air && n.Phase == 1 {
		inhibitSlot(u, slotAll) // k = 3 is slots 0, 1, 2 in order [04 R-ORD-01 §1]
		return Code(1)          // *advance* — the same pump cascade re-enters phase 2
	}
	// slot 0 is null [01 §6.1] — a live unit never has Handle 0; the assist
	// legs cannot address such a guard, so it falls straight to maintenance.
	if u.Handle == 0 {
		return guardFollowMaintenance(u, n, ward, satisfied, tick)
	}
	if modernGuardSeekPad(u, n, tick) {
		return Code(2) // reload the landing head without resetting this guard
	}
	// The ward's recorded-attacker link, resolved once: legs 1 and 2 share it
	// [04 R-UNIT-06 §1]. RWU-19-13 closes what §1 recorded as Unknown and
	// inverts its Supported inference: the link is not the ward's combat target
	// but the unit that LAST DAMAGED the ward — the damage dispatcher's recorded
	// attacker [06 R-WPN-04 §2] — written after the reaction routine on every
	// non-heal packet with a nonzero attacker, and cleared only at spawn, death
	// and console kill. Nothing clears it per tick, and nothing clears it when
	// the attacker dies, so a reader must tolerate a dead or reused slot: the
	// liveness filter below and the command resolver's own tests are that guard
	// [04 R-UNIT-06 §5 part 1]. In one line: the guard attacks, and points its
	// free slots at, whatever last hurt its ward.
	//
	// The writer is the damage dispatcher, beside the attacker-side snapshot it
	// already stored (WU-19-43).
	wardTarget := liveUnitByHandle(u, ward.EngagementTarget)

	// Leg 1 — the combat join [04 R-UNIT-06 §1]. Four terms, in order: the
	// ward's recorded-attacker link is set; the diplomacy term (that attacker is
	// hostile to the guard); the satisfied bits carry the guard's re-arm bit
	// `0x10`; and the attacker's definition is not in the guard's no-chase
	// array. On a successful
	// enqueue the record's dynamic gate CLEARS and the handler returns the wait
	// code, so a guard that resumes after its spawned attack re-enters phase 1
	// with an empty gate and installs immediately [04 R-ORD-01 §8 point 4].
	//
	// This leg replaces the build-assist branch that stood here. §1's correction
	// is explicit that the old branch (a) was mis-read: there is no build assist
	// at the top of the guard's phase 1 — an unfinished ward is leg 3's business,
	// because command code 8 forks to help-build while the ward is unfinished
	// and to repair otherwise [04 R-ORD-02 §1].
	if wardTarget != nil &&
		attackerHostileToGuard(u, wardTarget) &&
		satisfied&guardCombatJoinBit != 0 &&
		guardWillChase(u, wardTarget) {
		if guardCombatJoin(u, wardTarget) {
			n.DynamicGate = 0
			return Code(3) // *wait* [04 §3.3]
		}
		// Leg 2 is the failed-join fallback inside these same four gates.
		// Maintenance alone must not offer a stale attacker to the slots
		// [04 R-UNIT-06 §1][04 R-ORD-02 §3].
		guardRetargetSlots(u, wardTarget)
	}

	// Leg 3 — repair/assist the ward [04 R-UNIT-06 §1]: "when the ward's health
	// compares below its definition's maximum-damage word and the guard's
	// definition has the builder bit, resolve command code 8 (assist-or-repair:
	// help-build while unfinished, the repair order otherwise) through the
	// canfly-forking resolver against the ward itself".
	//
	// The by-name `RepairUnit` lookup that stood here is gone: it could never
	// reach code 8's unfinished arm, and with the build-assist branch above
	// retired it would have left a guard unable to assist a nanoframe at all.
	// Code 8 carries its own nano-reach admission [04 R-ORD-02 §1], so a guard
	// with no nanolathe resolves no name and falls through to leg 4.
	if wardIsDamaged(ward) && canRepairGuard(u) && rulesOfUnit(u).AllowAutomaticRepair(u, tick) {
		if repID := Resolve(8, u, ward, nil); repID != 0 {
			q := QueueForUnit(u)
			// "clear the record's goal payload" before the insert, then head
			// insert [04 §3.3 "Follow_Ground"]. The payload is the movement-side
			// installation, NOT the record's goal triple: that triple holds the
			// anchor OFFSET the admit phase drew and the maintenance leg reads
			// [04 R-ORD-01 §8 point 2], so zeroing it would destroy the guard's
			// own follow position.
			releaseGoalPayload(u, n)
			q.PushHead(repID, Node{Owner: u.Handle, Target: n.Target, GoalX: ward.X, GoalY: ward.Y, GoalZ: ward.Z, GoalSupplied: true, automaticWork: true})
			n.DynamicGate = 0
			return Code(3) // *wait* [04 §3.3]
		}
	}

	// Leg 4 — join the ward's order [04 R-UNIT-06 §1]: "when the ward's front
	// order exists with a nonzero descriptor, BOTH guard and ward definitions
	// have the builder bit, the front order carries flag `0x100000`, and its
	// target is not the guard itself".
	//
	// The two definition bits and the self-target exclusion were missing while a
	// latch hid how often this leg fires; with the latch gone they are load
	// bearing — without the exclusion a guard whose ward is building the guard
	// enqueues help-build against itself, once per resume.
	//
	// The two arms are settled by trace (WU-19-159, [04 R-ORD-01 §13]) and both
	// are issued now. The ward's front record decides which:
	//
	//	arm A — its descriptor is `MobileBuild` or `BuildingBuild`, the two names
	//	        the handler resolves by literal at this site. It spawns
	//	        `HelpBuild`, resolved by NAME with no canfly fork, and falls
	//	        through to leg 5 when the front record has no target yet.
	//	arm B — otherwise, and only when the front record has something to copy:
	//	        a bound target (static bit 9 set and the target non-null) or a
	//	        goal position (static bit 10). It spawns a COPY of the ward's
	//	        descriptor. Neither term is "queued", which is what §1's
	//	        "payload-carrying queued order" had been read as.
	//
	// Both arms construct the spawned record with the ward's front record's
	// target AND goal triple, release the guard record's payload first, clear
	// the dynamic gate and return the wait code.
	//
	// The air row has its own leg here, with a different first arm and a
	// nano-reach gate the ground leg lacks [04 R-ORD-02 §3]; it never runs the
	// ground arms below.
	if air {
		if vtolFollowCopyWork(u, n, ward, tick) {
			n.DynamicGate = 0
			return Code(3) // *wait* [04 §3.3]
		}
	} else if wardHasBuildOrder(ward) && canRepairGuard(u) && canRepairGuard(ward) && rulesOfUnit(u).AllowAutomaticRepair(u, tick) {
		var head *Node
		if wq := QueueForUnit(ward); wq != nil && len(wq.primary) > 0 {
			head = wq.primary[0]
		}
		if head != nil && head.ID != 0 && head.Target != u.Handle {
			spawnID := ID(0)
			switch {
			case head.ID == rowMobileBuild || head.ID == rowBuildingBuild:
				// The canfly fork is deliberately absent: the handler resolves
				// the literal name `HelpBuild`, so a VTOL guard joins with the
				// ground descriptor too [04 R-ORD-01 §13].
				if head.Target != 0 {
					spawnID = rowHelpBuild
				}
			case head.StaticGate&staticTargetObserver != 0 && head.Target != 0,
				head.StaticGate&staticGoalObserver != 0:
				spawnID = head.ID // a copy: same descriptor, same target, same goal
			}
			if spawnID != 0 {
				q := QueueForUnit(u)
				releaseGoalPayload(u, n)
				// "same descriptor, same target, same goal": the spawn is
				// constructed with a goal exactly when the ward's record was,
				// which its own bit 10 states [04 R-ORD-01 §13]. Asserting a
				// goal unconditionally here would hand bit 10 to a copy taken
				// through the target arm of the switch above, where the ward's
				// record carries no goal at all.
				goalSupplied := head.StaticGate&staticGoalObserver != 0
				q.PushHead(spawnID, Node{Owner: u.Handle, Target: head.Target, GoalX: head.GoalX, GoalY: head.GoalY, GoalZ: head.GoalZ, GoalSupplied: goalSupplied, automaticWork: true})
				n.DynamicGate = 0
				return Code(3) // *wait* [04 §3.3]
			}
		}
	}
	if modernGuardNearbyWork(u, n, tick) {
		return Code(2) // reload temporary work without resetting this guard
	}
	// Leg 5, the follow maintenance, on every visit that falls through the
	// legs above [04 R-UNIT-06 §1][04 R-ORD-01 §8 point 3]; for the air twin it
	// is leg 4 of [04 R-ORD-02 §3], which is the same position in the same list.
	return guardFollowMaintenance(u, n, ward, satisfied, tick)
}

// vtolFollowCopyWork is the air guard's "copy the ward's work" leg
// [04 R-ORD-02 §3 leg 3]. With `h` the ward's front record:
//
//	`h` exists with a nonzero identity, I have `builder`, nano-reach passes for
//	`h`'s TARGET, the ward has `builder`, `h`'s static-mask copy has bit 20, and
//	`h`'s target is not me. Then: `h` is `MobileBuild`, `BuildingBuild` or
//	`VTOL_MobileBuild` and has a target → spawn `VTOL_HelpBuild` on that target;
//	else (`h` has bit 9 and a target) or (`h` has bit 10) → spawn `h`'s own
//	descriptor, substituting the air twin for `RepairUnit`, `Reclaim`,
//	`ReclaimUnit` and `HelpBuild`, with `h`'s target and goal. Either spawn
//	releases the payload and inserts at the head; any other `h` falls to the
//	orbit.
//
// The air leg differs from the ground one ([04 R-UNIT-06 §1] leg 4) in three
// ways that matter. `VTOL_MobileBuild` is in the first arm, so an air builder
// guarding an air builder joins as a helper rather than copying the build
// record. The helper is the air descriptor, not the ground one. And the whole
// leg is behind nano-reach on `h`'s target, whose first term is that the target
// exists: a ward still flying to a site it has not stamped has a targetless
// head, fails the gate, and the guard orbits until the nanoframe exists.
//
// Running the ground arms for an air guard (the defect this replaces) copied a
// ward's `VTOL_MobileBuild` record through the goal-bit arm. The copy carries
// the target and goal only — zero parameters, so no product — and its
// placement phase could resolve no definition.
//
// It reports whether it spawned; the caller clears the gate and waits.
func vtolFollowCopyWork(u *units.Unit, n *Node, ward *units.Unit, tick uint32) bool {
	if !canRepairGuard(u) || !canRepairGuard(ward) || !wardHasBuildOrder(ward) || !rulesOfUnit(u).AllowAutomaticRepair(u, tick) {
		return false
	}
	wq := QueueForUnit(ward)
	if wq == nil || len(wq.primary) == 0 {
		return false
	}
	head := wq.primary[0]
	if head == nil || head.ID == 0 || head.Target == u.Handle {
		return false
	}
	if !nanoReach(u, liveUnitByHandle(u, head.Target)) {
		return false
	}
	spawnID := ID(0)
	switch {
	case head.ID == rowMobileBuild || head.ID == rowBuildingBuild || head.ID == rowVTOLMobileBuild:
		// Nano-reach has already established the target.
		spawnID = rowVTOLHelpBuild
	case head.StaticGate&staticTargetObserver != 0 && head.Target != 0,
		head.StaticGate&staticGoalObserver != 0:
		switch head.ID {
		case rowRepairUnit:
			spawnID = rowVTOLRepairUnit
		case rowReclaim:
			spawnID = rowVTOLReclaim
		case rowReclaimUnit:
			spawnID = rowVTOLReclaimUnit
		case rowHelpBuild:
			spawnID = rowVTOLHelpBuild
		default:
			spawnID = head.ID
		}
	}
	if spawnID == 0 {
		return false
	}
	releaseGoalPayload(u, n)
	// The spawn has a goal exactly when the ward's record did, as in the ground
	// leg's copy [04 R-ORD-01 §13].
	goalSupplied := head.StaticGate&staticGoalObserver != 0
	QueueForUnit(u).PushHead(spawnID, Node{Owner: u.Handle, Target: head.Target, GoalX: head.GoalX, GoalY: head.GoalY, GoalZ: head.GoalZ, GoalSupplied: goalSupplied, automaticWork: true})
	return true
}

// guardAdmit is the ground guard's phase 0 [04 R-ORD-01 §8 points 1 and 2],
// which is [04 R-UNIT-06 §1]'s admit phase:
//
//	Clear all three weapon-slot build targets (the weapon-clear walk with its
//	TargetCleared signals); write p1 = (FootPrintX(me) + FootPrintX(ward) + 2)
//	· 16; draw ONE RNG(65536) for the anchor direction and store
//	(−sin(h)·r, 0, −cos(h)·r) in the record's goal triple as an offset from the
//	ward, with r the same radius promoted to 16.16; advance.
//
// The draw is the unit's own queue-bound simulation stream and happens exactly
// once per guard record: the maintenance leg draws nothing (I4). p1 is
// overwritten unconditionally — whatever the issuer stored in it survives only
// until this phase runs, and there is no zero case to fall back from.
//
// The stored triple is an OFFSET, not a position. Every reader of a guard
// record's goal — the order-line overlay, a save, the movement side's own goal
// derivation — sees the offset and must add the ward's position to it
// [04 R-ORD-01 §8 point 2]; GuardFollowPoint is that sum.
func guardAdmit(u *units.Unit, n *Node, ward *units.Unit) Code {
	if unitCanFly(u) {
		return vtolFollowAdmit(u, n)
	}
	clearWeaponBuildTargets(u)
	radius := guardFollowRadius(u, ward)
	n.Param1 = uint32(radius)
	h := numeric.Angle(uint16(drawBelow(u, 0x10000)))
	r := int32(radius) << 16 // the radius promoted to 16.16 for the multiply
	n.GoalX = -numeric.Fixed(numeric.MulRound(numeric.Sin(h), r))
	n.GoalY = 0
	n.GoalZ = -numeric.Fixed(numeric.MulRound(numeric.Cos(h), r))
	return Code(1) // *advance* — the same pump cascade re-enters phase 1
}

// guardFollowMaintenance is leg 5 of [04 R-UNIT-06 §1] as closed by
// [04 R-ORD-01 §8 points 3 and 4]:
//
//	Install a POINT goal at `ward + storedOffset` with radius p1 / 2; set the
//	deadline to tick + 30 (fixed, no draw) through the shared setter, which
//	also ORs gate bit 0x01; OR 0x18 into the dynamic gate; return hold (2) with
//	the phase left at 1.
//
// The gate on the way out is 0x19 — the pump wipes the gate before dispatch,
// so the OR always lands on an empty word. Arrival (`0x20`) and path failure
// (`0x40`) are deliberately NOT gated: the follow goal is re-issued on a fixed
// 30-tick period whether or not the guard has arrived, each re-issue releasing
// the previous payload and starting a fresh path request, and a failed path
// never ends the guard.
//
// Legs 1 and 2 are above and no longer approximations; every phase-1 visit that
// falls through legs 1-4 reaches this one [04 R-ORD-01 §8 point 3].
//
// The air twin's maintenance is a different leg with a different marker family:
// airspace circling with the `0x80` arrival radius [04 R-UNIT-06 §1], written
// out in full as leg 4 of [04 R-ORD-02 §3]. It is vtolFollowOrbit below; the
// ground body here is unchanged. The air twin also holds with its phase left at
// 2 rather than 1, since its legs run one phase later [04 R-ORD-02 §3]; neither
// body writes the phase, so that follows from the caller.
func guardFollowMaintenance(u *units.Unit, n *Node, ward *units.Unit, satisfied uint32, tick uint32) Code {
	if unitCanFly(u) {
		return vtolFollowOrbit(u, n, ward, satisfied, tick)
	}
	x, y, z, radius := GuardFollowPoint(n, ward.X, ward.Y, ward.Z)
	// The payload form is used rather than installPointGoal so the record's
	// goal triple keeps the offset [04 R-ORD-01 §8 point 2].
	installPointGoalPayload(u, n, x, y, z, radius)
	armDeadline(n, tick, 30)        // fixed 30, no draw; the setter ORs gate bit 0x01
	n.DynamicGate |= guardRearmBits // the two re-arm bits [04 R-ORD-01 §8 point 4]
	return Code(2)                  // *hold*, phase left at 1
}

// The air guard's orbit constants, all of them [04 R-ORD-02 §3] leg 4 and its
// phase 0. They are the same family the two seek states already fly in
// internal/movement (`legVTOLSeekAttack`, `legVTOLSeekGuard`), which is why the
// bonus, the arrival radius and the gate word repeat those values exactly:
// §3 states leg 4 once and `VTOL_SeekGuard` reuses it by reference.
const (
	// airFollowOrbitBonus is the `+ 0xA0` world units the orbit adds to the
	// slot-0 weapon `Range` [04 R-ORD-02 §3][04 R-AIR-01 §7].
	airFollowOrbitBonus int32 = 0xA0
	// airFollowUnarmedRadius is the orbit radius of a guard whose state word
	// has no armed bit: "else 320" [04 R-ORD-02 §3].
	airFollowUnarmedRadius int32 = 320
	// airFollowArrivalRadius is the horizontal arrival radius of the installed
	// marker, the `0x80` [04 R-UNIT-06 §1] names for the air twin.
	airFollowArrivalRadius int32 = 0x80
	// airFollowOrbitStep and airFollowOrbitJitter are the subtractive bearing
	// step `p1 −= 0x4000 + RNG(0x2000)`: a quarter turn plus up to 45° further,
	// always subtractive [04 R-ORD-02 §3].
	airFollowOrbitStep   uint32 = 0x4000
	airFollowOrbitJitter uint32 = 0x2000
	// airFollowArrivalBits is the `satisfied ∩ 0xE0` that gates that step, and
	// airFollowOrbitGate the `gate |= 0xF8` the leg leaves behind — the three
	// movement outcomes plus the guard's own two re-arm bits.
	airFollowArrivalBits uint32 = 0xE0
	airFollowOrbitGate   uint32 = 0xF8
	// airFollowHandOffBits is the `satisfied ∩ 0x48` of the air row's entry
	// pre-check [04 R-ORD-02 §3]: `0x08` *target removed* [04 R-ORD-01 §6] and
	// `0x40` the cannot-get-there signal [04 R-ORD-01 §0] — the same pair
	// `VTOL_RepairPatrol`'s pre-check reads, and the second of them is the whole
	// entry test of the two seek states (pendNoRoute in vtolair.go).
	airFollowHandOffBits uint32 = 0x48
)

// guardLegPhase is the phase byte at which the follow guard's four legs run:
// 1 for `Follow_Ground` [04 R-UNIT-06 §1], 2 for `VTOL_Follow`, whose phase 1
// is the separate slot-inhibit step [04 R-ORD-02 §3]. It doubles as the
// cancel-all boundary — "a phase byte beyond 1 cancels all" for the ground row,
// "other phase: cancel-all" for the air one.
func guardLegPhase(air bool) int {
	if air {
		return 2
	}
	return 1
}

// vtolFollowHandOff is the air row's entry pre-check and its hand-off
// [04 R-ORD-02 §3]:
//
//	target null or satisfied ∩ `0x48` → if this record has no successor,
//	allocate `VTOL_SeekGuard` with this record's target (null when the target is
//	gone) and goal and tail-append it; complete either way.
//
// Four things the wording pins down and this body reproduces:
//
//   - "complete either way" is code 5 whether or not the hand-off record was
//     allocated, so the reported code does not depend on the queue's shape;
//   - "no successor" is the record's next link being null, which is
//     hasSuccessor's question asked the other way round. It is why a guard that
//     still has orders queued behind it does NOT plant a seeker in front of
//     them: the pre-check just ends the guard and the queue moves on;
//   - "tail-append" is [04 R-ORD-02 §4]'s helper — walk the segment the
//     record's rear-segment flag selects to its last link and append, inheriting
//     no flag — which is Queue.appendTail, routing on the same static bit 18
//     Push routes on. No new insert is written here;
//   - "this record's target (null when the target is gone)" is the record's own
//     target handle, zeroed when it no longer resolves to a live unit. Both
//     halves of the trigger can leave a stale handle behind — `0x08` is raised
//     precisely BECAUSE the target went away — so the resolve is what decides,
//     not the handle's being non-zero.
//
// The goal triple copied across is the record's, which the previous visit's
// entry step set to the ward's last known position: the seeker therefore starts
// its orbit about the place the ward was, which is what `VTOL_SeekGuard`'s own
// fall-through leg circles when it finds nothing to guard [04 R-ORD-02 §3].
func vtolFollowHandOff(u *units.Unit, n *Node, satisfied uint32) (Code, bool) {
	if n.Target != 0 && satisfied&airFollowHandOffBits == 0 {
		return 0, false
	}
	// QueueOfUnit, not QueueForUnit: the record being dispatched already lives
	// in a queue, and a guard with none has nothing to append to — ending an
	// order must not create one as a side effect. It is also the accessor
	// hasSuccessor reads, so the two cannot disagree about which queue.
	if q := QueueOfUnit(u); q != nil && !hasSuccessor(u, n) {
		if id := rowVTOLSeekGuard; id != 0 {
			target := n.Target
			if getLookupForWard(n, u) == nil {
				target = 0 // "null when the target is gone"
			}
			owner := n.Owner
			if u != nil {
				owner = u.Handle // "the record's owner is set" [04 R-ORD-02 §4]
			}
			q.appendTail(id, Node{
				Owner:        owner,
				Target:       target,
				GoalX:        n.GoalX,
				GoalY:        n.GoalY,
				GoalZ:        n.GoalZ,
				CreationTick: n.CreationTick,
				GoalSupplied: true, // the hand-off carries the guard's stored anchor [04 R-ORD-02 §3]
			})
		}
	}
	return Code(5), true // *complete* either way [04 R-ORD-02 §3]
}

// vtolFollowOffMap is the off-map recovery leg [04 R-AIR-01 §5] that six air
// executors, `VTOL_Follow` among them, run before their phase switch and return
// from immediately: a point marker at `unitPos + offset`, `offset` the negated
// sine/cosine pair of `bearing(unitPos, mapCentre)` at 800 world units, arrival
// radius `0x80`, gate `|= 0xE0`, result code 2.
//
// The marker is one of the air path-marker family of [04 R-AIR-01 §4], which
// internal/movement owns, and the sentinel test reads the air sector grid,
// which internal/movement also owns — neither is reachable from this package
// (the dependency runs the other way). So the leg is reached through the
// existing air seam, `MovementGoalAdapter.RunAir`, exactly as the five other
// executors that carry this step reach their legs. This is airHandOff without
// its no-runner fallback: a `VTOL_Follow` record must NOT be parked on a
// one-tick deadline when no runner is bound, because unlike those five its
// remaining phases live here and run perfectly well without one.
//
// The runner declines (false) whenever the guard is on the map, so the ordinary
// path costs one seam call and no allocation.
func vtolFollowOffMap(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool) {
	q := QueueForUnit(u)
	if q == nil {
		return 0, false
	}
	b := q.Binding()
	if b == nil || b.Movement == nil || b.Movement.RunAir == nil {
		return 0, false
	}
	return b.Movement.RunAir(u, n, satisfied, tick)
}

// vtolFollowAdmit is `VTOL_Follow`'s phase 0 [04 R-ORD-02 §3]:
//
//	mover and `canfly` (else cancel-all); caption clear with `Guarding`; the
//	takeoff preamble; draw RNG(0x10000) into p1 (the orbit bearing) and its low
//	bit into p2; advance.
//
// airWorkPreamble is that caption-clear-plus-takeoff-preamble pair, already
// this package's expression of it for the five air work twins [04 R-ORD-01 §7],
// and its own mover/`canfly` gate is the row's. The record's goal triple is NOT
// written here: the entry copy in guardHandler has already put the ward's
// position there, and the air row keeps no anchor offset — p1 is a bearing, not
// the ground row's radius.
//
// The stream cost is one RNG(0x10000), exactly as the ground admit's anchor
// draw, so forking the two admits does not move the simulation stream for any
// guard already in flight (I4).
//
// The three entry-side differences this comment used to carry as a T25 marker —
// the satisfied-`0x48` pre-check with its tail-appended `VTOL_SeekGuard`
// hand-off, the sentinel-sector diversion to the off-map loiter path of
// [04 R-AIR-01 §5], and §3's separate phase 1 — are wired: vtolFollowHandOff,
// vtolFollowOffMap and guardLegPhase above, all three read by guardHandler's
// entry in §3's own order.
func vtolFollowAdmit(u *units.Unit, n *Node) Code {
	if code := airWorkPreamble(u, n, "Guarding"); code != Code(1) {
		return code
	}
	draw := drawBelow(u, 0x10000)
	n.Param1 = draw
	n.Param2 = draw & 1
	return Code(1) // *advance*
}

// vtolFollowOrbit is leg 4 of [04 R-ORD-02 §3], the air guard's follow
// maintenance — what [04 R-UNIT-06 §1] calls "airspace circling (radius `0x80`
// arrival)" where the ground row installs a point goal:
//
//	Satisfied ∩ 0xE0 → p1 −= 0x4000 + RNG(0x2000). Radius r = my slot-0 weapon
//	`Range + 0xA0` world units when my state word has bit 31, else 320. Point
//	marker at `wardPos − offset(p1, r)` with horizontal arrival radius 0x80;
//	install; deadline 30; gate |= 0xF8; hold.
//
// Three points of care:
//
//   - `wardPos − offset(...)` is the negate-and-add sign of [04 R-MOV-01 §4]:
//     `pos − bearingOffset(h, r)` moves r world units ALONG h, so the marker
//     sits r units from the ward on the bearing p1 and the subtractive step
//     walks it around the ward. Both air seek legs write the same expression.
//   - the "state word bit 31" is the armed bit [04 R-ORD-01 §3], which this
//     build carries as units.ArmedStatus — set once by the allocator for a
//     definition that resolved at least one weapon slot — so an unarmed
//     aircraft (a scout, a transport) circles at the flat 320 instead of at a
//     range it does not have.
//   - the marker's Y is the ward's, and the air marker family recomputes it
//     from the sector height at cruise altitude on every update
//     [04 R-AIR-01 §4], so the guard circles overhead rather than diving to the
//     ward's own height.
//
// The draw is conditional, and it is the leg's only one: a visit that is not
// answering an arrival advances no stream state (I4).
func vtolFollowOrbit(u *units.Unit, n *Node, ward *units.Unit, satisfied uint32, tick uint32) Code {
	if satisfied&airFollowArrivalBits != 0 {
		n.Param1 = uint32(uint16(n.Param1) - uint16(airFollowOrbitStep+drawBelow(u, airFollowOrbitJitter)))
	}
	r := airFollowUnarmedRadius
	if u != nil && u.Flags&units.ArmedStatus != 0 {
		r = engagementDistance(u, 0) + airFollowOrbitBonus
	}
	ox, oz := bearingOffset(uint16(n.Param1), numeric.Fixed(int64(r)<<16))
	if b := bindingOfUnit(u); b != nil && b.Movement != nil && b.Movement.InstallAir != nil {
		// The air seam owns the release of the displaced payload, the `0x80`
		// raise on its owner and the closing pending clear [04 R-ORD-01 §9],
		// exactly as it does for `VTOL_Patrol`'s marker next door.
		b.Movement.InstallAir(AirGoalRequest{
			Owner:  n.Owner,
			Node:   n,
			X:      ward.X - ox,
			Y:      ward.Y,
			Z:      ward.Z - oz,
			Radius: airFollowArrivalRadius,
		})
	}
	armDeadline(n, tick, 30) // deadline 30; the setter ORs gate bit 0x01
	n.DynamicGate |= airFollowOrbitGate
	return Code(2) // *hold*, phase left where it was (2 for the air row)
}

func unitCanFly(u *units.Unit) bool {
	return u != nil && u.Def != nil && u.Def.CanFly
}

// ---------------------------------------------------------------------------
// Registration [PLAN_06 WU-06-4] additive registration onto existing table's descriptors
// ---------------------------------------------------------------------------

func init() {
	if len(Table()) == 0 {
		return
	}
	registerChaseGuardHandlers()
	if len(table) != 0 {
		if id := rowAttackChase; id != 0 && table[int(id)].Handler != nil {
			handlersRegistered = true
		}
	}
}

func registerChaseGuardHandlers() {
	if id := rowAttackChase; id != 0 {
		table[int(id)].Handler = attackChaseHandler
	}
	if id := rowFollowGround; id != 0 {
		table[int(id)].Handler = guardHandler
	}
	if id := rowVTOLFollow; id != 0 {
		table[int(id)].Handler = guardHandler
	}
	// `Guard_NoMove` used to be assigned guardHandler here. It is a different
	// order, not a follow variant: it installs no goal and never moves
	// [04 R-ORD-01 §8 point 5]. combat.go's installer owns it now.
}
