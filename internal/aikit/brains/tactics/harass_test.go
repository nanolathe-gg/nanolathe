package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// harassFixture is a held main squad of n light tanks (worth 120 each, all
// present) at (500, 500) and one undefended zone worth 300 at (1200, 400),
// of economy or not.
func harassFixture(t *testing.T, n int, eco bool) (*Army, *core.Board, *squad) {
	t.Helper()
	a, b, s := postureFixture(t, 0, 2000)
	light := &aikit.UnitInfo{DPS: 30, HP: 600, Range: 250, Value: 120, Speed: 90}
	for i := 0; i < n; i++ {
		s.total.add(light, 600, 1000)
	}
	s.present = s.total
	a.candidates = append(a.candidates[:0], 1)
	if !eco {
		a.zones[1].eco = 0
	}
	return a, b, s
}

// Held, two light tanks raid an undefended economy zone; the old launch
// rule wanted three units worth 300, and a third tank launches either way.
func TestHarassSmallRaid(t *testing.T) {
	a, b, s := harassFixture(t, 2, true)
	if !a.chooseTarget(b, s) || s.target != 1 || !s.held || !a.canLaunch(b, s) {
		t.Fatalf("two held tanks: target %d held %v launch %v, want a raid on zone 1", s.target, s.held, a.canLaunch(b, s))
	}
	a.P.Harass = false
	if !a.chooseTarget(b, s) || a.canLaunch(b, s) {
		t.Errorf("harass off: two tanks worth 240 launched")
	}
	a, b, s = harassFixture(t, 1, true)
	if a.chooseTarget(b, s) && a.canLaunch(b, s) {
		t.Errorf("one tank launched a raid")
	}
	a, b, s = harassFixture(t, 3, true)
	a.P.Harass = false
	if !a.chooseTarget(b, s) || !a.canLaunch(b, s) {
		t.Errorf("harass off: three tanks worth %d did not launch", s.total.value)
	}
}

// A held squad raids economy: a zone without any (a lone remembered
// mobile) is no raid target, as for the raid squad. Unheld, and with
// harass off, the zone's whole value counts as before.
func TestHarassEconomyOnly(t *testing.T) {
	a, b, s := harassFixture(t, 2, false)
	if a.chooseTarget(b, s) {
		t.Errorf("held squad chose zone %d without economy", s.target)
	}
	a.P.Harass = false
	if !a.chooseTarget(b, s) || s.target != 1 {
		t.Errorf("harass off: held squad did not take the undefended zone")
	}
	a, b, s = harassFixture(t, 2, false)
	b.Posture.AttackValue = 100
	if !a.chooseTarget(b, s) || s.held || s.target != 1 {
		t.Errorf("unheld squad did not take the undefended zone (held %v)", s.held)
	}
}

// Out on a raid the hold allowed, the main squad pulls out below the engage
// margin and takes field fights only at it, like the raid squad; once it
// regroups, and on an offensive, the retreat margin applies again.
func TestHarassRaidTurnsBack(t *testing.T) {
	a, b, s := harassFixture(t, 2, true)
	if !a.chooseTarget(b, s) {
		t.Fatal("no raid target")
	}
	a.noteLaunch(b, s)
	if !s.soft || a.pullMargin(b, s) != a.engageMargin(b) {
		t.Errorf("soft raid: soft %v pull margin %d, want the engage margin %d", s.soft, a.pullMargin(b, s), a.engageMargin(b))
	}
	a.setState(b, s, stApproach)
	if !s.soft {
		t.Error("the approach ended the raid")
	}
	a.setState(b, s, stGather)
	if s.soft || a.pullMargin(b, s) != a.retreatMargin(b) {
		t.Errorf("regrouped: soft %v pull margin %d, want the retreat margin", s.soft, a.pullMargin(b, s))
	}
	b.Posture.AttackValue = 100 // an offensive
	a.chooseTarget(b, s)
	a.noteLaunch(b, s)
	if s.soft || !s.offensive {
		t.Errorf("offensive: soft %v offensive %v", s.soft, s.offensive)
	}
	a, b, s = harassFixture(t, 2, true)
	a.P.Harass = false
	a.chooseTarget(b, s)
	a.noteLaunch(b, s)
	if s.soft {
		t.Error("harass off: a soft raid turns back like a raider")
	}
}

