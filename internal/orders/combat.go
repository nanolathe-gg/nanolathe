package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	// The attack family's disengage bit, the one `Attack_Chase` and `Suppress`
	// complete on in their first pre-check [04 R-ORD-01 §3].
	pendDisengage uint32 = 0x800
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
// Corrected 2026-08-31 [04 R-ORD-01 §7]. This header used to say that
// [R-ORDER-02 §2]'s control-byte halves — "bit 1 set (slot assigned) and bit 4
// clear" — had "nowhere to live" in this build and could not be evaluated, so
// release and inhibit both collapsed into an unconditional target clear. Both
// bits are now traced: bit 1 is *the slot is enabled* and bit 4 is the inhibit
// latch, and the guard lives on releaseSlot and inhibitSlot below. What stays
// here is the third half — the already-empty test — which retail applies AFTER
// the control-byte write, not instead of it.
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

// slotEnabled is the control byte's bit 1, "slot assigned" [R-ORDER-02 §2], as
// [04 R-ORD-01 §7] renames it: the *slot is enabled* bit. It is the same bit
// the command resolver reads as "my slot 1 is enabled" in its code-3
// water-weapon rejects [R-ORD-02 §1].
//
// Its writer is settled [06 R-WPN-05 §3]: the weapon-slot initializer unit
// construction runs, and nothing else, so a slot is never disabled during play
// and the bit stands for exactly "the weapon link resolved". Save load restores
// it wholesale, which is the one path that can part it from the resolved
// pointer — so the byte, not the pointer, is what this reads. That closes
// [04 R-ORD-01 §7]'s "no runtime writer of bit 1 was found ... whether a slot
// can be disabled after load is open".
func slotEnabled(s *units.Slot) bool { return s.IsEnabled() }

// releaseSlot is "*release slot k*: clears that bit and clears the target"
// [04 R-ORD-01 §1], under the guard [04 R-ORD-01 §7] recovers: the slot's
// control byte must have bit 1 set (the slot is enabled) AND bit 4 set (it is
// currently inhibited). Bit 4 is then cleared, and only then is the target
// cleared with its notification.
//
// Corrected 2026-08-31. This cleared the target of every selected slot
// unconditionally. Retail releases only a slot it had previously inhibited, so
// the unconditional form destroyed autonomously acquired targets on slots the
// order never touched: `Attack_Chase` phase 1 releases slots 0 and 2 on its way
// to binding one of them, and under the old form that silenced whichever of
// those two the acquisition path had just armed.
func releaseSlot(u *units.Unit, k int) {
	eachSlot(u, k, func(idx int) {
		s := u.SlotAt(idx)
		if s == nil || !slotEnabled(s) || s.Flags&units.SlotFlagAutonomous == 0 {
			return
		}
		s.Flags &^= units.SlotFlagAutonomous
		if !rulesOfUnit(u).PreserveAutomaticTarget(u, idx) {
			clearSlotTarget(u, idx)
		}
	})
}

// inhibitSlot is "*inhibit slot k*: sets the slot's control-byte bit 4 and
// clears its target" [04 R-ORD-01 §1], under the mirrored guard: bit 1 set and
// bit 4 **clear**. Inhibiting an already-inhibited slot is a no-op, which is
// what makes a handler that inhibits on every wake — `Suppress` phase 2,
// `Guard_NoMove` phase 0 — emit one TargetCleared rather than one per wake.
func inhibitSlot(u *units.Unit, k int) {
	eachSlot(u, k, func(idx int) {
		s := u.SlotAt(idx)
		if s == nil || !slotEnabled(s) || s.Flags&units.SlotFlagAutonomous != 0 {
			return
		}
		s.Flags |= units.SlotFlagAutonomous
		clearSlotTarget(u, idx)
	})
}

// defaultAttackSlot is the weapon-slot pick `Attack_Chase` phase 0 takes when
// the record carries no slot yet [04 R-ORD-01 §3][04 R-ORD-01 §7]: the lowest
// slot index whose control byte says the slot is enabled, and 0 when none is.
//
// Retail's helper returns 0 both for "slot 0 is enabled" and for "no slot is
// enabled", because its third arm returns the *bit value* 2 — which doubles as
// the index — and 0 when that bit is clear. The two cases are indistinguishable
// by construction, so this reproduces them as one.
func defaultAttackSlot(u *units.Unit) int {
	if u == nil {
		return 0
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		if s := u.SlotAt(idx); s != nil && slotEnabled(s) {
			return idx
		}
	}
	return 0
}

