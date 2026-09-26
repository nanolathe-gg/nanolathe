package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The square law's survivor share: winning at strength ratio 2 costs
// 1 − √(1/2) ≈ 29% of the force, at 4 about 13%, and an even fight costs
// everything.
func TestLossPermille(t *testing.T) {
	for _, c := range []struct{ r, want int64 }{{1000, 1000}, {900, 1000}, {2000, 293}, {4000, 134}, {100000, 6}} {
		if got := lossPermille(c.r); got != c.want {
			t.Errorf("lossPermille(%d) = %d, want %d", c.r, got, c.want)
		}
	}
}

// Doubling a force quadruples its strength; a longer range tips an even
// fight; an unarmed opponent is capped.
func TestRatio(t *testing.T) {
	u := &aikit.UnitInfo{DPS: 50, HP: 500, Range: 200, Value: 100}
	var one, two force
	one.add(u, 500, 1000)
	two.add(u, 500, 1000)
	two.add(u, 500, 1000)
	if r := ratio(&two, &one); r != 4000 {
		t.Errorf("ratio(2 vs 1) = %d, want 4000", r)
	}
	long := &aikit.UnitInfo{DPS: 50, HP: 500, Range: 400, Value: 100}
	var l force
	l.add(long, 500, 1000)
	if r := ratio(&l, &one); r <= 1000 {
		t.Errorf("outranging force ratio %d, want > 1000", r)
	}
	var none force
	if r := ratio(&one, &none); r != ratioCap {
		t.Errorf("ratio vs nothing = %d, want cap", r)
	}
}

// The router goes around an expensive wall through its gap, and the
// compressed route turns at known ground only.
func TestRouteAroundWall(t *testing.T) {
	m := &aikit.MapInfo{SectorW: 12, SectorH: 12}
	var r router
	r.init(12, 12)
	known := make([]uint8, 144)
	for i := range r.cost {
		r.cost[i] = 10
		known[i] = 1
	}
	// A wall at x = 6 except a gap at z = 10.
	for z := int32(0); z < 12; z++ {
		if z != 10 {
			r.cost[z*12+6] = 2000
		}
	}
	from, to := int32(5*12+1), int32(5*12+10)
	cost := r.search(from, to)
	if cost >= routeInf {
		t.Fatal("no route")
	}
	through := false
	for _, s := range r.path {
		if s%12 == 6 {
			through = s/12 == 10
		}
	}
	if !through {
		t.Errorf("route crossed the wall outside the gap: %v", r.path)
	}
	ax, az := m.SectorCentre(from)
	bx, bz := m.SectorCentre(to)
	if direct := r.lineCost(m, ax, az, bx, bz); direct <= cost {
		t.Errorf("direct line cost %d not above routed cost %d", direct, cost)
	}
	// Through the squad's own storage, as planRoute and airRoute use it.
	a := &Army{rt: r}
	s := &squad{}
	a.routeInto(m, s, known)
	if s.nwp == 0 || s.nwp > maxWaypoints || !s.routed {
		t.Errorf("waypoints %v (%d)", s.wps, s.nwp)
	}
}

// A route with more turns than a squad keeps is thinned to an even subset
// of them, and that subset — not the first turns — is what the squad
// stores: the compression appends every turn before thinning, past the
// capacity of the squad's array.
func TestRouteManyTurns(t *testing.T) {
	const w = 30
	m := &aikit.MapInfo{SectorW: w, SectorH: w}
	var r router
	r.init(w, w)
	known := make([]uint8, w*w)
	for i := range known {
		known[i] = 1
	}
	// A staircase: four east, four south, repeated — six turns.
	x, z := int32(0), int32(0)
	r.path = append(r.path[:0], 0)
	for leg := 0; leg < 7; leg++ {
		for k := 0; k < 4; k++ {
			if leg%2 == 0 {
				x++
			} else {
				z++
			}
			r.path = append(r.path, z*w+x)
		}
	}
	all := r.waypoints(m, nil, known)
	if len(all) != maxWaypoints {
		t.Fatalf("thinned to %d, want %d", len(all), maxWaypoints)
	}
	a := &Army{rt: r}
	s := &squad{}
	a.routeInto(m, s, known)
	if int(s.nwp) != len(all) {
		t.Fatalf("squad keeps %d waypoints, want %d", s.nwp, len(all))
	}
	for i := range all {
		if s.wps[i] != all[i] {
			t.Errorf("waypoint %d = %v, want %v (the thinned route, not the first turns)", i, s.wps[i], all[i])
		}
	}
}

