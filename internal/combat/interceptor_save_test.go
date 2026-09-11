package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Acquisition stores the current projectile point as whole-world target words
// [06 §11.2][06 R-WPN-04 §1]. Fractional motion must not make a live battle
// impossible to serialize through the unchanged slot codec [08 R-SAVE-WEAPON-01].
func TestInterceptorAcquisitionRemainsSaveable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		current, want Vec3
	}{
		{"fractional", Vec3{X: fixedI(20) + 32768, Z: fixedI(30) + 49152}, Vec3{X: fixedI(20), Z: fixedI(30)}},
		{"negative", Vec3{X: fixedI(-2) + 32768, Z: fixedI(-3) + 49152}, Vec3{X: fixedI(-2), Z: fixedI(-3)}},
		{"sentinel", Vec3{X: fixedI(20) + 32768, Z: fixedI(-32768) + 1}, Vec3{X: fixedI(20), Z: fixedI(-32767)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, _ := newTestWorldAndUnits(t)
			incoming := weaponForStockpile(300, 10, 0, 0, true, false, true, 0, 0, 0)
			interceptor := weaponForStockpile(301, 10, 0, 100, true, true, false, 0, 0, 0)
			cat := interceptorTestCatalog(t, incoming, interceptor)
			shooter.InstallWeapon(0, interceptor)
			slot := shooter.SlotAt(0)
			slot.Ammo = 1
			slot.Aim.Ready = true
			slot.Flags |= units.SlotFlagAimLatch
			flags, aim := slot.Flags, slot.Aim
			shooter.Pending = units.PendingSlotSetterClear | 8
			var svc Service
			h, ok := svc.Reserve()
			if !ok {
				t.Fatal("reserve incoming projectile")
			}
			svc.Records[int(h)-1] = Projectile{
				WeaponID: incoming.ID, ShooterSide: 1,
				Pos:       tc.current,
				TargetPos: Vec3{X: shooter.X, Z: shooter.Z},
			}
			svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, nil, cat, nil)
			if slot.Target.Kind != units.TargetGround {
				t.Fatalf("interceptor did not acquire: %+v", slot.Target)
			}
			resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h != 0 }
			image, err := units.RetailUnitImage(shooter, 0, resolve, resolve, units.RetailUnitWriterScratch{})
			if err != nil {
				t.Fatalf("save during interception: %v", err)
			}
			if slot.Target.X != tc.want.X || slot.Target.Z != tc.want.Z {
				t.Fatalf("target=%+v; want whole-world current position %+v", slot.Target, tc.want)
			}
			if slot.Flags != flags || slot.Aim != aim || shooter.Pending != 8 {
				t.Fatal("acquisition changed aim/control state or failed to clear setter events")
			}
			var restored units.Unit
			if err := units.RetailUnitBase(&restored, image); err != nil {
				t.Fatal(err)
			}
			if err := units.RetailUnitWeaponTargets(&restored, nil); err != nil {
				t.Fatal(err)
			}
			if restored.SlotAt(0).Target != slot.Target {
				t.Fatalf("restored target=%+v, live=%+v", restored.SlotAt(0).Target, slot.Target)
			}
		})
	}
}
