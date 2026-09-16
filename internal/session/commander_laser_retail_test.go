package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The original commander laser must finish aiming under repeated damage
// [08 R-AI-01 §11][06 R-WPN-04 §1]. Disable the tertiary slot to isolate laser
// cancellation from the authored script's D-gun/laser mutual exclusion. Hold
// task deadlines and movement so construction or chasing cannot change range.
func TestRetailCommanderLaserUnderFire(t *testing.T) {
	f := loadRetailFixture(t)
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		for _, key := range []string{"ARMCOM", "CORCOM"} {
			for _, damage := range []bool{false, true} {
				name := "undamaged"
				if damage {
					name = "under-fire"
				}
				t.Run(string(mode)+"/"+key+"/"+name, func(t *testing.T) {
					s := f.session(t)
					s.SetGameplay(mode)
					stepRetail(s, 2)
					for i := range s.AI[1].Deadlines {
						s.AI[1].Deadlines[i] = ^uint32(0)
					}
					commander := placeCompleteRetailUnit(t, s, key, 1, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
					enemy := placeCompleteRetailUnit(t, s, "ARMLAB", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(750))
					commander.SlotAt(2).Flags &^= units.SlotFlagEnabled
					before := enemy.Health
					for i := 0; i < 600; i++ {
						commander.Flags &^= units.StandingFieldMask << units.StandingMoveShift
						// Enough for the laser, below the authored D-gun shot cost.
						s.Econ.Players[1].Stock[economy.Energy] = 100
						if damage && i%3 == 0 {
							s.Combat.AcceptDamage(s.Units, s.Clock.GlobalTick, combat.DamageInput{Victim: commander.Handle, Attacker: enemy.Handle, Nominal: 1, Kind: combat.KindOrdinary})
						}
						s.Step(s.Clock.ScaledAnchor + 1)
					}
					if enemy.Health >= before {
						t.Fatalf("commander never hit its enemy: health=%d/%d laser=%+v", enemy.Health, before, commander.SlotAt(0))
					}
				})
			}
		}
	}
}
