package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestOrderWeaponPointFlowsThroughProjectileSpawner(t *testing.T) {
	weapon := &content.WeaponDef{ID: 7, LineOfSight: true, WaterWeapon: true, Range: 100, Tolerance: wideDriftTolerance}
	w, terrain, u, _ := newTestWorldAndUnits(t)
	u.InstallWeapon(0, weapon)
	u.SlotAt(0).OrderControl |= orderControlInhibit
	u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: 9}
	if !ReleaseWeaponSlot(u, 0) || u.SlotAt(0).OrderControl&orderControlInhibit != 0 || u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatalf("release did not clear the represented latch and target: %+v", u.SlotAt(0))
	}
	u.SlotAt(0).OrderControl &^= orderControlInhibit
	u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: 9}
	if !InhibitWeaponSlot(u, 0) || u.SlotAt(0).OrderControl&orderControlInhibit == 0 || u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatalf("inhibit did not set the represented latch and clear target: %+v", u.SlotAt(0))
	}
	// Point fire is an explicit order after the stop latch; release the slot
	// before running the normal pipeline so this first shot is admissible.
	if !ReleaseWeaponSlot(u, 0) {
		t.Fatal("release before point fire was not accepted")
	}
	if !FireWeaponPoint(u, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(4), 9) {
		t.Fatal("order point fire was not accepted")
	}
	if got := u.SlotAt(0).Target; got.Kind != units.TargetGround || got.X != numeric.FixedFromInt(12) || got.Z != numeric.FixedFromInt(4) {
		t.Fatalf("point target = %+v, want the order point [06 §1.2]", got)
	}
	// Run the actual units-owned slot through the normal combat phase. This
	// proves the order latch produces a real projectile rather than a
	// presentation-only event [06 §3.3][06 §4].
	var svc Service
	sim := rng.NewSimulation(1)
	sum := svc.StepWeaponsForUnit(u, 9, w, nil, terrain, nil, nil, &sim, nil)
	if sum.Fired != 1 || svc.Count() != 1 {
		t.Fatalf("normal unit step fired=%d count=%d, want one projectile [06 §4]", sum.Fired, svc.Count())
	}

	// Inhibiting the slot clears its target, so the shot the retained point
	// target would have produced does not happen. That target clear is the
	// whole of the established effect [04 R-ORD-01 §1]; the previous
	// expectation here also claimed the control byte suppressed reacquisition,
	// which no section establishes.
	if !InhibitWeaponSlot(u, 0) {
		t.Fatal("inhibit was not accepted")
	}
	blocked := svc.StepWeaponsForUnit(u, 10, w, nil, terrain, nil, nil, &sim, nil)
	if blocked.Fired != 0 || svc.Count() != 1 {
		t.Fatalf("cleared-target unit step fired=%d count=%d, want no shot from an emptied slot [04 R-ORD-01 §1]", blocked.Fired, svc.Count())
	}
}

// TestControlByteBitFourDoesNotGateFiring locks the play-test PT5 correction:
// the slot's order control byte is not consulted by the weapon slot visit.
// [R-ORDER-02 §2]'s cleanup walk sets bit 4 on every assigned slot on every
// order-record removal, including the purge a player's own non-queued order
// performs, and no established path clears it again. Reading it as a firing
// gate therefore silenced a unit's weapons permanently after its owner's first
// order — the reported "units are still not attacking each other". Doc 06's
// slot visit and shot-time admission consult no order control byte
// [06 §1.2][06 §3.3].
func TestControlByteBitFourDoesNotGateFiring(t *testing.T) {
	weapon := &content.WeaponDef{ID: 11, LineOfSight: true, WaterWeapon: true, Range: 100, Tolerance: wideDriftTolerance}
	w, terrain, u, _ := newTestWorldAndUnits(t)
	u.InstallWeapon(0, weapon)

	// A target installed by any path, with the cleanup walk's bit already set
	// on the slot, must still reach the spawner.
	if !FireWeaponPoint(u, 0, numeric.FixedFromInt(12), numeric.FixedFromInt(4), 9) {
		t.Fatal("order point fire was not accepted")
	}
	u.SlotAt(0).OrderControl |= orderControlInhibit

	var svc Service
	sim := rng.NewSimulation(1)
	sum := svc.StepWeaponsForUnit(u, 9, w, nil, terrain, nil, nil, &sim, nil)
	if sum.Fired != 1 || svc.Count() != 1 {
		t.Fatalf("slot carrying control-byte bit 4 fired=%d count=%d, want the ordinary shot [06 §1.2][06 §3.3]", sum.Fired, svc.Count())
	}
	if u.SlotAt(0).OrderControl&orderControlInhibit == 0 {
		t.Fatal("the weapon visit must not write the order control byte [04 R-ORD-01 §1]")
	}
}
