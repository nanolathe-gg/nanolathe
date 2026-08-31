package orders

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The combat order handlers of [04 R-ORD-01 §3], plus the four air-attack
// executors' shared entry sequence of [04 R-AIR-01 §8].
//
// Every body here is an ORDER RECORD machine: the pre-checks, the phase
// switch, what it writes to the record and to the unit's weapon slots, and the
// result code [04 §3.3] maps to a queue effect. Weapon state itself belongs to
// internal/combat; nothing in this file fires, aims or reloads.
//
// Two properties of the record hold for every handler below and are not
// restated per row. First, "the record constructor zeroes the dynamic gate and
// the pending word, so a freshly inserted record is dispatched on its very next
// pump visit with an empty satisfied set" [04 R-ORD-01 §1] — so each phase 0
// is written for a first visit with nothing satisfied. Second, a handler that
// arms a dynamic gate and returns *advance* is re-visited immediately by the
// same walk and then blocks at [04 §3.3] step 3 until an outside writer raises
// one of the armed bits; that block is the row's own wait, not a stall.
//
// The result-code names follow [04 R-ORD-01 §1]: *complete* is 5, *abandon* 8,
// *cancel-all* 7, *wait* 3, *re-arm* 9, *rotate* 6, *hold* 2 or 4, *advance* 1,
// *restart* 0.

// The two located pending bits the combat pre-checks read [04 R-ORD-01 §6].
// `0x8` is raised on every reference registered on a unit by the removal path
// when that unit is destroyed; `0x10000` is raised on every reference
// registered on a cloaking unit by the edge machine's rising cloak edge. Their
// union is the "the thing I was told to attack is no longer attackable" test
// the rows spell as `0x10008`.
const (
	pendTargetRemoved uint32 = 0x8
	pendTargetCloaked uint32 = 0x10000
	pendTargetGone    uint32 = pendTargetRemoved | pendTargetCloaked
)

// slotAll is the weapon-slot helpers' `k = 3` sentinel: "take `k = 3` to mean
// all three slots in order 0, 1, 2" [04 R-ORD-01 §1].
const slotAll = 3

// ---------------------------------------------------------------------------
// The weapon-slot vocabulary of [04 R-ORD-01 §1], record side
// ---------------------------------------------------------------------------

// clearSlotTarget is the per-slot half of the weapon-target clear that the
// release and inhibit helpers both perform: the slot's target words are reset
// to the empty form and the owner's COB function `TargetCleared` is arranged
// with the slot index, under [R-ORDER-02 §2]'s guard that a slot whose target
// words are ALREADY empty is skipped entirely.
//
// TODO(question): [R-ORDER-02 §2]'s guard has two halves — "the slot's control
// byte has bit 1 set (slot assigned) and bit 4 clear — bit 4 is then set — AND
// the slot's target words are not already empty". This build's units.Slot has
// no control byte (its Flags word is the armed / aim-latch / tracking trio of
// [06 §1.2]), and [04 §3.9]'s own missing list still records "the semantic name
// of the weapon-slot control byte's bit 4" as unlocated, so the assigned and
// inhibit bits have nowhere to live and neither half of the control-byte guard
// can be evaluated. Only the empty test is reproduced here. A trace naming that
// byte's two bits, plus a field for it on the slot, would settle it; stop.go's
// unconditional entry carries the same question.
func clearSlotTarget(u *units.Unit, idx int) {
	s := u.SlotAt(idx)
	if s == nil || s.Target.Kind == units.TargetNone {
		return // already empty: no reset and no notification [R-ORDER-02 §2]
	}
	s.Target = units.Target{Kind: units.TargetNone}
	arrangeDeferred(callbackBridgeFor(u), "TargetCleared", []int32{int32(idx)})
}

// eachSlot walks the slots a helper's `k` selects, in the order
// [04 R-ORD-01 §1] fixes: `k = 3` is slots 0, 1, 2 in that order (I1).
func eachSlot(u *units.Unit, k int, visit func(idx int)) {
	if u == nil {
		return
	}
	if k == slotAll {
		for idx := 0; idx < units.NumSlots; idx++ {
			visit(idx)
		}
		return
	}
	if k >= 0 && k < units.NumSlots {
		visit(k)
	}
}

