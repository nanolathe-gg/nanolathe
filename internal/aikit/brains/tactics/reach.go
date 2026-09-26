package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Reach: which of our units can get where. The map's region labels per
// movement class (aikit.MapInfo.Reach, public terrain) are summarized into a
// per-sector table of the regions present, so a think asks "can this
// squad's members get within range of that point?" with a few table
// lookups. A sector's entry is filled the first time it is asked for: most
// classes are never fielded and most sectors never asked about, and
// filling every table at Init cost about 40 ms per player on the largest
// maps. Features and units are ignored by the labels, so a region is an
// upper bound on where a unit can walk; the stuck watch covers the rest.

// rclass is one movement profile of our side.
type rclass struct {
	mc aikit.MoveClass
	r  *aikit.Reach
	// sreg holds up to two regions with anchors in each sector (0 = none),
	// filled on first use (done: one bit per sector).
	sreg []uint16
	done []uint64
}

// canonClass caps a class's footprint (3 cells on land, 4 at sea) the same
// way the utility economy does, so both share the map's cached region maps:
// the question is depth and slope, and the cap keeps the flood fills to
// about a dozen.
func canonClass(mc aikit.MoveClass) aikit.MoveClass {
	lim := int32(3)
	if mc.MinDepth > 0 {
		lim = 4
	}
	if mc.FootX > lim {
		mc.FootX = lim
	}
	if mc.FootZ > lim {
		mc.FootZ = lim
	}
	return mc
}

// setupReach labels every mobile surface unit of our side with a movement
// profile and allocates each profile's sector table (filled lazily,
// sectorRegions). It allocates: Init only, never a think.
func (a *Army) setupReach(k *aikit.Kit) {
	m := k.Map
	t := k.Table
	a.defCls = make([]int8, len(t.Units))
	for i := range a.defCls {
		a.defCls[i] = -1
	}
	if m == nil || m.CellW <= 0 || m.SectorW <= 0 {
		return
	}
	for i, u := range t.Units {
		if u.Side != k.Side || !u.Role.Any(aikit.RoleCombat|aikit.RoleBuilder|aikit.RoleScout) {
			continue
		}
		mc, ok := aikit.MoveClassOf(u)
		if !ok {
			continue
		}
		mc = canonClass(mc)
		c := -1
		for j := range a.rcls {
			if a.rcls[j].mc == mc {
				c = j
				break
			}
		}
		if c < 0 {
			if len(a.rcls) >= 100 {
				continue
			}
			a.rcls = append(a.rcls, rclass{mc: mc, r: m.Reach(mc)})
			c = len(a.rcls) - 1
		}
		a.defCls[i] = int8(c)
	}
	n := m.SectorW * m.SectorH
	for j := range a.rcls {
		rc := &a.rcls[j]
		rc.sreg = make([]uint16, 2*n)
		rc.done = make([]uint64, (n+63)/64)
	}
	a.secW = m.SectorW
	a.reachReady = true
}

// sectorRegions is the (up to two) regions of class c with anchors near
// sector s's centre, nearest first, computed on the first ask.
func (a *Army) sectorRegions(c int8, s int32) (uint16, uint16) {
	rc := &a.rcls[c]
	if rc.done[s>>6]&(1<<(s&63)) == 0 {
		x := s%a.secW*aikit.SectorWorld + aikit.SectorWorld/2
		z := s/a.secW*aikit.SectorWorld + aikit.SectorWorld/2
		r1, r2 := rc.r.Near2(x, z, aikit.SectorWorld/2+16)
		rc.sreg[2*s], rc.sreg[2*s+1] = uint16(r1), uint16(r2)
		rc.done[s>>6] |= 1 << (s & 63)
	}
	return rc.sreg[2*s], rc.sreg[2*s+1]
}

// hasRegion reports whether region reg of class c has anchors in sector s.
func (a *Army) hasRegion(c int8, reg uint16, s int32) bool {
	if reg == 0 {
		return false
	}
	r1, r2 := a.sectorRegions(c, s)
	return r1 == reg || r2 == reg
}

// regionNear reports whether region reg of class c has anchors in a sector
// whose centre lies within radius of (x, z).
func (a *Army) regionNear(m *aikit.MapInfo, c int8, reg uint16, x, z, radius int32) bool {
	if reg == 0 {
		return false
	}
	r := radius + aikit.SectorWorld/2
	sx0, sz0 := (x-r)/aikit.SectorWorld, (z-r)/aikit.SectorWorld
	sx1, sz1 := (x+r)/aikit.SectorWorld, (z+r)/aikit.SectorWorld
	if sx0 < 0 {
		sx0 = 0
	}
	if sz0 < 0 {
		sz0 = 0
	}
	if sx1 >= m.SectorW {
		sx1 = m.SectorW - 1
	}
	if sz1 >= m.SectorH {
		sz1 = m.SectorH - 1
	}
	lim := int64(r) * int64(r)
	for sz := sz0; sz <= sz1; sz++ {
		for sx := sx0; sx <= sx1; sx++ {
			s := sz*m.SectorW + sx
			if r1, r2 := a.sectorRegions(c, s); r1 != reg && r2 != reg {
				continue
			}
			cx, cz := sx*aikit.SectorWorld+aikit.SectorWorld/2, sz*aikit.SectorWorld+aikit.SectorWorld/2
			if aikit.Dist2(cx, cz, x, z) <= lim {
				return true
			}
		}
	}
	return false
}

