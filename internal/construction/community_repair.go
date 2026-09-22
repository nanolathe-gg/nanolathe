package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type repairBank struct {
	target    pool.Handle
	remainder int32
}

// PrepareRepairBanks sizes scratch at composition, never in a repair visit.
// Source target pointers identify stable array slots, so handles preserve the
// same reuse semantics. Banks survive mode switches within one battle and reset
// when its unit world changes (community-patch-engine.md CP-DMG-4).
func (s *Service) PrepareRepairBanks(w *units.World) {
	if s == nil || w == nil || !s.Community.RepairRate.Enabled || s.repairWorld == w {
		return
	}
	s.repairBanks = make([][2]repairBank, w.TotalRecords())
	s.repairWorld = w
}

func (s *Service) repairBank(builder, target pool.Handle) *int32 {
	if builder == 0 || int(builder) >= len(s.repairBanks) {
		s.RepairBankFallbacks++
		return nil
	}
	pair := &s.repairBanks[builder]
	for i := range pair {
		if pair[i].target == target {
			return &pair[i].remainder
		}
	}
	for i := range pair {
		if pair[i].target == 0 {
			pair[i] = repairBank{target: target}
			return &pair[i].remainder
		}
	}
	pair[0] = repairBank{target: target}
	return &pair[0].remainder
}

// communityRepairEnergy retains the shared helper's working-precision sequence
// and low-word truncation, then applies the patch's minimum rather than retail's
// maximum of one (community-patch-engine.md CP-DMG-4; [05 R-WORK-01 §3]).
func communityRepairEnergy(cost float32, worker, buildTime int32) int32 {
	energy := numeric.TruncateFloat64ToLow32(1 + (float64(float64(cost)*float64(worker))-1)/float64(buildTime))
	if energy < 1 {
		return 1
	}
	return energy
}

func (CommunityRules) RepairContribution(s *Service, builder, target *units.Unit, worker int32, passive bool) bool {
	if s == nil || !s.Community.RepairRate.Enabled {
		return (StrictRules{}).RepairContribution(s, builder, target, worker, passive)
	}
	if builder == nil || target == nil || target.Def == nil {
		return false
	}
	d := target.Def
	if int32(int16(target.Health)) >= d.MaxDamage || d.BuildTime <= 0 {
		return false
	}
	energy := communityRepairEnergy(d.BuildCostEnergy, worker, d.BuildTime)
	if s.Economy == nil || !economy.AdmitOneResource(s.Economy.UnitBuckets(builder.Handle), float32(energy)) {
		return false
	}
	// Resource admission precedes every bank access, including zero work.
	if worker <= 0 {
		return true
	}
	multiplier := s.Community.RepairRate.RepairMultiplier
	if passive {
		multiplier = s.Community.RepairRate.SelfHealMultiplier
	}
	numerator := int64(d.MaxDamage) * int64(worker) * int64(multiplier)
	whole := int32(numerator / int64(d.BuildTime))
	remainder := int32(numerator % int64(d.BuildTime))
	if bank := s.repairBank(builder.Handle, target.Handle); bank != nil {
		*bank += remainder
		if *bank >= d.BuildTime {
			*bank -= d.BuildTime
			whole++
		}
	} else {
		if remainder > 0 {
			whole++
		}
		if whole < 1 {
			whole = 1
		}
	}
	if whole <= 0 {
		return true
	}
	if whole > 65535 {
		whole = 65535
	}
	if s.Combat == nil || s.World == nil {
		return false
	}
	s.Combat.DispatchHealingPacket(s.World, combat.Packet{
		Victim: uint16(target.Handle), Attacker: uint16(builder.Handle), Amount: uint16(whole), Kind: combat.KindHeal,
	})
	return true
}
