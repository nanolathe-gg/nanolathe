package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// attackAt is the attack value (before the enemy estimate and the wave
// jitter) of a style and persona at a game minute.
func attackAt(style string, persona aikit.Persona, v Variety, minute int64) int64 {
	vr := variety{v: v, style: styleIndex(style)}
	s := &shared{p: DefaultParams(), k: &aikit.Kit{Persona: persona}}
	s.tick = uint32(minute * 1800)
	s.minutes = int32(minute)
	return vr.attackValue(s, &s.p)
}

// Every persona's own army can reach its attack value: at every minute it
// is at most the army the ambition caps allow (the same tier curve) and at
// least the smallest launch.
func TestAttackValueReachable(t *testing.T) {
	for _, p := range []aikit.Persona{aikit.PersonaEasy, aikit.PersonaMed, aikit.PersonaHard} {
		for m := int64(0); m <= 20; m++ {
			av := attackAt("balanced", p, Variety{}, m)
			allowed := max64(topArmy.at(p.Ambition, uint32(m*1800))/1000, floorArmy/1000)
			if av < waveFloor || (av > waveFloor && av > allowed) {
				t.Errorf("%s minute %d: attack value %d, army allowed %d, floor %d", p.Name, m, av, allowed, waveFloor)
			}
		}
	}
}

// Lower ambition attacks with smaller waves: at minute 12 easy < medium <
// hard, and hard's value is waveShare % of the top tier's army curve.
func TestAttackValueAmbition(t *testing.T) {
	e := attackAt("balanced", aikit.PersonaEasy, Variety{}, 12)
	m := attackAt("balanced", aikit.PersonaMed, Variety{}, 12)
	h := attackAt("balanced", aikit.PersonaHard, Variety{}, 12)
	if !(e < m && m < h) {
		t.Errorf("attack values at minute 12 easy %d medium %d hard %d, want rising", e, m, h)
	}
	if want := topArmy.at(100, 12*1800) / 1000 * waveShare / 100; h < want-1 || h > want+1 {
		t.Errorf("hard at minute 12 = %d, want %d", h, want)
	}
}

// The style's lag moves the curve by its archetype's first-unit minute:
// units (O3) attacks at the smallest value, greedy (O6) at the largest,
// eco (O2) and expand (O1) above balanced; att_lag=0 removes it.
func TestAttackValueStyleLag(t *testing.T) {
	at := func(style string, v Variety) int64 { return attackAt(style, aikit.PersonaHard, v, 9) }
	units, bal, eco, expand, greedy := at("units", Variety{}), at("balanced", Variety{}), at("eco", Variety{}), at("expand", Variety{}), at("greedy", Variety{})
	if !(units < bal && bal < eco && eco < expand && expand < greedy) {
		t.Errorf("minute 9: units %d balanced %d eco %d expand %d greedy %d, want rising", units, bal, eco, expand, greedy)
	}
	if nl := at("units", Variety{AttNoLag: true}); nl != bal {
		t.Errorf("units without lag %d, want balanced's %d", nl, bal)
	}
}

// att_curve=0 restores att_min + att_grow per minute; att_share and
// att_floor override the curve's share and floor.
func TestAttackValueSwitches(t *testing.T) {
	d := DefaultParams()
	if got, want := attackAt("balanced", aikit.PersonaHard, Variety{AttLinear: true}, 7), int64(d.AttackMin)+7*int64(d.AttackGrow); got != want {
		t.Errorf("linear attack value %d, want %d", got, want)
	}
	if got := attackAt("balanced", aikit.PersonaHard, Variety{AttFloor: 900}, 4); got != 900 {
		t.Errorf("floor override: %d, want 900", got)
	}
	half := attackAt("balanced", aikit.PersonaHard, Variety{AttShare: 50}, 15)
	full := attackAt("balanced", aikit.PersonaHard, Variety{AttShare: 100}, 15)
	if d := full - 2*half; d < -2 || d > 2 || full != topArmy.at(100, 15*1800)/1000 {
		t.Errorf("share override: %d at half the curve, %d at all of it", half, full)
	}
	v, err := VarietyFrom(map[string]string{"att_curve": "0", "att_share": "55", "att_floor": "400", "att_lag": "0"})
	if err != nil || !v.AttLinear || v.AttShare != 55 || v.AttFloor != 400 || !v.AttNoLag {
		t.Errorf("VarietyFrom attack switches: %+v, %v", v, err)
	}
	if _, err := VarietyFrom(map[string]string{"att_share": "0"}); err == nil {
		t.Error("att_share=0 accepted")
	}
}
