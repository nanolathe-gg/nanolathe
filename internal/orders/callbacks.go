package orders

// Cleanup callbacks [R-ORDER-02 §2]: the StopBuilding emission, the
// TargetCleared weapon-slot walk, and the StartBuilding emitter that owns the
// StopBuilding-pending flag. The strict cleanup order that calls these lives
// in Queue.cleanupNode; every removal path (both pumps, the expiry delegate,
// the insertion purge, the leading-auto drop, and cancel-by-negative) reaches
// them through it.

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Weapon-slot control-byte bits [R-ORDER-02 §2].
//
// Corrected 2026-08-31 [04 R-ORD-01 §7]. `slotControlAssigned` stood here as
// `1 << 1` read off the slot's **Flags** word, with a TODO(T25) saying bit 4's
// semantic name was open. Both are now traced: bits 1 and 4 are two bits of one
// control byte — bit 1 is *the slot is enabled* and bit 4 is the inhibit latch
// — so reading bit 1 from Flags and bit 4 from OrderControl was reading one
// retail byte as two. Bit 1 has no runtime writer in this build and none was
// found in retail, so the single reading of it is combat.go's `slotEnabled`.
const (
	slotOrderInhibit uint8 = units.OrderControlInhibit
	slotTracking     uint8 = 1 << 4 // Flags' autonomous-tracking bit [06 §1.2]
)

// callbackBridgeFor returns the strict production callback bridge attached to
// the unit. A unit without a bound production script has no callback surface
// [04 §4.1]; every arrange below is then a no-op, exactly as retail's arranger
// no-ops when the name lookup fails.
func callbackBridgeFor(u *units.Unit) *cob.CallbackBridge {
	if u == nil {
		return nil
	}
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		return binding.Callbacks
	}
	return nil
}

// arrangeDeferred starts one deferred (mode D) callback by name through the
// bridge's primitive, so the callback mode discipline stays with the bridge
// [04 §4.2][04 §5.1]. The seven-argument arrange shape below is the retail
// call convention for these events [R-ORDER-02 §2].
func arrangeDeferred(bridge *cob.CallbackBridge, name string, args []int32) {
	if bridge == nil {
		return
	}
	bridge.Deferred(name, args, nil)
}

// clearWeaponBuildTargets is the cleanup-side weapon-target-clear helper
// [R-ORDER-02 §2]. It walks the three weapon slots in order; per slot, when
// the control byte has bit 1 set (slot assigned) and bit 4 clear, bit 4 is
// set FIRST, and then only when the slot's target is not already empty are
// the target words reset to their empty form (target word 0, companion word
// at the -32768 sentinel — the decoded TargetNone form here) and the owner's
// COB function TargetCleared arranged with (0, 0, 1, slotIndex, 0, 0, 0).
// The event is script-only: no network event accompanies it, and the arrange
// is a no-op when the unit's script defines no TargetCleared function.
// The starter's parameter map writes the pushed cells into window words 0..3
// in order, so the slot index is the FIRST script argument [R-UNIT-06 §4].
func clearWeaponBuildTargets(u *units.Unit) {
	if u == nil {
		return
	}
	// This walk is *inhibit slot 3* [04 R-ORD-01 §1] and nothing else: the same
	// guard, the same control-byte write, the same conditional target clear and
	// notification, over slots 0, 1, 2 in order. It is expressed as that one
	// helper so the two can never drift apart.
	inhibitSlot(u, slotAll)
}

// emitStopBuilding is the cleanup-side StopBuilding emission [R-ORDER-02 §2]:
// resolve StopBuilding by name in the owner's script and arrange it with an
// empty argument vector (retail pushes arity 0 with four zero cells; the
// window words above the logical top then read zero in retail versus stale
// here — unobservable unless a stock body reads above-top arguments, which
// the [R-UNIT-06 §4] census pattern argues against), and clear the record's pending flag. It runs on EVERY
// removal path and is NOT tombstone-gated; it always precedes the
// tombstone-gated TargetCleared step. A code-9 last-record re-arm keeps the
// record, so cleanup never runs for it and a running StartBuilding keeps
// running with no StopBuilding.
// TODO(T25): retail emits the matching network event alongside the script
// callback; this build has no network layer to receive it.
func emitStopBuilding(u *units.Unit, n *Node) {
	if n == nil || n.Flags&FlagStopBuildingPending == 0 {
		return
	}
	arrangeDeferred(callbackBridgeFor(u), "StopBuilding", nil)
	n.Flags &^= FlagStopBuildingPending
}