// Held with nothing known, a squad of the harass size probes the nearest
// unseen start position; without the probe (or unheld and small) it waits.
func TestHarassProbe(t *testing.T) {
	setup := func() (*Army, *core.Board, *squad) {
		a, b, s := harassFixture(t, 2, true)
		a.candidates = a.candidates[:0]
		m := b.K.Map
		m.Starts = [][2]int32{{400, 400}, {4000, 4000}, {1500, 4500}}
		a.startSeen = make([]uint32, len(m.Starts))
		b.HomeX, b.HomeZ = 400, 400
		return a, b, s
	}
	a, b, s := setup()
	if !a.chooseTarget(b, s) || s.target != tgtExplore || s.tx != 1500 || s.tz != 4500 || !a.canLaunch(b, s) {
		t.Errorf("held squad: target %d at (%d,%d), want to probe the start at (1500,4500)", s.target, s.tx, s.tz)
	}
	a, b, s = setup()
	a.P.Probe = false
	if a.chooseTarget(b, s) {
		t.Errorf("probe off: held squad explored (%d,%d)", s.tx, s.tz)
	}
	a, b, s = setup()
	b.Posture.AttackValue = 100
	if a.chooseTarget(b, s) {
		t.Errorf("unheld squad worth %d explored (it needs %d)", s.total.value, exploreValue)
	}
}

// With the tour, an idle scout looks next at the unseen start nearest to
// itself; without it, at the one nearest to home.
func TestScoutTour(t *testing.T) {
	for _, tour := range []bool{true, false} {
		a, m := testArmy(40, 40)
		a.P.Tour = tour
		a.avail = 1 << 30
		m.Starts = [][2]int32{{400, 400}, {1400, 400}, {400, 3800}, {4200, 4200}}
		a.startSeen = make([]uint32, len(m.Starts))
		jeep := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleScout | aikit.RoleCombat, DPS: 5, HP: 200, Range: 150, Value: 46, Speed: 120}
		o := &aikit.Obs{Tick: 3000, Own: []aikit.OwnUnit{{H: pool.Handle(4), Gen: 1, Info: jeep, X: 3900, Z: 3900, Built: true, HP: 200, Tag: tagScout}}}
		b := &core.Board{K: &aikit.Kit{Persona: aikit.PersonaHard, Map: m}, O: o, Tick: 3000, HomeX: 400, HomeZ: 400, Combat: []int32{0}}
		a.scouts(b)
		um := a.units[4]
		want := [2]int32{1400, 400}
		if tour {
			want = [2]int32{4200, 4200}
		}
		if um.ordKind != okMove || um.ordX != want[0] || um.ordZ != want[1] {
			t.Errorf("tour %v: scout sent to (%d,%d), want (%d,%d)", tour, um.ordX, um.ordZ, want[0], want[1])
		}
	}
}

// When the start assignment is public, the army looks only at the starts
// an opponent took: the held squad probes the enemy's start past a nearer
// empty one, and a scout skips the empty one too.
func TestKnownEnemyStart(t *testing.T) {
	for _, known := range []bool{true, false} {
		a, b, s := harassFixture(t, 2, true)
		a.candidates = a.candidates[:0]
		m := b.K.Map
		m.Starts = [][2]int32{{400, 400}, {1500, 1500}, {4000, 4000}}
		m.StartEnemy = nil
		if known {
			m.StartEnemy = []bool{false, false, true}
		}
		a.startSeen = make([]uint32, len(m.Starts))
		b.HomeX, b.HomeZ = 400, 400
		want := [2]int32{1500, 1500}
		if known {
			want = [2]int32{4000, 4000}
		}
		if !a.chooseTarget(b, s) || s.target != tgtExplore || s.tx != want[0] || s.tz != want[1] {
			t.Errorf("known %v: squad target %d at (%d,%d), want to probe (%d,%d)", known, s.target, s.tx, s.tz, want[0], want[1])
		}
		if x, z, ok := a.exploreTarget(b, 400, 400, nil, nil); !ok || x != want[0] || z != want[1] {
			t.Errorf("known %v: scout start (%d,%d), want (%d,%d)", known, x, z, want[0], want[1])
		}
	}
}
