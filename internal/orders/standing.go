package orders

// The trivial, standing, wait and cloak handlers of [04 R-ORD-01 §2]. Every
// body here is that section's table row for the order, and every row is a
// short state machine over the record fields of [04 §3.2] expressed in the
// shared vocabulary of [04 R-ORD-01 §1]: the phase byte, the dynamic gate, the
// deadline, the three general parameters, and the pending word.
//
// Result codes are [04 §3.3]'s: *complete* is 5, *advance* 1, *hold* 2,
// *cancel-all* 7. Each handler says which it returns and why, quoting its row.

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The two standing-order fields are two-bit pairs of the unit's runtime status
// word: the move stance at bits 18-19, the fire stance at bits 20-21
// [04 R-STANCE-01 §2]. They are the unit's OWN fields; the folded three-bit
// selection aggregate the side panel stages is published separately and is
// never what a handler writes [04 R-STANCE-01 §1].
const (
	stanceMoveShift = 18
	stanceFireShift = 20
	stanceFieldMask = uint32(3)
)

// Gate bits these handlers arm, from the census of [04 R-ORD-01 §0]:
//
//	0x8  interrupt/abandon      0x10    guard re-arm / second interrupt bit
//	0x2  cancel-current         0x10000 the pump's three unconditional slot clears
//
// The five pending movement/path bits, 0x20..0x200, have no name here: the
// three goal installers that clear them spell 0x3E0 at their own sites, and
// `VTOL_Patrol` phase 1 clears its own three-bit subset.
const (
	gateInterrupt uint32 = 0x8
	gateReArm     uint32 = 0x10
	gateSlotClear uint32 = 0x10000
	gateCancelBit uint32 = 0x2
)

// armDeadline is the shared deadline setter of [04 R-ORD-01 §1]: it stores
// `current tick + n` and ORs bit 0 into the dynamic gate, so the pump's step 1
// delivers bit 0 on expiry [04 §3.3].
//
// The tick is the handler's own fourth argument. WU-18-1 had to arm against
// queue's rear-segment tick — a base the rear-segment walk publishes and the
// front-segment walk did not, so every front-segment arm here measured its
// wait from a stale tick and could run a wait or a countdown faster than its
// row's arithmetic. WU-18-7 put the tick in the Handler signature, which is
// what [04 R-ORD-01 §1] describes, and every arm below is now exact.
func armDeadline(n *Node, tick uint32, ticks uint32) {
	if n == nil {
		return
	}
	n.Deadline = int32(tick + ticks)
	n.DynamicGate |= 1 // the setter always ORs bit 0 [04 R-ORD-01 §1]
}

// simRNGFor returns the simulation stream bound to this unit's queue. Every
// draw below is the simulation RNG taken as `state mod n`, and the helper
// returns 0 without advancing the state when n is below 2, so a handler whose
// bound collapses consumes no random state [04 R-ORD-01 §1][I4]. The stream is
// reached only through the queue's own binding — never a package global.
func simRNGFor(u *units.Unit) *rng.Simulation {
	q := QueueOfUnit(u)
	if q == nil {
		return nil
	}
	binding := q.Binding()
	if binding == nil {
		return nil
	}
	return binding.SimRNG
}

// drawBelow is the `RNG(n)` of [04 R-ORD-01 §1]. A queue with no bound stream
// (a bare fixture) draws nothing and yields 0, which is what the bound-below-2
// rule yields anyway.
func drawBelow(u *units.Unit, bound uint32) uint32 {
	sim := simRNGFor(u)
	if sim == nil {
		return 0
	}
	return sim.Uint32n(bound)
}

// releaseAllWeaponSlots and inhibitAllWeaponSlots are the `k = 3` forms of
// *release slot k* and *inhibit slot k* of [04 R-ORD-01 §1]. They are thin
// names over combat.go's pair, which is this package's single implementation.
// The per-slot names beside them had no caller left and are gone; callers that
// want one slot call releaseSlot/inhibitSlot directly.
//
// Corrected 2026-08-31 [04 R-ORD-01 §7]. These were a second, divergent
// implementation: they wrote the control byte and cleared the target
// unconditionally and guarded only the *notification*, carrying an
// open-question marker that said whether the target clear is also guarded "is
// not established", and reading the assigned bit off the slot's Flags word while
// reading the inhibit bit off OrderControl. The trace settles all three points
// at once — the guard is evaluated first and a rejected slot is left entirely
// alone, both bits belong to one byte, and bit 1 means *the slot is enabled* —
// so the two helper pairs the old comment asked to fold are now one.
func releaseAllWeaponSlots(u *units.Unit) { releaseSlot(u, slotAll) }

