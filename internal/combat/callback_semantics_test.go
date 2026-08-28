package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestWeaponMuzzleUsesAimFromFallbackAndQuerySeed locks the concrete weapon
// path's two synchronous queries: AimFromPrimary is seeded with -1, and its
// sentinel result selects QueryPrimary, which is seeded with zero [04 §5.3].
func TestWeaponMuzzleUsesAimFromFallbackAndQuerySeed(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	prog := &cob.Program{
		Code:        []uint32{0x10065000, 0x10065000}, // both queries preserve cell-zero seed
		Scripts:     map[string]int{"AimFromPrimary": 0, "QueryPrimary": 1},
		ScriptsByID: []int{0, 1},
		Pieces:      []string{"base"},
	}
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(30)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.MuzzlePiece = -1
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	catalog.RebuildWeaponIndex()

	var svc Service
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
	if slot.MuzzlePiece != 0 {
		t.Fatalf("AimFromPrimary sentinel did not select zero-seeded QueryPrimary: muzzle piece %d", slot.MuzzlePiece)
	}
}

// TestWeaponAimPoolExhaustionDeliversImplicitZero verifies that a full COB
// thread pool reaches the concrete combat receiver as an implicit zero, so
// the missing callback cannot authorize a shot [04 §4.6][06 §3.3].
func TestWeaponAimPoolExhaustionDeliversImplicitZero(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	prog := progWithAim([]uint32{0x10065000}, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	for i := range vm.Threads {
		vm.Threads[i].Status = cob.ThreadSleeping
		vm.Threads[i].Sleep = 100
	}
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(31)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	catalog.RebuildWeaponIndex()

	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
	if !sum.ReturnSeen || sum.ReturnValue != 0 {
		t.Fatalf("full callback pool did not deliver implicit zero: %+v", sum)
	}
	if sum.Fired != 0 || slot.Aim.Ready {
		t.Fatalf("implicit zero authorized a shot: summary=%+v aim=%+v", sum, slot.Aim)
	}
}
