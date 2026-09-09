package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The UnitStepSummary contract [ON-09 evidence contract]: the per-unit weapon
// step observes dispatch/return/fire outcomes so the central loop can emit
// truthful trace events and own the exactly-once COB drain [04 §4.2].

func TestRX03_Summary_AimReturnOneFiresOnNextVisit(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		0x10021001, 1, // push 1
		0x10065000, // return
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
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
	if sum.ReturnSeen || sum.Drained || sum.Fired != 0 {
		t.Fatalf("first visit reported deferred completion or fired: %+v", sum)
	}
	drainTestUnitCOB(t, shooter)
	if !slot.Aim.Ready {
		t.Fatal("post-weapons drain did not deliver the nonzero Aim result")
	}
	if next := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, nil, nil); next.Fired != 1 {
		t.Fatalf("next visit fired=%d, want 1 after the delivered Aim result", next.Fired)
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
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(8)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if !sum.Dispatched || sum.ReturnSeen || sum.Drained {
		t.Fatalf("want queued Aim with no combat drain, got %+v", sum)
	}
	drainTestUnitCOB(t, shooter)
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
	attachTestCOB(shooter, vm)
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
	if sum.Drained {
		t.Fatalf("combat must not drain the VM: %+v", sum)
	}
}

func TestRX03_Summary_WeaponlessScripted_NotDrainedByCombat(t *testing.T) {
	w, _, u, _ := newTestWorldAndUnits(t)
	code := []uint32{0x10065000} // immediate return
	prog := progWithAim(code, "Create", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(u, vm)
	var svc Service
	sum := svc.StepWeaponsForUnit(u, 1, w, nil, nil, nil, nil, nil, nil)
	if sum.Dispatched || sum.ReturnSeen || sum.Drained || sum.Fired != 0 {
		t.Fatalf("weaponless unit must report an untouched visit, got %+v", sum)
	}
}