func inhibitAllWeaponSlots(u *units.Unit) { inhibitSlot(u, slotAll) }

// releaseGoalPayload is the fourth goal installer of [04 R-ORD-01 §1] — the
// payload release. It is the record-level install/release helper called with no
// new object, and it runs entirely through the owner's mover: a mover-less unit
// is a no-op; otherwise the controller is handed a NULL goal (steps 1-4 of the
// route-acceptance rule of [04 R-PATH-01 §8]: cancel the in-flight search, OR
// `0x80` into the pending word of the record that owned the previous payload,
// clear has-waypoint, clear wants-repath), the payload object is virtually
// deleted and the record's payload field cleared.
//
// Corrected 2026-09-02 (WU-19-62). This used to raise `0x80` and then clear
// `0x20`-`0x200` on the record and stop, on the reading that "the net effect is
// that all five movement bits are clear", with a marker saying this build had
// no payload object for the order layer to release. Both halves were wrong.
// There is a payload object and a seam to it — the movement adapter's release
// port, which transport.go's releaseGoal already used — so the row's actual
// effect (a cancelled search and a follower with no waypoint and no repath
// armed) was simply absent. And the closing clear is not part of this form:
// [04 R-ORD-01 §1] gives the clear to the branch that installs a NEW object and
// says "the release form (no new object) is step (1) alone, which is why it
// leaves `0x80` visible". A release that cancelled its own `0x80` was the one
// case [04 R-ORD-01 §0] names as making the bit observable — a detach from
// outside the record — and it hid it.
//
// The unit is the helper's own argument because the mover it runs through is
// reached from the unit's queue binding; the record alone cannot name it.
func releaseGoalPayload(u *units.Unit, n *Node) {
	if n == nil {
		return
	}
	releaseGoal(u, n)
}

// ---------------------------------------------------------------------------
// Standing_MoveOrder / Standing_FireOrder [04 R-ORD-01 §2][04 R-STANCE-01 §2]
// ---------------------------------------------------------------------------