// bindSlotToUnit is "*bind slot k to unit*: stores the target's unit id word
// with the unit-companion marker" [04 R-ORD-01 §1]. The marker is this build's
// units.TargetUnit kind, which is what "read slot k target yields the unit only
// while the companion carries the unit marker and the id is nonzero" tests.
func bindSlotToUnit(u *units.Unit, k int, target pool.Handle) {
	clearSlotSetterBits(u)
	eachSlot(u, k, func(idx int) {
		if s := u.SlotAt(idx); s != nil {
			s.Target = units.Target{Kind: units.TargetUnit, Unit: target}
		}
	})
}

// clearSlotSetterBits is the half of both slot target setters that touches the
// owner rather than the slot: they clear bits 10-14 of the order-event word
// [04 R-ORD-01 §7] [06 R-WPN-05 §6]. Binding a new target therefore discards a
// stale "could not fire" (0x1000), which is what stops a disengage raised
// against the previous target from completing the order that just replaced it.
func clearSlotSetterBits(u *units.Unit) {
	if u != nil {
		u.Pending &^= units.PendingSlotSetterClear
	}
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
	clearSlotSetterBits(u)
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
// and draws a third of, and the standoff `d` every `Attack_Chase` orbit
// substate is built from [04 R-ORD-01 §3].
//
// Closed 2026-08-31 by [06 R-WPN-05 §1]: the weapon-slot engagement-distance
// helper reads the slot's resolved weapon record and returns its authored
// `range` — the same whole-world-unit integer the shot-time range test squares
// [06 §3.3]. The standoff is therefore the weapon's own range, so a unit orbits
// at exactly the distance from which it can shoot.
//
// This replaces a placeholder that returned 0 for every slot. [04 §3.9]'s
// missing list carried the value as "produced by the weapon-slot
// engagement-distance helper and remains inference"; that item is now closed,
// and the zero it stood in for was not neutral — it collapsed every chase
// substate onto the target's own position and drove `Suppress` phase 2 down its
// `p2 < 1` *re-arm* arm on the first wake.
//
// A slot with no resolved weapon has no range and reports 0, which is what the
// caller's own guards already expect.
func engagementDistance(u *units.Unit, slot uint32) int32 {
	if u == nil || slot >= uint32(units.NumSlots) {
		return 0
	}
	s := u.SlotAt(int(slot))
	if s == nil || s.Weapon == nil {
		return 0
	}
	return s.Weapon.Range
}

// captionClear is [04 R-ORD-01 §1]'s "caption clear": the one-shot helper that
// clears the record's runtime caption-pending bit and emits status kind 5
// (`ok`) with no text.
func captionClear(u *units.Unit, n *Node) { captionClearText(u, n, "") }

// captionClearText is the same helper called "with a state text" — the form
// `RepairUnit` ("Repairing"), `Capture` ("Capturing"), the air work preamble
// and the two transport rows use [04 R-ORD-01 §1].
//
// ONE-SHOT is the whole point of the helper, and it was missing: it emits only
// for a record whose caption-pending flag is still set, and clears the flag
// first [04 §3.2]. A `Move_Ground` that cannot occupy its goal cell re-arms
// every 30..59 ticks for the life of the record, re-entering phase 0 each time
// ([04 R-PATH-01 §14] item 4); with an unconditional emit here every one of
// those re-entries raised the `ok` acknowledgement voice, so a settled group of
// movers chattered forever. §14 states the steady state instead: "silent and
// unbounded ... no motion, no engine cue". `Patrol`, which re-runs its caption
// clause once per lap, had the same defect.
func captionClearText(u *units.Unit, n *Node, text string) {
	if n == nil || !n.CaptionPending {
		return
	}
	n.CaptionPending = false
	workStatus(u, statusOK, text)
}

// NotifyCaptionClear is the exported form of the one-shot caption clear, for
// the air transport legs whose executors live in internal/movement
// [04 R-ORD-01 §1][04 §10.2]. It is the same helper: kind 5 once per record,
// never again.
func NotifyCaptionClear(u *units.Unit, n *Node, text string) { captionClearText(u, n, text) }

// installPointGoal is the point form of [04 R-ORD-01 §1]'s goal installers:
// "a **point** goal at a position with an arrival radius ... skip the install
// entirely — release only — when the owner's definition has the `canfly` bit,
// and finish by clearing pending bits `0x20`–`0x200`".
//
// The helper performs all three halves of that contract by routing through the
// queue binding's movement adapter, which owns the payload registry:
// `InstallPoint` releases THIS record's previous payload and carries the
// arrival radius into the point goal; `Release` is the canfly release-only arm.
// The pending clear is applied here afterwards because retail clears
// `0x20`–`0x200` at the end of all four helpers, including the release-only
// arm, so the release's own `0x80` — raised on the record being installed for,
// which is the only record an installer touches [04 R-ORD-01 §0]
// [04 R-ORD-01 §1] — is cleared again by its own installer.
//
// Corrected 2026-08-31 (first correction): this carried an accepted-blocked marker saying the
// radius had nowhere to go and the previous payload could not be found, so the
// radius was dropped (`_ = radius`) and no release ran at all. Both seams are
// in this package already — `PointGoalRequest.Radius` and
// `MovementGoalAdapter.Release`, which `installWorkGoalWithRadius` next door
// has been using — and the movement side has implemented the release and the
// pending clear all along.
//
// Corrected 2026-08-31 (second correction): that first correction also read
// [04 R-ORD-01 §0]'s "a previous goal object is released" as licence to raise
// `0x80` on whichever OTHER record held the mover's payload slot, and the
// movement side did so. It froze every ground mover in the game — the
// root-cause note is on `installGroundPayload` in internal/movement/goals.go.
// An installer's `0x80` never leaves the record it is installing for.
func installPointGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed, radius int32) {
	if installPointGoalPayload(u, n, x, y, z, radius) {
		n.GoalX, n.GoalY, n.GoalZ = x, y, z
	}
}

