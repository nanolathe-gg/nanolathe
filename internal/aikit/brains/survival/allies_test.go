package survival

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// allyState is a lone buddy east of a start site at (2048, 2048) on a
// 4096-unit map, owning every sector, with a board over obs.
func allyState(t *testing.T, o *aikit.Obs) (*state, *core.Board) {
	t.Helper()
	m := &aikit.MapInfo{WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32}
	st := &state{p: DefaultParams(), sc: Scenario{CentreX: 2048, CentreZ: 2048, Me: 1, Team: []uint8{0, 1}, Computer: []bool{false, true},
		Starts: [][2]int32{{2048, 2048}, {2048 + 320, 2048}}}}
	b := &core.Board{K: &aikit.Kit{Map: m, Table: &aikit.Table{}}, O: o, Threat: aikit.NewGrid(m), OwnPower: aikit.NewGrid(m), Commander: -1}
	st.setup(b)
	return st, b
}

// An ally's towers on a bearing cover that lane: they cancel the sector's
// deficit up to what it is owed and no more, so this survivor's towers go
// to the other lanes, and they never make another sector owed less or this
// one owed less than nothing.
func TestAlliedTowersCoverTheirLane(t *testing.T) {
	st := &state{p: DefaultParams()}
	for s := range st.mine {
		st.mine[s], st.siteable[s] = true, true
		st.radius[s] = humanRoom
	}
	st.income = 100000
	st.tick = towerStart
	st.observeWeights()
	st.towerWant()
	before, owedBefore := st.deficits()
	st.ally.have[4] = before[4] + 5000
	st.ally.have[9] = before[9] / 2
	after, owed := st.deficits()
	for s := range after {
		want := before[s]
		switch s {
		case 4:
			want = 0
		case 9:
			want = before[9] - before[9]/2
		}
		if after[s] != want {
			t.Fatalf("sector %d owed %d with allied cover, want %d (was %d)", s, after[s], want, before[s])
		}
	}
	if owed != owedBefore-before[4]-before[9]/2 {
		t.Fatalf("total owed %d, want %d", owed, owedBefore-before[4]-before[9]/2)
	}
}

// A tower or wall site steps round an allied building and its factory's
// exit lane, or past it, rather than landing on it; with nothing allied on
// the bearing the site is where it always was.
func TestTowerSitesKeepClearOfAlliedBuildings(t *testing.T) {
	factory := &aikit.UnitInfo{Key: "lab", Role: aikit.RoleFactory, FootX: 6, FootZ: 6, Value: 600}
	o := &aikit.Obs{}
	st, b := allyState(t, o)
	x, z, ok := st.siteOnBearing(b, 0, 800, 0)
	if !ok || x != 2048+800 || z != 2048 {
		t.Fatalf("clear bearing: site (%d,%d) %v, want (%d,2048)", x, z, ok, 2048+800)
	}
	// An allied factory on the bearing, its exit lane (toward +Z) across it.
	o.Allies = []aikit.AllyUnit{{H: 9, Gen: 1, Info: factory, Owner: 0, X: 2048 + 790, Z: 2048 - 60, HP: 600, MaxHP: 600, Built: true}}
	st.observeSectors(b)
	if len(st.ally.boxes) != 2 || st.clearOfAllies(2048+800, 2048) || st.outOfLanes(2048+790, 2048-60+150) || !st.outOfLanes(2048+790, 2048-60-60) {
		t.Fatalf("keep-out %+v does not cover the factory and its lane", st.ally.boxes)
	}
	x, z, ok = st.siteOnBearing(b, 0, 800, 0)
	if !ok || !st.clearOfAllies(x, z) || x < 2048+800 {
		t.Fatalf("site (%d,%d) %v: want one clear of the allied factory, not behind it", x, z, ok)
	}
	// The factory also pushes the sector's perimeter out beyond itself.
	if st.radius[0] < 790+perimeterAhead {
		t.Fatalf("perimeter %d does not reach past the allied factory", st.radius[0])
	}
	// Hemmed in on every side, a site beside an allied building stands (the
	// placement search keeps footprints apart); one in a factory's lane is
	// pulled in toward the team instead.
	st.ally.boxes = append(st.ally.boxes[:0], keepOut{x0: 0, z0: 0, x1: 4096, z1: 4096})
	if x, z, ok = st.siteOnBearing(b, 0, 800, 0); !ok || x != 2048+800 || z != 2048 {
		t.Fatalf("hemmed in: site (%d,%d) %v, want the bearing's own point", x, z, ok)
	}
	st.ally.boxes = append(st.ally.boxes, keepOut{x0: 2048 + 700, z0: 1900, x1: 2048 + 900, z1: 2200, lane: true})
	if x, _, ok = st.siteOnBearing(b, 0, 800, 0); !ok || x >= 2048+700 {
		t.Fatalf("in a lane: site x %d %v, want it pulled in short of the lane", x, ok)
	}
}

