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
