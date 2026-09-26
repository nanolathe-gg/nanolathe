package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Zoning (layout switch). Human bases (tools/ai-layout-bench, REPORT.md §8
// L6–L7) put factories forward — within 60° of the enemy direction,
// 400–1,200 wu out — energy and makers on the flanks (45–90°), almost
// nothing behind, and grow outward: the economy's radius is ~850 wu by
// minute 10 and ~1,200 by 20. The request point for each class follows
// that, starting close to the start (the commander builds the first ones
// and walking costs the opening) and moving out with every building; the
// executor then extends or starts rows there (layout.go).

// zoneCounts is how many own buildings of each zoned class stand or are
// planned (frames and walking builders), refreshed each think, and the
// terrain's chokes (read once at setup for the defense plan).
type zoneCounts struct {
	fac, energy, maker int32
	choke              *aikit.ChokeMap
	// far is, per zone class and flank (0: the +angle side, 1: the other),
	// the distance from home of the class's farthest own building on that
	// flank (spread, front rules).
	far [zMaker + 1][2]int64

	// Instrumentation for the arena's Report (Strategy.Report), never read
	// by a decision: the defense plan's tower audit and the zones' own
	// (defense_audit.go, layout_audit.go). They live here because the
	// strategy and economy layers share only the model.
	audit  towerAudit
	laudit layoutAudit
	// f2 holds the second round of front rules' switches and state
	// (layout_base.go).
	f2 front2
}

func (s *shared) observeZones(b *core.Board) {
	z := &s.zones
	z.fac, z.energy, z.maker = 0, 0, 0
	for _, idx := range s.zoneDefs {
		n := s.count[idx]
		switch s.zoneOf[idx] {
		case zFactory:
			z.fac += n
		case zEnergy:
			z.energy += n
		case zMaker:
			z.maker += n
		}
	}
	z.f2.baseX, z.f2.baseZ = s.enemyBase(b)
	if z.f2.wide {
		b.K.SetRowNear(wideRowNear)
		s.observeFactoryFails(b)
	}
	if s.p.DefPlan != 0 {
		s.observeFrontier(b)
	}
	z.laudit.sample(s, b)
}

const (
	zNone uint8 = iota
	zFactory
	zEnergy
	zMaker
)

// setupZones lists the land buildings the zones count.
func (s *shared) setupZones(k *aikit.Kit) {
	t := k.Table
	s.zoneOf = make([]uint8, len(t.Units))
	for i, u := range t.Units {
		if u.Role.Has(aikit.RoleMobile) || s.info[i].water || s.info[i].geo {
			continue
		}
		switch r := u.Role; {
		case r.Has(aikit.RoleFactory):
			s.zoneOf[i] = zFactory
		case r.Any(aikit.RoleDefense | aikit.RoleRadar | aikit.RoleExtractor):
			continue
		case r.Has(aikit.RoleEnergy):
			s.zoneOf[i] = zEnergy
		case r.Any(aikit.RoleMetalMaker | aikit.RoleStorage):
			s.zoneOf[i] = zMaker
		}
		if s.zoneOf[i] != zNone {
			s.zoneDefs = append(s.zoneDefs, int32(i))
		}
	}
	if s.p.DefPlan != 0 {
		s.zones.choke = analyzeChokes(k)
	}
}

// Angles as (cos, sin) × 1000.
var (
	rot0  = [2]int64{1000, 0}
	rot15 = [2]int64{966, 259}
	rot30 = [2]int64{866, 500}
	rot45 = [2]int64{707, 707}
	rot70 = [2]int64{342, 940}
)

// factoryTurns spreads successive factories across the front.
var factoryTurns = [...]struct {
	r    [2]int64
	side int64
}{{rot0, 1}, {rot30, 1}, {rot30, -1}, {rot15, 1}, {rot15, -1}, {rot45, 1}, {rot45, -1}}

// zoneFor returns the request point for a zoned building, ok false for a
// class the zones leave to siteFor.
func (e *Economy) zoneFor(b *core.Board, p *aikit.UnitInfo, kind commitKind) (int32, int32, bool) {
	s := e.s
	zc := s.zoneOf[p.Index]
	if zc == zNone || (kind != cFactory && kind != cEnergy && kind != cMaker && kind != cStorage) {
		return 0, 0, false
	}
	hx, hz := b.HomeX, b.HomeZ
	ex, ez := s.zoneEnemy(b)
	dx, dz := int64(ex-hx), int64(ez-hz)
	d := aikit.ISqrt64(dx*dx + dz*dz)
	if d < 1 {
		return 0, 0, false
	}
	ux, uz := dx*1000/d, dz*1000/d
	fails := int64(s.prodFails[p.Index])
	spread := s.p.DefPlan != 0 // the front rules (layout.go's zones need the layout switch)
	mins := min64(int64(s.minutes), spreadMinutes)
	var rot [2]int64
	var side, want, limit int64
	switch zc {
	case zFactory:
		turn := int64(s.zones.fac)
		if spread && s.zones.f2.wide {
			turn += s.zones.f2.facRecent // the wider base: a factory that failed to place turns (layout_base.go)
		}
		t := factoryTurns[turn%int64(len(factoryTurns))]
		rot, side = t.r, t.side
		want, limit = 280+130*int64(s.zones.fac), min64(1100, d*35/100)
		if spread {
			want, limit = spreadFac0+spreadFac*int64(s.zones.fac), min64(1200, d*40/100)
		}
	case zEnergy:
		// Alternate flanks every block's worth (six); a failed placement
		// tries the other flank.
		rot, side = rot70, 1
		if (int64(s.zones.energy)/6+fails)%2 == 1 {
			side = -1
		}
		want, limit = 220+grow(int64(s.zones.energy), 18, 10, 36), min64(1300, d*45/100)
		if spread {
			want = max64(max64(min64(want, limit), spreadEnergy+spreadRate*mins), s.zones.far[zEnergy][flank(side)]+spreadStep)
			limit = min64(1500, d*50/100)
		}
	default:
		rot, side = rot45, -1
		if (int64(s.zones.maker)/5+fails)%2 == 1 {
			side = 1
		}
		want, limit = 240+grow(int64(s.zones.maker), 22, 6, 44), min64(1200, d*40/100)
		if spread {
			want = max64(max64(min64(want, limit), spreadMaker+spreadRate*mins), s.zones.far[zMaker][flank(side)]+spreadStep)
			limit = min64(1400, d*45/100)
		}
	}
	dist := min64(want, limit)
	la := &s.zones.laudit
	la.req[zc]++
	if want > limit {
		la.capped[zc]++
	}
	if zc == zFactory && spread && s.zones.f2.wide {
		dist += wideFacStep * min64(s.zones.f2.facRecent, wideFacSteps)
	} else {
		dist += 120 * (fails / 2 % 3)
	}
	m := b.K.Map
	if spread && s.zones.f2.wide && zc != zFactory {
		// The wider base: the flank narrows until its ray has room
		// (layout_base.go).
		vx, vz, g := s.wideFlank(m, hx, hz, ux, uz, zc, side, dist)
		if g < dist {
			la.pulled[zc]++
		}
		return clampWorld(hx+int32(vx*g/1000), m.WorldW), clampWorld(hz+int32(vz*g/1000), m.WorldH), true
	}
	// Rotate the enemy direction by ±angle.
	c, sn := rot[0], rot[1]*side
	vx := (ux*c - uz*sn) / 1000
	vz := (ux*sn + uz*c) / 1000
	if spread {
		if g := s.homeGround(hx, hz, vx, vz, dist); g < dist {
			la.pulled[zc]++
			dist = g
		}
	}
	return clampWorld(hx+int32(vx*dist/1000), m.WorldW), clampWorld(hz+int32(vz*dist/1000), m.WorldH), true
}

// Spread (front rules). People grow the economy outward: the median
// energy building stands ~510 wu from the start at minute 10 and ~730 at
// 20, makers ~580 and ~770, factories ~640 and ~860, and the economy's
// radius (80th percentile) is ~850 and ~1,200 wu (tools/ai-layout-bench,
// REPORT.md §5). With the plan and layout both on, each class's request
// point also moves out with the clock — the energy point from 300 wu by
// 36 wu a minute, the makers' from 360 — and stands at least spreadStep
// beyond the class's farthest building on its flank: the executor extends
// the rows within 700 wu of the point first, so a point that only kept
// pace with the clock went on filling the first rows beside the start.
// The first factory stands 340 wu out and each next one 150 wu beyond the
// last. A point is pulled back toward the start until it lies on ground
// our army's units reach from home and, where the terrain has chokes
// guarding the start, behind them.
const (
	spreadEnergy  = 300 // world units at minute 0
	spreadMaker   = 360
	spreadRate    = 36  // world units a minute
	spreadMinutes = 30  // the clock stops pushing after this
	spreadStep    = 160 // world units beyond the flank's farthest building (a two-rank block's depth)
	spreadFac0    = 340 // world units to the first factory
	spreadFac     = 150 // world units between successive factories
	spreadMouth   = 480 // world units kept between a request point and a choke guarding home
)

// flank indexes zoneCounts.far by the side of a zone's rotation.
func flank(side int64) int {
	if side < 0 {
		return 1
	}
	return 0
}

// observeFrontier records each economy class's farthest own building
// (standing or framed) on either flank of the line toward the enemy.
func (s *shared) observeFrontier(b *core.Board) {
	z := &s.zones
	z.far = [zMaker + 1][2]int64{}
	o := b.O
	hx, hz := b.HomeX, b.HomeZ
	x0, z0 := s.zoneEnemy(b)
	ex, ez := int64(x0-hx), int64(z0-hz)
	for i := range o.Own {
		u := &o.Own[i]
		zc := s.zoneOf[u.Info.Index]
		if zc != zEnergy && zc != zMaker {
			continue
		}
		px, pz := int64(u.X-hx), int64(u.Z-hz)
		// The rotation's +angle side lies where the cross product with the
		// enemy direction is positive.
		f := flank(1)
		if ex*pz-ez*px < 0 {
			f = flank(-1)
		}
		if d := int64(aikit.Dist(u.X, u.Z, hx, hz)); d > z.far[zc][f] {
			z.far[zc][f] = d
		}
	}
}

// homeGround pulls a request point (home + v×dist, v a unit vector ×1000)
// back toward home until it stands in home's land region, behind the
// chokes that guard home and spreadMouth or more from each of them: rows
// and factories in front of a ramp's mouth leave the army that gathers or
// fights there nowhere to stand but the ramp.
func (s *shared) homeGround(hx, hz int32, vx, vz, dist int64) int64 {
	cm := s.zones.choke
	if cm == nil || cm.Reach == nil || cm.Home == 0 {
		return dist
	}
	want := cm.Guards(hx, hz)
	for ; dist > 160; dist -= 80 {
		x, z := hx+int32(vx*dist/1000), hz+int32(vz*dist/1000)
		if cm.Reach.At(x, z) != cm.Home || cm.Guards(x, z)&want != want {
			continue
		}
		clear := true
		for i := range cm.Chokes {
			c := &cm.Chokes[i]
			if want&(1<<uint(i)) != 0 && aikit.Dist2(x, z, c.X, c.Z) < spreadMouth*spreadMouth {
				clear = false
			}
		}
		if clear {
			break
		}
	}
	return dist
}

// grow is how far out a zone's point has moved after n buildings: step per
// building for the first k (the opening stays near the start), then late
// per building (the base grows outward, as people's do: ~850 wu of
// economy radius at minute 10, ~1,200 at 20).
func grow(n, step, k, late int64) int64 {
	if n <= k {
		return n * step
	}
	return k*step + (n-k)*late
}
