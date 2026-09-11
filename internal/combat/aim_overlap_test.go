package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Retargeting can dispatch another Aim while the earlier callback sleeps.
// Both receivers address the weapon slot: a zero return must leave readiness
// granted by the other return untouched [04 R-CB-01 §6][06 §3.3].
func TestOverlappingAimZeroPreservesGrantedReadiness(t *testing.T) {
	w, terrain, u, target := newTestWorldAndUnits(t)
	vm := cob.NewVM(progWithAim([]uint32{
		0x10021001, 100, 0x10013000, // sleep, then return the supplied yaw
		0x10021002, 0, 0x10065000,
	}, "AimPrimary", 0))
	attachTestCOB(u, vm)
	u.Move.Heading = 0
	weapon := weaponTurret(1)
	weapon.Range = 1000
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	slot.Flags |= units.SlotFlagEnabled
	slot.Reload = 100 // retain readiness until both deferred returns arrive
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	sawReady := false
	dispatches := 0
	for tick := uint32(1); tick <= 12; tick++ {
		if tick == 2 {
			w.Destroy(target.Handle, units.DeathKilled)
		}
		if tick == 3 {
			// North at heading zero gives the second callback a zero yaw.
			slot.Target = units.Target{Kind: units.TargetGround, X: u.X, Z: u.Z - numeric.FixedFromInt(10)}
		}
		sum := svc.StepWeaponsForUnit(u, tick, w, nil, terrain, nil, cat, nil, nil)
		if sum.Dispatched {
			dispatches++
		}
		drainTestUnitCOB(t, u)
		sawReady = sawReady || slot.Aim.Ready
	}
	if dispatches != 2 || !sawReady {
		t.Fatalf("fixture did not overlap a granting and zero Aim: dispatches=%d sawReady=%t", dispatches, sawReady)
	}
	if !slot.Aim.Ready {
		t.Fatal("later zero Aim erased readiness granted by the earlier callback")
	}
	slot.Reload = 0
	if sum := svc.StepWeaponsForUnit(u, 13, w, nil, terrain, nil, cat, nil, nil); sum.Fired != 1 {
		t.Fatalf("retained Aim grant did not permit the shot: %+v", sum)
	}
}
