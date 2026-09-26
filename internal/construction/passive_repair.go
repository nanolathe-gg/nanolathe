package construction

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// StepPassiveRepair follows the bound caller contract before spending resources
// through the existing passive contribution helper. No timer state is needed.
func (s *Service) StepPassiveRepair(target *units.Unit, tick uint32) bool {
	if s == nil || target == nil || target.Def == nil {
		return false
	}
	work, admitted := s.rules().PassiveRepairWork(s, target, tick)
	return admitted && s.RepairPassive(target, work)
}

// PassiveRepairWork retains the retail caller, including its lack of a
// completion gate [05 R-WORK-01 §3].
func (StrictRules) PassiveRepairWork(_ *Service, target *units.Unit, tick uint32) (int32, bool) {
	return HealQuantum(target.Def.HealTime), tick&7 == 0
}

// PassiveRepairWork supplies the Gold executable's caller contract; health
// conversion and energy admission remain with RepairContribution
// [research/extensions/escalation-shields.md "Passive generator healing"].
func (CommunityRules) PassiveRepairWork(s *Service, target *units.Unit, tick uint32) (int32, bool) {
	if s == nil || !s.Community.HealTimeBitmask {
		return (StrictRules{}).PassiveRepairWork(s, target, tick)
	}
	healTime := int32(int16(target.Def.HealTime))
	if healTime == 0 || math.Float32bits(target.Remaining) != 0 || uint8(tick)&uint8(healTime) != 0 {
		return 0, false
	}
	return healTime * 32 / 30, true
}
