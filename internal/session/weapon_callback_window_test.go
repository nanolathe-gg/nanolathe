package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestWeaponCallbacksUseTheUnitVisitDrain locks the unit-phase callback window:
// Aim is delivered by the normal drain after its visit's fire decision, while
// Fire and RockUnit queued by a later visit run in that visit's same drain
// [04 §1.1][04 R-MOV-03 §1][04 §5.4][I7].
func TestWeaponCallbacksUseTheUnitVisitDrain(t *testing.T) {
	s := newLoopTestSession(t, 2)
	shooter, target := callbackWindowUnits(s)
	if shooter == nil || target == nil {
		t.Fatal("fixture lacks shooter or target")
	}
	vm := callbackWindowVM()
	attachCallbackWindowVM(shooter, vm)
	weapon := callbackWindowWeapon(1)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= units.SlotFlagEnabled
	s.Catalog.Weapons = map[string]*content.WeaponDef{"window": weapon}
	s.Catalog.RebuildWeaponIndex()

	vm.DrainCalls = 0
	s.stepUnitPhase(1)
	if got := s.Combat.Count(); got != 0 {
		t.Fatalf("visit 1 fired %d projectiles; its Aim return is delivered after the fire decision [04 §1.1]", got)
	}
	if !slot.Aim.Ready || vm.DrainCalls != 1 {
		t.Fatalf("visit 1 aim/drain = ready %t drains %d, want delivered aim and one normal drain", slot.Aim.Ready, vm.DrainCalls)
	}
	if activeSleepers(vm) != 0 {
		t.Fatalf("visit 1 left %d Fire/Rock sleepers without a shot", activeSleepers(vm))
	}

	s.stepUnitPhase(2)
	if got := s.Combat.Count(); got != 1 {
		t.Fatalf("visit 2 projectiles = %d, want one after the prior visit's Aim permission", got)
	}
	if vm.DrainCalls != 2 || activeSleepers(vm) != 2 {
		t.Fatalf("visit 2 drains/sleepers = %d/%d, want one new drain and Fire/Rock both started by it", vm.DrainCalls, activeSleepers(vm))
	}
}

// TestWeaponSlotZeroCallbacksPrecedeSlotOneAimPreparation locks visit order at
// the producer boundary, before the shared normal drain [04 §1.1][I7].
func TestWeaponSlotZeroCallbacksPrecedeSlotOneAimPreparation(t *testing.T) {
	s := newLoopTestSession(t, 2)
	shooter, target := callbackWindowUnits(s)
	if shooter == nil || target == nil {
		t.Fatal("fixture lacks shooter or target")
	}
	vm := callbackWindowVM()
	attachCallbackWindowVM(shooter, vm)
	for i := 0; i < 2; i++ {
		weapon := callbackWindowWeapon(int32(i + 1))
		shooter.InstallWeapon(i, weapon)
		slot := shooter.SlotAt(i)
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
		slot.Flags |= units.SlotFlagEnabled
		if i == 0 {
			slot.Aim.Ready = true
			slot.Aim.IssueBit = true
		}
	}
	s.Catalog.Weapons = map[string]*content.WeaponDef{"zero": shooter.SlotAt(0).Weapon, "one": shooter.SlotAt(1).Weapon}
	s.Catalog.RebuildWeaponIndex()
	s.stepUnitPhase(1)
	if got := s.Combat.Count(); got != 1 {
		t.Fatalf("slot zero did not fire before slot one setup: %d projectiles", got)
	}
	// All three callbacks sleep on their first instruction. Allocation is first
	// inactive slot, so their thread slots prove producer order without merely
	// counting callback names [04 §4.2].
	for i, wantPC := range []int{6, 10, 14} {
		if got := vm.Threads[i]; got.Status != cob.ThreadSleeping || got.PC != wantPC {
			t.Fatalf("thread %d = status %d PC %d, want Fire/Rock/Aim sleeper at PC %d", i, got.Status, got.PC, wantPC)
		}
	}
}

func callbackWindowWeapon(id int32) *content.WeaponDef {
	return &content.WeaponDef{ID: id, Range: 10000, WeaponVelocity: 100 * 65536 / 30, LineOfSight: true, Turret: true, Tolerance: 32767}
}

func callbackWindowVM() *cob.VM {
	// Each callback sleeps for one normal tick. The parked threads make the
	// unit-phase drain observable without relying on a callback-name counter.
	return cob.NewVM(&cob.Program{Code: []uint32{
		0x10021001, 1, 0x10065000, // AimPrimary
		0x10021001, 34, 0x10013000, 0x10065000, // FirePrimary
		0x10021001, 34, 0x10013000, 0x10065000, // RockUnit
		0x10021001, 34, 0x10013000, 0x10065000, // AimSecondary
	}, Scripts: map[string]int{"AimPrimary": 0, "FirePrimary": 3, "RockUnit": 7, "AimSecondary": 11}, ScriptsByID: []int{0, 3, 7, 11}, Pieces: []string{"root"}})
}

func attachCallbackWindowVM(u *units.Unit, vm *cob.VM) {
	binding := &cob.Binding{VM: vm, Callbacks: cob.NewCallbackBridge(vm), Model: &model.Model{Root: 0, Pieces: []model.Piece{{Name: "root", Parent: -1}}}, PieceMap: []int{0}}
	u.Script = vm
	u.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
}

func activeSleepers(vm *cob.VM) int {
	n := 0
	for _, thread := range vm.Threads {
		if thread.Status == cob.ThreadSleeping {
			n++
		}
	}
	return n
}

func callbackWindowUnits(s *Session) (*units.Unit, *units.Unit) {
	if s == nil || s.Units == nil {
		return nil, nil
	}
	all := s.Units.IterSliced()
	if len(all) < 2 {
		return nil, nil
	}
	return all[0], all[1]
}