// startBuildingBearing is the shared two-position bearing helper behind every
// `StartBuilding` emission [04 R-CB-01 §3]. Given the builder's own position
// and its work target's it forms
//
//	dx = selfX - targetX
//	dz = selfZ - targetZ          (both in whole world units)
//	bearing = round_half_even(atan2(dx, dz) * 65536/2*pi)
//
// The reversed delta is not a slip: with the position step of
// [04 R-MOV-01 §4] travelling along (-sin h, -cos h), the angle of
// (self - target) is exactly the heading that points from the builder at the
// target. The scale factor is the compiled-in 65536/2*pi and the store is an
// x87 integer store, so it rounds to nearest EVEN rather than truncating.
//
// I2 allowlist: retail evaluates this in floating point and the allowlist
// carries the row "`StartBuilding` first-argument bearing"; the float64 is a
// transient narrowed here at the uint16 angle boundary.
func startBuildingBearing(selfX, selfZ, targetX, targetZ numeric.Fixed) uint16 {
	// Whole world units: the high word of a 16.16 coordinate, taken with an
	// arithmetic shift so negative coordinates floor [03 §2.1].
	dx := (selfX.Raw() >> 16) - (targetX.Raw() >> 16)
	dz := (selfZ.Raw() >> 16) - (targetZ.Raw() >> 16)
	return numeric.AngleFromAtan2(dx, dz).Raw()
}

// bearingOffset resolves a heading and a radius into the signed component pair
// the air legs displace a position by: `pos - bearingOffset(h, r)` moves r world
// units ALONG h, and `pos + bearingOffset(h, r)` moves r units opposite it
// [04 §10.3][04 R-AIR-01 §4].
//
// This is the same four-line arithmetic as `offsetAtBearing` in
// internal/movement, deliberately restated rather than shared: that package
// imports this one, so the helper cannot travel in the direction that would let
// one copy serve both, and promoting it into internal/sim/numeric would be a
// cross-package refactor for four lines. Both copies cite the same contract, and
// a change to one is a change to the other.
func bearingOffset(heading uint16, radius numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	sin := int64(numeric.Sin(numeric.Angle(heading)))
	cos := int64(numeric.Cos(numeric.Angle(heading)))
	return numeric.Fixed((int64(radius)*sin + 0x1000) >> 13), numeric.Fixed((int64(radius)*cos + 0x1000) >> 13)
}

// EmitStartBuilding is the StartBuilding emitter [R-ORDER-02 §2] and the ONLY
// writer of FlagStopBuildingPending. Its nine call sites are the
// nanolathe/assist handlers: MobileBuild, VTOL_MobileBuild, HelpBuild,
// VTOL_HelpBuild, Capture (two sites), Reclaim, Resurrect, and RepairUnit.
// It resolves the function named StartBuilding in the owning unit's COB
// script and arranges it at arity 1, window words 1..3 zero-filled
// [R-UNIT-06 §4].
//
// The FIRST script argument is the bearing from the builder to its work
// target, in the 65536-per-circle domain, relative to the builder's own
// heading [04 R-CB-01 §3]. That correction retired the earlier reading, which
// had the value as the low 16 bits of the issuing order record's identity —
// a heap address that no implementation could reproduce — and with it the
// `CreationTick & 0xffff` substitute this emitter used to push. The stock
// census stands and now has an explanation: 49 of 133 shipped StartBuilding
// bodies consume the argument as a build/turret heading because it IS an
// angle. Pushing an order age there turned commander and factory torsos to a
// meaningless direction.
//
// The work target is the order record's goal. The session refreshes a
// target-bearing record's goal from the live target unit each tick, so the
// goal is the work position for the unit-target sites (Reclaim, HelpBuild,
// Capture, Resurrect, RepairUnit) as well as for the placed-site ones.
//
// Eight of retail's nine sites subtract the builder's own heading; the ninth,
// `VTOL_HelpBuild`, passes the absolute bearing so an air builder's script
// receives a world heading [04 R-CB-01 §3]. This build has no VTOL_HelpBuild
// emitter yet; when one is added it must skip the subtraction below.
func EmitStartBuilding(u *units.Unit, n *Node) {
	if u == nil || n == nil {
		return
	}
	bearing := startBuildingBearing(u.X, u.Z, n.GoalX, n.GoalZ)
	arg := bearing - u.Move.Heading // relative to the builder's facing [04 R-CB-01 §3]
	arrangeDeferred(callbackBridgeFor(u), "StartBuilding", []int32{int32(arg)})
	n.Flags |= FlagStopBuildingPending
}