// installPointGoalPayload is the whole of the point installer's contract —
// release the record's previous payload, install (unless the owner has
// `canfly`, which is release only), clear pending `0x20`-`0x200`
// [04 R-ORD-01 §1] — WITHOUT mirroring the installed position into the
// record's goal triple. It reports whether an install happened, which is the
// only condition under which installPointGoal writes that mirror.
//
// The split exists for `Follow_Ground`, whose goal triple holds the anchor
// OFFSET from the ward rather than a position [04 R-ORD-01 §8 point 2]: its
// maintenance leg installs at `ward + offset` every 30 ticks, and mirroring
// that sum into the triple would overwrite the offset it is computed from,
// which is also the value a reader of the guard record's goal is supposed to
// see. Every other caller keeps the mirror.
func installPointGoalPayload(u *units.Unit, n *Node, x, y, z numeric.Fixed, radius int32) bool {
	if n == nil {
		return false
	}
	canfly := u != nil && u.Def != nil && u.Def.CanFly
	if b := bindingOfUnit(u); b != nil && b.Movement != nil {
		if canfly {
			if b.Movement.Release != nil {
				b.Movement.Release(n)
			}
		} else if b.Movement.InstallPoint != nil {
			b.Movement.InstallPoint(PointGoalRequest{Owner: n.Owner, Node: n, X: x, Y: y, Z: z, Radius: radius})
		}
	}
	n.Satisfied &^= 0x3E0 // clear pending 0x20..0x200 [04 R-ORD-01 §0][04 R-ORD-01 §1]
	return !canfly        // canfly: release only, no install [04 R-ORD-01 §1]
}

