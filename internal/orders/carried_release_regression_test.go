package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// BeCarried uses the guarded release verb, including its inhibit-latch write
// and preservation of autonomous targets [04 R-ORD-01 §2][04 R-ORD-01 §7].
func TestBeCarriedReleasesInhibitedSlotsAndPreservesAutonomousTargets(t *testing.T) {
	_, u := standingFixture(nil)
	u.Attachment.Carrier = 2
	u.Slots[0].Flags = units.SlotFlagEnabled | units.SlotFlagAutonomous
	u.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: 3}
	u.Slots[1].Flags = units.SlotFlagEnabled
	u.Slots[1].Target = units.Target{Kind: units.TargetUnit, Unit: 4}
	if got := beCarriedHandler(u, &Node{}, 0, 1); got != 1 {
		t.Fatalf("phase 0 returned %d, want advance", got)
	}
	if u.Slots[0].Flags&units.SlotFlagAutonomous != 0 || u.Slots[0].Target.Kind != units.TargetNone {
		t.Error("inhibited slot was not released")
	}
	if u.Slots[1].Target.Unit != 4 || u.Slots[1].Target.Kind != units.TargetUnit {
		t.Error("release discarded an autonomous target")
	}
}