func absI(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// unitRegion is the movement class and region of an own unit where it
// stands (class -1 for aircraft and buildings).
func (a *Army) unitRegion(u *aikit.OwnUnit) (int8, uint16) {
	if !a.reachReady {
		return -1, 0
	}
	c := a.defCls[u.Info.Index]
	if c < 0 {
		return -1, 0
	}
	return c, uint16(a.rcls[c].r.At(u.X, u.Z))
}

// fleetWater makes "water" the sectors of the regions our ships sail in:
// the fleet can go anywhere in them and nowhere else. It is rebuilt only
// when the set of regions changes (a new class of ship, a ship in another
// sea); with no ships the previous picture stays.
func (a *Army) fleetWater(b *core.Board) {
	s := &a.sq[sqNaval]
	var sig int64
	for i := int32(0); i < s.ngroups; i++ {
		g := &s.groups[i]
		if g.cls >= 0 {
			v := int64(g.cls+1)<<16 | int64(g.reg)
			sig += v * v
		}
	}
	if sig == 0 || sig == a.fleetSig {
		return
	}
	a.fleetSig = sig
	m := b.K.Map
	for sec := range a.water {
		w := uint8(0)
		for i := int32(0); i < s.ngroups; i++ {
			g := &s.groups[i]
			if g.cls >= 0 && a.hasRegion(g.cls, g.reg, int32(sec)) {
				w = 1
				break
			}
		}
		a.water[sec] = w
	}
	a.updateWaterDist(m)
}

// maxGroups bounds the movement groups tracked per squad.
const maxGroups = 6

// rgroup is the part of a squad in one region of one movement class.
type rgroup struct {
	cls   int8
	reg   uint16
	value int64
	rng   int32 // longest weapon range among them
}

// addGroup counts a member in its squad's movement groups.
func (s *squad) addGroup(c int8, reg uint16, value int64, rng int32) {
	for i := int32(0); i < s.ngroups; i++ {
		g := &s.groups[i]
		if g.cls == c && g.reg == reg {
			g.value += value
			if rng > g.rng {
				g.rng = rng
			}
			return
		}
	}
	if s.ngroups < maxGroups {
		s.groups[s.ngroups] = rgroup{cls: c, reg: reg, value: value, rng: rng}
		s.ngroups++
	}
}

// reachShare is the permille of a squad's surface value that can come
// within fire range (plus slack) of (x, z): 1000 when the reach tables are
// not in use or the squad has no surface members.
func (a *Army) reachShare(b *core.Board, s *squad, x, z, slack int32) int64 {
	if !a.reachReady || !a.P.Naval || s.ngroups == 0 {
		return 1000
	}
	m := b.K.Map
	var in, all int64
	for i := int32(0); i < s.ngroups; i++ {
		g := &s.groups[i]
		all += g.value
		if g.cls < 0 {
			in += g.value // aircraft
			continue
		}
		rng := g.rng
		if rng < 150 {
			rng = 150
		}
		if a.regionNear(m, g.cls, g.reg, x, z, rng+slack) {
			in += g.value
		}
	}
	if all <= 0 {
		return 1000
	}
	return in * 1000 / all
}

// reaches reports whether most of a squad can get within range of (x, z).
func (a *Army) reaches(b *core.Board, s *squad, x, z int32) bool {
	return a.reachShare(b, s, x, z, 32) >= 500
}

// mainGroup is the squad's largest movement group.
func (s *squad) mainGroup() *rgroup {
	var best *rgroup
	for i := int32(0); i < s.ngroups; i++ {
		g := &s.groups[i]
		if g.cls >= 0 && (best == nil || g.value > best.value) {
			best = g
		}
	}
	return best
}

// landCutNow reports whether the walkers' region at home does not come
// near the enemy base: the land route is cut by water or cliffs.
func (a *Army) landCutNow(b *core.Board) bool {
	if !a.reachReady {
		return false
	}
	c, reg := a.walkerHome(b)
	if c < 0 {
		return false
	}
	return !a.regionNear(b.K.Map, c, reg, b.EnemyX, b.EnemyZ, 800)
}

// walkerHome is the movement class of our most numerous walker and its
// region at home.
func (a *Army) walkerHome(b *core.Board) (int8, uint16) {
	o := b.O
	var counts [16]int32
	for _, i := range b.Combat {
		u := &o.Own[i]
		if a.cls(u.Info).kind != ukGround {
			continue
		}
		if c := a.defCls[u.Info.Index]; c >= 0 && c < 16 {
			counts[c]++
		}
	}
	best := int8(-1)
	for c := range counts {
		if counts[c] > 0 && (best < 0 || counts[c] > counts[best]) {
			best = int8(c)
		}
	}
	if best < 0 {
		return -1, 0
	}
	return best, a.homeReg[best]
}

// unitReaches reports whether an own unit can get near (x, z) for an order
// of the kind: within fire range for patrols and attacks, within 250 wu
// for a move. The answer is kept per unit while it stands in the same
// region and the point does not move.
func (a *Army) unitReaches(b *core.Board, u *aikit.OwnUnit, um *unitMem, x, z int32, kind uint8) bool {
	c, reg := a.unitRegion(u)
	if c < 0 || reg == 0 {
		return true // aircraft, or standing nowhere a region knows (take it on trust)
	}
	radius := int32(250)
	if kind != okMove && u.Info.Range+32 > radius {
		radius = u.Info.Range + 32
	}
	if um.rTested && um.rReg == reg && um.rRadius == radius && aikit.Dist2(um.rX, um.rZ, x, z) <= 64*64 {
		return um.rOK
	}
	ok := a.regionNear(b.K.Map, c, reg, x, z, radius)
	um.rTested, um.rReg, um.rRadius, um.rX, um.rZ, um.rOK = true, reg, radius, x, z, ok
	return ok
}

// recall gives units whose orders lead out of their reach an order they
// can carry out: members standing with their squad's body hold there (they
// fight what comes in range); the rest go to where they wait — ships to
// the fleet's gather point, everything else to the squad's gather point
// when they can reach it, else home, where the base defends.
func (a *Army) recall(b *core.Board, list []int32) {
	if len(list) == 0 {
		return
	}
	a.recalling = true
	o := b.O
	s := a.curSq
	for pass := 0; pass < 3; pass++ {
		var x, z int32
		a.rbuf = a.rbuf[:0]
		for _, i := range list {
			u := &o.Own[i]
			naval := a.cls(u.Info).kind == ukNaval
			withBody := s != nil && s.hasCentre && aikit.Dist2(u.X, u.Z, s.cx, s.cz) <= presentRadius*presentRadius
			switch pass {
			case 0: // hold with the body
				if withBody {
					a.rbuf = append(a.rbuf, i)
				}
			case 1: // ships wait at sea
				if !withBody && naval {
					a.rbuf = append(a.rbuf, i)
				}
			default:
				if !withBody && !naval {
					a.rbuf = append(a.rbuf, i)
				}
			}
		}
		if len(a.rbuf) == 0 {
			continue
		}
		switch pass {
		case 0:
			x, z = s.cx, s.cz
		case 1:
			x, z = a.navalGather(b)
		default:
			x, z = b.HomeX, b.HomeZ
			// (membership recalls before the first gather point is set)
			if s != nil && (a.gatherSet || s.id == sqNaval) {
				gx, gz := a.gatherPoint(b, s)
				u := &o.Own[a.rbuf[0]]
				if a.unitReaches(b, u, a.unit(u), gx, gz, okMove) {
					x, z = gx, gz
				}
			}
		}
		a.issueOnly(b, a.rbuf, okMove, x, z, 0, false)
		a.stranded += int64(a.lastSent)
	}
	a.recalling = false
}

// homeGround is where walkers wait when the way toward the enemy offers no
// ground of theirs: the sector of their home region nearest home that
// keeps clear of our factories (an army standing in a yard's exit lane
// boxes new units in), leaning toward the enemy; home itself when none.
func (a *Army) homeGround(b *core.Board, c int8, reg uint16, ex, ez int32) (int32, int32) {
	m := b.K.Map
	hx, hz := b.HomeX, b.HomeZ
	cx, cz := hx/aikit.SectorWorld, hz/aikit.SectorWorld
	best := int64(-1)
	bx, bz := hx, hz
	for ring := int32(1); ring <= 9; ring++ {
		for j := -ring; j <= ring; j++ {
			sz := cz + j
			if sz < 0 || sz >= m.SectorH {
				continue
			}
			for i := -ring; i <= ring; i++ {
				if absI(i) != ring && absI(j) != ring {
					continue
				}
				sx := cx + i
				if sx < 0 || sx >= m.SectorW || !a.hasRegion(c, reg, sz*m.SectorW+sx) {
					continue
				}
				x, z := sx*aikit.SectorWorld+aikit.SectorWorld/2, sz*aikit.SectorWorld+aikit.SectorWorld/2
				if a.nearFactory(b, x, z) {
					continue
				}
				// Distance from home, less a little for leaning toward the enemy.
				d := int64(aikit.Dist(x, z, hx, hz))*4 + int64(aikit.Dist(x, z, ex, ez))
				if best < 0 || d < best {
					best, bx, bz = d, x, z
				}
			}
		}
		if best >= 0 {
			break
		}
	}
	return bx, bz
}
