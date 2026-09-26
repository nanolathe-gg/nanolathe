package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// postureFixture is a main squad at (500, 500) and two target zones: a rich
// one whose defenses give a ratio between the hard engage margin and the
// soft margin, and a poor undefended one of economy (a held squad raids
// economy, harass.go).
func postureFixture(t *testing.T, squadUnits int, av int32) (*Army, *core.Board, *squad) {
	t.Helper()
	a, m := testArmy(40, 40)
	a.P.Naval = false // no reach bookkeeping in this fixture
	k := &aikit.Kit{Persona: aikit.PersonaHard, Map: m}
	b := &core.Board{K: k, O: &aikit.Obs{Tick: 1000}, Tick: 1000,
		Posture: core.Posture{AttackValue: av, Aggression: 50}}
	s := &a.sq[sqMain]
	s.id = sqMain
	s.target = tgtNone
	tank := &aikit.UnitInfo{DPS: 50, HP: 1000, Range: 300, Value: 200, Speed: 60}
	for i := 0; i < squadUnits; i++ {
		s.total.add(tank, 1000, 1000)
	}
	s.cx, s.cz = 500, 500
	// Rich zone: one tower; five tanks predict 2083‰ against it (between
	// the hard engage margin, 1260‰, and the soft margin), two tanks 333‰.
	rich := &a.zones[0]
	rich.value, rich.eco, rich.x, rich.z = 5000, 5000, 2000, 2000
	tower := &aikit.UnitInfo{DPS: 200, HP: 3000, Range: 300, Value: 300}
	rich.stat.add(tower, 3000, 1000)
	poor := &a.zones[1]
	poor.value, poor.eco, poor.x, poor.z = 300, 300, 1200, 400
	a.candidates = append(a.candidates[:0], 0, 1)
	return a, b, s
}

// Below the attack value the main squad takes only the clearly undefended
// zone; at the attack value, on an offensive already under way, without an
// attack value or with the posture off it takes the rich defended one.
func TestPostureHoldsOffensive(t *testing.T) {
	a, b, s := postureFixture(t, 5, 2000)
	r := ratio(&s.total, &a.zones[0].stat)
	if r < a.engageMargin(b) || r >= softMargin {
		t.Fatalf("fixture ratio %d‰ not between the engage margin %d and the soft margin %d", r, a.engageMargin(b), softMargin)
	}
	if !a.chooseTarget(b, s) || s.target != 1 || !s.held {
		t.Fatalf("held squad chose zone %d (held %v), want the undefended zone 1", s.target, s.held)
	}
	a.noteLaunch(b, s)
	if s.offensive || a.off.soft != 1 || a.off.n != 0 {
		t.Errorf("a soft raid began an offensive (offensive %v, soft %d, offensives %d)", s.offensive, a.off.soft, a.off.n)
	}

	// Measured on the main squad alone, other squads do not lift the hold.
	a.sq[sqRaid].total.value = 1000
	a.P.PostureMain = true
	if a.chooseTarget(b, s); !s.held {
		t.Errorf("main-squad measure: not held with the main squad below the attack value")
	}
	a.sq[sqRaid].total.value = 0
	a.P.PostureMain = false

	// Only the defended zone left: nothing is chosen and the hold is counted.
	a.candidates = a.candidates[:1]
	s.target = tgtNone
	if a.chooseTarget(b, s) || a.off.held != 1 {
		t.Errorf("held squad chose zone %d (held thinks %d), want none and one", s.target, a.off.held)
	}

	for _, c := range []struct {
		name string
		set  func(a *Army, b *core.Board, s *squad)
	}{
		{"attack value reached", func(a *Army, b *core.Board, s *squad) { b.Posture.AttackValue = 1000 }},
		{"offensive under way", func(a *Army, b *core.Board, s *squad) { s.offensive = true }},
		{"no attack value", func(a *Army, b *core.Board, s *squad) { b.Posture.AttackValue = 0 }},
		{"posture off", func(a *Army, b *core.Board, s *squad) { a.P.Posture = false }},
		{"other squads count", func(a *Army, b *core.Board, s *squad) {
			a.sq[sqRaid].total.value = 1000
		}},
	} {
		a, b, s := postureFixture(t, 5, 2000)
		c.set(a, b, s)
		if !a.chooseTarget(b, s) || s.target != 0 || s.held {
			t.Errorf("%s: chose zone %d (held %v), want the rich zone 0", c.name, s.target, s.held)
		}
	}
}

// The attack value never overrides the prediction: a zone below the engage
// margin is not attacked by an offensive either.
func TestPostureKeepsPrediction(t *testing.T) {
	a, b, s := postureFixture(t, 2, 100)
	a.candidates = a.candidates[:1]
	if r := ratio(&s.total, &a.zones[0].stat); r >= a.engageMargin(b) {
		t.Fatalf("fixture ratio %d‰ not below the engage margin %d", r, a.engageMargin(b))
	}
	if a.chooseTarget(b, s) {
		t.Errorf("attacked zone %d below the engage margin", s.target)
	}
}

