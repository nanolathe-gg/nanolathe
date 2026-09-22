package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func communityVeterancyService(enabled bool) *Service {
	return &Service{Rules: &CommunityRules{}, Community: community.Features{Veterancy: enabled}}
}

func TestCommunityVeterancyDefaultsAreRetailIdentity(t *testing.T) {
	def := &content.UnitDef{VeterancyThresholds: []uint32{5, 10, 15, 20, 25}, VeterancyAccuracyBuffRate: 12}
	service := communityVeterancyService(true)
	for _, kills := range []int32{0, 5, 6, 23, 24, 36, 65535} {
		if got, want := service.VeteranLevel(def, kills), uint32(veteranTier(kills)); got != want {
			t.Fatalf("kills %d: bounded level = %d, want retail %d", kills, got, want)
		}
		if got, want := service.rules().VeteranLevel(VeteranLevelRequest{Service: service, Definition: def, Kills: uint16(kills), Unbounded: true}), uint32(uint16(kills))/5; got != want {
			t.Fatalf("kills %d: unbounded level = %d, want retail %d", kills, got, want)
		}
		if got, want := service.veteranLeadAdmitted(def, kills), uint16(kills) > 5; got != want {
			t.Fatalf("kills %d: lead = %v, want retail %v", kills, got, want)
		}
		if got, want := service.veteranSpreadDivisor(def, kills), uint32(uint16(kills))/12; got != want {
			t.Fatalf("kills %d: spread divisor = %d, want retail %d", kills, got, want)
		}
	}
}

// The retail-only arithmetic wrappers below are fixtures for older focused
// contracts. Live simulation resolves its level/divisor through bound rules
// before entering the same primitives.
func veteranTier(kills int32) int32 {
	return int32(StrictRules{}.VeteranLevel(VeteranLevelRequest{Kills: uint16(kills)}))
}

func AccuracySpreadBound(accuracy, health, maxHealth, kills int32) uint16 {
	return accuracySpreadBoundWithDivisor(accuracy, health, maxHealth, uint32(uint16(kills))/12)
}

func ComputeStoredReload(health, maxHealth, kills, authoredReload int32) int32 {
	return computeStoredReloadAtLevel(health, maxHealth, veteranTier(kills), authoredReload)
}

func TestCommunityVeterancyCustomConsumerBounds(t *testing.T) {
	thresholds := make([]uint32, 30)
	for i := range thresholds {
		thresholds[i] = uint32(i + 1)
	}
	def := &content.UnitDef{UnitName: "veteran", DamageModifier: 65536, VeterancyThresholds: thresholds, VeterancyAccuracyBuffRate: 7}
	service := communityVeterancyService(true)
	strict := &Service{Rules: StrictRules{}, Community: community.Features{Veterancy: true}}
	if got := strict.VeteranLevel(def, 30); got != 5 {
		t.Fatalf("Strict read authored thresholds: level = %d, want retail 5", got)
	}
	if got := strict.storedReload(def, 100, 100, 30, 100); got != 70 {
		t.Fatalf("Strict read authored reload level: reload = %d, want retail 70", got)
	}

	shooter := &units.Unit{Def: def, Kills: 30}
	target := &units.Unit{Def: &content.UnitDef{UnitName: "target", DamageModifier: 65536, VeterancyThresholds: thresholds}, Health: 1000}
	weapon := &content.WeaponDef{DamageDefault: 100}
	if got := service.effectiveDamage(weapon, target, shooter); got != 280 {
		t.Fatalf("unclamped attacker damage = %d, want 280", got)
	}
	target.Kills = 30
	if got := service.effectiveDamage(weapon, target, nil); got != 0 {
		t.Fatalf("clamp-25 defender damage = %d, want 0", got)
	}
	responseService, _, responseTerrain, responseShooter, responseTarget, _ := modernCombatFixture(t)
	responseService.Community.Veterancy = true
	responseTarget.Def.VeterancyThresholds = thresholds
	responseTarget.Kills = 30
	if ModernResponseAdmits(responseShooter, responseTarget, 0, responseTerrain, nil, responseService) {
		t.Fatal("Modern response admitted a shot whose authored defender level reduces damage to zero")
	}
	if got := service.storedReload(def, 100, 100, 30, 100); got != 4 {
		t.Fatalf("clamp-16 reload = %d, want 4", got)
	}

	leadTarget := &units.Unit{Def: &content.UnitDef{BMCode: 1}}
	slot := &units.Slot{Flags: units.SlotFlagEnabled}
	leadWeapon := &content.WeaponDef{WeaponVelocity: 1}
	leadDef := &content.UnitDef{VeterancyThresholds: []uint32{3, 9}, VeterancyAccuracyBuffRate: 7}
	leadShooter := &units.Unit{Def: leadDef, Kills: 3}
	if PreFireLeadGate(leadShooter, leadTarget, slot, leadWeapon, service) {
		t.Fatal("lead admitted at the authored threshold; comparison must be strict")
	}
	leadShooter.Kills = 4
	if !PreFireLeadGate(leadShooter, leadTarget, slot, leadWeapon, service) {
		t.Fatal("lead rejected above the authored threshold")
	}
	if PreFireLeadGate(leadShooter, leadTarget, slot, leadWeapon, strict) {
		t.Fatal("Strict read the authored lead threshold")
	}

	if got := accuracySpreadBoundWithDivisor(0, 50, 100, service.veteranSpreadDivisor(leadDef, 13)); got != 1024 {
		t.Fatalf("spread below divisor boundary = %d, want 1024", got)
	}
	if got := accuracySpreadBoundWithDivisor(0, 50, 100, service.veteranSpreadDivisor(leadDef, 14)); got != 512 {
		t.Fatalf("spread at divisor boundary = %d, want 512", got)
	}
	leadDef.VeterancyAccuracyBuffRate = 0
	if got := service.veteranSpreadDivisor(leadDef, 100); got != 0 {
		t.Fatalf("disabled spread rate divisor = %d, want 0", got)
	}
}

func TestAuthoredVeteranLevelPreservesUpperBoundAndTailFault(t *testing.T) {
	// A literal count would answer 2 here. The source's upper-bound search
	// answers 1 for this malformed authored ordering, which must not be sorted.
	def := &content.UnitDef{VeterancyThresholds: []uint32{5, 100, 6}}
	if got := AuthoredVeteranLevel(def, 10, false); got != 1 {
		t.Fatalf("unsorted upper-bound answer = %d, want 1", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("equal final thresholds did not fault in unbounded extrapolation")
		}
	}()
	AuthoredVeteranLevel(&content.UnitDef{VeterancyThresholds: []uint32{5, 5}}, 6, true)
}
