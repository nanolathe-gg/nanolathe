package orders

// Cleanup callbacks [R-ORDER-02 §2]: the StopBuilding emission, the
// TargetCleared weapon-slot walk, and the StartBuilding emitter that owns the
// StopBuilding-pending flag. The strict cleanup order that calls these lives
// in Queue.cleanupNode; every removal path (both pumps, the expiry delegate,
// the insertion purge, the leading-auto drop, and cancel-by-negative) reaches
// them through it.

import (
	"github.com/nanolathe/nanolathe/internal/cob"
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
		arrangeDeferred(bridge, "TargetCleared", []int32{0, 0, 1, int32(slot), 0, 0, 0})
	}
}

// emitStopBuilding is the cleanup-side StopBuilding emission [R-ORDER-02 §2]:
// resolve StopBuilding by name in the owner's script, arrange it with seven
// zero arguments, and clear the record's pending flag. It runs on EVERY
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
	arrangeDeferred(callbackBridgeFor(u), "StopBuilding", []int32{0, 0, 0, 0, 0, 0, 0})
	n.Flags &^= FlagStopBuildingPending
}

// EmitStartBuilding is the StartBuilding emitter [R-ORDER-02 §2] and the ONLY
// writer of FlagStopBuildingPending. Its nine call sites are the
// nanolathe/assist handlers: MobileBuild, VTOL_MobileBuild, HelpBuild,
// VTOL_HelpBuild, Capture (two sites), Reclaim, Resurrect, and RepairUnit.
// It resolves the function named StartBuilding in the owning unit's COB
// script, arranges it with the seven arguments (0, 0, 1, value16, 0, 0, 0),
// and sets the pending flag on the issuing record so cleanup emits the
// StopBuilding counterpart on every removal path.
//
// The fourth argument carries the low 16 bits of the issuing record's
// identity in retail. Retail order records are heap objects, so that value is
// an allocation artifact; no retail script consumer of it is traced, and a
// Go record has no stable numeric identity to read it from. Deterministic
// simulation state forbids deriving it from a pointer [INVARIANTS I1], so the
// argument is passed as zero.
// TODO(T25): settle the fourth argument's consumer-side identity.
func EmitStartBuilding(u *units.Unit, n *Node) {
	if u == nil || n == nil {
		return
	}
	arrangeDeferred(callbackBridgeFor(u), "StartBuilding", []int32{0, 0, 1, 0, 0, 0, 0})
	n.Flags |= FlagStopBuildingPending
}
