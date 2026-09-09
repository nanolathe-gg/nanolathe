package session

import "github.com/nanolathe-gg/nanolathe/internal/units"

// finalizeReclaimRefund runs within the victim's one normal death visit, after
// ordinary teardown and before explosion/corpse processing. The raw attacker
// slot may be freed or reused; its owner also identifies the cached economy
// selector through normal creation, capture and restore [05 R-WORK-01 §4].
func (s *Session) finalizeReclaimRefund(victim *units.Unit) {
	if s.Econ == nil || s.Units == nil || victim == nil || victim.Def == nil || victim.LastDamageCause != 5 {
		return
	}
	attacker := s.Units.RawUnitRecord(victim.EngagementTarget)
	if attacker == nil {
		return
	}
	var controller uint8
	if int(attacker.Owner) < len(s.Econ.Players) {
		player := &s.Econ.Players[attacker.Owner]
		if player.Exists {
			controller = player.ControllerState
		}
	}
	s.Econ.CreditUnitReclaimRefund(attacker.Handle, victim.Remaining, victim.Def.BuildCostMetal, controller)
}
