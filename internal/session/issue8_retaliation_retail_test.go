package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The authored Gaat Gun has sight 350 and weapon range 400. Retail damage
// retaliation can target its attacker outside sight [06 R-WPN-04 §2]
// [08 R-AI-01 §11]. In Modern, maintenance must not erase that reaction before
// the tower finishes answering the hit; Strict retains its ordinary attack.
func TestGaatGunRetaliatesOutsideSight(t *testing.T) {
	f := loadRetailFixture(t)
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(mode)
			stepRetail(s, 2)
			tower := placeCompleteRetailUnit(t, s, "CORHLT", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			attacker := placeCompleteRetailUnit(t, s, "ARMFLASH", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(980))
			weapon := tower.SlotAt(0).Weapon
			if weapon == nil || tower.Def.SightDistance >= 380 || weapon.Range < 380 {
				t.Fatalf("authored sight/range = %d/%v, need 380 between them", tower.Def.SightDistance, weapon)
			}
			if s.IsUnitVisible(int(tower.Owner), attacker) {
				t.Fatal("attacker is visible before damage")
			}
			result := s.Combat.AcceptDamage(s.Units, s.Clock.GlobalTick, combat.DamageInput{
				Victim: tower.Handle, Attacker: attacker.Handle, Nominal: 1, Kind: combat.KindOrdinary,
			})
			if !result.Accepted {
				t.Fatal("damage packet rejected")
			}
			hits, health := 0, attacker.Health
			for i := 0; i < 50; i++ {
				s.Step(s.Clock.ScaledAnchor + 1)
				if attacker.Health < health {
					hits++
				}
				health = attacker.Health
			}
			if s.IsUnitVisible(int(tower.Owner), attacker) {
				t.Fatal("attacker became visible during counterattack")
			}
			if slot := tower.SlotAt(0); slot.Target.Kind != units.TargetUnit || slot.Target.Unit != attacker.Handle {
				t.Fatalf("%s dropped unseen attacker after damage: target=%+v", mode, slot.Target)
			}
			if hits < 2 {
				t.Fatalf("%s answered with only %d hit(s), want continued fire", mode, hits)
			}
		})
	}
}
