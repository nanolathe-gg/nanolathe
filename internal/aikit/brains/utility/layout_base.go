package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Second round of the front rules (README §12.4), with the defense plan and
// the layout switch on: tower timing (defense_time.go) and a wider base
// (below). The Variety switches tower_time=0 and wide_base=0 turn each off;
// off, the game is the one before this round.
type front2 struct {
	towers bool // tower timing
	wide   bool // the wider base
	// baseX, baseZ is this think's enemy-base estimate for the zones
	// (enemyBase); computed whenever the layout switch is on, used only
	// with wide.
	baseX, baseZ int32
	// Factory placements that failed since the own factory count (built or
	// framed) last changed: facRecent, from the failures counted when it
	// changed (facFail0) at count facN (wide).
	facN                int32
	facFail0, facRecent int64
}

// Wider base. People's economy radius (80th percentile of factories,
// energy, makers and storage from the start) is ~850 wu at minute 10 and
// ~1,200 at 20 (tools/ai-layout-bench, REPORT.md §5); the zones reached
// ~800 at 20. Two things held them in:
//
//   - The executor extended any row of the class within 700 wu of the
//     request point before it started a new one, so the rows beside the
//     start absorbed buildings asked for far beyond them. The zones now ask
//     it (Kit.SetRowNear) to extend only rows within wideRowNear of the
//     point, so each class grows where its point is: along the flank,
//     block after block.
//   - The zones' caps (a share of the way to the enemy) and directions read
//     the board's enemy estimate: the centroid of every remembered enemy
//     building — the forward extractors and towers seen first, well short
//     of the base — or, before any is seen, the nearest start nobody of
//     ours has looked at, a quarter of the way over on a ten-start map.
//     The zones now use enemyBase: an enemy player's start, identified by
//     its factories.
//
// A third held the energy in: a start stands near a map edge or a cliff,
// and the energy flank's 70° ray from it often runs into one within a few
// hundred world units (the home-ground rule pulled half the energy points
// back), so half the energy stayed beside the start whatever the clock
// asked. With the wider base a flank narrows toward the enemy direction —
// energy 70°, 55°, then 40°; makers 45°, then 30° — until its ray keeps
// the requested distance on home ground and inside the map (people's
// energy stands at a median 60° [44°–92°], makers 46° [27°–78°]); failing
// every angle, the other flank's angles are tried, and failing those the
// ray with the most room.
//
// A factory that fails to place (the executor found no site within 640 wu
// of its point under the rules) turns to the next of the front bearings
// (factoryTurns: 0°, ±15°, ±30°, ±45°) and moves wideFacStep further out
// with each failure since the own factory count last changed (up to
// wideFacSteps), instead of cycling 0/120/240 wu out along one bearing: on
// a cramped start (great divide, by the south edge, where the one site
// near home stands beside a metal spot the commander took first) the
// first factory failed for six minutes.
const wideRowNear = 250 // world units

// wideEdge keeps a flank's request point this far inside the map.
const wideEdge = 96

// wideFacStep is how much further out (world units) each recent failed
// factory placement moves the factory point, up to wideFacSteps of them.
const (
	wideFacStep  = 160
	wideFacSteps = 6
)

// observeFactoryFails counts the factory placements that failed since the
// own factory count (built or framed) last changed.
func (s *shared) observeFactoryFails(b *core.Board) {
	f2 := &s.zones.f2
	var fails int64
	var n int32
	for _, idx := range s.zoneDefs {
		if s.zoneOf[idx] == zFactory {
			fails += int64(s.prodFails[idx])
		}
	}
	o := b.O
	for i := range o.Own {
		if r := o.Own[i].Info.Role; r.Has(aikit.RoleFactory) && !r.Has(aikit.RoleMobile) {
			n++
		}
	}
	if n != f2.facN {
		f2.facN, f2.facFail0 = n, fails
	}
	f2.facRecent = fails - f2.facFail0
}

var (
	rot55 = [2]int64{574, 819}
	rot40 = [2]int64{766, 643}
	// Flank angles by zone class, widest first.
	wideRots = [zMaker + 1][][2]int64{zEnergy: {rot70, rot55, rot40}, zMaker: {rot45, rot30}}
)

// wideFlank is the request direction (unit ×1000) and distance of an
// energy or maker point on one side (±1) of the enemy direction (ux, uz)
// under the wider base: the widest of the class's angles on that side
// whose ray keeps dist (groundRoom), then the same on the other side,
// else the ray with the most room, at that room.
func (s *shared) wideFlank(m *aikit.MapInfo, hx, hz int32, ux, uz int64, zc uint8, side, dist int64) (int64, int64, int64) {
	var bx, bz, bg int64 = 0, 0, -1
	for _, sd := range [2]int64{side, -side} {
		for _, r := range wideRots[zc] {
			c, sn := r[0], r[1]*sd
			vx := (ux*c - uz*sn) / 1000
			vz := (ux*sn + uz*c) / 1000
			g := s.groundRoom(m, hx, hz, vx, vz, dist)
			if g >= dist {
				return vx, vz, dist
			}
			if g > bg {
				bx, bz, bg = vx, vz, g
			}
		}
	}
	return bx, bz, bg
}