// standingMoveOrderHandler deposits the order's first general parameter, masked
// to two bits, into the unit's move stance at bits 18-19:
//
//	state = (state &^ (3 << 18)) | ((param & 3) << 18)
//
// and returns the completion code 5, so the record is consumed the tick it runs
// [04 R-STANCE-01 §2]. The move handler has no further effect.
//
// The definition gates `mobilestandorders`/`firestandorders` are NOT tested
// here: they are read in exactly two interface/command sites — the aggregate
// refresh and the selection broadcast — and never in simulation
// [04 R-STANCE-01 §5], so an order that reaches this handler by any other route
// executes.
func standingMoveOrderHandler(u *units.Unit, n *Node, _ uint32, _ uint32) Code {
	if u == nil || n == nil {
		return Code(5)
	}
	u.Flags = (u.Flags &^ (stanceFieldMask << stanceMoveShift)) | ((n.Param1 & stanceFieldMask) << stanceMoveShift)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

// standingFireOrderHandler is the move handler's twin at bits 20-21, plus the
// fire handler's extra effect [04 R-STANCE-01 §2]: when the UNMASKED parameter
// is exactly 0 or exactly 1 — a transition to hold fire or return fire — it
// walks the three weapon slots and, for every slot whose autonomous-targeting
// bit (bit 4 of the slot control byte) is set, clears that slot's stored
// target and raises `TargetCleared` with the slot index. The autonomous bit
// itself is not cleared.
//
// The parameter test is deliberately on the unmasked value while the deposit
// masks to two bits: [04 R-STANCE-01 §2] records that asymmetry as an
// intentional reproduction ("reproduce the unmasked test"), so a parameter of 4
// sets the field to 0 WITHOUT clearing the slots.
func standingFireOrderHandler(u *units.Unit, n *Node, _ uint32, _ uint32) Code {
	if u == nil || n == nil {
		return Code(5)
	}
	u.Flags = (u.Flags &^ (stanceFieldMask << stanceFireShift)) | ((n.Param1 & stanceFieldMask) << stanceFireShift)
	if n.Param1 == 0 || n.Param1 == 1 { // unmasked, exactly [04 R-STANCE-01 §2]
		clearAutonomousSlotTargets(u)
	}
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

// clearAutonomousSlotTargets is the fire handler's slot walk
// [04 R-STANCE-01 §2]. Retail resets the target id to zero and the second
// target word to its empty sentinel; the decoded form of that pair in this
// build is the empty target kind [06 §1.2].
//
// Retired (WU-19-159). [04 R-STANCE-01 §2] describes this walk as also killing
// "the slot's `StartBuilding` emission", and the marker that stood here treated
// that as a step it could not implement. There is nothing to implement:
// [04 §5.3]'s producer census already established that the four
// weapon-target-clearing routines "call the `StartBuilding` name lookup while
// clearing targets but discard the result and are not producers". The lookup
// resolves a script function name and throws the answer away — no emission is
// started and none is stopped. §2's phrase is corrected in place; the reversal
// is recorded in [04 R-ORD-01 §13].
func clearAutonomousSlotTargets(u *units.Unit) {
	if u == nil {
		return
	}
	bridge := callbackBridgeFor(u)
	for slot := 0; slot < units.NumSlots; slot++ {
		s := u.SlotAt(slot)
		if s == nil || s.Flags&slotTracking == 0 {
			continue // only slots carrying the autonomous-targeting bit
		}
		s.Target = units.Target{Kind: units.TargetNone}
		arrangeDeferred(bridge, "TargetCleared", []int32{int32(slot)})
	}
}

// ---------------------------------------------------------------------------
// Cloak_On / Cloak_Off [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// cloakOnHandler and cloakOffHandler set / clear the unit's cloak-wanted state
// when the definition's capability word carries the can-cloak bit — derived by
// the FBI parser as `cloakcost > 0`, not from `init_cloaked` — and complete
// either way [04 R-ORD-01 §2].
//
// The row writes state-word bit 11, the cloak-wanted bit of [03 "cloakActive =
// status bit 11"]; the same logical field in this build is units.Unit.IsCloaked,
// whose one runtime writer is SetCloaked and whose one reader is the cloak
// upkeep debit's gate [05 "Cloak debit"] (I13: one logical field, one Go field).
//
// There is no callback and no caption: the *Cloaked* / *Visible* captions come
// from the edge machine's bit 2, which this handler does not touch — and
// neither does it hide or show the unit. `Cloak_Off` clears the request and
// nothing else; the unit stays hidden until its owner's next settlement pass
// finds the gate no longer due and clears the INSTANCE bit, units.Unit.Hidden
// [05 R-ECO-01 §9] (WU-19-92).
func cloakOnHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	setCloakIfCapable(u, true)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

func cloakOffHandler(u *units.Unit, _ *Node, _ uint32, _ uint32) Code {
	setCloakIfCapable(u, false)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

func setCloakIfCapable(u *units.Unit, on bool) {
	if u == nil || u.Def == nil || u.Def.CloakCost <= 0 {
		return
	}
	u.SetCloaked(on)
}

// ---------------------------------------------------------------------------
// WaitForAttack [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// waitForAttackHandler: target null -> complete. Phase 0 arms gate 0x18 and
// advances; phase 1 completes; any other phase cancels the whole queue. The
// gate is the interrupt pair — 0x8 target loss and 0x10 the unlocated re-arm
// bit [04 R-ORD-01 §0] — so the record wakes when its target is taken from it
// and completes on the next visit.
func waitForAttackHandler(_ *units.Unit, n *Node, _ uint32, _ uint32) Code {
	if n == nil || n.Target == 0 {
		return Code(5) // *complete* — no target to wait on [04 R-ORD-01 §2]
	}
	switch n.Phase {
	case 0:
		n.DynamicGate = gateInterrupt | gateReArm // gate = 0x18 [04 R-ORD-01 §2]
		return Code(1)                            // *advance*
	case 1:
		return Code(5) // *complete*
	default:
		return Code(7) // *cancel-all* — a phase outside the machine [04 R-ORD-01 §2]
	}
}

// ---------------------------------------------------------------------------
// Paralyze [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// paralyzeMaxCredit is the row's clamp on the stun credit [04 R-ORD-01 §2] —
// 1,800 ticks, sixty seconds. The compare against it is SIGNED [06 §10]: the
// credit accumulates as a plain 32-bit add across repeated paralyzer hits, and
// a credit that has wrapped to a negative value is NOT capped. Reading the
// compare as unsigned turned that wrap into a full-length stun; reading it
// signed keeps retail's behavior, in which the wrapped credit arms a deadline
// in the past and the wait expires immediately.
const paralyzeMaxCredit int32 = 1800

// paralyzeHandler holds the stun credit in p1, in ticks.
//
//	p1 = 0 -> lower the stun edge; complete.
//	otherwise clamp p1 to 1800, release all slots, clear the three slot targets
//	unconditionally, release the goal payload, deadline = p1, p1 = 0, raise the
//	stun edge; advance.
//
// Phase 1 on expiry re-enters with p1 = 0 and therefore takes the first arm:
// the stun is lowered and the record completes. Later paralyzer hits add to p1
// of the waiting head record (doc 06 owns the packet arithmetic).
//
// Retired (WU-19-159). The row raises and lowers edge bit 4 of the unit's
// engine-state byte, and the marker that stood here called the two named fields
// this build keeps instead — a boolean and the absolute tick the stun ends at
// [06 §10] — a placeholder for edge machinery it was missing. They are not a
// placeholder: bit 4 carries no edge effect of its own. [04 R-UNIT-06 §2]
// enumerates the machine's edge effects exhaustively and assigns every one of
// them to bit 0 (the `Activate`/`Deactivate` starts plus notifications 3 and 4),
// bit 2 (yard-open: notifications 14 and 15 and the waiter walk) or bit 3
// (`StartBuilding`/`StopBuilding`); "the remaining bits of the byte are written
// through the same machine by their producers and are otherwise opaque". A
// bit-4 edge therefore starts no script and emits no notification. The machine's
// two unconditional effects are the battle interface's dirty mark for a selected
// local unit, which is presentation, and the computer-player network event that
// a single-player build has no receiver for. So the pair written below is every
// simulation-visible consequence the edge has.
func paralyzeHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return Code(5)
	}
	if n.Param1 == 0 {
		u.Stunned = false
		u.ParalyzeExpire = 0
		return Code(5) // *complete* [04 R-ORD-01 §2]
	}
	if int32(n.Param1) > paralyzeMaxCredit { // SIGNED compare [06 §10]
		n.Param1 = uint32(paralyzeMaxCredit)
	}
	releaseAllWeaponSlots(u)
	clearWeaponTargetsUnconditional(u)
	releaseGoalPayload(u, n)
	armDeadline(n, tick, n.Param1)
	n.Param1 = 0
	u.Stunned = true
	u.ParalyzeExpire = uint32(n.Deadline)
	return Code(1) // *advance* [04 R-ORD-01 §2]
}

// PushParalyzeCredit is the packet side of the stun [06 §10] — the entry point
// a kind-2 damage packet reaches this row through. internal/combat cannot call
// it directly, because this package imports that one, so combat declares the
// seam (combat.ParalyzeTaskPush) and the initializer below installs this
// function into it.
//
// [06 §10]: the engine resolves the task type by the authored alias `paralyze`
// and inspects only the HEAD of the victim's primary command list. If the head
// already carries that type, the packet's unsigned 16-bit amount is added to
// the head's 32-bit accumulated credit — a plain add, so repeated hits
// accumulate with 32-bit wrap and never allocate. Otherwise a record is
// constructed with the credit as its parameter and PREPENDED; the linker does
// not append and does not search the list. A hit arriving DURING a stun
// therefore lands on the same head record and extends the CREDIT, not the
// deadline: the extension takes effect only when the wait expires and the
// record re-activates, which is the behavior a clone must reproduce rather than
// adding the remainder to the deadline.
//
// Nothing else happens here. The release verb on all three slots, the
// unconditional target clear, the goal-payload release, the wait arm and the
// raising of the stunned mark are the record's own first visit
// [06 R-DMG-01 §11] — paralyzeHandler above — and its re-activation with a zero
// credit is the mark's only clearer.
func PushParalyzeCredit(u *units.Unit, credit uint32, tick uint32) {
	if u == nil {
		return
	}
	id := rowParalyze
	if id == 0 {
		return
	}
	q := QueueForUnit(u)
	if q == nil {
		return
	}
	if head := q.Head(); head != nil && head.ID == id {
		head.Param1 += credit // the plain 32-bit add, wrap included [06 §10]
		return
	}
	n := NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false)
	n.Param1 = credit
	q.PushHead(id, n) // prepended, never appended [06 §10]
}

