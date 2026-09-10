package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Retaliation reaches the ordinary unit-target setter only when it replaces
// the slot target. That setter discards prior shot feedback while preserving
// other order events and the asynchronous Aim state [06 R-WPN-04 §2]
// [06 R-WPN-05 §6][06 §3.2].
func TestRetaliationTargetReplacementClearsShotEvents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keep    bool
		refuse  bool
		wantSet bool
	}{
		{name: "replacement", wantSet: true},
		{name: "retained target", keep: true},
		{name: "commandfire refused", refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReactionFixture(t)
			installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 400, CommandFire: tc.refuse})
			slot := f.victim.SlotAt(0)
			slot.Aim.IssueBit, slot.Aim.Ready = true, true
			slot.Flags |= units.SlotFlagAimLatch
			if tc.keep {
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: f.attacker.Handle}
			}
			priorAim, priorFlags := slot.Aim, slot.Flags
			const otherEvents uint32 = 0x21
			f.victim.Pending = units.PendingSlotSetterClear | otherEvents
			f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
			want := units.PendingSlotSetterClear | otherEvents
			if tc.wantSet {
				want = otherEvents
				if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != f.attacker.Handle {
					t.Fatal("retaliation did not install its accepted attacker")
				}
			}
			if f.victim.Pending != want {
				t.Fatalf("order events = %#x, want %#x", f.victim.Pending, want)
			}
			if slot.Aim != priorAim || slot.Flags != priorFlags {
				t.Fatal("retaliation target setter changed Aim or slot ownership")
			}
		})
	}
}
