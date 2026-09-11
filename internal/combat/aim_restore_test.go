package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The saved request latch and readiness word are independent. Restoring
// either must not dispatch a replacement callback or manufacture a return
// [08 R-SAVE-WEAPON-01][04 R-CB-01 §6].
func TestRestoredAimPreservesNextWeaponDecision(t *testing.T) {
	for _, ready := range []bool{false, true} {
		name := "pending"
		if ready {
			name = "ready"
		}
		t.Run(name, func(t *testing.T) {
			w, terrain, u, target := newTestWorldAndUnits(t)
			attachTestCOB(u, cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "AimPrimary", 0)))
			weapon := weaponTurret(1)
			weapon.Range = 1000
			u.InstallWeapon(0, weapon)
			slot := u.SlotAt(0)
			slot.Flags |= units.SlotFlagEnabled
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
			cat.RebuildWeaponIndex()
			var before Service
			if sum := before.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, cat, nil, nil); !sum.Dispatched {
				t.Fatal("fixture did not dispatch its initial Aim")
			}
			if ready {
				drainTestUnitCOB(t, u)
			}
			resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), true }
			data, err := units.RetailUnitImage(u, 0, resolve, resolve, units.RetailUnitWriterScratch{})
			if err != nil {
				t.Fatal(err)
			}
			slot.Aim = cob.AimSlot{}
			if err := units.RetailUnitBase(u, data); err != nil {
				t.Fatal(err)
			}
			if err := units.RetailUnitWeaponTargets(u, func(id uint16) (pool.Handle, bool) { return pool.Handle(id), true }); err != nil {
				t.Fatal(err)
			}
			if !slot.Aim.IssueBit || slot.Aim.Ready != ready {
				t.Fatalf("restored Aim=%+v, want held request with ready=%t", slot.Aim, ready)
			}
			var svc Service
			sum := svc.StepWeaponsForUnit(u, 1, w, nil, terrain, nil, cat, nil, nil)
			if sum.Dispatched || (sum.Fired != 0) != ready {
				t.Fatalf("next weapon decision=%+v, want no new Aim and firing=%t", sum, ready)
			}
		})
	}
}