// ---------------------------------------------------------------------------
// Wait [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// waitScanBudgetStep is the fixed part of the scan variant's per-failure drain
// and of its deadline: `r + 150` with `r = RNG(30)` [04 R-ORD-01 §2].
const waitScanBudgetStep = 150

// waitHandler holds a timeout budget in p1 and a scan radius in p2.
//
// With p2 != 0, on EVERY phase: enumerate the target registry within p2 of the
// unit for the unit's side (inclusive `d² <= r²` in whole units); any hit ->
// complete; else if p1 < 1 -> complete; else draw `r = RNG(30)`, subtract
// `r + 150` from p1, arm the deadline at `r + 150`, and hold.
//
// With p2 = 0: phase 0 arms the deadline at p1 and advances, phase 1 completes,
// any other phase cancels the whole queue.
//
// The budget test is `p1 < 1`, not `p1 == 0`: the drain subtracts at least 150
// per failed scan and so runs the budget negative, and the signed test is what
// terminates it [04 R-ORD-01 §2][R-ORDER-02 §1 "Wait | timeout drain"].
func waitHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if n == nil {
		return Code(5)
	}
	if n.Param2 != 0 {
		if u != nil && len(scanRegistryAroundPoint(u, u.X, u.Z, int32(n.Param2))) != 0 {
			return Code(5) // a hostile registry entry is already in range
		}
		if int32(n.Param1) < 1 {
			return Code(5) // *complete* — budget exhausted [04 R-ORD-01 §2]
		}
		r := drawBelow(u, 30)
		n.Param1 = uint32(int32(n.Param1) - int32(r+waitScanBudgetStep))
		armDeadline(n, tick, r+waitScanBudgetStep)
		return Code(2) // *hold* [04 R-ORD-01 §2]
	}
	switch n.Phase {
	case 0:
		armDeadline(n, tick, n.Param1)
		return Code(1) // *advance*
	case 1:
		return Code(5) // *complete*
	default:
		return Code(7) // *cancel-all*
	}
}