// An offensive begins once per launch series, records the squad's value
// and ends when the squad regroups, retreats or defends.
func TestOffensiveLifetime(t *testing.T) {
	a, b, s := postureFixture(t, 12, 2000)
	if !a.chooseTarget(b, s) || s.held {
		t.Fatalf("squad at the attack value was held")
	}
	a.noteLaunch(b, s)
	a.noteLaunch(b, s) // a chain within the same offensive
	if !s.offensive || a.off.n != 1 || a.off.firstValue != 2400 || a.off.firstAV != 2000 || a.off.first != 1000 {
		t.Errorf("offensive %v n %d first value %d av %d tick %d", s.offensive, a.off.n, a.off.firstValue, a.off.firstAV, a.off.first)
	}
	for _, st := range []squadState{stGather, stRetreat, stDefend} {
		s.offensive = true
		s.state = stEngage
		a.setState(b, s, st)
		if s.offensive {
			t.Errorf("offensive survived %s", stateNames[st])
		}
	}
	s.offensive = true
	a.setState(b, s, stApproach)
	if !s.offensive {
		t.Error("offensive ended on the approach")
	}
}

// Aggression shifts the margins by the slope per point away from 50, capped,
// the retreat margin by half; with the posture off nothing moves.
func TestAggressionMargins(t *testing.T) {
	a, b, _ := postureFixture(t, 1, 0)
	a.P.AggrSlope = 4
	baseE, baseR := int64(1600-4*85), int64(450+4*85)
	for _, c := range []struct{ aggr, dE int64 }{{50, 0}, {80, -120}, {100, -150}, {20, 120}, {0, 150}} {
		b.Posture.Aggression = int32(c.aggr)
		if e, r := a.engageMargin(b), a.retreatMargin(b); e != baseE+c.dE || r != baseR+c.dE/2 {
			t.Errorf("aggression %d: margins %d/%d, want %d/%d", c.aggr, e, r, baseE+c.dE, baseR+c.dE/2)
		}
	}
	a.P.Posture = false
	b.Posture.Aggression = 100
	if e, r := a.engageMargin(b), a.retreatMargin(b); e != baseE || r != baseR {
		t.Errorf("posture off: margins %d/%d, want %d/%d", e, r, baseE, baseR)
	}
}

// fixedTemper is a TemperSource for tests.
type fixedTemper Temper

func (t fixedTemper) ArmyTemper() Temper { return Temper(t) }

// A temper lowers the engage margin and the retreat margin by half as much,
// and scales the raid squad's split value and the harass raid's least value,
// once: Init applies it and clears it. The neutral temper moves nothing.
func TestTemperShiftsMarginsAndRaids(t *testing.T) {
	a, b, _ := postureFixture(t, 1, 0)
	baseE, baseR := a.engageMargin(b), a.retreatMargin(b)
	a.P.Temper = fixedTemper{Engage: 80, RaidPct: 60, HarassPct: 75}
	a.Init(&core.Board{})
	if e, r := a.engageMargin(b), a.retreatMargin(b); e != baseE-80 || r != baseR-40 {
		t.Errorf("margins %d/%d, want %d/%d", e, r, baseE-80, baseR-40)
	}
	if a.P.RaidOn != raidOnValue*60/100 || a.P.HarassValue != harassValue*75/100 || a.P.Temper != nil {
		t.Errorf("raid split %d, harass value %d, temper left %v", a.P.RaidOn, a.P.HarassValue, a.P.Temper)
	}
	a.Init(&core.Board{})
	if a.engageMargin(b) != baseE-80 || a.P.RaidOn != raidOnValue*60/100 {
		t.Error("a second Init applied the temper again")
	}

	n, _, _ := postureFixture(t, 1, 0)
	want := n.P
	n.P.Temper = fixedTemper{Engage: 0, RaidPct: 100, HarassPct: 100}
	n.Init(&core.Board{})
	if n.P != want {
		t.Errorf("the neutral temper moved the army: %+v", n.P)
	}
}

// Raid-squad launches are counted for the report, chains included; a main
// squad's launch is not a raid.
func TestRaidLaunchesAreCounted(t *testing.T) {
	a, b, s := postureFixture(t, 5, 2000)
	r := &a.sq[sqRaid]
	r.id = sqRaid
	a.noteLaunch(b, r)
	a.noteLaunch(b, r)
	a.noteLaunch(b, s)
	if a.off.raids != 2 {
		t.Errorf("raid launches %d, want 2", a.off.raids)
	}
}