// The kernel disc painter must reproduce the grid's own disc exactly:
// every cell, every radius, clipping at the edges, negative values.
func TestAddDiscMatchesGrid(t *testing.T) {
	m := &aikit.MapInfo{SectorW: 23, SectorH: 17}
	var a Army
	for _, c := range []struct{ x, z, radius, value int32 }{
		{0, 0, 0, 7}, {100, 100, 64, 55}, {1500, 900, 700, 123}, {2900, 2100, 2000, -97},
		{50, 2150, 1300, 1000}, {1400, 1000, 128*48 + 100, 31}, {1400, 1000, 129, 1},
	} {
		want, got := aikit.NewGrid(m), aikit.NewGrid(m)
		want.AddDisc(c.x, c.z, c.radius, c.value)
		a.addDisc(got, c.x, c.z, c.radius, c.value)
		for i := range want.V {
			if want.V[i] != got.V[i] {
				t.Fatalf("disc %+v: cell %d = %d, want %d", c, i, got.V[i], want.V[i])
			}
		}
	}
}

// A pool slot that holds a different unit of the same definition starts
// with fresh memory: the dead unit's order, sortie and withdrawal do not
// carry over and its loss is counted; a strike does not count the newcomer
// among its survivors; an order aimed at a recycled target slot is a new
// order.
func TestRecycledSlotResets(t *testing.T) {
	a, _ := testArmy(10, 10)
	bomber := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile | aikit.RoleAir | aikit.RoleCombat, Value: 300, HP: 500}
	a.classes = []uclass{{kind: ukBomber}}
	old := aikit.OwnUnit{H: 5, Gen: 1, Info: bomber, Built: true, HP: 500, Order: aikit.OrderAttack}
	um := a.unit(&old)
	um.ordKind, um.ordX, um.ordT, um.ordG, um.ordTick = okAttack, 1000, 9, 1, 100
	um.sortie, um.withdraw, um.built, um.seen = 77, 300, true, 600
	a.seenTick = 600
	if a.unit(&old).sortie != 77 {
		t.Fatal("the same instance lost its memory")
	}
	// The order is carried: an attack on the same target instance is not
	// re-sent, one on a new unit in the target's slot is.
	b := &core.Board{K: &aikit.Kit{Persona: aikit.PersonaHard}, O: &aikit.Obs{Own: []aikit.OwnUnit{old}}, Tick: 700}
	if a.needs(b, 0, okAttack, 1000, 0, 9, 1) || !a.needs(b, 0, okAttack, 1000, 0, 9, 2) {
		t.Error("attack order not told apart by the target's instance")
	}
	fresh := old
	fresh.Gen = 2
	um = a.unit(&fresh)
	if um.ordKind != okNone || um.sortie != 0 || um.withdraw != 0 || um.gen != 2 {
		t.Errorf("recycled slot kept the dead unit's memory: %+v", *um)
	}
	if a.lost[bkAir] != 300 {
		t.Errorf("dead unit's loss %d, want 300", a.lost[bkAir])
	}
	// noteStrike counts a sortie member alive only as the same instance.
	a.units[5] = unitMem{info: bomber, gen: 1, sortie: 77}
	s := &a.sq[sqStrike]
	s.sortie, s.launchValue = 77, 300
	a.noteStrike(&core.Board{O: &aikit.Obs{Own: []aikit.OwnUnit{fresh}}}, s)
	if a.calLoss != 300 {
		t.Errorf("sortie loss %d with the slot recycled, want 300", a.calLoss)
	}
	a.units[5] = unitMem{info: bomber, gen: 1, sortie: 77}
	s.sortie, s.launchValue = 77, 300
	a.noteStrike(&core.Board{O: &aikit.Obs{Own: []aikit.OwnUnit{old}}}, s)
	if a.calLoss != 300 {
		t.Errorf("sortie loss %d with the member alive, want no more", a.calLoss)
	}
}

