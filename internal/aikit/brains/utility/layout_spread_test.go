package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Spread (front rules): the energy and maker request points move out with
// the clock, each factory 150 wu beyond the last; with the defense plan off
// the zones are the earlier ones exactly.
func TestLayoutSpreadWithClock(t *testing.T) {
	dist := func(p Params, minute uint32, u func(d *defUnits) *aikit.UnitInfo, kind commitKind) int32 {
		w := newDefWorld(p)
		w.think(minute*1800, w.base())
		x, z, ok := w.e.zoneFor(w.b, u(w.d), kind)
		if !ok {
			t.Fatalf("no zone for %s", u(w.d).Key)
		}
		return aikit.Dist(x, z, w.b.HomeX, w.b.HomeZ)
	}
	sol := func(d *defUnits) *aikit.UnitInfo { return d.sol }
	on := DefaultParams()
	off := DefaultParams()
	off.DefPlan = 0
	e5, e20 := dist(on, 5, sol, cEnergy), dist(on, 20, sol, cEnergy)
	if e20 <= e5 {
		t.Errorf("energy point at minute 20 (%d wu) not beyond minute 5 (%d)", e20, e5)
	}
	if want := int32(spreadEnergy + spreadRate*20); e20 < want-2 || e20 > want+2 {
		t.Errorf("energy point at minute 20: %d wu, want %d", e20, want)
	}
	if o5, o20 := dist(off, 5, sol, cEnergy), dist(off, 20, sol, cEnergy); o5 != o20 {
		t.Errorf("plan off: energy point moved with the clock (%d → %d)", o5, o20)
	}
}

// The wider base's enemy estimate: before any enemy base building is seen,
// the start farthest from home (where the board takes the nearest start
// nobody has looked at); a forward extractor seen near a middle start does
// not move it; an enemy factory seen identifies its player's start.
func TestLayoutEnemyBase(t *testing.T) {
	w := newDefWorld(DefaultParams())
	w.k.Map.Starts = [][2]int32{{512, 512}, {1600, 512}, {3584, 3584}}
	d := w.d
	see := func(u *aikit.UnitInfo, x, z int32) aikit.Remembered {
		return aikit.Remembered{H: 90, Info: u, Owner: 1, X: x, Z: z, LastSeen: 9000, Building: true}
	}
	for _, c := range []struct {
		name         string
		mem          []aikit.Remembered
		wantX, wantZ int32
	}{
		{"nothing seen", nil, 3584, 3584},
		{"a forward extractor", []aikit.Remembered{see(d.mex, 1650, 600)}, 3584, 3584},
		{"a factory", []aikit.Remembered{see(d.mex, 1650, 600), see(d.fac, 1500, 700)}, 1600, 512},
		{"a factory far away", []aikit.Remembered{see(d.fac, 3300, 3400), see(d.mex, 1650, 600)}, 3584, 3584},
	} {
		w.obs = &aikit.Obs{Tick: 9000, Own: w.base(), Memory: c.mem}
		w.b.Update(w.k, w.obs)
		if x, z := w.e.s.enemyBase(w.b); x != c.wantX || z != c.wantZ {
			t.Errorf("%s: enemy base (%d,%d), want (%d,%d); board (%d,%d)", c.name, x, z, c.wantX, c.wantZ, w.b.EnemyX, w.b.EnemyZ)
		}
	}
	// With the start assignment public and the opponent at the middle start,
	// the prior is that start, and a factory seen nearer the far start
	// still snaps to the opponent's.
	w.k.Map.StartEnemy = []bool{false, true, false}
	for _, c := range []struct {
		name string
		mem  []aikit.Remembered
	}{
		{"known start, nothing seen", nil},
		{"known start, a factory near the empty one", []aikit.Remembered{see(d.fac, 3300, 3400)}},
	} {
		w.obs = &aikit.Obs{Tick: 9000, Own: w.base(), Memory: c.mem}
		w.b.Update(w.k, w.obs)
		if x, z := w.e.s.enemyBase(w.b); x != 1600 || z != 512 {
			t.Errorf("%s: enemy base (%d,%d), want (1600,512)", c.name, x, z)
		}
	}
}

// The wider base's flank narrows toward the enemy until its ray has room:
// from a start 700 wu from the west edge with the enemy due south (+Z),
// the energy flank's 70° and 55° rays toward the west edge are cut short,
// so the point stands at 40° at the full distance; the open flank keeps
// 70°; from 200 wu no west angle has room, so the point takes the east
// flank.
func TestLayoutWideFlank(t *testing.T) {
	w := newDefWorld(DefaultParams())
	m := w.k.Map
	s := w.e.s
	// Enemy due south: u = (0, 1000); side +1 rotates toward -X, and vz is
	// the cosine of the angle from the enemy direction (×1000).
	for _, c := range []struct {
		name string
		hx   int32
		side int64
		west bool
		cos  int64
		dist int64
	}{
		{"narrowed", 700, 1, true, rot40[0], 800},
		{"open flank", 700, -1, false, rot70[0], 800},
		{"no room on this side", 200, 1, false, rot70[0], 800},
	} {
		vx, vz, g := s.wideFlank(m, c.hx, 1000, 0, 1000, zEnergy, c.side, c.dist)
		if g != c.dist || (vx < 0) != c.west || vz != c.cos {
			t.Errorf("%s: direction (%d,%d) at %d wu, want west %v, cos %d, at %d", c.name, vx, vz, g, c.west, c.cos, c.dist)
		}
		if x := int64(c.hx) + vx*g/1000; x < wideEdge {
			t.Errorf("%s: point %d wu from the west edge", c.name, x)
		}
	}
}

// The wider base's failing factory: each factory placement that failed
// since the own factory count last changed turns the factory point to the
// next front bearing and moves it wideFacStep further out; a new factory
// (the count changes) starts afresh.
func TestLayoutWideFactoryFails(t *testing.T) {
	w := newDefWorld(DefaultParams())
	d := w.d
	s := w.e.s
	own := w.base()
	w.think(3*1800, own)
	x0, z0, _ := w.e.zoneFor(w.b, d.fac, cFactory)
	s.prodFails[d.fac.Index] = 2
	w.think(3*1800+15, own)
	if s.zones.f2.facRecent != 2 {
		t.Fatalf("recent factory failures %d, want 2", s.zones.f2.facRecent)
	}
	x1, z1, _ := w.e.zoneFor(w.b, d.fac, cFactory)
	d0, d1 := aikit.Dist(x0, z0, w.b.HomeX, w.b.HomeZ), aikit.Dist(x1, z1, w.b.HomeX, w.b.HomeZ)
	if d1 < d0+2*wideFacStep-2 || (x1-w.b.HomeX)*(z0-w.b.HomeZ) == (z1-w.b.HomeZ)*(x0-w.b.HomeX) {
		t.Errorf("after two failures the factory point (%d,%d) at %d wu did not turn and move %d out from (%d,%d) at %d", x1, z1, d1, 2*wideFacStep, x0, z0, d0)
	}
	own = append(own, aikit.OwnUnit{H: 30, Info: d.fac, X: 900, Z: 900, HP: 10, MaxHP: d.fac.HP, Progress: 5})
	w.think(3*1800+30, own)
	if s.zones.f2.facRecent != 0 {
		t.Errorf("a new factory frame: recent failures %d, want 0", s.zones.f2.facRecent)
	}
}
