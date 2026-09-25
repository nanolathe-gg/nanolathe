package survival

import "testing"

// Hits on one unit, split between shooters in any sizes, sum to exactly its
// cost, and damage past its last health earns nothing (DESIGN_SURVIVAL §8).
func TestDamageValueSumsToCost(t *testing.T) {
	const cost, maxHealth = 997, 1234
	for _, hits := range [][]int32{{1234}, {1, 1, 1, 1231}, {617, 617}, {300, 5000}, {7, 11, 13, 17, 19, 23, 1200}} {
		var removed int32
		var sum int64
		for _, h := range hits {
			before := removed
			removed = min(removed+h, maxHealth)
			sum += DamageValue(cost, maxHealth, removed) - DamageValue(cost, maxHealth, before)
		}
		if sum != cost {
			t.Fatalf("hits %v: credited %d, want the unit's cost %d", hits, sum, cost)
		}
	}
}

// The fast-clear bonus is paid only for a cleared wave and falls from its full
// percentage at arrival to none at the deadline; the clean bonus is flat.
func TestScoreWaveBonuses(t *testing.T) {
	tune := DefaultTuning(PaceNormal)
	const budget, window = 1000, 3000
	if w := tune.ScoreWave(budget, true, false, 0, window); w.Fast != budget*tune.FastClearBonus/100 || w.Clean != 0 {
		t.Fatalf("instant clear: %+v", w)
	}
	if w := tune.ScoreWave(budget, true, true, window, window); w.Fast != 0 || w.Total() != budget+budget*tune.CleanWaveBonus/100 {
		t.Fatalf("clear at the deadline: %+v", w)
	}
	if w := tune.ScoreWave(budget, false, false, 0, window); w.Total() != budget {
		t.Fatalf("an outlasted wave earned a fast-clear bonus: %+v", w)
	}
	early, late := tune.ScoreWave(budget, true, false, 500, window), tune.ScoreWave(budget, true, false, 2500, window)
	if early.Fast <= late.Fast {
		t.Fatalf("an earlier clear did not score more: %d <= %d", early.Fast, late.Fast)
	}
}
