package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// Exercise the session's ordinary caller, resource rejection before banking,
// completion and full-health gates. There are no extra timer or RNG effects
// [research/extensions/escalation-shields.md "Passive generator healing"].
func TestPassiveHealingRulesResourcesAndRNG(t *testing.T) {
	for _, rules := range []construction.Rules{construction.StrictRules{}, construction.CommunityRules{}, &construction.ModernRules{}} {
		s, def := healTimeFixture(t, 1)
		def.MaxDamage, def.BuildTime, def.BuildCostEnergy = 100, 250, 250
		h := healTimeSpawn(t, s, def, 90)
		u := s.Units.Unit(h)
		s.Build.Rules = rules
		s.Build.Community = community.Features{HealTimeBitmask: true, RepairRate: community.RepairRate{Enabled: true, RepairMultiplier: 1, SelfHealMultiplier: 1}}
		s.Build.PrepareRepairBanks(s.Units)
		sim, crt := s.SimRNG().Draws(), s.CrtRNG().Draws()
		b := &s.Econ.UnitBuckets(h)[economy.Energy]
		u.Remaining = 0.5
		s.stepHealTimeSelfRepair(u, 2)
		if b.Requested != 0 || u.Health != 90 {
			t.Fatal("construction received healing work")
		}
		u.Remaining = 0
		for tick := uint32(1); tick <= 5; tick++ {
			s.stepHealTimeSelfRepair(u, tick)
		}
		b.Carry = 1
		s.stepHealTimeSelfRepair(u, 6)
		b.Carry = 0
		if u.Health != 90 {
			t.Fatal("resource rejection delivered banked healing")
		}
		s.stepHealTimeSelfRepair(u, 8)
		_, strict := rules.(construction.StrictRules)
		if strict {
			if u.Health != 90 || b.Accepted != 0 || b.Requested != 0 {
				t.Fatalf("Strict consumed the extension: hp=%d bucket=%+v", u.Health, b)
			}
		} else if u.Health != 91 || b.Accepted != 3 || b.Requested != 4 {
			t.Fatalf("%T lost caller/bank/resource ordering: hp=%d bucket=%+v", rules, u.Health, b)
		}
		u.Health = 100
		accepted, requested := b.Accepted, b.Requested
		s.stepHealTimeSelfRepair(u, 10)
		u.Health = -1
		s.stepHealTimeSelfRepair(u, 12)
		if b.Accepted != accepted || b.Requested != requested || s.SimRNG().Draws() != sim || s.CrtRNG().Draws() != crt {
			t.Fatal("closed health gate or RNG identity changed")
		}
	}
}
