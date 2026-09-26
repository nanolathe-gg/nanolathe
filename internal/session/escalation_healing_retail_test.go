//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The authored HealTime=1 contributes one work unit on each even tick, with
// the shipped DLL's one-times fractional health bank. Exercise ordinary damage
// and script/economy dispatch [research/extensions/escalation-shields.md
// "Passive generator healing"].
func TestEscalationShieldHealing(t *testing.T) {
	for _, key := range []string{"ARMSHGEN", "CORSHGEN"} {
		t.Run(key, func(t *testing.T) {
			s := escalationSystemSession(t)
			u := placeCompleteRetailUnit(t, s, key, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			advanceEscalationShieldTicks(t, s, 600)
			if !u.Armored || u.Def.HealTime != 1 {
				t.Fatalf("generator did not finish startup: armored=%v HealTime=%d", u.Armored, u.Def.HealTime)
			}
			assertEscalationDamage(t, s, u, 100, 25)
			before := u.Health
			p := &s.Econ.Players[0]
			p.Capacity[economy.Energy] = 1e6
			for tick := 0; tick < 240; tick++ {
				p.Stock[economy.Energy] = 1e6
				advanceEscalationShieldTicks(t, s, 1)
			}
			want := int32(120 * int64(u.Def.MaxDamage) / int64(u.Def.BuildTime))
			if want <= 0 || want >= 25 || u.Health != before+want {
				t.Fatalf("funded healing: health %d -> %d, want +%d from H=%d B=%d", before, u.Health, want, u.Def.MaxDamage, u.Def.BuildTime)
			}
			// The starting commander supplies passive income. A second hit on an
			// empty stock triggers enough ordinary shield upkeep to leave debt
			// and close healing admission even with that income.
			p.Stock[economy.Energy] = 0
			assertEscalationDamage(t, s, u, 100, 25)
			advanceEscalationShieldTicks(t, s, 60)
			if s.Econ.UnitBuckets(u.Handle)[economy.Energy].Carry <= 0 {
				t.Fatal("shield hit did not establish an unpaid energy balance")
			}
			starved := u.Health
			advanceEscalationShieldTicks(t, s, 120)
			if u.Health != starved {
				t.Fatalf("healed without energy: %d -> %d", starved, u.Health)
			}
			for tick := 0; tick < 240; tick++ {
				p.Stock[economy.Energy] = 1e6
				advanceEscalationShieldTicks(t, s, 1)
			}
			if u.Health <= starved {
				t.Fatal("healing did not resume after energy returned")
			}
			if diagnostics := u.GetScript().Diagnostics(); len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			t.Logf("H=%d B=%d E=%.0f: 120 funded visits restored %d health", u.Def.MaxDamage, u.Def.BuildTime, u.Def.BuildCostEnergy, want)
		})
	}
}
