package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestAimKilledBySignalIsNotGrantedByTheSlotsNextOccupant is the combat-level
// half of the COB completion-ownership contract. Aim readiness is granted by
// one thing only: an explicit nonzero return from the Aim callback the weapon
// itself dispatched [06 §3.3][04 §5.3]. A script that signals that callback
// dead and starts an unrelated child into the thread slot it freed produces a
// return from a different script, and the weapon must not read it as its own.
//
// The fixture parks Aim in a sleep so it is still holding slot 0 when the
// second script signals it and refills the slot within the same drain — the
// arrangement the eight-slot pool with lowest-free, immediately-reused
// allocation makes ordinary [04 §4.2].
func TestAimKilledBySignalIsNotGrantedByTheSlotsNextOccupant(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		// AimPrimary @0 — sleep far past the test, then return 0.
		0x10021001, 100000, 0x10013000,
		0x10021001, 0, 0x10065000,
		// Replace @6 — move off signal mask 1 so this thread survives its own
		// signal, release every mask-1 thread (the sleeping Aim), then start
		// Child, which takes the slot the Aim just vacated.
		0x10021001, 2, 0x10068000,
		0x10021001, 1, 0x10067000,
		0x10061000, 2, 0,
		0x10021001, 0, 0x10065000,
		// Child @18 — an explicit nonzero return that belongs to nobody.
		0x10021001, 1, 0x10065000,
	}
	prog := &cob.Program{
		Code:        code,
		Scripts:     map[string]int{"AimPrimary": 0, "Replace": 6, "Child": 18},
		Pieces:      []string{"base"},
		ScriptsByID: []int{0, 6, 18},
	}
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(41)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	slot.Reload = 0
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()

	var svc Service
	// Tick 1 dispatches Aim; the sleep leaves it parked in slot 0.
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil); !sum.Dispatched {
		t.Fatalf("tick 1 did not dispatch Aim: %+v", sum)
	}
	if svc.Count() != 0 || slot.Aim.Ready {
		t.Fatalf("a sleeping Aim granted readiness: count %d ready %v", svc.Count(), slot.Aim.Ready)
	}
	aimThread := vm.LastStartedThread()
	if aimThread != 0 {
		t.Fatalf("Aim thread = %d, want slot 0", aimThread)
	}
	// The signalling script is engine-started like any other callback and takes
	// the next free slot.
	if !vm.StartByName("Replace", nil) {
		t.Fatal("Replace did not start")
	}

	// Tick 2's drain runs Replace: the Aim dies to the signal and Child takes
	// slot 0 in the same pass. Tick 3's drain runs Child, which returns 1.
	for tick := uint32(2); tick <= 4; tick++ {
		svc.StepWeaponsForUnit(shooter, tick, w, nil, terrain, nil, cat, nil, nil)
	}
	if slot.Aim.Ready {
		t.Fatal("a signalled Aim was granted readiness by the return of the script that reused its thread slot [04 §5.3][06 §3.3]")
	}
	if svc.Count() != 0 {
		t.Fatalf("the weapon fired on a return that was never its Aim's: count %d", svc.Count())
	}
}
