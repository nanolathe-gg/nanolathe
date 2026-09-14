package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// The unconditional clear has no enabled/autonomy guard and preserves both
// control and asynchronous Aim state [06 §3.2].
func TestStopWeaponFiringPreservesControlAndAim(t *testing.T) {
	for _, flags := range []uint8{0, units.SlotFlagEnabled, units.SlotFlagEnabled | units.SlotFlagAutonomous} {
		u := &units.Unit{Pending: 0xffff}
		slot := &u.Slots[0]
		slot.Flags = flags
		slot.Aim = cob.AimSlot{IssueBit: true, Ready: true}
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: 7}
		if !StopWeaponFiring(u, 0) || slot.Target.Kind != units.TargetNone {
			t.Fatalf("flags%x: target not cleared", flags)
		}
		if slot.Flags != flags || !slot.Aim.IssueBit || !slot.Aim.Ready || u.Pending != 0xffff {
			t.Fatalf("flags%x: clear changed unrelated state", flags)
		}
		if !StopWeaponFiring(u, 0) || slot.Flags != flags {
			t.Fatal("empty target clear changed control")
		}
	}
}
