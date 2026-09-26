package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Keeping factory exits open (unblock switch). Placement asks the executor
// for the exit guard (Kit.BuildKeep), so our own buildings never seal a
// factory. When a factory is sealed anyway — trees, rocks, wrecks, or
// buildings placed before its lane mattered — its units stay where they
// came out: a ground combat unit that has not moved 320 wu from where it
// was first seen after 90 s counts as stuck at the nearest factory. Two
// stuck units there (or a pad that stays blocked) send the nearest builder
// that can reclaim to open the exit (Kit.Unblock); the executor finds and
// reclaims the cheapest blockers, or leaves an open exit alone.

// trapTrack is where an own ground combat unit was first seen.
type trapTrack struct {
	def  *aikit.UnitInfo
	gen  uint32
	x, z int32
	tick uint32
	left bool
}

const (
	trapRadius   = 320  // world units a unit must move to count as having left
	trapTicks    = 2700 // 90 s
	trapFactoryR = 450  // a stuck unit is attributed to a factory this close to where it appeared
	trapMin      = 2    // stuck units that make a factory sealed
	unblockEvery = 1350 // ticks between attempts at one factory (45 s)
)

// trackOf returns the unit's track, starting it where the unit stands now
// when the slot is new or held another unit before.
func (s *shared) trackOf(u *aikit.OwnUnit) *trapTrack {
	s.traps = handleSlots(s.traps, u.H, s.hcap)
	t := &s.traps[u.H]
	if t.def != u.Info || t.gen != u.Gen {
		*t = trapTrack{def: u.Info, gen: u.Gen, x: u.X, z: u.Z, tick: s.tick}
	}
	return t
}

// observeTraps counts, per factory, the ground combat units stuck near it,
// and the ships that have not moved since they were launched.
func (s *shared) observeTraps(b *core.Board) {
	o := b.O
	for len(s.facTrapped) < len(b.Factories) {
		s.facTrapped = append(s.facTrapped, 0)
	}
	for j := range s.facTrapped {
		s.facTrapped[j] = 0
	}
	s.navalIdle = 0
	for _, i := range b.Combat {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		t := s.trackOf(u)
		if u.Info.Role.Has(aikit.RoleNaval) || s.info[u.Info.Index].water {
			if !t.left && aikit.Dist2(u.X, u.Z, t.x, t.z) > trapRadius*trapRadius {
				t.left = true
			}
			if !t.left && s.tick-t.tick >= trapTicks {
				s.navalIdle++
			}
			continue
		}
		if t.left {
			continue
		}
		if aikit.Dist2(u.X, u.Z, t.x, t.z) > trapRadius*trapRadius {
			t.left = true
			continue
		}
		if s.tick-t.tick < trapTicks {
			continue
		}
		best, bd := -1, int64(trapFactoryR)*trapFactoryR
		for j, fi := range b.Factories {
			f := &o.Own[fi]
			if d := aikit.Dist2(f.X, f.Z, t.x, t.z); d <= bd {
				best, bd = j, d
			}
		}
		if best >= 0 {
			s.facTrapped[best]++
		}
	}
}

// unblock sends builders to open sealed factory exits; it returns the
// actions used.
func (e *Economy) unblock(b *core.Board, budget int) int {
	s := e.s
	o := b.O
	used := 0
	for j, fi := range b.Factories {
		if used >= budget {
			break
		}
		f := &o.Own[fi]
		fs := s.fstateOf(f)
		sealed := (j < len(s.facTrapped) && s.facTrapped[j] >= trapMin) || fs.dead || (fs.blocked && fs.clears >= 2)
		if !sealed || (fs.lastUnblock != 0 && s.tick-fs.lastUnblock < unblockEvery) {
			continue
		}
		// The nearest builder that can reclaim; the commander only when no
		// constructor is available.
		pick, com := int32(-1), int32(-1)
		var pd int64
		for _, bi := range b.Builders {
			u := &o.Own[bi]
			if u.Info.Def == nil || !u.Info.Def.CanReclamate {
				continue
			}
			d := aikit.Dist2(u.X, u.Z, f.X, f.Z)
			if u.Info.Role.Has(aikit.RoleCommander) {
				com = bi
				continue
			}
			if c := s.commitOf(u); c.kind == cUnblock && s.tick-c.tick < unblockEvery {
				continue // already opening an exit
			}
			if pick < 0 || d < pd {
				pick, pd = bi, d
			}
		}
		if pick < 0 {
			pick = com
		}
		if pick < 0 {
			continue
		}
		u := &o.Own[pick]
		b.K.Unblock(u.H, f.H)
		fs.lastUnblock = s.tick
		if fs.unblocks < 250 {
			fs.unblocks++
		}
		*s.commitOf(u) = commitment{def: u.Info, gen: u.Gen, kind: cUnblock, spot: -1, x: f.X, z: f.Z, target: f.H, tick: s.tick}
		d := e.record(u)
		d.note = noteAssign
		d.top[0] = cand{kind: cUnblock, prod: f.Info, x: f.X, z: f.Z, target: f.H, score: 1, f: [4]int64{int64(s.facTrapped[j]), int64(fs.clears), int64(fs.unblocks), 0}}
		d.n, d.chosen = 1, 0
		used++
	}
	return used
}