// releaseSlot is "*release slot k*: clears that bit and clears the target"
// [04 R-ORD-01 §1]. The bit is the control byte's inhibit bit, which this build
// does not model (see clearSlotTarget's question); what survives is the target
// clear and its notification.
func releaseSlot(u *units.Unit, k int) {
	eachSlot(u, k, func(idx int) { clearSlotTarget(u, idx) })
}

// inhibitSlot is "*inhibit slot k*: sets the slot's control-byte bit 4 and
// clears its target" [04 R-ORD-01 §1]. Same missing byte, same surviving half.
func inhibitSlot(u *units.Unit, k int) {
	eachSlot(u, k, func(idx int) { clearSlotTarget(u, idx) })
}

// bindSlotToUnit is "*bind slot k to unit*: stores the target's unit id word
// with the unit-companion marker" [04 R-ORD-01 §1]. The marker is this build's
// units.TargetUnit kind, which is what "read slot k target yields the unit only
// while the companion carries the unit marker and the id is nonzero" tests.
func bindSlotToUnit(u *units.Unit, k int, target pool.Handle) {
	eachSlot(u, k, func(idx int) {
		if s := u.SlotAt(idx); s != nil {
			s.Target = units.Target{Kind: units.TargetUnit, Unit: target}
		}
	})
}

// bindSlotToPosition is "*bind slot k to position*: stores the whole-unit X and
// Z (a Z of exactly −32768 is nudged to −32767 so it cannot read as the empty
// sentinel)" [04 R-ORD-01 §1]. Both terms are truncated to whole world units
// before storage, which is what makes the sentinel collision possible at all.
func bindSlotToPosition(u *units.Unit, k int, x, z numeric.Fixed) {
	wx := int32(x.Raw() >> 16)
	wz := int32(z.Raw() >> 16)
	if wz == -32768 {
		wz = -32767 // cannot read as the empty companion sentinel [04 R-ORD-01 §1]
	}
	eachSlot(u, k, func(idx int) {
		if s := u.SlotAt(idx); s != nil {
			s.Target = units.Target{
				Kind: units.TargetGround,
				X:    numeric.Fixed(int64(wx) << 16),
				Z:    numeric.Fixed(int64(wz) << 16),
			}
		}
	})
}

// engagementDistance is the per-slot distance `Suppress` phase 0 stores in p2
// and phase 2 draws a third of [04 R-ORD-01 §3].
//
// TODO(question): [04 R-ORD-01 §3] defers this to doc 06, and doc 06 does not
// define it; [04 §3.9]'s own missing list still carries "the standoff value
// bound by the attack-chase orbit substates; it is produced by the weapon-slot
// engagement-distance helper and remains inference", with a static trace of
// that helper as its decider. No value is chosen here: the helper reports 0,
// which drives `Suppress` phase 2 down its own established `p2 < 1` arm
// (*re-arm*) instead of walking the unit closer by an invented fraction of an
// invented range.
func engagementDistance(_ *units.Unit, _ uint32) int32 { return 0 }

// captionClear is [04 R-ORD-01 §1]'s "caption clear": the one-shot helper that
// clears the record's runtime caption-pending bit and emits status kind 5
// (`ok`) with no text.
//
// TODO(T25): this build has neither a status emitter nor a caption-pending
// field on the record, so there is nothing to clear and nowhere to emit.
// Placeholder: the step is a no-op, exactly as stop.go's `Stop` row records for
// the same helper. The rows' observable halves — the slot binds, the goals and
// the result codes — all run.
func captionClear(_ *units.Unit) {}