// ---------------------------------------------------------------------------
// Teleport [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// teleportBox is the ordering unit's model bounding box in absolute world
// coordinates: its position plus the definition's min/max triple
// [04 R-ORD-01 §2], the "six signed world-unit extents stored in the unit
// definition around its position" of [03 R-LAYER §4].
//
// The triple is the bounding record the catalog compiler writes
// [02 R-CAT-01 §7]: the X and Z bounds come from the FOOTPRINT, not the model
// — `±(FootprintX << 20) / 2` and `±(FootprintZ << 20) / 2` in 16.16 — and the
// Y bounds are the model-top walk, with the LOWER bound "zeroed just before"
// the upper is stored. So the box is
//
//	X in [x − (footX<<20)/2, x + (footX<<20)/2]
//	Y in [y,                 y + ModelTopFixed ]
//	Z in [z − (footZ<<20)/2, z + (footZ<<20)/2 ]
//
// A cell is 2^20 in 16.16 (16 world units), so the half-extent is the
// footprint's half-width in cells expressed in world units. A definition whose
// model is missing or entirely below its origin has ModelTopFixed 0
// [02 R-CAT-01 §7], which collapses the box to the ground plane — the honest
// consequence of an unresolved model, not a substituted height.
func teleportBox(u *units.Unit) (minX, maxX, minY, maxY, minZ, maxZ numeric.Fixed, ok bool) {
	if u == nil || u.Def == nil {
		return 0, 0, 0, 0, 0, 0, false
	}
	footX, footZ := u.Def.FootprintX, u.Def.FootprintZ
	if footX < 0 || footZ < 0 {
		return 0, 0, 0, 0, 0, 0, false
	}
	halfX := numeric.Fixed((int64(footX) << 20) / 2)
	halfZ := numeric.Fixed((int64(footZ) << 20) / 2)
	top := numeric.Fixed(u.Def.ModelTopFixed)
	return u.X - halfX, u.X + halfX, u.Y, u.Y + top, u.Z - halfZ, u.Z + halfZ, true
}