// Reclaiming wrecks and clearing lanes (layout switch). The observation
// lists the features near the start (Obs.Features, refreshed by the
// executor): a blocking feature in a factory's front lane is cleared first;
// otherwise, while metal is needed, the richest reachable pile of wrecks is
// reclaimed. Kit.Clear reclaims up to four features around the point
// (blocking ones in factory lanes first, then those holding metal). A clear
// that finds nothing leaves its builder idle; clearing then rests a while.

const (
	clearRadius    = 400  // a clear reclaims within this of its point
	laneClearValue = 1500 // a tree in a factory's front lane
	clearRest      = 3600 // after a clear that found nothing (2 minutes)
	clearFullMetal = 600  // metal near a point at which clearing is fully wanted
	laneFront      = 12   // cells in front (+Z) of a factory that must stay open
)

// bestClear picks where a builder should clear: a blocking feature in an
// own factory's front lane (value laneClearValue), else the feature with
// the most metal around it per distance (value by that metal × need).
func (s *shared) bestClear(b *core.Board, u *aikit.OwnUnit) (int64, int32, int32) {
	o := b.O
	piles := s.piles(o)
	var bestV, bestSc int64
	var bx, bz int32
	lane := false // a lane feature was found: piles no longer compete
	for i := range o.Features {
		f := &o.Features[i]
		if !f.Reclaimable {
			continue
		}
		if f.Blocking && s.inLane(b, f) {
			sc := int64(laneClearValue)*1000 - int64(aikit.Dist(u.X, u.Z, f.X, f.Z))
			if !lane || sc > bestSc {
				lane = true
				bestV, bestSc, bx, bz = laneClearValue, sc, f.X, f.Z
			}
			continue
		}
		if f.Metal <= 0 || lane {
			continue
		}
		v := mul(lin(piles[i], 0, clearFullMetal), s.needM)
		sc := v * 1000 / (int64(aikit.Dist(u.X, u.Z, f.X, f.Z)) + 300)
		if v > 0 && sc > bestSc {
			bestV, bestSc, bx, bz = v, sc, f.X, f.Z
		}
	}
	return bestV, bx, bz
}

// pileSet is the reclaimable metal within clearRadius of each feature
// holding metal (a pile of wrecks), summed once per think: every builder
// that considers clearing reads the same features.
type pileSet struct {
	tick  uint32
	n     int     // len(Obs.Features) summed
	ok    bool    // sums are valid for (tick, n)
	sum   []int64 // per Obs.Features entry (features holding reclaimable metal)
	start []int32 // per clearRadius cell: first index into item (then one past the last cell)
	item  []int32 // Obs.Features indices of reclaimable metal, by cell
	fill  []int32 // scratch: next free item slot per cell
}

