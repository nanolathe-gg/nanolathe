package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// The terrain model: which of our units can get where. It is built once in
// setup (the policies' Init runs on the simulation thread) from the map's
// public terrain, so a think only looks values up.

// reachWindow is how far around a start position a class's home region is
// looked for (world units): a ship reaching water that close can shell it.
const reachWindow = 1500

// spotWorkR is how close an anchor must be to a metal spot's centre for a
// builder standing there to build on it.
const spotWorkR = 112

// reachCls is one movement profile: its region map, the region its units
// start in, how close that region comes to each start position, and which
// regions touch each metal spot.
type reachCls struct {
	mc      aikit.MoveClass
	r       *aikit.Reach
	naval   bool    // needs water: its units come out of a shipyard at the naval anchor
	home    int32   // region at home (naval: at the naval anchor)
	dist    []int32 // per start: distance from the home region, -1 beyond reachWindow
	spotReg []int32 // per spot: two regions from which it can be built on
	siteOK  []bool  // per definition: its water-site anchor is workable from home
}

// terrain is the static reading of the map.
type terrain struct {
	ready bool
	cls   []reachCls
	own   int32 // our start index, -1 when none is near home
	// reachAt[start][def]: how well a combat product reaches that start
	// (permille); reachAvg is the mean over the other starts.
	reachAt  [][]int16
	reachAvg []int16
	// Water building anchors (per definition).
	siteX, siteZ []int32
	siteOK       []bool
	navX, navZ   int32 // the naval base: nearest good shipyard site
	navOK        bool
	navCls       int32 // class of the shipyard's ships, -1 when none
	// landRoom: a land factory (and its lanes) fits near home.
	landRoom []bool
	homeLand int32 // dry flat cells within 800 wu of home
	inTree   []bool
	scratch  []aikit.RegionSite
}

// canonClass caps a class's footprint (3 cells on the ground, 4 at sea):
// larger units are few, and the island question is about depth and slope,
// so the cap keeps the analysis to about a dozen flood fills while a 3×3
// hovercraft is not credited with passages only a 2×2 tank fits through.
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

func (t *terrain) classOf(m *aikit.MapInfo, mc aikit.MoveClass) int32 {
	mc = canonClass(mc)
	for i := range t.cls {
		if t.cls[i].mc == mc {
			return int32(i)
		}
	}
	t.cls = append(t.cls, reachCls{mc: mc, r: m.Reach(mc), naval: mc.MinDepth > 0})
	return int32(len(t.cls) - 1)
}