// teleportHandler is a single visit. For every live unit other than itself
// whose position lies inside this unit's model bounding box — inclusive on all
// three axes — the row moves that unit by the same delta the teleporter's goal
// describes: its new position is `goal + (its position - my position)`. It
// emits the teleport effect from the old position to the new one, then places
// it there through the position setter, which re-registers occupancy when the
// footprint cell changes. Then complete. The teleporter itself never moves
// [04 R-ORD-01 §2].
//
// The row is ungated and free: no capability bit, no pairing, no economy call
// [04 R-SPEC-01 §2]. The `teleporter` FBI key has no reader in the image, so it
// is NOT tested here.
//
// Order within one enclosed unit is effect first, commit second — the two calls
// [03 R-LAYER §4] enumerates, in that order. The effect is emitted once PER
// MOVED UNIT, not once for the record.
//
// The walk is QueueBinding.ForEachUnit, the live-unit enumerator the
// typed-attack and repair scans use: players 0..9 ascending then pool slot
// ascending, live units only [01 §6.2][I1]. A carried unit is not skipped —
// [04 R-ORD-01 §2] says "every live unit other than itself" and names no
// exclusion — but the carried branch of the occupancy commit rewrites a carried
// unit's position from its carrier every tick, so it "cannot drift, be pushed,
// or be teleported while carried" [04 R-FAC-02 §2]: the displacement is undone
// on the next commit rather than being suppressed here.
//
// Retired (WU-19-142): the accepted-placeholder marker that stood here reported the row as
// blocked on a missing seam, not on missing research. The seam is now
// MovementGoalAdapter.PlaceUnit, bound to internal/movement's direct position
// commit [04 R-COLL-01 §4], so the ground words of a moved unit's old footprint
// are released and the class layers over them reclassified [04 R-MOV-03 §3].
func teleportHandler(u *units.Unit, n *Node, _ uint32, _ uint32) Code {
	if u == nil || n == nil {
		return Code(5) // *complete* — single visit [04 R-ORD-01 §2]
	}
	b := bindingFor(u)
	if b == nil {
		return Code(5)
	}
	minX, maxX, minY, maxY, minZ, maxZ, ok := teleportBox(u)
	if !ok {
		return Code(5)
	}
	b.ForEachUnit(func(h pool.Handle, other *units.Unit) bool {
		if h == 0 || other == nil || !other.Alive || other == u {
			return scanNext
		}
		// Inclusive on all three axes [04 R-ORD-01 §2].
		if other.X < minX || other.X > maxX ||
			other.Y < minY || other.Y > maxY ||
			other.Z < minZ || other.Z > maxZ {
			return scanNext
		}
		// `goal + (its position − my position)`, per axis, in the row's own
		// order of operations [04 R-ORD-01 §2]. The teleporter's own position
		// is read from the unit, not from a snapshot: it never moves, so the
		// delta is the same for every enclosed unit however many are moved.
		newX := n.GoalX + (other.X - u.X)
		newY := n.GoalY + (other.Y - u.Y)
		newZ := n.GoalZ + (other.Z - u.Z)
		if b.Presentation != nil && b.Presentation.Teleport != nil {
			b.Presentation.Teleport(other, other.X, other.Y, other.Z, newX, newY, newZ)
		}
		if b.Movement != nil && b.Movement.PlaceUnit != nil {
			b.Movement.PlaceUnit(PlaceRequest{Unit: h, X: newX, Y: newY, Z: newZ})
		}
		return scanNext
	})
	return Code(5) // *complete* — single visit [04 R-ORD-01 §2]
}

// ---------------------------------------------------------------------------
// Standby / Standby_Mine [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// opportunityScan is the fire-at-will-only acquisition helper shared by the
// idle/loiter arms of `Patrol`, `Standby`, `Standby_Mine`, `VTOL_Standby`,
// `VTOL_Patrol` and `VTOL_SeekAttack` [04 R-STANCE-01 §3]: it returns a target
// only when the unit's standing FIRE field reads exactly 2, in which case it
// runs the shared unit-level target search with the definition's
// `sightdistance` as its range; otherwise it returns nothing WITHOUT searching.
// That gate is the whole behavioral difference between return fire and fire at
// will.
func opportunityScan(u *units.Unit) *units.Unit {
	if u == nil || u.Flags>>stanceFireShift&stanceFieldMask != 2 {
		return nil // not fire at will: no search at all [04 R-STANCE-01 §3]
	}
	q := QueueOfUnit(u)
	if q == nil || q.Binding() == nil || q.Binding().Weapons == nil || q.Binding().Weapons.Acquire == nil {
		return nil
	}
	rangeLimit := uint32(0)
	if u.Def != nil && u.Def.SightDistance > 0 {
		rangeLimit = uint32(u.Def.SightDistance)
	}
	// The opportunity helper asks once through slot zero. Trying additional
	// weapons after a refusal changes both target choice and RNG consumption
	// [04 R-STANCE-01 §3].
	if handle, ok := q.Binding().Weapons.Acquire(u, 0, rangeLimit); ok {
		return q.Binding().Lookup(handle)
	}
	return nil
}

// standbyHandler is the idle loiter of a mobile unit — the stock
// `defaultmissiontype` of every mobile ground and sea definition in the
// reference install.
//
//	Phase 0: no mover reference -> cancel-all; inhibit all slots;
//	         gate |= 0x10000; deadline 1; advance.
//	Phase 1: run the opportunity scan; a target found and the auto-engage
//	         issuer succeeds -> complete; else gate |= 0x10000,
//	         deadline 30 + RNG(30), hold.
//	Other:   cancel-all.
//
// The draw is taken only on the no-issue exit, which is what makes a stance
// change reorder the simulation stream even though the stance fields draw
// nothing themselves [04 R-STANCE-01 §7].
//
// "No mover reference" is the definition's `bmcode`: a building-class unit
// never owns a mover, because the allocator constructs one only for `bmcode 1`
// [04 R-FAC-02 §5] — the same reading `Park`'s row already uses.
func standbyHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return Code(7)
	}
	switch n.Phase {
	case 0:
		if u.Def == nil || u.Def.BMCode != 1 {
			return Code(7) // *cancel-all* — no mover reference [04 R-ORD-01 §2]
		}
		inhibitAllWeaponSlots(u)
		n.DynamicGate |= gateSlotClear
		armDeadline(n, tick, 1)
		return Code(1) // *advance*
	case 1:
		if target := opportunityScan(u); target != nil && autoEngage(u, target, false) {
			return Code(5) // *complete* — the scan issued an order; no draw [04 R-STANCE-01 §7]
		}
		n.DynamicGate |= gateSlotClear
		armDeadline(n, tick, 30+drawBelow(u, 30))
		return Code(2) // *hold*
	default:
		return Code(7) // *cancel-all*
	}
}

