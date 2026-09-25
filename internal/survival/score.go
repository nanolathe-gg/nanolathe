package survival

// DamageValue prices the health removed from one attacker unit
// (DESIGN_SURVIVAL §8): the unit's cost times the fraction of its maximum
// health removed so far, never more than the whole. A caller keeps the
// running health removed per unit and credits the difference between the
// values before and after each hit, so the hits on one unit sum to exactly
// its cost when it dies from full health, whoever landed them, and damage
// past the last point of health earns nothing.
func DamageValue(cost int64, maxHealth, removed int32) int64 {
	if cost <= 0 || maxHealth <= 0 || removed <= 0 {
		return 0
	}
	return cost * int64(min(removed, maxHealth)) / int64(maxHealth)
}

// WaveScore is what one survived wave adds to the score (DESIGN_SURVIVAL §8).
type WaveScore struct {
	Budget int64 // the wave's budget, always scored
	Fast   int64 // the fast-clear bonus
	Clean  int64 // the clean-wave bonus
}

// Total is the wave's whole contribution.
func (w WaveScore) Total() int64 { return w.Budget + w.Fast + w.Clean }

// ScoreWave scores a survived wave. cleared says every unit it created is
// dead; elapsed is the ticks from its arrival to that moment and window the
// ticks from its arrival to the deadline on which the next wave comes anyway.
// Only a cleared wave earns the fast-clear bonus; clean says no finished
// structure fell to it.
func (t Tuning) ScoreWave(budget int64, cleared, clean bool, elapsed, window uint32) WaveScore {
	w := WaveScore{Budget: max(budget, 0)}
	if cleared && window > 0 && elapsed < window {
		w.Fast = w.Budget * t.FastClearBonus * int64(window-elapsed) / (100 * int64(window))
	}
	if clean {
		w.Clean = w.Budget * t.CleanWaveBonus / 100
	}
	return w
}