// installPointGoal is the point form of [04 R-ORD-01 §1]'s goal installers:
// "a **point** goal at a position with an arrival radius ... skip the install
// entirely — release only — when the owner's definition has the `canfly` bit,
// and finish by clearing pending bits `0x20`–`0x200`".
//
// TODO(T25): two halves of the installer have no home in this build. (a) The
// arrival radius is a field of the goal payload, and this package's record
// carries only the goal triple — internal/movement owns the payload classes
// (its air marker's setArrivalRadius is the air half) and is not reachable from
// here, so the radius argument is accepted, documented per call site, and
// dropped. (b) The release of the PREVIOUS payload raises pending `0x80` on the
// record that owned it [04 R-ORD-01 §0]; with no payload registry in this
// package there is no way to find that record, so no `0x80` is raised.
// Placeholder: the goal triple is written and the five movement pending bits
// are cleared, which is the half the pump and the handlers themselves observe.
func installPointGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed, radius int32) {
	_ = radius
	n.Satisfied &^= 0x3E0 // clear pending 0x20..0x200 [04 R-ORD-01 §0][04 R-ORD-01 §1]
	if u != nil && u.Def != nil && u.Def.CanFly {
		return // canfly: release only, no install [04 R-ORD-01 §1]
	}
	n.GoalX, n.GoalY, n.GoalZ = x, y, z
}

// targetOf reads the record's target smart-reference through the owning
// queue's binding [P0-I16]. A record with no target, or one whose target the
// binding cannot resolve, yields nil.
func targetOf(u *units.Unit, n *Node) *units.Unit {
	if n == nil || n.Target == 0 || u == nil {
		return nil
	}
	q := QueueForUnit(u)
	if q == nil {
		return nil
	}
	binding := q.Binding()
	if binding == nil || binding.Lookup == nil {
		return nil
	}
	return binding.Lookup(n.Target)
}

// leashBroken is the return-to-post test [R-STANCE-01 §4] that both the ground
// chase and the four air executors apply: with a nonzero leash word, the whole
// world-unit planar distance from the record's anchor pair is truncated to an
// integer and the order ends when `leash <= distance` [04 R-AIR-01 §8] step 5.
// The compare is inclusive, so a leash of 0 is "no leash" rather than "never
// move".
//
// `trunc(hypot(dx,dz)) >= leash` is exactly `dx² + dz² >= leash²` over
// non-negative integers, so the test is done in 64-bit integers and never
// touches a square root or a float (I2).
func leashBroken(u *units.Unit, n *Node) bool {
	if n == nil || n.Param3 == 0 || u == nil {
		return false
	}
	dx := int64(u.X.Raw()>>16) - int64(n.GuardX)
	dz := int64(u.Z.Raw()>>16) - int64(n.GuardY)
	leash := int64(n.Param3)
	return dx*dx+dz*dz >= leash*leash
}

// ---------------------------------------------------------------------------
// Attack_NoMove [04 R-ORD-01 §3]
// ---------------------------------------------------------------------------

// attackNoMoveHandler is the stationary attack.
//
//	Pre-check: target null, or satisfied ∩ 0x10008 (target lost, or target
//	cloaked — §6) → complete. Phase 0: caption clear; advance. Phase 1: release
//	slot 0, bind slot 0 to the target, gate = 0x11808; advance. Phase 2 (reached
//	when any of those bits arrive): inhibit all slots; re-arm (9). Other:
//	cancel-all.
//
// The unit never moves; the weapon layer fires from the bound slot. The gate
// `0x11808` is the cloak bit `0x10000`, the engage/disengage pair `0x1000` and
// `0x800`, and the target-removed bit `0x8` [04 R-ORD-01 §0]; the record sits
// on it until one of them arrives, which is the row's wait and not a stall.
func attackNoMoveHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	if n.Target == 0 || satisfied&pendTargetGone != 0 {
		return Code(5) // *complete* [04 R-ORD-01 §3]
	}
	switch n.Phase {
	case 0:
		captionClear(u)
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 1:
		releaseSlot(u, 0)
		bindSlotToUnit(u, 0, n.Target)
		n.DynamicGate = 0x11808
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 2:
		inhibitSlot(u, slotAll)
		return Code(9) // *re-arm* [04 R-ORD-01 §3]
	default:
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
}