// standbyMineHandler is `Standby` with two differences [04 R-ORD-01 §2]: phase
// 0 requires the unit's status-word bit 29 — the building-class/immobile bit
// [04 §3.4 "state bit 29 (immobile)"] — and phase 1 requires the scanned
// target's committed mover mode to be grounded (1) and this unit's own fire
// stance to be nonzero, after which it spawns `SelfDestruct` with p1 = 1
// (immediate) at the head of that order's segment and completes.
//
// Settled by trace (WU-19-159), and the code below was already right. The
// marker that stood here read [04 R-ORD-01 §3]'s "phase 0 **also** requires
// state-word bit 29" as leaving `Standby`'s "no mover reference -> cancel-all"
// in place beside it, which cannot hold: bit 29 is set at creation from
// `bmcode == 0` and a `bmcode 0` definition is exactly the one the mover
// allocator skips [04 §2.4][04 R-COLL-01 §1], so a mine would cancel its own
// queue on the first visit and no stock mine could ever detonate. The two
// handlers are separate bodies, and `Standby_Mine`'s phase 0 opens with the
// bit-29 test and contains **no mover test at all** — it replaces the mover
// test rather than adding to it. §3's "also" is corrected in place; the
// finding is [04 R-ORD-01 §13].
func standbyMineHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return Code(7)
	}
	switch n.Phase {
	case 0:
		if u.Flags&units.BuildingClassStatus == 0 {
			return Code(7) // *cancel-all* — status-word bit 29 required [04 R-ORD-01 §2]
		}
		inhibitAllWeaponSlots(u)
		n.DynamicGate |= gateSlotClear
		armDeadline(n, tick, 1)
		return Code(1) // *advance*
	case 1:
		target := opportunityScan(u)
		// "phase 1 requires the scanned target's committed mover mode to be
		// **grounded** (`1`) and my own fire stance nonzero" [04 R-ORD-01 §3].
		// The trace in [R-STANCE-01 §3] states the same test as "a target whose
		// state-word low two bits equal `1`", and [04 R-ORD-01 §12] identifies
		// those two bits: "The movement-mode mirror is bits 0-1". So the read is
		// the committed mirror, not `Move.Mode`, which is the request byte
		// [04 R-AIR-01 §3][04 R-COLL-01 §1][08 R-SAVE-02 §6, §8].
		if target != nil && moverMode(target) == 1 && u.Flags>>stanceFireShift&stanceFieldMask != 0 {
			spawnImmediateSelfDestruct(u, n)
			return Code(5) // *complete* [04 R-ORD-01 §2]
		}
		n.DynamicGate |= gateSlotClear
		armDeadline(n, tick, 30+drawBelow(u, 30))
		return Code(2) // *hold*
	default:
		return Code(7) // *cancel-all*
	}
}

