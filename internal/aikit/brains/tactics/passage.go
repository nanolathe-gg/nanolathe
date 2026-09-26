package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Passages. A squad that waits in a ramp or a pass closes it: on The Pass
// the army gathered in the only ramp down from its base, and new units
// stood trapped behind it. With Params.Passage on, the ground squads' gather
// and stage points keep out of terrain passages, judged by the layout's
// passage rule (aikit.MapInfo.InPassage: impassable ground within eight
// cells on both sides of some row or column of a footprint) for the land
// classes of the squad that would stand there:
//
//   - The gathered squad is judged as a blob: its centre and eight points
//     on a ring (and on a half-radius ring for a big squad), each tested as
//     one unit footprint of the class. The radius grows with the square
//     root of the member count, 27 wu per √member — a unit of two by two
//     cells with a cell of room around it, packed in a disc — between 64 and
//     320 wu. Ring points a unit of the class could not stand on, or in
//     another region, are skipped: the blob does not spread there.
//   - The main gather point stops short of the first passage on its way
//     toward the enemy: of the candidate points on the line from home
//     (the usual ones: 20–45 % of the way, at least 400 wu from home, on
//     the walkers' ground, clear of the factories and of danger) it takes
//     the farthest before the first in a passage, so the army waits on the
//     home side. When the first is already in one, a smaller blob is
//     tried — the half-radius ring, then the centre alone (on The Pass the
//     whole middle of the map is one long passage by the rule, and the
//     plateau's edge only fits part of a big army) — before the usual
//     fallback point (20 % of the way, or open ground near home). Failing
//     all of those, the point walks off the line: back toward home, no
//     nearer than 400 wu and clear of the factories (an army waiting in
//     the base's lanes traps its own units), or forward past the passage
//     (up to 1200 wu); failing that it stays where it was. The home
//     guard's point, a third of the way to it, walks the same way with no
//     least distance.
//   - A stage point, walked back from the target, skips points in a
//     passage: it lands on the side the squad comes from (the far side,
//     toward the target, is inside the target's defenses by construction)
//     — unless that is more than 640 wu farther back, when the first
//     point is kept (a long pass cannot be stepped around).
//
// Verdicts are cached per class on a 64 wu grid, so a point costs one
// lookup after its first test. The fleet, the air forces and the amphibious
// squad are unchanged. passage=0 restores the old points exactly.

const (
	gatherCands   = 11  // gather candidates: 20 % to 45 % of the way in 2.5 % steps
	passageGrid   = 4   // plot cells per cache cell (64 wu)
	gatherMinHome = 400 // wu: the least distance from home of the main gather point
	blobPerRoot   = 27  // wu of blob radius per √member
	blobMin       = 64  // wu
	blobMax       = 320 // wu
	blobInner     = 160 // wu: from this radius a half-radius ring is tested too
	passageStep   = 64  // wu per step when walking out of a passage
	passageReach  = 1200
)

// blobDirs are eight directions (cos, sin × 1000).
var blobDirs = [8][2]int64{{1000, 0}, {707, 707}, {0, 1000}, {-707, 707}, {-1000, 0}, {-707, -707}, {0, -1000}, {707, -707}}

// passageSquad reports whether squad s keeps its points out of passages.
func (a *Army) passageSquad(s *squad) bool {
	return a.P.Passage && a.reachReady && (s.id == sqMain || s.id == sqRaid || s.id == sqDefend)
}

// blobRadius is the radius of the disc the squad's members fill when
// gathered.
func blobRadius(n int32) int32 {
	r := int32(aikit.ISqrt64(int64(n))) * blobPerRoot
	if r < blobMin {
		r = blobMin
	}
	if r > blobMax {
		r = blobMax
	}
	return r
}

// cellPassage reports whether one unit footprint of class c centred at
// (x, z) stands in a passage, cached per 64 wu cell.
func (a *Army) cellPassage(m *aikit.MapInfo, c int8, x, z int32) bool {
	if len(a.passV) != len(a.rcls) {
		a.passW = (m.CellW + passageGrid - 1) / passageGrid
		a.passH = (m.CellH + passageGrid - 1) / passageGrid
		a.passV = make([][]uint8, len(a.rcls))
	}
	gx, gz := x/(16*passageGrid), z/(16*passageGrid)
	if gx < 0 || gz < 0 || gx >= a.passW || gz >= a.passH {
		return false
	}
	v := a.passV[c]
	if v == nil {
		v = make([]uint8, a.passW*a.passH)
		a.passV[c] = v
	}
	i := gz*a.passW + gx
	switch v[i] {
	case 1:
		return false
	case 2:
		return true
	}
	mc := &a.rcls[c].mc
	cx := gx*passageGrid + passageGrid/2 - mc.FootX/2
	cz := gz*passageGrid + passageGrid/2 - mc.FootZ/2
	in := m.InPassage(mc, cx, cz, mc.FootX, mc.FootZ)
	v[i] = 1
	if in {
		v[i] = 2
	}
	return in
}

// Blob extents: the centre alone, with the half-radius ring, and with both
// rings.
const (
	blobCentre = 0
	blobHalf   = 1
	blobFull   = 2
)

// blobInPassage reports whether squad s, gathered around (x, z), would
// stand in a passage for one of its land classes.
func (a *Army) blobInPassage(b *core.Board, s *squad, x, z int32) bool {
	return a.blobIn(b, s, x, z, blobFull)
}