// ---------------------------------------------------------------------------
// Attack_Kamikaze [04 R-ORD-01 §3]
// ---------------------------------------------------------------------------

// attackKamikazeHandler drives the unit onto its target and detonates it.
//
//	Pre-check: satisfied ∩ 0x10008 → complete. Every visit with a target:
//	goal = target position. Phase 0: carried → cancel-all; caption clear; point
//	goal at the goal with radius max(16, kamikazedistance); deadline 60; gate |=
//	0xE0; advance. Phase 1: satisfied 0x20 → status 6 (`Arrived`), spawn
//	SelfDestruct with p1 = 1 (no countdown: immediate 30000 self-damage, cause
//	3) at the head, complete; satisfied 0x40 → abandon; else phase = 0, hold
//	(the goal is re-issued on the next visit — deadline expiry or 0x80). Other:
//	cancel-all.
//
// The 60-tick deadline is the watchdog that re-issues the goal when none of
// the three movement outcomes arrives: phase 1's `phase = 0` hold leaves the
// record at phase 0 with gate 0xE1 standing, so the walk blocks on that gate
// [04 §3.3] step 3 and the next visit re-runs phase 0 only once the deadline
// expiry raises bit 0 (or a movement bit arrives). WU-18-4 could not form it —
// the handler had no tick then, and arming bit 0 with no deadline behind it
// would have parked the record on a bit nothing raises, while a hold with no
// gate at all spins the pump inside one visit. WU-18-7 gave the handler the
// tick [04 R-ORD-01 §1].
func attackKamikazeHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if satisfied&pendTargetGone != 0 {
		return Code(5) // *complete* [04 R-ORD-01 §3]
	}
	if tgt := targetOf(u, n); tgt != nil {
		n.GoalX, n.GoalY, n.GoalZ = tgt.X, tgt.Y, tgt.Z // every visit with a target
	}
	switch n.Phase {
	case 0:
		if u != nil && u.Attachment.Carrier != 0 {
			return Code(7) // carried → *cancel-all* [04 R-ORD-01 §3]
		}
		captionClear(u)
		radius := int32(16)
		if u != nil && u.Def != nil && u.Def.KamikazeDistance > radius {
			radius = u.Def.KamikazeDistance // max(16, kamikazedistance)
		}
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, radius)
		// "deadline 60; gate |= 0xE0": the deadline setter stores
		// `current tick + 60` and ORs bit 0 [04 R-ORD-01 §1], then the row's
		// own three movement bits are ORed on top.
		armDeadline(n, tick, 60)
		n.DynamicGate |= 0xE0
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 1:
		if satisfied&0x20 != 0 {
			// Status 6 (`Arrived`) is the caption half [04 R-ORD-01 §1]; see
			// captionClear's TODO(T25) for why it is not emitted here.
			spawnImmediateSelfDestruct(u, n)
			return Code(5) // *complete* [04 R-ORD-01 §3]
		}
		if satisfied&0x40 != 0 {
			return Code(8) // *abandon* [04 R-ORD-01 §3]
		}
		n.Phase = 0
		return Code(2) // *hold* with the phase already reset [04 R-ORD-01 §3]
	default:
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
}