// autoEngage is the shared auto-engage issuer of [04 R-STANCE-01 §3], taking
// `(unit, target, force)`. Its admission, in order:
//
//  1. the unit is not its own target;
//  2. the standing MOVE field is nonzero and `force` is clear;
//  3. the standing FIRE field is nonzero and `force` is clear;
//  4. command code 3 (attack a unit) resolves to a non-empty name through the
//     resolver of [04 §3.4].
//
// On success the resolved record is inserted through the head insert of
// [04 R-ORD-01 §1].
//
// With `force` clear, hold position refuses autonomous engagement exactly as
// hold fire does: either zero is enough to stop the unit acting on its own.
// `force` bypasses both stance gates and is passed by exactly one caller
// family — the two guard handlers' combat join ([04 R-UNIT-06 §1] branch 1) —
// which is what "the queued-mode attack bypasses the standing-order gates"
// means in that section. Every other caller passes false.
// An unforced maneuver engagement inserts the return move first, then the
// leashed attack, so completing the attack resumes the saved post. Forced
// guard joins and the other move stances carry no leash [04 R-STANCE-01 §4].
func autoEngage(u *units.Unit, target *units.Unit, force bool) bool {
	if u == nil || target == nil || u == target {
		return false
	}
	// Modern's authoritative Hold Fire also closes the forced guard join.
	// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
	if b := bindingOfUnit(u); b != nil && b.ModernHoldFire && u.Flags>>stanceFireShift&stanceFieldMask == 0 {
		return false
	}
	if !force {
		if u.Flags>>stanceMoveShift&stanceFieldMask == 0 {
			return false
		}
		if u.Flags>>stanceFireShift&stanceFieldMask == 0 {
			return false
		}
	}
	id := Resolve(3, u, target, nil)
	if id == 0 {
		return false
	}
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	node := Node{Owner: u.Handle, Target: target.Handle, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z, GoalSupplied: true}
	if !force && u.Flags>>stanceMoveShift&stanceFieldMask == 1 {
		if moveID := Resolve(2, u, nil, &ResolvePos{X: u.X, Y: u.Y, Z: u.Z}); moveID != 0 {
			q.PushHead(moveID, Node{Owner: u.Handle, GoalX: u.X, GoalY: u.Y, GoalZ: u.Z, GoalSupplied: true})
		}
		node.Param3 = uint32(uint16(u.Def.ManeuverLeashLength))
		node.GuardX, node.GuardY = int16(u.X.Raw()>>16), int16(u.Z.Raw()>>16)
	}
	if isSecondary(id) {
		q.PushSecondary(id, node)
		return true
	}
	q.PushHead(id, node)
	return true
}

// AutonomousEngage exposes the unforced issuer to movement's targeted seek
// entry. Success has inserted attack work before the seeker restarts; setting
// a weapon target alone cannot make that restart progress [04 R-AIR-01 §7].
func AutonomousEngage(u *units.Unit, target *units.Unit) bool {
	return autoEngage(u, target, false)
}

// AutonomousAcquire is "the ordinary autonomous acquisition" the idle rows of
// [04 R-ORD-01 §2] and [04 R-AIR-01 §7] ask for: the fire-at-will opportunity
// scan of [04 R-STANCE-01 §3] followed by the auto-engage issuer, reporting
// whether a target was both FOUND and ACCEPTED.
//
// It exists because the air family's idle row lives in another package.
// `VTOL_Standby` is registered on the queue as externally driven
// (Queue.SetExternallyDrivenHandler, queue_handlers.go) — its executor is the
// mover-side machine that owns the air marker family [04 R-AIR-01 §4] — and
// internal/movement cannot reach an unexported pair in here. Without a seam that
// executor's phase 1 stood as a placeholder that always took the no-target arm,
// so every stock aircraft, all of which author `defaultmissiontype =
// VTOL_Standby`, was incapable of acquiring anything on its own: an idle
// fighter or gunship with `fire at will` fell straight through to phase 2's
// no-cargo arm and landed itself instead of attacking.
//
// The scan's own stance gates still apply, so a definition that authors hold
// fire (every stock bomber does) still acquires nothing.
func AutonomousAcquire(u *units.Unit) bool {
	target := opportunityScan(u)
	return target != nil && autoEngage(u, target, false)
}

// ---------------------------------------------------------------------------
// Registration [04 §3.1]
// ---------------------------------------------------------------------------

// standingHandlers pairs each descriptor name with its handler, in a fixed
// slice so installation order is source order (I1).
var standingHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"Standing_MoveOrder", standingMoveOrderHandler},
	{"Standing_FireOrder", standingFireOrderHandler},
	{"Cloak_On", cloakOnHandler},
	{"Cloak_Off", cloakOffHandler},
	{"Wait", waitHandler},
	{"WaitForAttack", waitForAttackHandler},
	{"Paralyze", paralyzeHandler},
	{"Teleport", teleportHandler},
	{"Standby", standbyHandler},
	{"Standby_Mine", standbyMineHandler},
}

// ensureStandingHandlers installs this family onto the descriptor table. It is
// idempotent — it assigns only where the descriptor's Handler is still nil —
// and tolerates a table that has not been built yet, exactly as
// ensureStopHandler does.
func ensureStandingHandlers() {
	if len(table) == 0 {
		return
	}
	for _, entry := range standingHandlers {
		id := Lookup(entry.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = entry.handler
		}
	}
}

func init() {
	ensureStandingHandlers()
	// The stun's packet-side entry point [06 §10]. It is installed here rather
	// than by the session composer because it carries no session state: the
	// record it pushes reaches the mover, the RNG and every other session-owned
	// port through the victim's own queue binding.
	combat.ParalyzeTaskPush = PushParalyzeCredit
}
