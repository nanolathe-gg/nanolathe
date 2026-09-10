package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Air order adapters must preserve targets on a rejected control-byte guard
// and arrange exactly one deferred clear on a transition [04 R-ORD-01 §7].
func TestAirWeaponAdapterControlTransitions(t *testing.T) {
	for _, inhibit := range []bool{false, true} {
		name := "release"
		verb := ReleaseWeaponSlot
		if inhibit {
			name, verb = "inhibit", InhibitWeaponSlot
		}
		t.Run(name, func(t *testing.T) {
			_, _, u, _ := newTestWorldAndUnits(t)
			u.InstallWeapon(0, &content.WeaponDef{})
			slot := u.SlotAt(0)
			vm := cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "TargetCleared", 0))
			attachTestCOB(u, vm)
			target := units.Target{Kind: units.TargetUnit, Unit: 9}
			slot.Target = target
			slot.Flags &^= units.SlotFlagAutonomous
			if inhibit {
				slot.Flags |= units.SlotFlagAutonomous
			}
			verb(u, 0) // Already in the requested control state.
			if slot.Target != target {
				t.Fatal("unchanged control state discarded a target")
			}
			slot.Flags ^= units.SlotFlagAutonomous
			verb(u, 0)
			if slot.Target.Kind != units.TargetNone {
				t.Fatal("control transition retained target")
			}
			started := 0
			for _, thread := range vm.Threads {
				if thread.Status != cob.ThreadIdle {
					started++
					if thread.PC != 0 || thread.SP != 1 || thread.Stack[0] != 0 {
						t.Fatal("target-clear callback ran synchronously or lost slot index")
					}
				}
			}
			if started != 1 {
				t.Fatalf("target-clear callbacks = %d, want one", started)
			}
		})
	}
}

// Replacing the slot target discards the previous target's weapon outcome
// bits, preserving other events and the asynchronous Aim state [06 R-WPN-05 §6].
func TestAirWeaponAdapterTargetReplacementClearsOldOutcomes(t *testing.T) {
	for _, point := range []bool{false, true} {
		_, _, u, _ := newTestWorldAndUnits(t)
		u.InstallWeapon(0, &content.WeaponDef{})
		slot := u.SlotAt(0)
		slot.Aim.Ready, slot.Aim.IssueBit = true, true
		aim := slot.Aim
		u.Pending = units.PendingSlotSetterClear | 8
		if point {
			FireWeaponPoint(u, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(-32768), 0)
			if slot.Target.Z != numeric.FixedFromInt(-32767) {
				t.Fatal("point target collided with the empty sentinel")
			}
		} else {
			FireWeaponTarget(u, 0, 9, 0)
		}
		if u.Pending != 8 || slot.Aim != aim {
			t.Fatalf("point=%v pending=%#x aim=%+v: target replacement retained old outcomes or changed Aim", point, u.Pending, slot.Aim)
		}
	}
}