// analyzeTerrain fills s.terr and the per-definition class index. It runs
// once, in setup, only when a switch that needs it is on.
func (s *shared) analyzeTerrain(k *aikit.Kit, com *aikit.UnitInfo) {
	t := &s.terr
	m := k.Map
	tab := k.Table
	n := len(tab.Units)
	t.inTree = make([]bool, n)
	for i := range s.info {
		s.info[i].cls = -1
	}
	if com == nil || m == nil || m.CellW <= 0 {
		return
	}
	// Our side's build tree.
	queue := []*aikit.UnitInfo{com}
	t.inTree[com.Index] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, p := range u.Builds {
			if !t.inTree[p.Index] {
				t.inTree[p.Index] = true
				queue = append(queue, p)
			}
		}
	}
	for i, u := range tab.Units {
		if !t.inTree[i] {
			continue
		}
		if mc, ok := aikit.MoveClassOf(u); ok {
			s.info[i].cls = int8(t.classOf(m, mc))
		}
	}
	hx, hz := m.HomeX, m.HomeZ
	t.own = -1
	for j, st := range m.Starts {
		if aikit.Dist2(st[0], st[1], hx, hz) < 400*400 {
			t.own = int32(j)
		}
	}
	t.homeLand = m.CountCells(hx, hz, 800, -10000, 0, 10)

	// The naval base: the shipyard site nearest home in a sea that is large
	// or reaches another start.
	t.navCls = -1
	var yard *aikit.UnitInfo
	for _, p := range com.Builds {
		if p.Role.Has(aikit.RoleFactory) && s.info[p.Index].water {
			yard = p
			break
		}
	}
	if yard != nil {
		for _, q := range yard.Builds {
			if c := s.info[q.Index].cls; c >= 0 && t.cls[c].naval {
				t.navCls = int32(c)
				break
			}
		}
	}
	if t.navCls >= 0 {
		nc := &t.cls[t.navCls]
		d := yard.Def
		t.scratch = m.DepthSites(hx, hz, yard.FootX, yard.FootZ, d.MinWaterDepth, d.MaxWaterDepth, 255, 3000, nc.r, t.scratch[:0])
		best := int64(-1)
		for _, rs := range t.scratch {
			pen := int64(4000)
			if nc.r.Size(rs.Region) >= 300 {
				for j, st := range m.Starts {
					if int32(j) != t.own && m.MaybeEnemyStart(j) && nc.r.Dist(rs.Region, st[0], st[1], reachWindow) >= 0 {
						pen = 0
						break
					}
				}
			}
			if sc := int64(rs.Dist) + pen; best < 0 || sc < best {
				best, t.navX, t.navZ = sc, rs.X, rs.Z
			}
		}
		t.navOK = best >= 0
	}

	// Home regions and distances to every start.
	for c := range t.cls {
		rc := &t.cls[c]
		switch {
		case rc.naval && t.navOK:
			rc.home, _ = rc.r.Nearest(t.navX, t.navZ, 400)
		case rc.naval:
			rc.home = 0
		default:
			rc.home, _ = rc.r.Nearest(hx, hz, 600)
		}
		rc.dist = make([]int32, len(m.Starts))
		for j, st := range m.Starts {
			rc.dist[j] = rc.r.Dist(rc.home, st[0], st[1], reachWindow)
		}
		rc.spotReg = make([]int32, 2*len(m.Spots))
		for i := range m.Spots {
			sp := &m.Spots[i]
			rc.spotReg[2*i], rc.spotReg[2*i+1] = rc.r.Near2(sp.X, sp.Z, spotWorkR)
		}
		rc.siteOK = make([]bool, n)
	}

	// Water building anchors, and land room for factories.
	t.siteX = make([]int32, n)
	t.siteZ = make([]int32, n)
	t.siteOK = make([]bool, n)
	t.landRoom = make([]bool, n)
	for i, u := range tab.Units {
		if !t.inTree[i] || u.Role.Has(aikit.RoleMobile) || u.Def == nil {
			continue
		}
		si := &s.info[i]
		d := u.Def
		if !si.water {
			if u.Role.Has(aikit.RoleFactory) {
				// A factory and the lanes kept around it.
				_, _, t.landRoom[i] = m.DepthSite(hx, hz, u.FootX+2, u.FootZ+2, -10000, d.MaxWaterDepth, d.MaxSlope, 1200, nil, 0)
			}
			continue
		}
		if u.Role.Has(aikit.RoleExtractor) {
			continue // extractors go on spots
		}
		cx, cz := hx, hz
		if t.navOK {
			cx, cz = t.navX, t.navZ
		}
		lo, hi := waterBand(u)
		if u.Role.Has(aikit.RoleFactory) && t.navCls >= 0 {
			nc := &t.cls[t.navCls]
			t.siteX[i], t.siteZ[i], t.siteOK[i] = m.DepthSite(cx, cz, u.FootX, u.FootZ, lo, hi, 255, 2500, nc.r, nc.home)
		} else {
			t.siteX[i], t.siteZ[i], t.siteOK[i] = m.DepthSite(cx, cz, u.FootX, u.FootZ, lo, hi, 255, 2500, nil, 0)
		}
		if !t.siteOK[i] {
			continue
		}
		reach := int32(96 + 8*max32(u.FootX, u.FootZ))
		for c := range t.cls {
			rc := &t.cls[c]
			rc.siteOK[i] = rc.home != 0 && rc.r.Dist(rc.home, t.siteX[i], t.siteZ[i], reach) >= 0
		}
	}

	// How well each combat product reaches each start.
	ns := len(m.Starts)
	t.reachAt = make([][]int16, ns)
	for j := range t.reachAt {
		t.reachAt[j] = make([]int16, n)
	}
	t.reachAvg = make([]int16, n)
	for i, u := range tab.Units {
		if !t.inTree[i] || !u.Role.Has(aikit.RoleMobile) || u.DPS <= 0 {
			continue
		}
		var sum, cnt int64
		for j := 0; j < ns; j++ {
			v := int64(one)
			if c := s.info[i].cls; c >= 0 {
				rc := &t.cls[c]
				sl := int64(max32(u.Range, 150))
				dd := int64(rc.dist[j])
				switch {
				case rc.home == 0 || dd < 0:
					v = 0
				case dd <= sl+100:
					v = one
				default:
					v = lin(dd, sl+700, sl+100)
				}
			}
			t.reachAt[j][i] = int16(v)
			if int32(j) != t.own {
				sum += v
				cnt++
			}
		}
		if cnt > 0 {
			t.reachAvg[i] = int16(sum / cnt)
		} else {
			t.reachAvg[i] = one
		}
	}
	t.ready = true
}

