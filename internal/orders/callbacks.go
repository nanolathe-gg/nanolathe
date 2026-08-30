package orders

// Cleanup callbacks [R-ORDER-02 §2]: the StopBuilding emission, the
// TargetCleared weapon-slot walk, and the StartBuilding emitter that owns the
// StopBuilding-pending flag. The strict cleanup order that calls these lives
// in Queue.cleanupNode; every removal path (both pumps, the expiry delegate,
// the insertion purge, the leading-auto drop, and cancel-by-negative) reaches
// them through it.

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Weapon-slot control-byte bits [R-ORDER-02 §2]. Bit 1 marks the slot as
// assigned; bit 4 is the clear latch this package's walk sets and the
// mid-life clear variant clears.
// TODO(T25): bit 4's semantic name is an open research item; only the
// set/clear behavior is established.
const (
	slotControlAssigned uint8 = 1 << 1
	slotClearedLatch    uint8 = 1 << 4
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
	bridge := callbackBridgeFor(u)
	for slot := 0; slot < units.NumSlots; slot++ {
		s := u.SlotAt(slot)
		if s == nil {
			continue
		}
		if s.Flags&slotControlAssigned == 0 || s.Flags&slotClearedLatch != 0 {
			continue
		}
		s.Flags |= slotClearedLatch
		if s.Target.Kind == units.TargetNone {
			continue // target words already empty: reset and signal are skipped
		}
		s.Target = units.Target{Kind: units.TargetNone}
		arrangeDeferred(bridge, "TargetCleared", []int32{int32(slot)})
	}
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
	dx := float64(int64(selfX) >> 16)
	dz := float64(int64(selfZ) >> 16)
	dx -= float64(int64(targetX) >> 16)
	dz -= float64(int64(targetZ) >> 16)
	// 10430.37835047 = 65536 / 2*pi, the constant retail compiles in.
	return uint16(int32(math.RoundToEven(math.Atan2(dx, dz) * 65536.0 / (2 * math.Pi))))
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