// groundRoom is how far (at most dist) a request point can stand along
// the ray home + v×d: on home ground behind the chokes guarding it
// (homeGround) and wideEdge inside the map.
func (s *shared) groundRoom(m *aikit.MapInfo, hx, hz int32, vx, vz, dist int64) int64 {
	for {
		dist = s.homeGround(hx, hz, vx, vz, dist)
		x, z := int64(hx)+vx*dist/1000, int64(hz)+vz*dist/1000
		if dist <= 160 || x >= wideEdge && z >= wideEdge && x <= int64(m.WorldW)-wideEdge && z <= int64(m.WorldH)-wideEdge {
			return dist
		}
		dist -= 80
	}
}

// enemyBase estimates the enemy base for the zones: for each enemy player
// with a remembered factory — or, before any, another base building
// (energy, makers, storage, radar: not the extractors and towers that stand
// out on the expansion and the front) — the start position nearest the
// centroid of those buildings; of those, the one nearest home. Before any
// such building is seen, the opponent's start nearest home when the start
// assignment is public, otherwise the start farthest from home (the
// model's prior). Starts within 400 wu of home are ours.
func (s *shared) enemyBase(b *core.Board) (int32, int32) {
	o := b.O
	m := b.K.Map
	const owners = 16
	var fx, fz, fn, bx, bz, bn [owners]int64
	for i := range o.Memory {
		r := &o.Memory[i]
		if !r.Building || int(r.Owner) >= owners {
			continue
		}
		switch ro := r.Info.Role; {
		case ro.Has(aikit.RoleFactory):
			fx[r.Owner] += int64(r.X)
			fz[r.Owner] += int64(r.Z)
			fn[r.Owner]++
		case ro.Any(aikit.RoleExtractor | aikit.RoleDefense):
		default:
			bx[r.Owner] += int64(r.X)
			bz[r.Owner] += int64(r.Z)
			bn[r.Owner]++
		}
	}
	hx, hz := b.HomeX, b.HomeZ
	best, found := int64(-1), false
	var ex, ez int32
	for p := 0; p < owners; p++ {
		var cx, cz int32
		switch {
		case fn[p] > 0:
			cx, cz = int32(fx[p]/fn[p]), int32(fz[p]/fn[p])
		case bn[p] > 0:
			cx, cz = int32(bx[p]/bn[p]), int32(bz[p]/bn[p])
		default:
			continue
		}
		x, z := snapStart(m, hx, hz, cx, cz)
		if d := aikit.Dist2(x, z, hx, hz); !found || d < best {
			best, found, ex, ez = d, true, x, z
		}
	}
	if found {
		return ex, ez
	}
	// With the start assignment public, the prior is the opponent's start
	// nearest home (MapInfo.StartEnemy).
	if m.StartEnemy != nil {
		near := int64(-1)
		for i, st := range m.Starts {
			if !m.MaybeEnemyStart(i) {
				continue
			}
			if d := aikit.Dist2(st[0], st[1], hx, hz); d >= 400*400 && (near < 0 || d < near) {
				near, ex, ez = d, st[0], st[1]
			}
		}
		if near >= 0 {
			return ex, ez
		}
	}
	var far int64 = -1
	for _, st := range m.Starts {
		if d := aikit.Dist2(st[0], st[1], hx, hz); d >= 400*400 && d > far {
			far, ex, ez = d, st[0], st[1]
		}
	}
	if far < 0 {
		return m.WorldW - hx, m.WorldH - hz
	}
	return ex, ez
}

// snapStart is the start position (not ours, and an opponent's when the
// start assignment is public) nearest (x, z); (x, z) itself on a map
// without such a start.
func snapStart(m *aikit.MapInfo, hx, hz, x, z int32) (int32, int32) {
	best := int64(-1)
	rx, rz := x, z
	for i, st := range m.Starts {
		if aikit.Dist2(st[0], st[1], hx, hz) < 400*400 || !m.MaybeEnemyStart(i) {
			continue
		}
		if d := aikit.Dist2(st[0], st[1], x, z); best < 0 || d < best {
			best, rx, rz = d, st[0], st[1]
		}
	}
	return rx, rz
}

// zoneEnemy is the enemy point the zones read: enemyBase with the wider
// base, the board's estimate otherwise.
func (s *shared) zoneEnemy(b *core.Board) (int32, int32) {
	if s.zones.f2.wide {
		return s.zones.f2.baseX, s.zones.f2.baseZ
	}
	return b.EnemyX, b.EnemyZ
}