// spawnImmediateSelfDestruct head-inserts the `SelfDestruct` record the
// kamikaze arrival names: p1 = 1, which is the row's "no countdown" path —
// `SelfDestruct` with p1 nonzero applies 30000 damage to itself with damage
// cause 3 and completes [04 R-ORD-01 §2].
//
// The head insert is [04 R-ORD-01 §1]'s: the spawned record becomes the head
// and runs before the spawning one resumes — here the kamikaze record completes
// in the same visit, so the self-destruct is what the unit runs next.
//
// Correction (WU-18-1, merge). This unconditionally pushed onto the head of the
// PRIMARY segment. [04 R-ORD-01 §1] puts the head insert at "the front of the
// segment the record's rear-segment flag selects", and `SelfDestruct` carries
// that flag — [04 R-ORD-01 §2]'s own row ends "the record lives on the rear
// segment ([R-ORDER-02 §1])", which is what makes it run purely on its own
// deadline with an empty satisfied set. Putting it on the primary head instead
// gave the spawned record the wrong pump and blocked every order behind it.
// `Standby_Mine` reaches this same site, so the routing is shared.
func spawnImmediateSelfDestruct(u *units.Unit, n *Node) {
	id := Lookup("SelfDestruct")
	if id == 0 || u == nil || n == nil {
		return
	}
	q := QueueOfUnit(u)
	if q == nil {
		return
	}
	spawned := Node{
		Owner:        u.Handle,
		GoalX:        n.GoalX,
		GoalY:        n.GoalY,
		GoalZ:        n.GoalZ,
		Param1:       1, // p1 = 1: immediate, no countdown [04 R-ORD-01 §2]
		CreationTick: n.CreationTick,
	}
	if isSecondary(id) {
		q.PushSecondary(id, spawned) // the rear segment head-inserts too [04 §3.3]
		return
	}
	q.PushHead(id, spawned)
}

// ---------------------------------------------------------------------------
// AttackSpecial [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// attackSpecialHandler is the special-weapon attack, whose whole body is a
// re-identification:
//
//	Resolve command code 3 (attack a unit, §3.4) against the record's target
//	with no position, re-identify **this record** as the resolved descriptor
//	(keeping mask bits 0x600), set p1 = 2, return hold (2). The record runs the
//	resolved attack handler from its next visit with the weapon-slot selection 2.
//
// Returning *hold* leaves the record at the head with its phase untouched, so
// the same pump pass re-dispatches it — now under the resolved descriptor's
// handler, which sees phase 0 and p1 = 2 [04 §3.3].
func attackSpecialHandler(u *units.Unit, n *Node, _ uint32, _ uint32) Code {
	resolved := Resolve(3, u, targetOf(u, n), nil)
	if resolved == 0 {
		// TODO(question): [04 R-ORD-01 §2]'s row does not say what the handler
		// does when the resolver rejects — it describes only the resolving
		// case, and §3.4's resolver returns the reject sentinel for a missing
		// target or a unit that cannot make the attack. Re-identifying the
		// record as the sentinel descriptor would leave it with no handler at
		// all, and returning *hold* unchanged would re-dispatch this same body
		// forever inside one pump pass [04 §3.3]. The placeholder is the
		// outcome every other row in [04 R-ORD-01 §3] gives for "there is
		// nothing to attack" — target null → complete — recorded as a queue
		// diagnostic so the failure is visible. A trace of this handler's
		// reject arm settles it.
		if q := QueueForUnit(u); q != nil {
			q.recordDiagnostic("orders: AttackSpecial could not resolve command code 3 against its target, completed")
		}
		return Code(5)
	}
	n.ID = resolved
	// "keeping mask bits 0x600": the record's static-mask copy becomes the
	// resolved descriptor's, with its own 0x600 bits carried across
	// [04 R-ORD-01 §2][04 §3.2].
	n.StaticGate = DescriptorFor(resolved).StaticGate | (n.StaticGate & 0x600)
	n.Param1 = 2 // weapon-slot selection 2
	return Code(2)
}

// ---------------------------------------------------------------------------
// AttackUType [04 R-ORD-01 §3]
// ---------------------------------------------------------------------------