// blobIn is blobInPassage for a blob of the given extent.
func (a *Army) blobIn(b *core.Board, s *squad, x, z int32, extent int) bool {
	if !a.passageSquad(s) {
		return false
	}
	m := b.K.Map
	n := s.total.n
	var tested [maxGroups]int8
	nt := 0
	test := func(c int8, reg uint16) bool {
		for i := 0; i < nt; i++ {
			if tested[i] == c {
				return false
			}
		}
		tested[nt] = c
		nt++
		r := a.rcls[c].r
		if a.cellPassage(m, c, x, z) {
			return true
		}
		R := blobRadius(n)
		for ring, rr := range [...]int32{R / 2, R} {
			if ring >= extent || (ring == 0 && R < blobInner && extent == blobFull) {
				continue
			}
			for _, d := range blobDirs {
				px := x + int32(d[0]*int64(rr)/1000)
				pz := z + int32(d[1]*int64(rr)/1000)
				if px < 0 || pz < 0 || px >= m.WorldW || pz >= m.WorldH {
					continue
				}
				if reg != 0 && uint16(r.At(px, pz)) != reg {
					continue // the blob does not spread onto ground its units cannot stand on
				}
				if a.cellPassage(m, c, px, pz) {
					return true
				}
			}
		}
		return false
	}
	for i := int32(0); i < s.ngroups; i++ {
		g := &s.groups[i]
		if g.cls < 0 || a.rcls[g.cls].mc.MinDepth > 0 || nt >= len(tested) {
			continue
		}
		if test(g.cls, g.reg) {
			return true
		}
	}
	if nt == 0 {
		if c, reg := a.walkerHome(b); c >= 0 && test(c, reg) {
			return true
		}
	}
	return false
}

// outOfPassage moves a squad's waiting point out of a passage: back toward
// home to open ground the squad's ground holds, at least minHome from home
// and clear of the factories; when the home side has none, forward toward
// (fx, fz). The whole blob is tried first, then smaller extents. It returns
// the point unchanged when no extent fits on either side.
func (a *Army) outOfPassage(b *core.Board, s *squad, x, z, fx, fz, minHome int32) (int32, int32) {
	for extent := blobFull; extent >= blobCentre; extent-- {
		if extent < blobFull && !a.blobIn(b, s, x, z, extent) {
			return x, z // the point itself fits this extent
		}
		for pass := 0; pass < 2; pass++ {
			tx, tz := b.HomeX, b.HomeZ
			if pass == 1 {
				tx, tz = fx, fz
			}
			d := int64(aikit.Dist(x, z, tx, tz))
			lim := d
			if pass == 1 && lim > passageReach {
				lim = passageReach
			}
			for off := int64(passageStep); off <= lim; off += passageStep {
				px := x + int32(int64(tx-x)*off/d)
				pz := z + int32(int64(tz-z)*off/d)
				if pass == 0 && aikit.Dist2(px, pz, b.HomeX, b.HomeZ) < int64(minHome)*int64(minHome) {
					break
				}
				if !a.waitGround(b, s, px, pz) || a.nearFactory(b, px, pz) {
					continue
				}
				if !a.blobIn(b, s, px, pz, extent) {
					a.stats.passage++
					return px, pz
				}
			}
		}
	}
	return x, z
}

// waitGround reports whether the squad's main movement group can stand at
// (x, z) (always, without reach tables or members).
func (a *Army) waitGround(b *core.Board, s *squad, x, z int32) bool {
	if !a.reachReady {
		return true
	}
	gc, greg := int8(-1), uint16(0)
	if g := s.mainGroup(); g != nil {
		gc, greg = g.cls, g.reg
	} else {
		gc, greg = a.walkerHome(b)
	}
	return gc < 0 || a.hasRegion(gc, greg, b.K.Map.Sector(x, z))
}

// gatherOutOfPassage chooses the main gather point when the usual one
// (x, z) stands in a passage: the farthest of the candidates before the
// first in a passage, for the whole blob, then the half-radius blob, then
// its centre; then the fallback point (fx0, fz0) for any extent; then a
// walk off the line (outOfPassage). It returns the point and the extent
// it fits.
func (a *Army) gatherOutOfPassage(b *core.Board, s *squad, x, z, fx0, fz0 int32, cand [][2]int32, ex, ez int32) (int32, int32, int) {
	for extent := blobFull; extent >= blobCentre; extent-- {
		best := -1
		for i := range cand {
			if a.blobIn(b, s, cand[i][0], cand[i][1], extent) {
				break
			}
			best = i
		}
		if best >= 0 {
			a.stats.passage++
			return cand[best][0], cand[best][1], extent
		}
	}
	for extent := blobFull; extent >= blobCentre; extent-- {
		if (fx0 != x || fz0 != z) && !a.blobIn(b, s, fx0, fz0, extent) {
			a.stats.passage++
			return fx0, fz0, extent
		}
	}
	px, pz := a.outOfPassage(b, s, x, z, ex, ez, gatherMinHome)
	return px, pz, blobCentre
}

// noteWait counts, for Report, the thinks the main squad waits gathered at
// its gather point and those in which its blob's centre and half-radius
// ring stand in a passage by the rule — measured whether or not the rule
// moves the point (the cache it fills never changes a verdict).
func (a *Army) noteWait(b *core.Board) {
	s := &a.sq[sqMain]
	if !a.reachReady || s.state != stGather || !s.hasCentre || s.total.n < 3 ||
		aikit.Dist2(s.cx, s.cz, a.gx, a.gz) > 300*300 {
		return
	}
	a.stats.waits++
	p := a.P.Passage
	a.P.Passage = true
	if a.blobIn(b, s, s.cx, s.cz, blobHalf) {
		a.stats.waitPassage++
	}
	a.P.Passage = p
}