// Damaged allied buildings within the team's ground are repaired as the
// survivor's own are, the most valuable first; an ally's mobile units and
// buildings far out are not, and a building already being repaired is not
// picked twice.
func TestRepairsReachAlliedBuildings(t *testing.T) {
	store := &aikit.UnitInfo{Key: "store", Role: aikit.RoleStorage, Value: 100}
	lab := &aikit.UnitInfo{Key: "lab", Role: aikit.RoleFactory, Value: 300}
	tank := &aikit.UnitInfo{Key: "tank", Role: aikit.RoleMobile | aikit.RoleCombat, Value: 900}
	o := &aikit.Obs{
		Own: []aikit.OwnUnit{{H: 3, Gen: 1, Info: store, X: 2600, Z: 2048, HP: 50, MaxHP: 100, Built: true}},
		Allies: []aikit.AllyUnit{
			{H: 5, Gen: 1, Info: tank, Owner: 0, X: 2100, Z: 2048, HP: 10, MaxHP: 100, Built: true},
			{H: 6, Gen: 2, Info: lab, Owner: 0, X: 2048 + 1400, Z: 2048, HP: 10, MaxHP: 100, Built: true},
			{H: 7, Gen: 3, Info: lab, Owner: 0, X: 1900, Z: 2048, HP: 40, MaxHP: 100, Built: true},
		},
	}
	st, b := allyState(t, o)
	got, ok := st.repairTarget(b)
	if !ok || !got.ally || got.who != (handleGen{7, 3}) {
		t.Fatalf("picked %+v %v, want the allied factory in the team's ground", got, ok)
	}
	st.jobs.list = append(st.jobs.list, job{kind: jobRepair, target: got.who, ally: true})
	if got, ok = st.repairTarget(b); !ok || got.ally || got.who != (handleGen{3, 1}) {
		t.Fatalf("picked %+v %v, want the own store once the factory is taken", got, ok)
	}
}

// With attackers near, a commander in danger keeps clear of another
// survivor's commander — its death explosion would take that one with it —
// even when it is strong enough to stand; its refuge is blastKeep from
// every allied commander and blastClear from the start site. One in no
// danger stays: it is often what saves the human's commander from a raider.
func TestCommanderKeepsClearOfAlliedCommanders(t *testing.T) {
	com := &aikit.UnitInfo{Key: "com", Role: aikit.RoleMobile | aikit.RoleCommander | aikit.RoleBuilder, DPS: 400, HP: 3000, Value: 3000}
	scout := &aikit.UnitInfo{Key: "flea", Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 10, HP: 50, Value: 20}
	o := &aikit.Obs{}
	st, b := allyState(t, o)
	home := [2]int32{st.hx, st.hz}
	o.Own = []aikit.OwnUnit{{H: 2, Gen: 1, Info: com, X: home[0], Z: home[1], HP: 3000, MaxHP: 3000, Built: true}}
	o.Enemy = []aikit.Contact{{H: 40, Gen: 1, Info: scout, Owner: 2, X: home[0] + 300, Z: home[1], HPPct: 100, Visible: true, Built: true}}
	b.Commander = 0
	ally := [2]int32{home[0] - 200, home[1] + 100}
	o.Allies = []aikit.AllyUnit{{H: 1, Gen: 1, Info: com, Owner: 0, X: ally[0], Z: ally[1], HP: 3000, MaxHP: 3000, Built: true}}
	st.observeSectors(b)
	st.refuge(b)
	if len(st.jobs.list) != 0 {
		t.Fatalf("a commander in no danger fled a scout: %+v", st.jobs.list)
	}
	o.Own[0].HP = 2200 // wounded, though not badly enough to flee on its own
	st.refuge(b)
	if len(st.jobs.list) != 1 || st.jobs.list[0].kind != jobRefuge {
		t.Fatalf("jobs %+v: want the wounded commander moved clear of the allied one", st.jobs.list)
	}
	j := st.jobs.list[0]
	if aikit.Dist2(j.x, j.z, ally[0], ally[1]) < blastKeep*blastKeep || aikit.Dist2(j.x, j.z, st.cx, st.cz) < blastClear*blastClear {
		t.Fatalf("refuge (%d,%d) is within a commander's explosion of the allied commander or the site", j.x, j.z)
	}
	st.jobs.list = st.jobs.list[:0]
	o.Allies = nil
	st.observeSectors(b)
	st.refuge(b)
	if len(st.jobs.list) != 0 {
		t.Fatalf("with no allied commander near, the wounded commander fled a scout: %+v", st.jobs.list)
	}
}

// A repair goes to the nearest free constructor that can reach the
// building — a ship only for one afloat — and a building a repair could
// not start on is left alone for a while, so the same order is not given
// every think.
func TestRepairsGoToABuilderThatCanReach(t *testing.T) {
	ship := &aikit.UnitInfo{Key: "ship", Role: aikit.RoleMobile | aikit.RoleBuilder | aikit.RoleNaval}
	cons := &aikit.UnitInfo{Key: "cons", Role: aikit.RoleMobile | aikit.RoleBuilder}
	o := &aikit.Obs{Own: []aikit.OwnUnit{
		{H: 2, Gen: 1, Info: ship, X: 2000, Z: 2048, Built: true},
		{H: 3, Gen: 1, Info: cons, X: 2900, Z: 2048, Built: true},
	}}
	st, b := allyState(t, o)
	b.Builders = []int32{0, 1}
	land := repairPick{who: handleGen{9, 1}, x: 2048, z: 2048}
	if got := st.freeRepairerNear(b, &land); got != 1 {
		t.Fatalf("building ashore: repairer %d, want the land constructor", got)
	}
	afloat := land
	afloat.water = true
	if got := st.freeRepairerNear(b, &afloat); got != 0 {
		t.Fatalf("building afloat: repairer %d, want the ship", got)
	}
	st.blockRepair(land.who, 100)
	if !st.repairBlocked(land.who, 100+repairBlockTicks-1) || st.repairBlocked(land.who, 100+repairBlockTicks) {
		t.Fatal("a failed repair target is not held back for repairBlockTicks")
	}
}
