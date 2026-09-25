package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// proTAPackageSources is the content-profile gameplay block that enables the
// five ProTA 4.8 package switches (DESIGN_COMMUNITY_PATCH §4.7).
func proTAPackageSources() CommunitySources {
	on := true
	return CommunitySources{Content: []community.Overrides{{
		Table:                  "prota",
		AIDifficultyIncome:     &on,
		AIStockpileProducts:    &on,
		TargetLockRelease:      &on,
		AIApplianceEnergy:      &on,
		AIBuilderStopThreshold: &on,
	}}}
}

// TestProTAPackageSwitchesProjectOntoTheirOwners checks the composition path:
// the content-profile block reaches the economy, combat and every computer
// player through their own copies under Community 3.9 and Modern, and Strict
// 3.1 projects the zero table whatever the block says.
func TestProTAPackageSwitchesProjectOntoTheirOwners(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Community39, gameplay.Modern} {
		s := &Session{Combat: &combat.Service{}, Econ: &economy.Service{}, CommunitySources: proTAPackageSources()}
		s.AI[2] = &ai.Manager{Player: 2}
		if err := s.SetRules(string(mode)); err != nil {
			t.Fatal(err)
		}
		if !s.Econ.Community.AIDifficultyIncome || !s.Combat.Community.TargetLockRelease {
			t.Fatalf("%s: economy/combat projection %+v / %+v", mode, s.Econ.Community, s.Combat.Community)
		}
		want := community.Features{AIStockpileProducts: true, AIApplianceEnergy: true, AIBuilderStopThreshold: true}
		if s.AI[2].Community != want {
			t.Fatalf("%s: AI projection %+v, want %+v", mode, s.AI[2].Community, want)
		}
		if err := s.SetRules(StrictRuleSetName); err != nil {
			t.Fatal(err)
		}
		if s.Econ.Community != (community.Features{}) || s.Combat.Community != (community.Features{}) || s.AI[2].Community != (community.Features{}) {
			t.Fatalf("%s then Strict: package switches survived", mode)
		}
	}
	// Retail content under Community 3.9 resolves the mainline table, which
	// leaves every package switch off.
	s := &Session{Econ: &economy.Service{}}
	s.AI[1] = &ai.Manager{Player: 1}
	if err := s.SetRules(CommunityRuleSetName); err != nil {
		t.Fatal(err)
	}
	if s.Econ.Community.AIDifficultyIncome || s.AI[1].Community != (community.Features{}) {
		t.Fatal("mainline table enabled a ProTA package switch")
	}
}
