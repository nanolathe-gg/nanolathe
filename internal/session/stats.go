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

// deathCauseForResolution is the cause the finalizer hands the shared death
// path — the Killed query, the corpse chain and the death-explosion weapon
// selection [06 §12.1] C22–C25.
//
// Retail keeps ONE cause: the damage-kind byte recorded at damage time, which
// the death packet carries as its high nibble and which every branch of the
// handler reads [06 §12.1]. This build also carries a four-valued label on the
// unit, and the finalizer used to re-derive the cause from that label instead
// — collapsing every packet death to cause 1. That erased the distinctions the
// section draws BELOW the label's resolution: the cargo cascade's cause 6, the
// water-damage dispatch's cause 11, and the deconstruction refund's cause 9,
// which alone decides whether the query runs at all.
//
// So the recorded kind wins whenever there is one. The label is the fallback
// for a death that retained no packet — kind 0 — which in this build means the
// producers that latch a death without emitting one: the factory's cancelled
// product, the capture victim's old record, and the commander-death sweep.
// Retail stamps a kind byte on all three (causes 9, 4 and 3 respectively per
// [06 §12.1]'s producer list) and this build does not yet, so the fallback
// keeps those paths at the reading they already had rather than silently
// moving them; wiring their producers is what retires it.
func deathCauseForResolution(label units.DeathCause, u *units.Unit) combat.Cause {
	if u != nil {
		if c := combat.Cause(u.LastDamageCause); c != 0 {
			return c // the recorded damage-kind byte [06 §12.1]
		}
	}
	switch label {
	case units.DeathKilled:
		return combat.CauseOrdinary // 1 [06 §12.1]
	case units.DeathSelfDestruct:
		return combat.CauseSelfDestruct // 3 [06 §12.1]
	case units.DeathReclaimed:
		return combat.CauseReclaim // 5 [06 §12.1] C24 bypass
	default:
		if u != nil && u.Health > 0 {
			return combat.CauseReclaim
		}
		return combat.CauseOrdinary
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
	// The cause-6 producer exists now: the carrier-death cascade stamps both
	// provenance fields on every cargo unit it damages — the kind byte, and
	// the attacker side taken from the carrier's killer, or the neutral side
	// when the carrier died with no attacker behind it [06 §12.1][06 §9.1].
	// The full credit path below reads exactly that pair.
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
