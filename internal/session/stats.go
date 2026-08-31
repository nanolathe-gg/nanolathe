package session

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RecordDeathStatistics applies the death-credit switch at the authoritative
// finalization boundary. Callers provide the packet's stored attacker-side
// snapshot and the already-resolved cause-3 alliance gate; no live-world scan
// is performed [06 §12.1][08 R-CAMP-01 §7].
func (s *Session) RecordDeathStatistics(in combat.DeathCreditInput) {
	if s == nil || s.Econ == nil {
		return
	}
	credit := combat.ComputeDeathCredit(in)
	if credit.VictimLoss && int(in.VictimOwner) < len(s.Econ.Players) {
		s.Econ.Players[in.VictimOwner].RecordUnitLoss(credit.VictimCommanderLoss)
	}
	if credit.AttackerKill && int(in.AttackerSide) < len(s.Econ.Players) {
		// The full path increments the ordinary kill word and, for a commander
		// victim, the commander-kill word at the same event boundary [06 §12.1].
		s.Econ.Players[in.AttackerSide].RecordUnitKill(in.VictimCommander)
	}
	if credit.AttackerCommanderKill && int(in.AttackerSide) < len(s.Econ.Players) {
		if !credit.AttackerKill {
			s.Econ.Players[in.AttackerSide].RecordCommanderKill()
		}
	}
}

// recordFinalizedDeathStatistics is the session's unit-finalizer bridge. The
// existing OnDeath callback exposes the victim and cause at this boundary, so
// victim loss is filed here exactly once. The combat packet's stored attacker
// side is not part of the Units.OnDeath signature; callers with that packet
// snapshot must use RecordDeathStatistics directly to file kill credit.
func (s *Session) recordFinalizedDeathStatistics(cause units.DeathCause, u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	// O2's packet-provenance fields are authoritative. A zero cause means the
	// death did not retain a known damage packet and therefore receives no
	// inferred statistics credit.
	c := combat.Cause(u.LastDamageCause)
	if c == 0 {
		return
	}
	// TODO(question): central cargo cascade writer must copy the carrier's
	// stored attacker side into LastDamageSide when it emits cause 6; until
	// that producer is wired, this bridge only handles cause 6 packets that
	// already carry both provenance fields [06 §12.1].
	attackerPresent := c == combat.CauseOrdinary || c == combat.CauseSelfDestruct || c == combat.CauseReclaim || c == combat.CauseCargo
	cause3LossEligible := false
	if c == combat.CauseSelfDestruct && s.Econ != nil && int(s.LocalOwner) < len(s.Econ.Players) && int(u.Owner) < len(s.Econ.Players) {
		// The local player's outbound alliance row is the established cause-3
		// gate; a zero byte admits the victim loss [06 §12.1].
		cause3LossEligible = !s.Econ.Players[s.LocalOwner].Allies[u.Owner]
	}
	s.RecordDeathStatistics(combat.DeathCreditInput{
		Cause:              c,
		VictimOwner:        u.Owner,
		AttackerSide:       u.LastDamageSide,
		AttackerPresent:    attackerPresent,
		VictimCommander:    s.isCommanderForOwner(u),
		RemainingFraction:  u.Remaining,
		Cause3LossEligible: cause3LossEligible,
	})
	_ = cause // retained for the callback's lifecycle signature
}