// waterBand is the water depth band a water building's placement actually
// enforces. Yard cells that sample the floor (o, c, y, O, f, G) bound it by
// the authored maximum depth; cells that only bound the floor from above
// (w, C, Y — how floating structures are authored) leave the maximum
// unchecked but must lie below the waterline as well as the minimum depth
// [04 §6.4].
func waterBand(u *aikit.UnitInfo) (int32, int32) {
	d := u.Def
	lo, hi := d.MinWaterDepth, int32(10000)
	sampled, floats := false, false
	for i := 0; i < len(d.YardMap); i++ {
		switch d.YardMap[i] {
		case 'o', 'c', 'y', 'O', 'f', 'G':
			sampled = true
		case 'w', 'C', 'Y':
			floats = true
		}
	}
	if sampled || d.YardMap == "" {
		hi = d.MaxWaterDepth
	}
	if floats && !sampled && d.Waterline > lo {
		lo = d.Waterline
	}
	if hi < lo {
		hi = 10000
	}
	return lo, hi
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// selectReach picks the reach table for the enemy base as currently
// believed: the start nearest seen enemy buildings, or the mean over the
// other starts while none is known.
func (s *shared) selectReach(b *core.Board) {
	t := &s.terr
	if !t.ready {
		return
	}
	sel := int32(-1)
	if b.EnemyKnown {
		m := b.K.Map
		best := int64(-1)
		for j, st := range m.Starts {
			if int32(j) == t.own || (s.p.ReachMix != 0 && s.nearHome(j)) || !m.MaybeEnemyStart(j) {
				continue
			}
			if d := aikit.Dist2(st[0], st[1], b.EnemyX, b.EnemyZ); d < 2000*2000 && (best < 0 || d < best) {
				best, sel = d, int32(j)
			}
		}
	}
	if sel < 0 && s.p.ReachMix != 0 {
		s.selectReachMix(b)
		return
	}
	if sel == s.reachSel && s.reach != nil {
		return
	}
	s.reachSel = sel
	if sel < 0 {
		s.reach = t.reachAvg
	} else {
		s.reach = t.reachAt[sel]
	}
}

// reachOf is how well a unit reaches the enemy base (permille); 1000 when
// the terrain model is off.
func (s *shared) reachOf(u *aikit.UnitInfo) int64 {
	if s.reach == nil {
		return one
	}
	return int64(s.reach[u.Index])
}

// builderPlace returns a builder's movement class (-1: flies, goes
// anywhere, or the naval switch is off) and the region it stands in.
func (s *shared) builderPlace(u *aikit.OwnUnit) (int32, int32) {
	c := int32(s.info[u.Info.Index].cls)
	if c < 0 || !s.terr.ready || s.p.Naval == 0 {
		return -1, 0
	}
	rc := &s.terr.cls[c]
	reg := rc.r.At(u.X, u.Z)
	if reg == 0 {
		reg = rc.home
	}
	return c, reg
}

// canWorkSpot reports whether a builder in region reg of class c can build
// on metal spot i.
func (s *shared) canWorkSpot(c, reg, i int32) bool {
	if c < 0 {
		return true
	}
	rc := &s.terr.cls[c]
	return rc.spotReg[2*i] == reg || rc.spotReg[2*i+1] == reg
}

// canWorkSite reports whether a builder of class c in region reg can build
// product p at its site: water products at their anchor (from the class's
// home region), land products only by a class that stands on land.
func (s *shared) canWorkSite(c, reg int32, p *aikit.UnitInfo) bool {
	if c < 0 {
		return !s.info[p.Index].water || s.terr.siteOK[p.Index]
	}
	rc := &s.terr.cls[c]
	if s.info[p.Index].water {
		if s.p.TidalField != 0 && p.Role.Has(aikit.RoleFactory) && !s.yardWorkable(c, reg) {
			return false // the next yard's ring site is out of this class's reach
		}
		return s.terr.siteOK[p.Index] && reg == rc.home && rc.siteOK[p.Index]
	}
	return rc.mc.MinDepth <= 0 && reg == rc.home
}

// fitsSpot reports whether extractor p can stand on spot sp: its authored
// water depth band must contain the spot's floor.
func fitsSpot(m *aikit.MapInfo, p *aikit.UnitInfo, sp *aikit.MetalSpot) bool {
	d := p.Def
	if d == nil {
		return !sp.Water
	}
	return m.SeaLevel-sp.Hi >= d.MinWaterDepth && m.SeaLevel-sp.Lo <= d.MaxWaterDepth
}
