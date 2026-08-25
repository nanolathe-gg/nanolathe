package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The UnitStepSummary contract [ON-09 evidence contract]: the per-unit weapon
// step observes dispatch/return/fire outcomes so the central loop can emit
// truthful trace events and own the exactly-once COB drain [04 §4.2].

func TestRX03_Summary_AimReturnOne_Fires(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		0x10021001, 1, // push 1
		0x10065000, // return
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	shooter.SetScript(vm)
	weapon := weaponTurret(7)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if !sum.Dispatched || sum.DispatchSlot != 0 {
		t.Fatalf("Dispatched=%v slot=%d want true/0", sum.Dispatched, sum.DispatchSlot)
	}
	if !sum.ReturnSeen || sum.ReturnValue != 1 {
		t.Fatalf("ReturnSeen=%v val=%d want true/1", sum.ReturnSeen, sum.ReturnValue)
	}
	if !sum.Drained {
		t.Fatalf("combat must own the drain when Aim dispatched [04 §4.2]")
	}
	if sum.Fired < 1 {
		t.Fatalf("Fired=%d want >=1 after nonzero return", sum.Fired)
	}
}

func TestRX03_Summary_AimReturnZero_NoFire(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		0x10021001, 0, // push 0
		0x10065000,
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	shooter.SetScript(vm)
	weapon := weaponTurret(8)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if !sum.Dispatched || !sum.ReturnSeen || sum.ReturnValue != 0 {
		t.Fatalf("want dispatched+zero return, got %+v", sum)
	}
	if sum.Fired != 0 {
		t.Fatalf("zero return must block fire, Fired=%d", sum.Fired)
	}
}

func TestRX03_Summary_AimSleeping_PendingNotReturned(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// AimPrimary: sleep 67ms (=2 ticks), then push 1 and return.
	code := []uint32{
		0x10021001, 67, // push 67
		0x10013000, // sleep
		0x10021001, 1,
		0x10065000,
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	shooter.SetScript(vm)
	weapon := weaponTurret(9)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if !sum.Dispatched || sum.ReturnSeen {
		t.Fatalf("sleeping aim: Dispatched=%v ReturnSeen=%v want true/false", sum.Dispatched, sum.ReturnSeen)
	}
	if !sum.Drained {
		t.Fatalf("dispatch path drains synchronously [GAP T15 C17]")
	}
}

func TestRX03_Summary_WeaponlessScripted_NotDrainedByCombat(t *testing.T) {
	w, _, u, _ := newTestWorldAndUnits(t)
	code := []uint32{0x10065000} // immediate return
	prog := progWithAim(code, "Create", 0)
	vm := cob.NewVM(prog)
	u.SetScript(vm)
	var svc Service
	sum := svc.StepWeaponsForUnit(u, 1, w, nil, nil, nil, nil, nil, nil)
	if sum.Dispatched || sum.ReturnSeen || sum.Drained || sum.Fired != 0 {
		t.Fatalf("weaponless unit must report an untouched visit, got %+v", sum)
	}
}
