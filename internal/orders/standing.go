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
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
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
const (
	gateInterrupt   uint32 = 0x8
	gateReArm       uint32 = 0x10
	gateSlotClear   uint32 = 0x10000
	gateCancelBit   uint32 = 0x2
	pendingMovement uint32 = 0x3e0 // the five movement/path bits 0x20..0x200 [04 R-ORD-01 §0]
)

// armDeadline is the shared deadline setter of [04 R-ORD-01 §1]: it stores
// `current tick + n` and ORs bit 0 into the dynamic gate, so the pump's step 1
// delivers bit 0 on expiry [04 §3.3].
//
// The tick is the handler's own fourth argument. WU-18-1 had to arm against
// `Queue.SecondaryTick` — a base the rear-segment walk publishes and the
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

// releaseWeaponSlot is *release slot k* of [04 R-ORD-01 §1]: it clears the
// slot's control-byte bit 4 and clears its target, and fires `TargetCleared`
// under the guard of [R-ORDER-02 §2] — the mid-life mirror form, which
// notifies only while bit 4 was set and only when the target words were not
// already empty. The script event carries the slot index as its first argument.
//
// TODO(question): [04 R-ORD-01 §1] gives *release* and *inhibit* as one line
// each ("clears that bit and clears the target", "sets the slot's control-byte
// bit 4 and clears its target") and puts only the notification "under the
// guard of [R-ORDER-02 §2]", so whether the target clear itself is also guarded
// — skipped on a slot the guard rejects — is not established. Both helpers here
// write the control byte and the target unconditionally and guard only the
// notification, which is the reading that keeps the two verbs meaning what
// they say. A trace of the two entry points' writes to the slot's target words
// on a guard-rejected slot would settle it.
//
// These two write the control byte through the constants callbacks.go already
// declares on the slot's flags word (bit 1 assigned, bit 4 the clear latch),
// which is what the cleanup-side walk of [R-ORDER-02 §2] uses. WU-18-4's
// combat.go reads the same field as [06 §1.2]'s armed / aim-latch / tracking
// trio instead and so reproduces only the target clear in its own releaseSlot
// and inhibitSlot. The two readings of one byte should be settled and the two
// helper pairs folded into one; neither is observable today, because nothing
// outside internal/combat's own slot type reads bit 4.
func releaseWeaponSlot(u *units.Unit, slot int) {
	s := slotAt(u, slot)
	if s == nil {
		return
	}
	notify := s.OrderControl&slotOrderInhibit != 0 && s.Target.Kind != units.TargetNone
	s.OrderControl &^= slotOrderInhibit
	s.Target = units.Target{Kind: units.TargetNone}
	if notify {
		arrangeDeferred(callbackBridgeFor(u), "TargetCleared", []int32{int32(slot)})
	}
}

// inhibitWeaponSlot is *inhibit slot k* of [04 R-ORD-01 §1]: it sets the
// slot's control-byte bit 4 and clears its target, notifying under the same
// guard as the release above ([R-ORDER-02 §2]: the slot is assigned, bit 4 was
// clear, and the target words were not already empty).
func inhibitWeaponSlot(u *units.Unit, slot int) {
	s := slotAt(u, slot)
	if s == nil {
		return
	}
	notify := s.Flags&slotControlAssigned != 0 && s.OrderControl&slotOrderInhibit == 0 && s.Target.Kind != units.TargetNone
	s.OrderControl |= slotOrderInhibit
	s.Target = units.Target{Kind: units.TargetNone}
	if notify {
		arrangeDeferred(callbackBridgeFor(u), "TargetCleared", []int32{int32(slot)})
	}
}

// releaseAllWeaponSlots and inhibitAllWeaponSlots are the `k = 3` form: all
// three slots in order 0, 1, 2 [04 R-ORD-01 §1].
func releaseAllWeaponSlots(u *units.Unit) {
	for slot := 0; slot < units.NumSlots; slot++ {
		releaseWeaponSlot(u, slot)
	}
}

func inhibitAllWeaponSlots(u *units.Unit) {
	for slot := 0; slot < units.NumSlots; slot++ {
		inhibitWeaponSlot(u, slot)
	}
}

func slotAt(u *units.Unit, slot int) *units.Slot {
	if u == nil {
		return nil
	}
	return u.SlotAt(slot)
}

