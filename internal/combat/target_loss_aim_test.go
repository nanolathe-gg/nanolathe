package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Failed resolution clears the request, not the receiver or desired angles.
// Only an installed stale target emits TargetCleared [06 R-WPN-04 §1].
func TestMissingTargetClearsOnlyAimRequest(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "empty"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			attachTestCOB(shooter, cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "TargetCleared", 0)))
			shooter.InstallWeapon(0, weaponTurret(903))
			slot := shooter.SlotAt(0)
			slot.Aim.IssueBit, slot.Aim.Ready = true, true
			slot.Flags |= units.SlotFlagAimLatch
			// These angles are unrelated to whether a target was installed.
			slot.DesiredYaw, slot.DesiredPitch = 0, 0x8000
			if stale {
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
				target.Alive = false
			}
			clears := 0
			shooter.ScriptState.Binding.Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
				if e.Name == "TargetCleared" && e.Phase == "start" {
					clears++
				}
			})
			var svc Service
			sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, nil, nil, nil)
			if sum.Dispatched || sum.Fired != 0 || slot.Aim.IssueBit || slot.Flags&units.SlotFlagAimLatch != 0 || !slot.Aim.Ready {
				t.Fatalf("failed resolution changed more than the request latch: summary=%+v aim=%+v", sum, slot.Aim)
			}
			if slot.DesiredYaw != 0 || slot.DesiredPitch != 0x8000 || (clears == 1) != stale {
				t.Fatalf("angles or callback changed incorrectly: yaw=%d pitch=%d clears=%d", slot.DesiredYaw, slot.DesiredPitch, clears)
			}
		})
	}
}

// A cancelled Aim does not grant readiness. The empty-target visit clears its
// request so a later target can start a replacement [06 R-WPN-04 §1].
func TestEmptyTargetAllowsAimAfterCancellation(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	vm := cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "AimPrimary", 0))
	attachTestCOB(shooter, vm)
	shooter.InstallWeapon(0, weaponTurret(904))
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	var svc Service
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, nil, nil, nil); !sum.Dispatched {
		t.Fatal("initial Aim was not dispatched")
	}
	vm.Signal(1)
	slot.Target = units.Target{}
	svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, nil, nil, nil)
	if slot.Aim.IssueBit || slot.Aim.Ready {
		t.Fatal("empty target retained the cancelled request or granted readiness")
	}
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	if sum := svc.StepWeaponsForUnit(shooter, 3, w, nil, terrain, nil, nil, nil, nil); !sum.Dispatched {
		t.Fatal("replacement target did not dispatch a fresh Aim")
	}
	drainTestUnitCOB(t, shooter)
	if !slot.Aim.Ready {
		t.Fatal("replacement callback did not grant readiness")
	}
}