// piles returns the pile sums for this think's features. Features are
// bucketed by clearRadius cells so each sum reads only the 3×3 cells
// around its feature (anything within clearRadius lies there); the sums
// are the same as over every feature.
func (s *shared) piles(o *aikit.Obs) []int64 {
	ps := &s.pile
	if ps.ok && ps.tick == s.tick && ps.n == len(o.Features) {
		return ps.sum
	}
	ps.tick, ps.n, ps.ok = s.tick, len(o.Features), true
	ps.sum = ps.sum[:0]
	var x0, z0, x1, z1 int32
	first := true
	for i := range o.Features {
		g := &o.Features[i]
		ps.sum = append(ps.sum, 0)
		if !g.Reclaimable || g.Metal <= 0 {
			continue
		}
		if first {
			x0, z0, x1, z1, first = g.X, g.Z, g.X, g.Z, false
		}
		x0, z0, x1, z1 = min(x0, g.X), min(z0, g.Z), max(x1, g.X), max(z1, g.Z)
	}
	if first {
		return ps.sum
	}
	w, h := (x1-x0)/clearRadius+1, (z1-z0)/clearRadius+1
	cell := func(x, z int32) int32 { return (z-z0)/clearRadius*w + (x-x0)/clearRadius }
	ps.start = append(ps.start[:0], make([]int32, w*h+1)...)
	for i := range o.Features {
		if g := &o.Features[i]; g.Reclaimable && g.Metal > 0 {
			ps.start[cell(g.X, g.Z)+1]++
		}
	}
	for c := int32(1); c <= w*h; c++ {
		ps.start[c] += ps.start[c-1]
	}
	ps.item = append(ps.item[:0], make([]int32, ps.start[w*h])...)
	ps.fill = append(ps.fill[:0], ps.start[:w*h]...)
	for i := range o.Features {
		if g := &o.Features[i]; g.Reclaimable && g.Metal > 0 {
			c := cell(g.X, g.Z)
			ps.item[ps.fill[c]] = int32(i)
			ps.fill[c]++
		}
	}
	for i := range o.Features {
		f := &o.Features[i]
		if !f.Reclaimable || f.Metal <= 0 {
			continue
		}
		cx, cz := (f.X-x0)/clearRadius, (f.Z-z0)/clearRadius
		var pile int64
		for zz := max(cz-1, 0); zz <= min(cz+1, h-1); zz++ {
			for xx := max(cx-1, 0); xx <= min(cx+1, w-1); xx++ {
				c := zz*w + xx
				for _, j := range ps.item[ps.start[c]:ps.start[c+1]] {
					if g := &o.Features[j]; aikit.Dist2(g.X, g.Z, f.X, f.Z) <= clearRadius*clearRadius {
						pile += int64(g.Metal)
					}
				}
			}
		}
		ps.sum[i] = pile
	}
	return ps.sum
}

// inLane reports whether a feature lies in an own factory's front lane.
func (s *shared) inLane(b *core.Board, f *aikit.Feature) bool {
	o := b.O
	for _, fi := range b.Factories {
		u := &o.Own[fi]
		fx, fz := u.Info.FootX, u.Info.FootZ
		x0 := (u.X/16 - fx/2 - 1) * 16
		x1 := (u.X/16 - fx/2 + fx + 1) * 16
		z0 := (u.Z/16 - fz/2 + fz) * 16
		z1 := z0 + laneFront*16
		hx, hz := f.FootX*8, f.FootZ*8
		if f.X+hx > x0 && f.X-hx < x1 && f.Z+hz > z0 && f.Z-hz < z1 {
			return true
		}
	}
	return false
}

// evalClear scores clearing a factory lane or reclaiming wrecks.
func (e *Economy) evalClear(b *core.Board, u *aikit.OwnUnit, d *decision) {
	s := e.s
	if u.Info.Def == nil || !u.Info.Def.CanReclamate || s.tick < s.clearRest {
		return
	}
	v, x, z := s.bestClear(b, u)
	if v == 0 {
		return
	}
	dist := int64(aikit.Dist(u.X, u.Z, x, z))
	travel := half(dist/speedOf(u.Info), int64(s.p.TravelHalf))
	c := cand{kind: cClear, spot: -1, x: x, z: z, spacing: clearRadius}
	c.score = mul(mul(int64(s.p.WReclaim)*10, v), travel)
	c.f = [4]int64{v, s.needM, travel, 0}
	d.offer(&c)
}

// clearFailures notes clears issued last think that left their builder
// idle: nothing was there to reclaim. Only a clear the brain chose rests
// clearing; one sent to a failed extractor spot (spot set) says nothing
// about the wrecks elsewhere.
func (e *Economy) clearFailures(b *core.Board) {
	s := e.s
	o := b.O
	for _, i := range b.Builders {
		u := &o.Own[i]
		c := s.commitIf(u)
		if c == nil || c.kind != cClear || c.tick != s.lastThink || c.tick == 0 || u.Order != aikit.OrderIdle {
			continue
		}
		if c.spot < 0 {
			s.clearRest = s.tick + clearRest
		}
		c.kind = cNone
	}
}