// releaseGoalPayload is the fourth goal installer of [04 R-ORD-01 §1] — the
// payload release. Every installer first releases the previous payload (which
// raises pending 0x80) and finishes by clearing pending bits 0x20 through
// 0x200, so the record cannot see a stale movement outcome; the release form
// stops there. The net effect on the record is that all five movement bits are
// clear [04 R-ORD-01 §0].
//
// TODO(T25): this build has no goal-payload object for the orders package to
// release — the movement layer owns the goal and reads the record's goal
// triple — so only the record-side half of the release is represented here.
// Placeholder: clear the five pending bits; the goal triple is left alone,
// because the row that calls this does not write a new goal.
func releaseGoalPayload(n *Node) {
	if n == nil {
		return
	}
	n.Satisfied |= 0x80             // releasing the previous payload raises 0x80 [04 R-ORD-01 §0]
	n.Satisfied &^= pendingMovement // ... and the installer clears 0x20..0x200 last
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
// TODO(T25): the row also has each cleared slot's `StartBuilding` emission
// killed. The StopBuilding-pending flag this build carries is per order RECORD,
// not per weapon slot ([R-ORDER-02 §2] gives it exactly one writer, the
// StartBuilding emitter, which stamps the issuing record), so there is no
// per-slot emission to kill. Placeholder: the target clear and the notification
// run; the emission step is a no-op.
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
// status bit 11"]; the same logical field in this build is the unit's cloak
// request, whose one writer is SetCloaked and whose reader is the cloak upkeep
// debit [05 "Cloak debit"] (I13: one logical field, one Go field).
//
// There is no callback and no caption: the *Cloaked* / *Visible* captions come
// from the edge machine's bit 2, which this handler does not touch.
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

// paralyzeMaxCredit is the row's clamp on the stun credit [04 R-ORD-01 §2].
const paralyzeMaxCredit = 1800

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
// TODO(T25): the row raises and lowers edge bit 4 of the unit's engine-state
// byte. This build models that byte only for bit 0 (activation, the one edge
// setter of [04 R-UNIT-06 §2]) and splits the stun into two named fields
// instead — a boolean and the absolute tick it ends at [06 §10] — with no edge
// machine behind them and therefore no edge callbacks or notifications.
// Placeholder: write both named fields, the expiry from this record's own
// deadline, because that is the same fact and the combat layer's paralyzer
// packets and the weapon and movement gates already read the pair together.
func paralyzeHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if u == nil || n == nil {
		return Code(5)
	}
	if n.Param1 == 0 {
		u.Stunned = false
		u.ParalyzeExpire = 0
		return Code(5) // *complete* [04 R-ORD-01 §2]
	}
	if n.Param1 > paralyzeMaxCredit {
		n.Param1 = paralyzeMaxCredit
	}
	releaseAllWeaponSlots(u)
	clearWeaponTargetsUnconditional(u)
	releaseGoalPayload(n)
	armDeadline(n, tick, n.Param1)
	n.Param1 = 0
	u.Stunned = true
	u.ParalyzeExpire = uint32(n.Deadline)
	return Code(1) // *advance* [04 R-ORD-01 §2]
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
		if scanRadiusTarget(u, int32(n.Param2), true) != nil {
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

// teleportHandler is a single visit. For every live unit other than itself
// whose position lies inside this unit's model bounding box, the row moves that
// unit by the same delta the teleporter's goal describes — its new position is
// `goal + (its position - my position)` — emits the teleport effect (kind 5,
// duration 30) from old to new, and places it through the position setter,
// which re-registers occupancy when the footprint cell changes. Then complete.
// The teleporter itself never moves.
//
// TODO(T25): the row's enumeration has no surface in this package. A handler
// receives the owning unit and its record; the queue's binding resolves a
// single handle and offers no walk over live units, and the position setter
// with its occupancy re-registration lives in the world/movement layer, which
// the orders package must not reach into. Placeholder: nothing is moved and the
// record completes on its single visit, which is also what retail does when the
// bounding box contains no other unit.
func teleportHandler(_ *units.Unit, _ *Node, _ uint32, _ uint32) Code {
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
	for slot := 0; slot < units.NumSlots; slot++ {
		if handle, ok := q.Binding().Weapons.Acquire(u, slot, rangeLimit); ok {
			if target := q.Binding().Lookup(handle); target != nil {
				return target
			}
		}
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
		if u.Def == nil || !u.Def.BMCode {
			return Code(7) // *cancel-all* — no mover reference [04 R-ORD-01 §2]
		}
		inhibitAllWeaponSlots(u)
		n.DynamicGate |= gateSlotClear
		armDeadline(n, tick, 1)
		return Code(1) // *advance*
	case 1:
		if target := opportunityScan(u); target != nil && autoEngage(u, target) {
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
// TODO(question): the row gives the bit-29 test as something phase 0 does "also",
// which would leave `Standby`'s "no mover reference -> cancel-all" in place as
// well — and the two cannot both hold, because a unit carrying bit 29 is
// building-class and a building-class unit never owns a mover [04 R-FAC-02 §5].
// Checked against the reference install (I14): all five stock mines, ARMMINE1
// through ARMMINE5, are `bmcode 0` and name `Standby_Mine` as their
// `defaultmissiontype`, so keeping both tests would cancel every stock mine's
// queue on its first visit and no mine could ever detonate. This handler
// therefore runs the bit-29 test IN PLACE OF the mover test. A trace of
// `Standby_Mine`'s own phase 0 — whether it re-runs the mover test at all —
// would settle it.
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
		if target != nil && target.Move.Mode&0x3 == 1 && u.Flags>>stanceFireShift&stanceFieldMask != 0 {
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

// autoEngage is the shared auto-engage issuer of [04 R-STANCE-01 §3], called
// here with `force = 0`. Its admission, in order: the unit is not its own
// target; the standing MOVE field is nonzero; the standing FIRE field is
// nonzero; and command code 3 (attack a unit) resolves to a non-empty name
// through the resolver of [04 §3.4]. On success the resolved record is inserted
// through the head insert of [04 R-ORD-01 §1].
//
// Hold position refuses autonomous engagement exactly as hold fire does: either
// zero is enough to stop the unit acting on its own.
func autoEngage(u *units.Unit, target *units.Unit) bool {
	if u == nil || target == nil || u == target {
		return false
	}
	if u.Flags>>stanceMoveShift&stanceFieldMask == 0 {
		return false
	}
	if u.Flags>>stanceFireShift&stanceFieldMask == 0 {
		return false
	}
	id := Resolve(3, u, target, nil)
	if id == 0 {
		return false
	}
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	node := Node{Owner: u.Handle, Target: target.Handle, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z}
	if isSecondary(id) {
		q.PushSecondary(id, node)
		return true
	}
	q.PushHead(id, node)
	return true
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

func init() { ensureStandingHandlers() }
