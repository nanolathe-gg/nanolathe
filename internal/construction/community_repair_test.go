package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

func TestCommunityRepairBanksAfterResourceAdmission(t *testing.T) {
	s, builder, target, _ := reclaimFixture(t, 10, 16)
	s.Rules = CommunityRules{}
	s.Community.RepairRate = community.RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 2}
	s.PrepareRepairBanks(s.World)
	target.Def.BuildTime, target.Def.BuildCostEnergy = 1000, 1000
	b := &s.Economy.UnitBuckets(builder.Handle)[economy.Energy]
	if !s.Repair(builder, target, 1) || target.Health != 10 {
		t.Fatal("fractional visit must draw beam without healing yet")
	}
	b.Carry = 1
	if s.Repair(builder, target, 1) {
		t.Fatal("unfunded visit accepted")
	}
	b.Carry = 0
	for range 3 {
		if !s.Repair(builder, target, 1) {
			t.Fatal("funded visit refused")
		}
	}
	if target.Health != 11 || b.Accepted != 4 || b.Requested != 5 {
		t.Fatalf("bank/resource order: health=%d bucket=%+v", target.Health, b)
	}
	if !s.Repair(builder, target, 0) || b.Accepted != 5 {
		t.Fatal("zero work must still charge minimum energy and return true")
	}
	target.Def.BuildTime = 0
	if s.Repair(builder, target, 1) || b.Requested != 6 {
		t.Fatal("invalid build time touched resources")
	}
	target.Def.BuildTime = 1000
	// Passive healing and explicit self-repair use different multipliers but
	// share the self-target bank, which coexists with the active target bank.
	builder.Health = 10
	builder.Def.BuildTime, builder.Def.BuildCostEnergy = 1000, 1000
	if !s.RepairPassive(builder, 1) || !s.Repair(builder, builder, 1) || !s.RepairPassive(builder, 1) || !s.Repair(builder, builder, 1) || builder.Health != 11 {
		t.Fatal("passive/active caller distinction or shared self bank lost")
	}
	for range 3 {
		s.Repair(builder, target, 1)
	}
	if target.Health != 12 {
		t.Fatal("self repair discarded the other target's fraction")
	}
	if s.RepairBankFallbacks != 0 {
		t.Fatal("composed repair fell back")
	}
}

func TestCommunityRepairStrictAndDisabledRetainRetail(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}, &ModernRules{}} {
		s, builder, target, _ := reclaimFixture(t, 10, 16)
		s.Rules = rules
		s.Community.RepairRate = community.RepairRate{RepairMultiplier: 3, SelfHealMultiplier: 3}
		target.Def.BuildTime, target.Def.BuildCostEnergy = 1000, 1000
		if !s.Repair(builder, target, 1) || target.Health != 11 {
			t.Fatalf("%T did not retain retail repair", rules)
		}
	}
	s, builder, target, _ := reclaimFixture(t, 10, 16)
	s.Rules = StrictRules{}
	s.Community.RepairRate = community.RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 3}
	target.Def.BuildTime = 1000
	if !s.Repair(builder, target, 1) || target.Health != 11 || len(s.repairBanks) != 0 {
		t.Fatal("Strict consumed Community policy")
	}
}

func TestCommunityRepairFallbackAndPacketClamp(t *testing.T) {
	s, builder, target, _ := reclaimFixture(t, 10, 16)
	s.Rules = CommunityRules{}
	s.Community.RepairRate = community.RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 3}
	// A deliberately uncomposed service exercises the diagnostic fallback.
	target.Def.BuildTime = 1000
	if !s.Repair(builder, target, 1) || target.Health != 11 || s.RepairBankFallbacks != 1 {
		t.Fatal("missing bank must round up and record its fallback")
	}
	s.PrepareRepairBanks(s.World)
	target.Health, target.MaxHealth, target.Def.BuildTime = 10, 65535, 1
	if !s.Repair(builder, target, 1000) || target.Health != -1 || s.RepairBankFallbacks != 1 {
		t.Fatal("healing quantum must clamp before the unsigned packet and signed health store")
	}
}