// attackUTypeHandler is the hunt: p1 is the definition index to hunt.
//
//	Phase 0: definition must have `canattack` (else cancel-all); deadline
//	RNG(90) + 1; advance. Phase 1: scan every live unit from the second slot on
//	whose definition index equals p1 and whose owner is hostile to mine; score
//	each as d² − RNG(d²/2) with d² the whole-unit squared planar distance; keep
//	the lowest score (ties → later unit). None → complete. Else resolve command
//	code 3 against it, spawn the resolved attack record (target = it) at the
//	head, restart. Other phase: cancel-all.
//
// TODO(T25): phase 1's scan has no input. The scan walks the live unit array
// from slot 1 onward; the queue's binding [P0-I16] exposes `Lookup(handle)` and
// `Hostility(actor, target)` but no enumerator, and the world the pump holds is
// not reachable from a queue. Placeholder: with no candidates the row's own
// "None → complete" arm runs, which is the same outcome retail reaches when
// nothing of that definition is hostile and in play. The per-candidate
// `RNG(d²/2)` draw (subject to [04 R-ORD-01 §1]'s rule that a bound below 2
// returns 0 without advancing the stream) belongs with the enumerator and is
// not taken either.
//
// WU-18-7 examined this and left it: closing it needs two things this package
// cannot settle. (a) A slot-ASCENDING live-unit enumerator on the binding —
// the session's existing `units.World.VisitActiveSlots` is player-major, a
// different order, and the row's "ties → later unit" makes iteration order
// behavior (I1). The producer of the binding is the session, not this package.
// (b) An answer to which index space p1 is in: the row says "definition
// index", and the only producer in this build (the initial-mission `a name`
// verb) stores its catalog index there alongside the canonical key. Treating
// the two as the same numbering is an assumption, not a finding.
//
// Phase 0's "deadline RNG(90) + 1" is armed from the handler's tick argument
// (WU-18-7); WU-18-4 could not form it and took no draw, because the draw
// exists only to size that wait and taking it would have moved every later
// simulation draw (I4). It is taken now, once per hunt, from the unit's own
// queue binding [04 R-ORD-01 §1][I4].
func attackUTypeHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	switch n.Phase {
	case 0:
		if u == nil || u.Def == nil || !u.Def.CanAttack {
			return Code(7) // *cancel-all* [04 R-ORD-01 §3]
		}
		armDeadline(n, tick, drawBelow(u, 90)+1) // "deadline RNG(90) + 1"
		return Code(1)                           // *advance* [04 R-ORD-01 §3]
	case 1:
		if q := QueueForUnit(u); q != nil {
			q.recordDiagnostic(fmt.Sprintf("orders: AttackUType hunt for definition index %d found no candidate: the queue binding exposes no live-unit enumerator", n.Param1))
		}
		return Code(5) // none → *complete* [04 R-ORD-01 §3]
	default:
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
}

// ---------------------------------------------------------------------------
// Suppress [04 R-ORD-01 §3]
// ---------------------------------------------------------------------------

// suppressHandler is fire at a position.
//
//	Pre-check: satisfied 0x800 → complete. Phase 0: `canfly` → abandon; caption
//	clear; p2 = the engagement distance of slot p1; advance. Phase 1: p1 = 2 →
//	release all slots, bind slot 2 to the goal position; else release slots 0
//	and 1 and bind both to the goal; gate = 0x1C00; advance. Phase 2: inhibit
//	all; satisfied 0x400 → phase = 1, rotate; else with a mover: p2 < 1 →
//	re-arm; point goal at the goal radius p2, gate = 0xE0,
//	p2 −= RNG(engagementDistance(p1) / 3), phase = 1, return 4 — each wake walks
//	the unit closer by a random fraction of a third of its range. No mover →
//	re-arm. Other: cancel-all.
//
// With engagementDistance reporting 0 (the open question recorded at that
// helper, whose decider is a static trace of it), phase 0 stores p2 = 0 and phase 2 takes its own established `p2 < 1` arm, so the
// walk-closer leg — the only consumer of the unknown distance — is simply not
// reached. No distance is invented to reach it.
func suppressHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	if satisfied&0x800 != 0 {
		return Code(5) // *complete* [04 R-ORD-01 §3]
	}
	switch n.Phase {
	case 0:
		if u != nil && u.Def != nil && u.Def.CanFly {
			return Code(8) // *abandon* [04 R-ORD-01 §3]
		}
		captionClear(u)
		n.Param2 = uint32(engagementDistance(u, n.Param1))
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 1:
		if n.Param1 == 2 {
			releaseSlot(u, slotAll)
			bindSlotToPosition(u, 2, n.GoalX, n.GoalZ)
		} else {
			releaseSlot(u, 0)
			releaseSlot(u, 1)
			bindSlotToPosition(u, 0, n.GoalX, n.GoalZ)
			bindSlotToPosition(u, 1, n.GoalX, n.GoalZ)
		}
		n.DynamicGate = 0x1C00
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 2:
		inhibitSlot(u, slotAll)
		if satisfied&0x400 != 0 {
			n.Phase = 1
			return Code(6) // *rotate* [04 R-ORD-01 §3]
		}
		if u == nil || u.Def == nil || !u.Def.CanMove {
			return Code(9) // no mover → *re-arm* [04 R-ORD-01 §3]
		}
		if n.Param2 < 1 {
			return Code(9) // *re-arm* [04 R-ORD-01 §3]
		}
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, int32(n.Param2))
		n.DynamicGate = 0xE0
		step := engagementDistance(u, n.Param1) / 3
		if q := QueueForUnit(u); q != nil {
			if sim := q.simForJitter(); sim != nil {
				// The draw helper returns 0 WITHOUT advancing the state for a
				// bound below 2, so a collapsed third of the range costs no
				// random state [04 R-ORD-01 §1][I4].
				n.Param2 -= sim.Uint32n(uint32(step))
			}
		}
		n.Phase = 1
		return Code(4) // *hold* with the phase already reset [04 R-ORD-01 §3]
	default:
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
}