// installAnnulusGoal is the banded form of [04 R-ORD-01 §1]'s goal installers:
// a **banded** goal at a position with an outer and an inner radius. It is the
// installer `Attack_Chase` substates 7 and 8 reach [04 R-ORD-01 §3], carrying
// the same three obligations as the point form — release the record's previous
// payload, install, and clear pending `0x20`-`0x200` — and the same canfly
// release-only arm, because both installers end in the one shared tail.
//
// The chase never reaches this on an aircraft (its phase 0 rejects `canfly`
// outright), but the arm is written here rather than assumed away: an installer
// that behaved differently on the two families would be a second contract.
func installAnnulusGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed, outer, inner int32) {
	if n == nil {
		return
	}
	canfly := u != nil && u.Def != nil && u.Def.CanFly
	if b := bindingOfUnit(u); b != nil && b.Movement != nil {
		if canfly {
			if b.Movement.Release != nil {
				b.Movement.Release(n)
			}
		} else if b.Movement.InstallAnnulus != nil {
			b.Movement.InstallAnnulus(AnnulusGoalRequest{
				Owner: n.Owner, Node: n, X: x, Y: y, Z: z,
				OuterRadius: outer, InnerRadius: inner,
			})
		}
	}
	n.Satisfied &^= 0x3E0 // clear pending 0x20..0x200 [04 R-ORD-01 §0][04 R-ORD-01 §1]
	if canfly {
		return
	}
	n.GoalX, n.GoalY, n.GoalZ = x, y, z
}

