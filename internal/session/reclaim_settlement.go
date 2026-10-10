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
	// The refund is discounted when the owner's record is an existing
	// computer player's (control byte 2) that the lobby did not mark Modern;
	// a Modern one is paid in full (DESIGN_ECONOMY_CONSTRUCTION "Modern AI
	// full income"). The discount selects on that owner's word, its own in a
	// battle whose computers each carry one (docs/DESIGN_MULTIPLAYER.md §6.6).
	s.Econ.CreditUnitReclaimRefund(attacker.Handle, attacker.Owner, victim.Remaining, victim.Def.BuildCostMetal)
}