// ---------------------------------------------------------------------------
// AirStrike, AirToAir, AirToGround, AirToGroundHover — the shared entry
// sequence of [04 R-AIR-01 §8]
// ---------------------------------------------------------------------------

// airEntry runs the entry sequence the four air-attack executors share, in the
// order [04 R-AIR-01 §8] fixes, and reports the result code when one of its
// arms ends the order.
//
//  1. If the satisfied set intersects 0x1000A (`AirStrike`, `AirToGround`) or
//     0x10008 (`AirToGroundHover`): ... return 5 either way.
//  2. If the target reference is null but the record's 0x200 "cached goal
//     valid" bit is set, replace the current order with `VTOL_SeekAttack` at
//     the unit's own position and return 5.
//  3. If the target reference is live, refresh the record's cached goal from
//     the target's current position every visit — the cached goal trails a
//     live target and stands in for it once it is gone; it is never a fixed
//     aim point.
//  4. Off-map recovery ([04 R-AIR-01 §5]).
//  5. The maneuver leash ... return 5 when `leash <= distance`.
//
// Step 1's own text makes the return unconditional ("return 5 either way"), so
// the arm is reproduced as a completion; what is NOT reproduced is the
// replacement it performs first.
//
// TODO(question): step 1's replacement is gated on "the record has no successor
// marker **and** the unit's status word has either of bits 0x300000", and step
// 2's on "the record's 0x200 'cached goal valid' bit". [04 R-AIR-01 §8] names
// neither field: the successor marker is not one of the record fields
// [04 §3.2] enumerates, the two status-word bits are not in [04 §2.4]'s census,
// and the 0x200 bit is stated without saying which of the record's words holds
// it — the record's pending word's 0x200 is the path-search "start out of
// bounds" bit [04 R-ORD-01 §0], which is not a cached-goal validity flag. So
// the conditions cannot be evaluated and the `VTOL_SeekAttack` replacements are
// not issued; the established return code is. A trace naming those three fields
// settles it.
//
// TODO(T25): step 4 is not reproduced. Off-map recovery is [04 R-AIR-01 §5], a
// leg of internal/movement's air marker family, and `AirToGround`'s divergence
// from it ("sets the record's deadline to the current tick plus 30, forces the
// phase to 2, and falls through") needs both that family and the tick the
// primary pump does not pass down. Placeholder: the step is skipped, so an
// aircraft that has left the map does not take the recovery leg.
func airEntry(u *units.Unit, n *Node, satisfied uint32, interruptMask uint32) (Code, bool) {
	if satisfied&interruptMask != 0 {
		return Code(5), true // step 1, "return 5 either way"
	}
	tgt := targetOf(u, n)
	if tgt == nil {
		if n.Target == 0 {
			return Code(5), true // step 2's completion, without its replacement
		}
	} else {
		// Step 3: the cached goal follows the target every visit.
		n.GoalX, n.GoalY, n.GoalZ = tgt.X, tgt.Y, tgt.Z
	}
	if leashBroken(u, n) {
		return Code(5), true // step 5, the maneuver leash [R-STANCE-01 §4]
	}
	return Code(0), false
}

