package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// TestProTAOrderSwitchesProjectOntoTheOrderBinding checks that the three
// ProTA 4.8 order switches reach the order package's projected copy under
// Community 3.9 and Modern, and that Strict 3.1 projects none of them
// (DESIGN_COMMUNITY_PATCH §4.7).
func TestProTAOrderSwitchesProjectOntoTheOrderBinding(t *testing.T) {
	on := true
	sources := CommunitySources{Content: []community.Overrides{{
		WorkingWeaponsAutonomous: &on, AttackSingleSlotTake: &on, ResurrectionTextFix: &on,
	}}}
	for _, mode := range []gameplay.Mode{gameplay.Community39, gameplay.Modern} {
		s := &Session{CommunitySources: sources}
		if err := s.SetRules(string(mode)); err != nil {
			t.Fatal(err)
		}
		got := s.newOrderBinding().Community
		if !got.WorkingWeaponsAutonomous || !got.AttackSingleSlotTake || !got.ResurrectionTextFix {
			t.Fatalf("%s: order projection %+v", mode, got)
		}
		if err := s.SetRules(StrictRuleSetName); err != nil {
			t.Fatal(err)
		}
		if got := s.newOrderBinding().Community; got != (community.Features{}) {
			t.Fatalf("%s then Strict: order projection %+v", mode, got)
		}
	}
}