// bindingOfUnit reads a unit's queue binding without creating a queue.
func bindingOfUnit(u *units.Unit) *QueueBinding {
	q := QueueOfUnit(u)
	if q == nil {
		return nil
	}
	return q.Binding()
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
//	Pre-check: target null, or satisfied ∩ 0x10808 (target lost, target
//	cloaked — §6 — or the attack family's 0x800) → complete. Phase 0: caption
//	clear; advance. Phase 1: release slot 0, bind slot 0 to the target, gate =
//	0x11808; advance. Phase 2 (reached only when 0x1000 arrives — the other
//	three gate bits complete in the pre-check first): inhibit all slots; re-arm
//	(9). Other: cancel-all.
//
// The unit never moves; the weapon layer fires from the bound slot. The gate
// `0x11808` is the cloak bit `0x10000`, the engage/disengage pair `0x1000` and
// `0x800`, and the target-removed bit `0x8` [04 R-ORD-01 §0]; the record sits
// on it until one of them arrives, which is the row's wait and not a stall.
//
// The pre-check mask is `0x10808`, not the `0x10008` this used to test
// (corrected 2026-09-02, [04 R-ORD-01 §3]): `0x800` completes the order here
// exactly as it does in `Attack_Chase`'s first pre-check, so of the four bits
// the gate waits on, only `0x1000` ever reaches phase 2.
//
// `0x1000` is the "could not fire" bit [06 R-WPN-05 §6]: the weapon layer
// raises it when a shot-time gate fails or a turret's aim geometry yields no
// solution, and the pump hands it to the first record whose gate names it.
// Phase 2 is this handler's DISENGAGE arm — "my weapon tried and could not" —
// and it is the only place in the build that consumes the bit. The weapon
// layer never reads it back, so it gates no firing decision.
func attackNoMoveHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	if n.Target == 0 || satisfied&(pendTargetGone|pendDisengage) != 0 {
		return Code(5) // *complete* [04 R-ORD-01 §3]
	}
	switch n.Phase {
	case 0:
		// Phase 0 is the caption clear alone. The handler holds exactly ONE
		// inhibit-all-slots call and it is phase 2's, so nothing returns the
		// three slots to autonomy here [04 R-ORD-01 §3] — traced, and
		// [04 R-UNIT-06 §5]'s "phase 0 returns all three" is corrected there.
		// Adding one would not be inert: inhibiting returns a slot an earlier
		// order had taken, resets that slot's target and posts `TargetCleared`
		// [04 R-ORD-01 §7].
		captionClear(u, n)
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
		captionClear(u, n)
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
			// Status 6 (`Arrived`) is the caption half [04 R-ORD-01 §1].
			workStatus(u, statusArrived, "Arrived")
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
	id := rowSelfDestruct
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
		GoalSupplied: true, // the arrival point the kamikaze record carried
		Param1:       1,    // p1 = 1: immediate, no countdown [04 R-ORD-01 §2]
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
		// Closed by [04 R-ORD-01 §12]: the handler has NO reject arm. Its body
		// is three statements with no branch, so a rejected resolution simply
		// re-identifies the record as the reject sentinel — descriptor 0 — and
		// the pump re-dispatches it in the same pass under descriptor 0's
		// handler, which returns 5 (complete) unconditionally: it reads
		// nothing, writes nothing, and emits no caption. The record therefore
		// completes silently, which is what this arm returns directly; the
		// earlier queue diagnostic was ours, not retail's, and is withdrawn.
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
		if target := scanAttackUType(u, n.Param1); target != nil {
			id := Resolve(3, u, target, nil)
			if id != 0 {
				q := QueueOfUnit(u)
				if q != nil {
					q.PushHead(id, NewNodeForOrder(id, target.Handle, target.X, target.Y, target.Z, tick, u.Handle, false))
					return Code(0) // restart with the spawned attack at the head
				}
			}
		}
		return Code(5) // none (or rejected resolution) → *complete* [04 R-ORD-01 §3]
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
		captionClear(u, n)
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
// Guard_NoMove [04 R-ORD-01 §3]
// ---------------------------------------------------------------------------

// guardNoMoveHandler is the stationary guard: scan, bind, count shot attempts,
// re-scan. It installs no goal of any kind and never moves the unit.
//
//	Pre-check: satisfied ∩ 0x10008 → phase = 3, hold (jump to the scan).
//	Phase 0: inhibit all; deadline 30; advance. Phase 1: read slot 0's target as
//	a unit and bind the record's smart-reference to it; if it exists and carries
//	bit 28: goal = its position, release slot 0, bind slot 0 to it, p1 = 0,
//	p2 = RNG(3) + 3; advance. Else deadline 30; hold. Phase 2:
//	`p1 = satisfied has 0x4000 ? 0 : p1 + 1`; if `p1 ≤ p2` and the shot gate
//	admits the target: gate |= 0x7008, hold; else draw RNG(100): below 80 →
//	p1 = 0, advance (to the scan); else restart. Phase 3: enumerate the target
//	registry within 640 world units of the record's goal for my side; a hit →
//	pick index RNG(count), bind the smart-reference and slot 0 to it, phase = 1,
//	hold; none → restart. Other: cancel-all.
//
// Written 2026-09-01 (WU-19-6). Until now `Guard_NoMove` shared the follow
// handler in resolve.go, so a stationary guard ran the ground guard's assist
// legs and had a movement goal written around its ward — the opposite of its
// contract. [04 R-ORD-01 §8 point 5] states the separation outright: the
// stationary guard "calls **no** goal installer of any kind, never moves the
// unit, and reads no radius parameter", and its p1/p2 are the shot-attempt
// counter and its budget rather than a standoff.
//
// The record's goal triple here IS a position — the bound target's, copied at
// bind — which is why the 640-unit scan is centred on it and not on the unit.
func guardNoMoveHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if n == nil {
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
	if satisfied&pendTargetGone != 0 {
		n.Phase = 3
		return Code(2) // *hold* at the scan phase [04 R-ORD-01 §3]
	}
	switch n.Phase {
	case 0:
		inhibitSlot(u, slotAll)
		armDeadline(n, tick, 30)
		return Code(1) // *advance* [04 R-ORD-01 §3]
	case 1:
		tgt := slotTargetUnit(u, 0)
		n.BindTarget(0)
		if tgt != nil {
			n.BindTarget(tgt.Handle)
		}
		// "if it exists and carries bit 28": closed by [04 R-ORD-01 §12]'s
		// writer census. Bit 28 is the ALIVE bit and nothing else — the unit
		// initializer sets it for both classes and the teardown clears it, so
		// it means "constructed and not yet torn down"; the building-class bit
		// is bit 29 [08 R-AI-03 §4]. The "/building-class" half of the label in
		// [04 R-ORD-01 §1] is withdrawn, so the guard's phase-1 test is an
		// aliveness test satisfied by mobile targets, which is this build's own
		// live flag.
		// A held unit's stationary guard takes no slot: the target would then
		// be one an order holds, and fire through Hold Fire. Strict answers
		// false and takes it, as retail does [04 R-STANCE-01 §3].
		// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
		if tgt != nil && tgt.Alive && !rulesOfUnit(u).HoldsFire(u) {
			n.GoalX, n.GoalY, n.GoalZ = tgt.X, tgt.Y, tgt.Z
			releaseSlot(u, 0)
			bindSlotToUnit(u, 0, n.Target)
			n.Param1 = 0
			n.Param2 = drawBelow(u, 3) + 3 // p2 = RNG(3) + 3 [04 R-ORD-01 §3]
			return Code(1)                 // *advance*
		}
		armDeadline(n, tick, 30)
		return Code(2) // *hold*
	case 2:
		// 0x4000 is the satisfied bit that resets the consecutive shot-attempt
		// counter [04 R-ORD-01 §3][04 R-ORD-01 §8 point 5].
		if satisfied&0x4000 != 0 {
			n.Param1 = 0
		} else {
			n.Param1++
		}
		if n.Param1 <= n.Param2 && canEngageSlot(u, n.Target, 0) {
			n.DynamicGate |= 0x7008
			return Code(2) // *hold*
		}
		if result, handled := guardRuleScan(u, n, tick); handled {
			return result
		}
		if drawBelow(u, 100) < 80 {
			n.Param1 = 0
			return Code(1) // *advance* to the scan
		}
		return Code(0) // *restart*
	case 3:
		if result, handled := guardRuleScan(u, n, tick); handled {
			return result
		}
		list := scanRegistryAroundPoint(u, n.GoalX, n.GoalZ, guardNoMoveScanRadius)
		if len(list) == 0 {
			return Code(0) // *restart*
		}
		pick := list[int(drawBelow(u, uint32(len(list))))]
		n.BindTarget(pick)
		bindSlotToUnit(u, 0, pick)
		n.Phase = 1
		return Code(2) // *hold* with the phase already set [04 R-ORD-01 §3]
	default:
		return Code(7) // *cancel-all* [04 R-ORD-01 §3]
	}
}

// The policy scan preserves an unchanged target without entering a restart
// that would clear it and cancel its outstanding Aim callback.
func guardRuleScan(u *units.Unit, n *Node, tick uint32) (Code, bool) {
	pick, handled := rulesOfUnit(u).GuardTarget(u, n)
	if !handled {
		return 0, false
	}
	if pick == 0 {
		return Code(0), true
	}
	s := u.SlotAt(0)
	if pick == n.Target && s != nil && s.Target.Kind == units.TargetUnit && s.Target.Unit == pick {
		armDeadline(n, tick, 30)
		return Code(2), true
	}
	n.BindTarget(pick)
	bindSlotToUnit(u, 0, pick)
	n.Phase = 1
	return Code(2), true
}

// guardNoMoveScanRadius is the stationary guard's scan radius in whole world
// units [04 R-ORD-01 §3]: "enumerate the target registry within 640 world
// units of the record's goal for my side".
const guardNoMoveScanRadius int32 = 640

// slotTargetUnit is "*read slot k target*": the unit only while the companion
// carries the unit marker and the id is nonzero [04 R-ORD-01 §1]. The id is
// resolved through the queue binding, this package's smart-reference reader.
func slotTargetUnit(u *units.Unit, k int) *units.Unit {
	if u == nil {
		return nil
	}
	s := u.SlotAt(k)
	if s == nil || s.Target.Kind != units.TargetUnit || s.Target.Unit == 0 {
		return nil
	}
	b := bindingOfUnit(u)
	if b == nil || b.Lookup == nil {
		return nil
	}
	return b.Lookup(s.Target.Unit)
}

// scanRegistryAroundPoint shares the cached target-registry query between
// Guard_NoMove and Wait [04 R-SPEC-01 §8]. Walking the live hostile pool here
// would bypass the primary visibility list and the secondary upgrade gate.
func scanRegistryAroundPoint(u *units.Unit, x, z numeric.Fixed, radius int32) []pool.Handle {
	b := bindingFor(u)
	if u == nil || b == nil || b.Weapons == nil || b.Weapons.TargetsInRadius == nil {
		return nil
	}
	return b.Weapons.TargetsInRadius(u, x, z, radius)
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
//     0x10008 (`AirToGroundHover`, `AirToAir`): when the record is the last on its segment
//     and the unit's fire stance is not *hold fire*, replace the current order
//     with a fresh `VTOL_SeekAttack` carrying the same target and cached goal;
//     return 5 either way.
//  2. If the target reference is null but the record was issued against a
//     target, return 5; if it is the last on its segment, first replace it
//     with `VTOL_SeekAttack` at the unit's own position.
//  3. AirStrike and AirToGround refresh the cached goal from a live target;
//     position-issued attacks retain their goal. The other two entries do
//     not refresh it.
//  4. Off-map recovery ([04 R-AIR-01 §5]).
//  5. The maneuver leash ... return 5 when `leash <= distance`.
//
// Steps 1 and 2 were carried as an open question until [04 R-AIR-01 §16] named
// their three fields: the "successor marker" is the record's next-record link,
// so the test is "the record is the last on its segment" (`hasSuccessor`); bits
// `0x300000` are the unit's fire-stance pair, bits 20–21 [04 R-STANCE-01 §2],
// and "either set" means the stance is not *hold fire*; and the `0x200` bit is
// bit 9 of the record's STATIC-mask copy, which means *this record was issued
// against a target*, not "cached goal valid". §16 also adds the successor gate
// to step 2, which the earlier text stated unconditionally.
//
// Step 2 distinguishes a lost target from an order issued without one using
// static bit 9 alone. Removal clears the observer link but retains that bit;
// a movement-only gate can then reach this fallback on arrival without passing
// the removal event to step 1 [04 R-MOV-03 §7][04 R-AIR-01 §16].
//
// Off-map recovery is owned by the movement runner's marker family [04
// R-AIR-01 §5]. The queue-side entry performs the shared record checks first;
// the runner then applies the recovery leg with the current tick.
func airEntry(u *units.Unit, n *Node, satisfied uint32, interruptMask uint32, tick uint32) (Code, bool) {
	if satisfied&interruptMask != 0 {
		// Step 1: the replacement carries the same target and cached goal, and
		// runs only for the last record on the segment whose unit is not on
		// hold fire [04 R-AIR-01 §16].
		if !airRecordHasSuccessor(u, n) && u != nil && (u.Flags>>units.StandingFireShift)&units.StandingFieldMask != 0 {
			spawnSeekAttack(u, n, n.Target, n.GoalX, n.GoalY, n.GoalZ, tick)
		}
		return Code(5), true // "return 5 either way"
	}
	tgt := targetOf(u, n)
	if tgt == nil && n.StaticGate&staticTargetObserver != 0 {
		// Step 2: with the target gone, the seek starts from the unit's own
		// position and carries no target [04 R-AIR-01 §16].
		if !airRecordHasSuccessor(u, n) && u != nil {
			spawnSeekAttack(u, n, 0, u.X, u.Y, u.Z, tick)
		}
		return Code(5), true
	}
	// Step 3: bomber and strafer goals follow a live target. Position-issued
	// attacks retain their clicked goal [04 R-AIR-01 §8].
	if tgt != nil && (DescriptorFor(n.ID).Name == "AirStrike" || DescriptorFor(n.ID).Name == "AirToGround") {
		n.GoalX, n.GoalY, n.GoalZ = tgt.X, tgt.Y, tgt.Z
	}
	// The rule set decides whether an accepted bombing pass outruns its leash
	// this visit [04 R-AIR-01 §8]; Strict 3.1 always says no.
	if !rulesOfUnit(u).DeferBomberLeash(u, n) && leashBroken(u, n) {
		return Code(5), true // step 5, the maneuver leash [R-STANCE-01 §4]
	}
	return Code(0), false
}

// airRecordHasSuccessor retains the removed record's next-link answer while a
// purge callback runs after unlinking it. Outside that narrow cleanup window it
// is the ordinary live-segment query [04 R-MOV-03 §6][04 R-AIR-01 §16].
func airRecordHasSuccessor(u *units.Unit, n *Node) bool {
	if q := QueueOfUnit(u); q != nil && q.detachedNode == n {
		return q.detachedHasSuccessor
	}
	return hasSuccessor(u, n)
}

// spawnSeekAttack tail-appends the fresh `VTOL_SeekAttack` record the air
// entry's two replacement arms issue [04 R-AIR-01 §16]. The replacement
// allocates the record with the current handler tick, constructs it with the
// caller's target and position triple, and hands it to the ordinary tail-append
// helper; that helper writes no active marker and inherits no auto flag. The
// entry then returns 5, so removal of the old record exposes the appended seek.
// A missing descriptor or unbound queue leaves the completion alone.
func spawnSeekAttack(u *units.Unit, n *Node, target pool.Handle, x, y, z numeric.Fixed, tick uint32) {
	id := rowVTOLSeekAttack
	if id == 0 || u == nil || n == nil {
		return
	}
	q := QueueOfUnit(u)
	if q == nil {
		return
	}
	q.appendTail(id, NewNodeForOrder(id, target, x, y, z, tick, u.Handle, false))
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
	{"Guard_NoMove", guardNoMoveHandler},
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