// An unseen enemy commander is presumed at work among its factories, and
// it is the commander of the side that built them: in a mirror game the
// enemy's side is our own.
func TestPresumedCommanderBySide(t *testing.T) {
	armcom := &aikit.UnitInfo{Index: 0, Side: "ARM", Role: aikit.RoleCommander | aikit.RoleMobile | aikit.RoleBuilder, DPS: 100, HP: 3000, Value: 3000, Range: 300}
	corcom := &aikit.UnitInfo{Index: 1, Side: "CORE", Role: aikit.RoleCommander | aikit.RoleMobile | aikit.RoleBuilder, DPS: 125, HP: 1500, Value: 3000, Range: 300}
	armlab := &aikit.UnitInfo{Index: 2, Side: "ARM", Role: aikit.RoleFactory, Value: 600, HP: 2000}
	corlab := &aikit.UnitInfo{Index: 3, Side: "CORE", Role: aikit.RoleFactory, Value: 600, HP: 2000}
	tab := &aikit.Table{Units: []*aikit.UnitInfo{armcom, corcom, armlab, corlab}}
	for _, c := range []struct {
		lab, com *aikit.UnitInfo
	}{{armlab, armcom}, {corlab, corcom}} {
		m := &aikit.MapInfo{SectorW: 40, SectorH: 40, WorldW: 40 * aikit.SectorWorld, WorldH: 40 * aikit.SectorWorld}
		k := &aikit.Kit{Side: "ARM", Table: tab, Map: m, Persona: aikit.PersonaHard}
		o := &aikit.Obs{Tick: 900, Memory: []aikit.Remembered{{H: 7, Gen: 1, Info: c.lab, X: 4000, Z: 4000, LastSeen: 900, Building: true}}}
		b := &core.Board{K: k, O: o, Tick: 900, Commander: -1}
		a := New(DefaultParams())
		a.setup(b)
		a.buildPicture(b)
		z := &a.zones[a.zoneOf(4000, 4000)]
		if z.mob.hp != int64(c.com.HP)/2 || z.mob.dps != int64(c.com.DPS)/2+commanderDPSBonus/2 {
			t.Errorf("%s factory: presumed defender dps %d hp %d, want half of the %s commander", c.lab.Side, z.mob.dps, z.mob.hp, c.com.Side)
		}
	}
}

// A unit that has stood still far from its order for thirty seconds is
// stuck — unless it is fighting where it stands (hurt lately, or an enemy
// within its weapon range), when it stays in its squad and gets its
// squad's orders, the retreat included.
func TestStuckNotWhileFighting(t *testing.T) {
	a, _ := testArmy(40, 40)
	tank := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 50, HP: 1000, Range: 300, Value: 200}
	enemy := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 50, HP: 1000, Range: 300, Value: 200}
	u := aikit.OwnUnit{H: 3, Gen: 1, Info: tank, X: 1000, Z: 1000, Built: true, HP: 1000}
	um := a.unit(&u)
	um.ordKind, um.ordX, um.ordZ, um.ordTick, um.movedTick = okPatrol, 3000, 1000, 100, 100
	b := &core.Board{O: &aikit.Obs{Tick: 2000}, Tick: 2000}
	if !a.stuck(b, &u, um) {
		t.Fatal("a unit standing still far from its order with nothing around is stuck")
	}
	b.O.Enemy = []aikit.Contact{{H: 9, Gen: 1, Info: enemy, X: 1300, Z: 1000, Visible: true}}
	if a.stuck(b, &u, um) {
		t.Error("a unit with an enemy in weapon range is fighting, not stuck")
	}
	b.O.Enemy[0].X = 2000
	um.hurtTick = 1900
	if a.stuck(b, &u, um) {
		t.Error("a unit hurt in the last five seconds is fighting, not stuck")
	}
	um.hurtTick = 1000
	if !a.stuck(b, &u, um) {
		t.Error("an old hurt and a distant enemy do not keep it in the squad")
	}
}

// A squad whose members are all worth nothing (possible with modded
// content) and have all left its previous centre still has a body: the
// centre search must not come up empty.
func TestSummarizeWorthlessSquad(t *testing.T) {
	a, _ := testArmy(40, 40)
	free := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 10, HP: 100, Range: 200, Speed: 60}
	o := &aikit.Obs{Tick: 300}
	for i := 0; i < 3; i++ {
		o.Own = append(o.Own, aikit.OwnUnit{H: pool.Handle(1 + i), Gen: 1, Info: free, X: 3000 + int32(i)*50, Z: 3000, Built: true, HP: 100})
	}
	b := &core.Board{O: o, Tick: 300}
	for i := range o.Own {
		a.unit(&o.Own[i])
	}
	s := &a.sq[sqMain]
	s.members = []int32{0, 1, 2}
	s.hasCentre, s.cx, s.cz = true, 500, 500 // far from every member
	a.summarize(b, s)
	if !s.hasCentre || s.present.n != 3 {
		t.Errorf("centre %v present %d, want the three members", s.hasCentre, s.present.n)
	}
}
