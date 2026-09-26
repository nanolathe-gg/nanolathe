//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestEscalationShieldDisruption exercises the actual Prophet detector marker
// in the Aegis and extractor scripts. Their scores differ: one hostile removes
// one source's protection, but two completed sources outweigh one hostile.
// [research/extensions/escalation-shields.md "Absorption, overlap and disruption"]
func TestEscalationShieldDisruption(t *testing.T) {
	s := escalationSystemSession(t)
	place := func(key string, owner uint8, x, z int64) *units.Unit {
		return placeCompleteRetailUnit(t, s, key, owner, numeric.FixedFromInt(x), numeric.FixedFromInt(z))
	}
	provider := place("ARMSHGEN", 0, 600, 600)
	recipient := place("ARMMEX", 0, 700, 600)
	advanceEscalationShieldTicks(t, s, 600)
	check := func(label string, armored bool) {
		t.Helper()
		for _, u := range []*units.Unit{provider, recipient} {
			if !u.Alive || u.Armored != armored {
				t.Fatalf("%s: %s alive=%v armor=%v, want %v", label, u.Def.UnitName, u.Alive, u.Armored, armored)
			}
			if diags := u.GetScript().Diagnostics(); len(diags) != 0 {
				t.Fatalf("%s: %s diagnostics: %v", label, u.Def.UnitName, diags)
			}
		}
	}
	check("one source", true)
	disruptor := place("ARMCRAWL", 1, 1600, 600)
	advanceEscalationShieldTicks(t, s, 600)
	check("one hostile disruptor", false)
	assertEscalationDamage(t, s, recipient, 100, 100)
	second := place("ARMSHGEN", 0, 600, 800)
	advanceEscalationShieldTicks(t, s, 600)
	check("two sources outweigh one disruptor", true)
	assertEscalationDamage(t, s, recipient, 100, 25)
	s.Units.Destroy(second.Handle, units.DeathReclaimed)
	advanceEscalationShieldTicks(t, s, 600)
	check("remaining source is disrupted", false)
	s.Units.Destroy(disruptor.Handle, units.DeathReclaimed)
	advanceEscalationShieldTicks(t, s, 600)
	check("disruptor removed", true)
	assertEscalationDamage(t, s, recipient, 100, 25)
}