// airAttackHandler is the descriptor handler the four air-attack executors
// share. It runs the entry sequence above and then stops.
//
// TODO(T25): the four phase machines are not here. [04 R-AIR-01 §8] builds
// every leg of `AirStrike`, `AirToGround`, `AirToGroundHover` and `AirToAir`
// out of air path markers — the point marker, the frozen terrain-relative
// marker, the follow-unit marker, the takeoff preamble, and, for `AirToAir`,
// a second payload class that carries a velocity and steers it toward a
// commanded heading. That whole family is internal/movement's
// (`airorders.go`, which already owns `VTOL_Move`, `VTOL_LandIfCan` and
// `VTOL_Standby` for exactly this reason), it is unexported, and this package
// cannot import it — internal/movement imports internal/orders, not the other
// way round. Writing half a leg here and half there would be worse than saying
// so, so the bodies belong beside their twins in internal/movement.
//
// Placeholder: the record completes with a queue diagnostic naming the order.
// Completion is bounded and visible, and it is the code three of the entry
// sequence's own four arms return; the alternative — arming a leg's gate
// (0x100E8, 0xE2, 0x110E8) with no marker behind it to raise those bits — is
// exactly the silent forever-park PLAN 18 exists to remove.
func airAttackHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	mask := airInterruptMask(n.ID)
	if code, done := airEntry(u, n, satisfied, mask); done {
		return code
	}
	if q := QueueForUnit(u); q != nil {
		q.recordDiagnostic(fmt.Sprintf("orders: %s entry sequence ran but its attack legs live in internal/movement's air marker family, completed", DescriptorFor(n.ID).Name))
	}
	return Code(5)
}

// airInterruptMask is step 1's per-order mask: 0x1000A for `AirStrike` and
// `AirToGround`, 0x10008 for `AirToGroundHover` [04 R-AIR-01 §8].
//
// TODO(question): [04 R-AIR-01 §8] gives the mask for three of the four
// executors and omits `AirToAir` from both lists, while calling step 1 part of
// the sequence "all four share". Which of the two masks `AirToAir` takes is
// therefore unstated, and the two differ by pending bit `0x2` — the
// cancel-current notification [04 R-ORD-01 §0], whose producer is a removal
// path. The narrower `0x10008` is used, so an `AirToAir` record is not ended by
// a bit its own section never lists for it. A trace of that executor's entry
// test settles it.
func airInterruptMask(id ID) uint32 {
	switch DescriptorFor(id).Name {
	case "AirStrike", "AirToGround":
		return 0x1000A
	default:
		return pendTargetGone // 0x10008
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// combatHandlers is the family's registration list, a slice so that source
// order is registration order (I1).
var combatHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"Attack_NoMove", attackNoMoveHandler},
	{"Attack_Kamikaze", attackKamikazeHandler},
	{"AttackSpecial", attackSpecialHandler},
	{"AttackUType", attackUTypeHandler},
	{"Suppress", suppressHandler},
	{"AirStrike", airAttackHandler},
	{"AirToAir", airAttackHandler},
	{"AirToGround", airAttackHandler},
	{"AirToGroundHover", airAttackHandler},
}

// ensureCombatHandlers installs this family onto the descriptor table. It is
// idempotent and assigns only where the descriptor still has no handler, so
// running the installer list again is a no-op, and it tolerates a table that
// has not been built yet — this file's init may run before table.go's.
func ensureCombatHandlers() {
	if len(table) == 0 {
		return
	}
	for _, entry := range combatHandlers {
		id := Lookup(entry.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = entry.handler
		}
	}
}

func init() { ensureCombatHandlers() }
