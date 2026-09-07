package economy

// RecordUnitLoss files one finalized unit loss. The commander word is kept
// separately because the result/statistics collector reports both ordinary
// and commander losses [06 §12.1][08 R-CAMP-01 §7].
func (p *Player) RecordUnitLoss(commander bool) {
	if p == nil {
		return
	}
	p.Losses++
	if commander {
		p.CommanderLosses++
	}
}

// RecordUnitKill files one credited kill at the same finalization boundary as
// RecordUnitLoss. The unit's own veterancy word is maintained by the combat
// caller; this method owns only the player-level result counters.
func (p *Player) RecordUnitKill(commander bool) {
	if p == nil {
		return
	}
	p.Kills++
	if commander {
		p.CommanderKills++
	}
}

// RecordCommanderKill records the commander-specific counter when the
// ordinary unit-kill gates did not pass. The full commander branch still
// records both counters when those gates do pass [06 §12.1].
func (p *Player) RecordCommanderKill() {
	if p != nil {
		p.CommanderKills++
	}
}

// SeedScorePanelRank is the rank half of slot registration: the per-slot
// registration helper "writes the slot's score-panel rank byte to the slot
// index" beside the controller byte, and that is the byte's initial value —
// battle entry never touches it again, so before the first credited kill the
// ranks read 0..9 in slot order [08 R-SKIR-01 §2][07 R-HUD-04 §1].
//
// It is a method on the record rather than a bare assignment so that one
// citation covers every registration path — the skirmish row-to-player
// conversion, the campaign seat setup and the local two-player preload — and
// so the kill-lead shift of [08 R-CAMP-01 §9] has exactly one initial-value
// contract to reason against.
//
// A restored battle re-runs registration: the session shell is staged before
// the save image is applied, and the `Player%i` account is closed at nineteen
// scalars that name no rank [08 "Player records"]. The byte is therefore the
// slot index again on load, with the restored counters sitting around it.
//
// TODO(question): whether retail recomputes the ranks after a load — so that a
// resumed battle's panel reflects the restored kill counters immediately
// rather than slot order until the next credited kill — is not settled. The
// save writer and reader were traced end to end and name no rank item, and the
// shift has no producer outside the death path, so nothing in the traced
// material recomputes it; a trace of the load path's post-restore fixups would
// settle it. Only the panel's row ORDER depends on the answer.
func (p *Player) SeedScorePanelRank(slot int) {
	if p == nil || slot < 0 || slot > 9 {
		return
	}
	p.Rank = uint8(slot)
}
