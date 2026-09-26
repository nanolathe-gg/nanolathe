package profiles

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
)

// Gold's installed DLL predates the multiplier addition in the named source
// table [research/extensions/escalation-shields.md "Passive generator healing"].
func TestEscalationHistoricalHealingProfile(t *testing.T) {
	profile, err := Lookup("escalation")
	if err != nil {
		t.Fatal(err)
	}
	got, err := community.Resolve(false, profile.GameplaySources()...)
	if err != nil || !got.HealTimeBitmask || !got.RepairRate.Enabled || got.RepairRate.RepairMultiplier != 1 || got.RepairRate.SelfHealMultiplier != 1 {
		t.Fatalf("Gold historical healing: %+v, %v", got, err)
	}
	strict, err := community.Resolve(true, profile.GameplaySources()...)
	if err != nil || strict != (community.Features{}) {
		t.Fatalf("Strict profile bypass: %+v, %v", strict, err)
	}
}
