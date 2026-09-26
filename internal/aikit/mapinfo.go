package aikit

import (
	"reflect"
	"slices"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// SectorCells is the edge of one coarse planning sector in terrain cells
// (16 world units each): 8 cells = 128 world units.
const SectorCells = 8

// SectorWorld is a sector edge in world units.
const SectorWorld = SectorCells * 16

// MetalSpot is an extractor site found in the terrain's metal layer.
type MetalSpot struct {
	CellX, CellZ int32 // footprint anchor (north-west cell)
	X, Z         int32 // footprint centre, world units
	Metal        int32 // Σ metal bytes under the footprint
	Water        bool
	// Lo and Hi are the lowest floor minimum and the highest floor maximum
	// under the footprint (height bytes): with SeaLevel they give the water
	// depth band an extractor there must accept.
	Lo, Hi int32
}

// MapInfo is static map knowledge: what any player can learn from the map
// file before the battle starts. Each host has its own, because the spot
// order and the home point depend on where the player starts; the parts
// that do not — the terrain snapshot, the extractor candidates and the
// region maps — are computed once per battle and shared (mapShared).
type MapInfo struct {
	CellW, CellH     int32
	WorldW, WorldH   int32
	SectorW, SectorH int32
	SeaLevel         int32 // height-byte units
	WaterFraction    int32 // percent of cells below sea level
	Spots            []MetalSpot
	UniformMetal     bool // metal is spread evenly (a "metal map"): extract anywhere
	Starts           [][2]int32
	// StartEnemy is, per Starts entry, whether an opponent began the battle
	// there. Nil when the start assignment is not public (random starts, a
	// restored battle, a campaign): then any start may hold one.
	StartEnemy   []bool
	FootX, FootZ int32 // extractor footprint the spots were computed for
	// Authored wind speed range and tidal strength (public map data).
	WindMin, WindMax int32
	TidalPermille    int32
	// HomeX, HomeZ is the observer's own start (its commander at analysis).
	HomeX, HomeZ int32

	// Per plot cell: derived floor minimum and maximum height bytes and
	// whether the cell is a void hole no unit enters. The terrain's shape is
	// public map data; Reach and DepthSite read it. The slices may be shared
	// by every host of a battle and are never written after the analysis.
	cellLo, cellHi []uint8
	cellVoid       []bool
	reaches        []*Reach
	runScratch     []uint16 // Reach construction scratch, reused across classes
	depthScratch   []int32  // DepthSite/DepthSites summed-area table (sumScratch)
	colScratch     []int32
	// shared is the battle's analysis these cells came from, which also
	// holds the region maps every host asks for; nil when this analysis is
	// the host's own.
	shared *mapShared
}

// MaybeEnemyStart reports whether start i may hold an opponent's base as far
// as the public start assignment tells: any start when the assignment is
// unknown, otherwise only a start an opponent took.
func (m *MapInfo) MaybeEnemyStart(i int) bool {
	return m.StartEnemy == nil || (i < len(m.StartEnemy) && m.StartEnemy[i])
}

// mapBase is the part of the analysis that depends on the terrain alone:
// the grid's scalars, the floor band and void cells, and the metal
// summed-area table the extractor candidates are cut from.
type mapBase struct {
	t                *world.Terrain
	cellW, cellH     int32
	seaLevel         int32
	waterFraction    int32
	windMin, windMax int32
	tidalPermille    int32
	cellLo, cellHi   []uint8
	cellVoid         []bool
	sat              []int32 // (w+1)×(h+1) summed metal bytes
}

// siteCand is one extractor candidate: a footprint anchor (x, z) and the
// metal under it.
type siteCand struct{ x, z, s int32 }

// mapShared is one battle's map analysis, kept in the managers' shared
// slot (ai.Manager.Shared) so that every computer player's preparation uses
// one analysis instead of repeating it: the terrain snapshot, the extractor
// candidates per footprint and the region map per movement class. Each part
// is a pure function of the terrain and its key, so whichever host's
// preparation computes it, every host gets the answer it would have
// computed alone. The preparations run on their own goroutines, so each
// part is built under its own sync.Once.
type mapShared struct {
	baseOnce sync.Once
	base     *mapBase

	mu     sync.Mutex
	sites  []*siteSlot
	reach  []*reachSlot
	tables []sharedTable // unit tables by catalog and build rules (tableFor)
}

// sharedTable is a unit table and what it summarizes.
type sharedTable struct {
	cat   *content.Catalog
	rules construction.Rules
	t     *Table
}

// tableFor returns the unit table for the manager's catalog and build
// rules. Every computer player of a battle summarizes the same catalog
// under the same rules, and a Table is never written once built, so the
// battle builds it once (on the simulation thread, at the first host's
// begin) and its hosts share it. Every host begins on the battle's first
// tick, where building it per host cost a quarter of a millisecond for
// each computer player. A manager without the shared slot (a fixture), or
// rules that cannot be compared, gets a table of its own.
func tableFor(m *ai.Manager) *Table {
	sh := sharedAnalysis(m)
	if sh == nil || !comparableRules(m.ConstructionRules) {
		return BuildTable(m.Catalog, m.ConstructionRules)
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	for _, e := range sh.tables {
		if e.cat == m.Catalog && reflect.TypeOf(e.rules) == reflect.TypeOf(m.ConstructionRules) && e.rules == m.ConstructionRules {
			return e.t
		}
	}
	t := BuildTable(m.Catalog, m.ConstructionRules)
	sh.tables = append(sh.tables, sharedTable{cat: m.Catalog, rules: m.ConstructionRules, t: t})
	return t
}

// comparableRules reports whether rules can be compared with ==: nil, or
// a value whose dynamic type is comparable.
func comparableRules(r construction.Rules) bool {
	return r == nil || reflect.TypeOf(r).Comparable()
}

type siteSlot struct {
	fx, fz int32
	once   sync.Once
	list   []siteCand
}

type reachSlot struct {
	class MoveClass
	once  sync.Once
	r     *Reach
}

// sharedAnalysis returns the battle's map analysis from the manager's
// shared slot, creating it on first use; nil for a manager without a slot
// (a fixture), whose host then analyzes the map alone.
func sharedAnalysis(m *ai.Manager) *mapShared {
	if m == nil || m.Shared == nil {
		return nil
	}
	sh, _ := m.Shared.Value(func() any { return &mapShared{} }).(*mapShared)
	return sh
}

// baseFor returns the shared terrain snapshot, or nil when it was taken
// from another terrain or other void cells than this host read. Void cells
// come from the feature word a battle rewrites, so a host that began on a
// later tick than the first could in principle have read different ones;
// it then analyzes the map alone rather than take an answer that is not its
// own.
func (sh *mapShared) baseFor(t *world.Terrain, void []bool) *mapBase {
	sh.baseOnce.Do(func() { sh.base = newMapBase(t, void) })
	if b := sh.base; b.t == t && slices.Equal(b.cellVoid, void) {
		return b
	}
	return nil
}

// siteList returns the extractor candidates for a footprint, computing them
// once per battle.
func (sh *mapShared) siteList(b *mapBase, fx, fz int32) []siteCand {
	sh.mu.Lock()
	var slot *siteSlot
	for _, s := range sh.sites {
		if s.fx == fx && s.fz == fz {
			slot = s
			break
		}
	}
	if slot == nil {
		slot = &siteSlot{fx: fx, fz: fz}
		sh.sites = append(sh.sites, slot)
	}
	sh.mu.Unlock()
	slot.once.Do(func() { slot.list = b.siteCands(fx, fz) })
	return slot.list
}

// reachOf returns a movement class's region map, building it once per
// battle with the asking host's scratch.
func (sh *mapShared) reachOf(m *MapInfo, c MoveClass) *Reach {
	sh.mu.Lock()
	var slot *reachSlot
	for _, s := range sh.reach {
		if s.class == c {
			slot = s
			break
		}
	}
	if slot == nil {
		slot = &reachSlot{class: c}
		sh.reach = append(sh.reach, slot)
	}
	sh.mu.Unlock()
	slot.once.Do(func() { slot.r = m.buildReach(c) })
	return slot.r
}

// Sector returns the sector index of a world point, clamped to the grid.
func (m *MapInfo) Sector(x, z int32) int32 {
	sx, sz := x/SectorWorld, z/SectorWorld
	if sx < 0 {
		sx = 0
	}
	if sz < 0 {
		sz = 0
	}
	if sx >= m.SectorW {
		sx = m.SectorW - 1
	}
	if sz >= m.SectorH {
		sz = m.SectorH - 1
	}
	return sz*m.SectorW + sx
}

// SectorCentre returns the world centre of sector s.
func (m *MapInfo) SectorCentre(s int32) (int32, int32) {
	sx, sz := s%m.SectorW, s/m.SectorW
	return sx*SectorWorld + SectorWorld/2, sz*SectorWorld + SectorWorld/2
}

// maxSpots bounds the spot list; a metal map offers thousands of equal sites.
const maxSpots = 384

// Plot cell bytes the analysis reads, by offset in world.PlotCell
// [02 "Terrain file"]. The height, floor band and metal bytes are written at
// map load and never during a battle, so a preparation running beside the
// simulation (Host) reads them one byte at a time and never touches the
// bytes a battle rewrites (occupancy, the feature word, flags). The
// PlotCell accessors take the whole cell by value and would read those.
const (
	plotHeight   = 4
	plotFloorMax = 5
	plotFloorMin = 6
	plotMetal    = 7
	plotFeature  = 8 // two bytes, little-endian
)

// terrainVoid reports, per plot cell, whether it is a void hole no unit
// enters: the feature word's void band, 0xFFFB to 0xFFFD, which movement
// blocks as a whole (world.PlotCell.IsVoid) — the engine's voids and a
// map's own sentinels alike. The feature word is rewritten during a battle,
// so this runs on the simulation thread.
func terrainVoid(t *world.Terrain) []bool {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return nil
	}
	n := int(t.CellW * t.CellH)
	void := make([]bool, n)
	plot := t.Plot
	if len(plot) < n {
		n = len(plot)
	}
	for i := 0; i < n; i++ {
		c := &plot[i]
		f := uint16(c[plotFeature]) | uint16(c[plotFeature+1])<<8
		void[i] = f >= 0xFFFB && f <= world.PlotFeatureVoid
	}
	return void
}

// AnalyzeMap reads the terrain once. footX/footZ is the extractor footprint;
// (ownX, ownZ) orders equal-value spots nearest first.
func AnalyzeMap(t *world.Terrain, starts [][2]int32, footX, footZ, ownX, ownZ int32) *MapInfo {
	return analyzeMap(nil, t, terrainVoid(t), starts, footX, footZ, ownX, ownZ)
}

// analyzeMap is AnalyzeMap with the void cells already read (terrainVoid):
// it reads only the plot bytes fixed at map load, so it may run beside the
// simulation. With a battle's shared analysis (sh) it takes the terrain
// snapshot, the extractor candidates and later the region maps from there
// and computes only what depends on this player's start: the spot order and
// the home point. The result is the same either way.
func analyzeMap(sh *mapShared, t *world.Terrain, void []bool, starts [][2]int32, footX, footZ, ownX, ownZ int32) *MapInfo {
	m := &MapInfo{Starts: starts, FootX: footX, FootZ: footZ, HomeX: ownX, HomeZ: ownZ}
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return m
	}
	var b *mapBase
	if sh != nil {
		b = sh.baseFor(t, void)
	}
	if b == nil {
		sh = nil
		b = newMapBase(t, void)
	}
	w, h := b.cellW, b.cellH
	m.CellW, m.CellH = w, h
	m.WorldW, m.WorldH = w*16, h*16
	m.SectorW = (w + SectorCells - 1) / SectorCells
	m.SectorH = (h + SectorCells - 1) / SectorCells
	m.SeaLevel = b.seaLevel
	m.WindMin, m.WindMax = b.windMin, b.windMax
	m.TidalPermille = b.tidalPermille
	m.WaterFraction = b.waterFraction
	m.cellLo, m.cellHi, m.cellVoid = b.cellLo, b.cellHi, b.cellVoid
	m.shared = sh
	if footX <= 0 {
		footX = 2
	}
	if footZ <= 0 {
		footZ = 2
	}
	var sites []siteCand
	if sh != nil {
		sites = sh.siteList(b, footX, footZ)
	} else {
		sites = b.siteCands(footX, footZ)
	}

	// Greedy non-overlap acceptance in value order (richest, then nearest
	// the owner, then north, then west: a total order). The acceptance stops
	// after maxSpots sites while a metal map offers hundreds of thousands of
	// candidates, so they are drawn from a heap rather than sorted. Distance
	// to the owner is the one start-dependent key, so the heap is this
	// player's own.
	type cand struct {
		x, z, s int32
		d       int64
	}
	cands := make([]cand, len(sites))
	for i, c := range sites {
		cx, cz := c.x*16+footX*8, c.z*16+footZ*8
		dx, dz := int64(cx-ownX), int64(cz-ownZ)
		cands[i] = cand{c.x, c.z, c.s, dx*dx + dz*dz}
	}
	before := func(a, b *cand) bool {
		if a.s != b.s {
			return a.s > b.s
		}
		if a.d != b.d {
			return a.d < b.d
		}
		if a.z != b.z {
			return a.z < b.z
		}
		return a.x < b.x
	}
	sift := func(i, n int) {
		for {
			l := 2*i + 1
			if l >= n {
				return
			}
			c := l
			if r := l + 1; r < n && before(&cands[r], &cands[l]) {
				c = r
			}
			if !before(&cands[c], &cands[i]) {
				return
			}
			cands[i], cands[c] = cands[c], cands[i]
			i = c
		}
	}
	n := len(cands)
	for i := n/2 - 1; i >= 0; i-- {
		sift(i, n)
	}
	plot := t.Plot
	for n > 0 && len(m.Spots) < maxSpots {
		c := cands[0]
		n--
		cands[0] = cands[n]
		sift(0, n)
		overlap := false
		for i := range m.Spots {
			sp := &m.Spots[i]
			if absI32(sp.CellX-c.x) < footX+1 && absI32(sp.CellZ-c.z) < footZ+1 {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		ci := (c.z+footZ/2)*w + c.x + footX/2
		wet := int(ci) < len(plot) && int32(plot[ci][plotHeight]) < m.SeaLevel
		lo, hi := m.band(c.x, c.z, footX, footZ)
		m.Spots = append(m.Spots, MetalSpot{CellX: c.x, CellZ: c.z, X: c.x*16 + footX*8, Z: c.z*16 + footZ*8, Metal: c.s, Water: wet, Lo: lo, Hi: hi})
	}
	// A metal map: the median accepted site is as rich as the best one.
	if n := len(m.Spots); n >= 64 && m.Spots[n/2].Metal*10 >= m.Spots[0].Metal*9 {
		m.UniformMetal = true
	}
	return m
}

// newMapBase reads the terrain's fixed bytes: the per-cell floor band, the
// water census and the summed-area table of metal bytes, (w+1)×(h+1). The
// void cells are the host's own read (terrainVoid), copied.
func newMapBase(t *world.Terrain, void []bool) *mapBase {
	b := &mapBase{t: t}
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return b
	}
	w, h := t.CellW, t.CellH
	b.cellW, b.cellH = w, h
	b.seaLevel = int32(t.SeaLevel)
	b.windMin, b.windMax = t.WindMin, t.WindMax
	b.tidalPermille = int32(t.Tidal * 1000)
	sat := make([]int32, (w+1)*(h+1))
	b.cellLo = make([]uint8, w*h)
	b.cellHi = make([]uint8, w*h)
	b.cellVoid = make([]bool, w*h)
	copy(b.cellVoid, void)
	plot := t.Plot
	var water int64
	for z := int32(0); z < h; z++ {
		var row int32
		for x := int32(0); x < w; x++ {
			i := z*w + x
			var metal int32
			if int(i) < len(plot) {
				c := &plot[i]
				metal = int32(c[plotMetal])
				if int32(c[plotHeight]) < b.seaLevel {
					water++
				}
				b.cellLo[i], b.cellHi[i] = c[plotFloorMin], c[plotFloorMax]
			}
			row += metal
			sat[(z+1)*(w+1)+x+1] = sat[z*(w+1)+x+1] + row
		}
	}
	b.waterFraction = int32(water * 100 / int64(w*h))
	b.sat = sat
	return b
}

// siteCands lists the extractor candidates for a footprint in row-major
// order: every anchor whose footprint metal is a local maximum over its
// eight neighbours (on a plateau, only the footprint-aligned lattice, so
// equal sites tile instead of piling up) and at least a sixth of the
// richest — weaker sites are not worth an extractor.
func (b *mapBase) siteCands(footX, footZ int32) []siteCand {
	w, h := b.cellW, b.cellH
	if w <= 0 || h <= 0 {
		return nil
	}
	sat := b.sat
	// fs holds the metal under the footprint anchored at every cell that can
	// hold one, so the neighbourhood test below reads nine sums, not 36 table
	// entries.
	sw, sh := w-footX, h-footZ
	if sw < 0 {
		sw = 0
	}
	if sh < 0 {
		sh = 0
	}
	fs := make([]int32, sw*sh)
	for z := int32(0); z < sh; z++ {
		r0, r1 := z*(w+1), (z+footZ)*(w+1)
		for x := int32(0); x < sw; x++ {
			fs[z*sw+x] = sat[r1+x+footX] - sat[r0+x+footX] - sat[r1+x] + sat[r0+x]
		}
	}
	var cands []siteCand
	var best int32
	for z := int32(1); z+footZ < h-1; z++ {
		up, row, down := fs[(z-1)*sw:], fs[z*sw:], fs[(z+1)*sw:]
		for x := int32(1); x+footX < w-1; x++ {
			s := row[x]
			if s <= 0 {
				continue
			}
			if s < row[x-1] || s < row[x+1] || s < up[x] || s < down[x] ||
				s < up[x-1] || s < down[x+1] || s < down[x-1] || s < up[x+1] {
				continue
			}
			if s == row[x-1] && s == up[x] && (x%footX != 0 || z%footZ != 0) {
				continue
			}
			cands = append(cands, siteCand{x, z, s})
			if s > best {
				best = s
			}
		}
	}
	floor := best / 6
	keep := cands[:0]
	for _, c := range cands {
		if c.s >= floor {
			keep = append(keep, c)
		}
	}
	return slices.Clip(keep)
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// Dist2 is the squared planar distance between two world points.
func Dist2(ax, az, bx, bz int32) int64 {
	dx, dz := int64(ax-bx), int64(az-bz)
	return dx*dx + dz*dz
}

// ISqrt64 is an integer square root.
func ISqrt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	var root int64
	bit := int64(1) << 62
	for bit > v {
		bit >>= 2
	}
	for bit != 0 {
		if v >= root+bit {
			v -= root + bit
			root = (root >> 1) + bit
		} else {
			root >>= 1
		}
		bit >>= 2
	}
	return root
}

// Dist is the planar distance, world units.
func Dist(ax, az, bx, bz int32) int32 { return int32(ISqrt64(Dist2(ax, az, bx, bz))) }

// band returns the lowest floor minimum and highest floor maximum over a
// footprint anchored at cell (cx, cz).
func (m *MapInfo) band(cx, cz, fx, fz int32) (lo, hi int32) {
	lo, hi = 255, 0
	for z := cz; z < cz+fz && z < m.CellH; z++ {
		for x := cx; x < cx+fx && x < m.CellW; x++ {
			if x < 0 || z < 0 {
				continue
			}
			i := z*m.CellW + x
			if v := int32(m.cellLo[i]); v < lo {
				lo = v
			}
			if v := int32(m.cellHi[i]); v > hi {
				hi = v
			}
		}
	}
	return lo, hi
}

// MoveClass is a mobile unit's terrain limits, in the terms the placement
// validator judges one mobile cell by: too deep, too shallow, too steep on
// land or under water. Depth is sea level minus a floor height, so land is
// depth ≤ 0.
type MoveClass struct {
	MinDepth, MaxDepth      int32
	MaxSlope, MaxWaterSlope int32
	FootX, FootZ            int32
}

// MoveClassOf returns the class of a mobile surface unit. Aircraft and
// buildings have none.
func MoveClassOf(u *UnitInfo) (MoveClass, bool) {
	if u == nil || u.Def == nil || !u.Role.Has(RoleMobile) || u.Def.CanFly {
		return MoveClass{}, false
	}
	d := u.Def
	c := MoveClass{MinDepth: d.MinWaterDepth, MaxDepth: d.MaxWaterDepth, MaxSlope: d.MaxSlope, MaxWaterSlope: d.MaxWaterSlope, FootX: u.FootX, FootZ: u.FootZ}
	if c.FootX < 1 {
		c.FootX = 1
	}
	if c.FootZ < 1 {
		c.FootZ = 1
	}
	return c, true
}

// cellLegal is the per-cell mobile terrain test for a class.
func (m *MapInfo) cellLegal(i int32, c *MoveClass) bool {
	if m.cellVoid[i] {
		return false
	}
	lo, hi := int32(m.cellLo[i]), int32(m.cellHi[i])
	sea := m.SeaLevel
	if lo < sea-c.MaxDepth || hi > sea-c.MinDepth {
		return false
	}
	if slope := hi - lo; slope > c.MaxSlope {
		if lo >= sea || slope > c.MaxWaterSlope {
			return false
		}
	}
	return true
}

// Reach labels the footprint anchors one movement class can stand on by
// connected region (4-connected moves between anchors). Two points in the
// same region are mutually reachable over terrain; features and units are
// ignored, so it is an upper bound on what a path search finds.
type Reach struct {
	Class MoveClass
	w, h  int32
	label []uint16 // per anchor cell: 0 = cannot stand, else region id
	sizes []int32  // anchors per region, index = region id
}

// reachOverflow labels anchors of regions past the id space; they are
// treated as one shared, unreliable region.
const reachOverflow = 65535

// Reach returns the class's region map, computing it on first use. It
// allocates and walks the whole map (a few milliseconds on a large one):
// call it from a brain's Init, which runs on the host's preparation
// goroutine, not from a think. The map is built once per battle and shared
// by every host that asks for the same class (mapShared); a Reach is never
// written after it is built.
func (m *MapInfo) Reach(c MoveClass) *Reach {
	for _, r := range m.reaches {
		if r.Class == c {
			return r
		}
	}
	var r *Reach
	if m.shared != nil {
		r = m.shared.reachOf(m, c)
	} else {
		r = m.buildReach(c)
	}
	m.reaches = append(m.reaches, r)
	return r
}

func (m *MapInfo) buildReach(c MoveClass) *Reach {
	w, h := m.CellW, m.CellH
	r := &Reach{Class: c, w: w, h: h, sizes: []int32{0}}
	if w <= 0 || h <= 0 || len(m.cellLo) != int(w*h) || w > 65535 || h > 65535 {
		return r
	}
	n := w * h
	fx, fz := c.FootX, c.FootZ
	// run holds, per cell, the legal run length rightward in its row, then
	// (reused) whether the footprint anchored there stands.
	if len(m.runScratch) != int(n) {
		m.runScratch = make([]uint16, n)
	}
	run := m.runScratch
	for z := int32(0); z < h; z++ {
		row := z * w
		var k uint16
		for x := w - 1; x >= 0; x-- {
			if m.cellLegal(row+x, &c) {
				k++
			} else {
				k = 0
			}
			run[row+x] = k
		}
	}
	// An anchor stands when fz consecutive rows from it have a run ≥ fx
	// (counted bottom-up per column, walking rows for cache order).
	r.label = make([]uint16, n)
	if len(m.colScratch) != int(w) {
		m.colScratch = make([]int32, w)
	}
	col := m.colScratch
	for x := range col {
		col[x] = 0
	}
	for z := h - 1; z >= 0; z-- {
		row := z * w
		for x := int32(0); x < w; x++ {
			if int32(run[row+x]) >= fx {
				col[x]++
			} else {
				col[x] = 0
			}
			if col[x] >= fz {
				r.label[row+x] = 1
			}
		}
	}
	// Scanline labelling: runs of standing anchors per row, united with the
	// overlapping runs of the row above (4-connected moves).
	type span struct{ x0, x1, id int32 }
	var parent []int32
	find := func(a int32) int32 {
		for parent[a] != a {
			parent[a] = parent[parent[a]]
			a = parent[a]
		}
		return a
	}
	union := func(a, b int32) {
		a, b = find(a), find(b)
		if a < b {
			parent[b] = a
		} else if b < a {
			parent[a] = b
		}
	}
	var prev, cur []span
	runID := make([]int32, 0, 1024)
	for z := int32(0); z < h; z++ {
		row := z * w
		cur = cur[:0]
		k := 0 // first span of prev that may still overlap
		for x := int32(0); x < w; {
			if r.label[row+x] == 0 {
				x++
				continue
			}
			x0 := x
			for x < w && r.label[row+x] != 0 {
				x++
			}
			id := int32(len(parent))
			parent = append(parent, id)
			for k < len(prev) && prev[k].x1 < x0 {
				k++
			}
			for j := k; j < len(prev) && prev[j].x0 <= x-1; j++ {
				union(prev[j].id, id)
			}
			cur = append(cur, span{x0, x - 1, id})
			runID = append(runID, id)
		}
		prev, cur = cur, prev
	}
	// Second pass: final region ids in first-seen order, labels and sizes.
	final := make([]int32, len(parent))
	next := int32(1)
	k := 0
	for z := int32(0); z < h; z++ {
		row := z * w
		for x := int32(0); x < w; {
			if r.label[row+x] == 0 {
				x++
				continue
			}
			root := find(runID[k])
			k++
			id := final[root]
			if id == 0 {
				// Ids 1..reachOverflow−1 are regions of their own; every
				// region past them shares reachOverflow, and sizes holds one
				// entry per id.
				if next < reachOverflow {
					id = next
					next++
					r.sizes = append(r.sizes, 0)
				} else {
					id = reachOverflow
					if len(r.sizes) == reachOverflow {
						r.sizes = append(r.sizes, 0)
					}
				}
				final[root] = id
			}
			x0 := x
			for x < w && r.label[row+x] != 0 {
				r.label[row+x] = uint16(id)
				x++
			}
			r.sizes[id] += x - x0
		}
	}
	return r
}

// anchor converts a unit centre to its footprint anchor cell.
func (r *Reach) anchor(x, z int32) (int32, int32) {
	return x/16 - r.Class.FootX/2, z/16 - r.Class.FootZ/2
}

func (r *Reach) labelAt(cx, cz int32) int32 {
	if cx < 0 || cz < 0 || cx >= r.w || cz >= r.h || r.label == nil {
		return 0
	}
	return int32(r.label[cz*r.w+cx])
}

// At returns the region of a unit of the class centred at (x, z), or of the
// nearest anchor it can stand on within three cells; 0 when there is none.
func (r *Reach) At(x, z int32) int32 {
	cx, cz := r.anchor(x, z)
	for ring := int32(0); ring <= 3; ring++ {
		for j := -ring; j <= ring; j++ {
			for i := -ring; i <= ring; i++ {
				if absI32(i) != ring && absI32(j) != ring {
					continue
				}
				if id := r.labelAt(cx+i, cz+j); id != 0 {
					return id
				}
			}
		}
	}
	return 0
}

// Size is the number of anchors in a region (its area in cells).
func (r *Reach) Size(region int32) int32 {
	if region <= 0 || int(region) >= len(r.sizes) {
		return 0
	}
	return r.sizes[region]
}

// Regions is the number of region ids in use (ids are 1..Regions()).
func (r *Reach) Regions() int32 { return int32(len(r.sizes)) - 1 }

// Dist is the distance from (x, z) to the nearest centre of an anchor of
// region within radius world units, or -1 when there is none. It scans the
// window around the point, so keep the radius modest outside Init.
func (r *Reach) Dist(region, x, z, radius int32) int32 {
	if region <= 0 || region > reachOverflow || r.label == nil {
		return -1
	}
	fx, fz := r.Class.FootX, r.Class.FootZ
	cx0, cz0 := r.anchor(x-radius, z-radius)
	cx1, cz1 := r.anchor(x+radius, z+radius)
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= r.w {
		cx1 = r.w - 1
	}
	if cz1 >= r.h {
		cz1 = r.h - 1
	}
	if cx0 > cx1 || cz0 > cz1 {
		return -1
	}
	// Rows and columns are walked outward from the point, and a direction
	// stops once its offset alone is no nearer than the best anchor found:
	// the minimum is the same as a full scan's, found in the few rows
	// around the point instead of the whole window.
	lim := int64(radius) * int64(radius)
	best := lim + 1
	lab := uint16(region)
	ax, az := r.anchor(x, z)
	ax = clampI32(ax, cx0, cx1)
	az = clampI32(az, cz0, cz1)
	row := func(cz int32, dz2 int64) {
		base := cz * r.w
		for cx := ax; cx <= cx1; cx++ {
			dx := int64(cx*16 + fx*8 - x)
			d := dx*dx + dz2
			if d >= best && dx >= 0 {
				break
			}
			if r.label[base+cx] == lab && d < best {
				best = d
			}
		}
		for cx := ax - 1; cx >= cx0; cx-- {
			dx := int64(cx*16 + fx*8 - x)
			d := dx*dx + dz2
			if d >= best && dx <= 0 {
				break
			}
			if r.label[base+cx] == lab && d < best {
				best = d
			}
		}
	}
	up, dn := az-1, az
	for {
		var dzUp, dzDn int64 = -1, -1
		if up >= cz0 {
			if dz := int64(up*16 + fz*8 - z); dz*dz < best || dz > 0 {
				dzUp = dz * dz
			}
		}
		if dn <= cz1 {
			if dz := int64(dn*16 + fz*8 - z); dz*dz < best || dz < 0 {
				dzDn = dz * dz
			}
		}
		if dzUp < 0 && dzDn < 0 {
			break
		}
		if dzDn >= 0 && (dzUp < 0 || dzDn <= dzUp) {
			row(dn, dzDn)
			dn++
		} else {
			row(up, dzUp)
			up--
		}
	}
	if best > lim {
		return -1
	}
	return int32(ISqrt64(best))
}

func clampI32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Nearest returns the region whose anchor centre is nearest (x, z) within
// radius (preferring the region under the point), and that distance; 0, -1
// when none.
func (r *Reach) Nearest(x, z, radius int32) (int32, int32) {
	if id := r.At(x, z); id != 0 {
		return id, 0
	}
	if r.label == nil {
		return 0, -1
	}
	fx, fz := r.Class.FootX, r.Class.FootZ
	cx0, cz0 := r.anchor(x-radius, z-radius)
	cx1, cz1 := r.anchor(x+radius, z+radius)
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= r.w {
		cx1 = r.w - 1
	}
	if cz1 >= r.h {
		cz1 = r.h - 1
	}
	best, id := int64(-1), int32(0)
	lim := int64(radius) * int64(radius)
	for cz := cz0; cz <= cz1; cz++ {
		row := cz * r.w
		for cx := cx0; cx <= cx1; cx++ {
			l := int32(r.label[row+cx])
			if l == 0 {
				continue
			}
			d := Dist2(cx*16+fx*8, cz*16+fz*8, x, z)
			if d <= lim && (best < 0 || d < best) {
				best, id = d, l
			}
		}
	}
	if best < 0 {
		return 0, -1
	}
	return id, int32(ISqrt64(best))
}

// sumScratch returns the MapInfo's summed-area table scratch, n zeroed
// entries, for DepthSite and DepthSites. It is the host's own, so one
// host's calls must not overlap (a host runs its brain's Init and thinks
// one after another).
func (m *MapInfo) sumScratch(n int) []int32 {
	if cap(m.depthScratch) < n {
		m.depthScratch = make([]int32, n)
	} else {
		m.depthScratch = m.depthScratch[:n]
		clear(m.depthScratch)
	}
	return m.depthScratch
}

// DepthSite finds the footprint centre nearest (x, z), within radius world
// units, whose every cell lies in the water depth band [minDepth, maxDepth]
// (depth = sea level − floor height; land is ≤ 0) with a floor span of at
// most maxSlope, and, when r is not nil, whose centre stands in region of
// r. It only reads terrain (with the MapInfo's scratch, sumScratch):
// placement still validates the exact site. ok is false when nothing
// qualifies.
func (m *MapInfo) DepthSite(x, z, fx, fz, minDepth, maxDepth, maxSlope, radius int32, r *Reach, region int32) (int32, int32, bool) {
	w, h := m.CellW, m.CellH
	if w <= 0 || h <= 0 || len(m.cellLo) != int(w*h) {
		return 0, 0, false
	}
	if fx < 1 {
		fx = 1
	}
	if fz < 1 {
		fz = 1
	}
	sea := m.SeaLevel
	cx0, cz0 := (x-radius)/16, (z-radius)/16
	cx1, cz1 := (x+radius)/16, (z+radius)/16
	if cx0 < 1 {
		cx0 = 1
	}
	if cz0 < 1 {
		cz0 = 1
	}
	// The last anchor the placement validator accepts leaves the footprint
	// a cell short of the map's last row and column (executor.validAt).
	if cx1+fx > w-2 {
		cx1 = w - 2 - fx
	}
	if cz1+fz > h-2 {
		cz1 = h - 2 - fz
	}
	if cx1 < cx0 || cz1 < cz0 {
		return 0, 0, false
	}
	// Summed-area table of out-of-band cells over the window plus footprint.
	ww, wh := cx1-cx0+fx, cz1-cz0+fz
	bad := m.sumScratch(int((ww + 1) * (wh + 1)))
	for j := int32(0); j < wh; j++ {
		var row int32
		for i := int32(0); i < ww; i++ {
			c := (cz0+j)*w + cx0 + i
			lo, hi := int32(m.cellLo[c]), int32(m.cellHi[c])
			if m.cellVoid[c] || sea-hi < minDepth || sea-lo > maxDepth || hi-lo > maxSlope {
				row++
			}
			bad[(j+1)*(ww+1)+i+1] = bad[j*(ww+1)+i+1] + row
		}
	}
	best := int64(-1)
	var bx, bz int32
	lim := int64(radius) * int64(radius)
	for j := int32(0); j+fz <= wh; j++ {
		for i := int32(0); i+fx <= ww; i++ {
			i2, j2 := i+fx, j+fz
			if bad[j2*(ww+1)+i2]-bad[j*(ww+1)+i2]-bad[j2*(ww+1)+i]+bad[j*(ww+1)+i] != 0 {
				continue
			}
			px, pz := (cx0+i)*16+fx*8, (cz0+j)*16+fz*8
			d := Dist2(px, pz, x, z)
			if d > lim || (best >= 0 && d >= best) {
				continue
			}
			if r != nil && r.At(px, pz) != region {
				continue
			}
			best, bx, bz = d, px, pz
		}
	}
	return bx, bz, best >= 0
}

// CountCells counts plot cells within radius of (x, z) that lie in the
// depth band [minDepth, maxDepth] with a floor span of at most maxSlope —
// for example buildable dry land around a base.
func (m *MapInfo) CountCells(x, z, radius, minDepth, maxDepth, maxSlope int32) int32 {
	w, h := m.CellW, m.CellH
	if w <= 0 || h <= 0 || len(m.cellLo) != int(w*h) {
		return 0
	}
	sea := m.SeaLevel
	cx0, cz0 := (x-radius)/16, (z-radius)/16
	cx1, cz1 := (x+radius)/16, (z+radius)/16
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= w {
		cx1 = w - 1
	}
	if cz1 >= h {
		cz1 = h - 1
	}
	lim := int64(radius) * int64(radius)
	var n int32
	for cz := cz0; cz <= cz1; cz++ {
		for cx := cx0; cx <= cx1; cx++ {
			c := cz*w + cx
			lo, hi := int32(m.cellLo[c]), int32(m.cellHi[c])
			if m.cellVoid[c] || sea-hi < minDepth || sea-lo > maxDepth || hi-lo > maxSlope {
				continue
			}
			if Dist2(cx*16+8, cz*16+8, x, z) <= lim {
				n++
			}
		}
	}
	return n
}

// Near2 returns up to two distinct regions with anchors within radius of
// (x, z), nearest first (0 for none): the regions from which a unit of the
// class could work on something at that point.
func (r *Reach) Near2(x, z, radius int32) (int32, int32) {
	if r.label == nil {
		return 0, 0
	}
	fx, fz := r.Class.FootX, r.Class.FootZ
	cx0, cz0 := r.anchor(x-radius, z-radius)
	cx1, cz1 := r.anchor(x+radius, z+radius)
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= r.w {
		cx1 = r.w - 1
	}
	if cz1 >= r.h {
		cz1 = r.h - 1
	}
	var a, b int32
	da, db := int64(-1), int64(-1)
	lim := int64(radius) * int64(radius)
	for cz := cz0; cz <= cz1; cz++ {
		row := cz * r.w
		for cx := cx0; cx <= cx1; cx++ {
			l := int32(r.label[row+cx])
			if l == 0 {
				continue
			}
			d := Dist2(cx*16+fx*8, cz*16+fz*8, x, z)
			if d > lim {
				continue
			}
			switch {
			case l == a:
				if d < da {
					da = d
				}
			case l == b:
				if d < db {
					db = d
				}
			case da < 0 || d < da:
				b, db = a, da
				a, da = l, d
			case db < 0 || d < db:
				b, db = l, d
			}
			if db >= 0 && db < da {
				a, b, da, db = b, a, db, da
			}
		}
	}
	return a, b
}

// RegionSite is the nearest qualifying site found in one region.
type RegionSite struct {
	Region, X, Z, Dist int32
}

// DepthSites is DepthSite for every region of r at once: for each region
// whose anchors hold a qualifying footprint centre, the nearest such centre
// to (x, z). The result is appended to dst in first-found order.
func (m *MapInfo) DepthSites(x, z, fx, fz, minDepth, maxDepth, maxSlope, radius int32, r *Reach, dst []RegionSite) []RegionSite {
	w, h := m.CellW, m.CellH
	if r == nil || w <= 0 || h <= 0 || len(m.cellLo) != int(w*h) {
		return dst
	}
	if fx < 1 {
		fx = 1
	}
	if fz < 1 {
		fz = 1
	}
	sea := m.SeaLevel
	cx0, cz0 := (x-radius)/16, (z-radius)/16
	cx1, cz1 := (x+radius)/16, (z+radius)/16
	if cx0 < 1 {
		cx0 = 1
	}
	if cz0 < 1 {
		cz0 = 1
	}
	// The last anchor the placement validator accepts leaves the footprint
	// a cell short of the map's last row and column (executor.validAt).
	if cx1+fx > w-2 {
		cx1 = w - 2 - fx
	}
	if cz1+fz > h-2 {
		cz1 = h - 2 - fz
	}
	if cx1 < cx0 || cz1 < cz0 {
		return dst
	}
	ww, wh := cx1-cx0+fx, cz1-cz0+fz
	bad := m.sumScratch(int((ww + 1) * (wh + 1)))
	for j := int32(0); j < wh; j++ {
		var row int32
		for i := int32(0); i < ww; i++ {
			c := (cz0+j)*w + cx0 + i
			lo, hi := int32(m.cellLo[c]), int32(m.cellHi[c])
			if m.cellVoid[c] || sea-hi < minDepth || sea-lo > maxDepth || hi-lo > maxSlope {
				row++
			}
			bad[(j+1)*(ww+1)+i+1] = bad[j*(ww+1)+i+1] + row
		}
	}
	first := len(dst)
	lim := int64(radius) * int64(radius)
	for j := int32(0); j+fz <= wh; j++ {
		for i := int32(0); i+fx <= ww; i++ {
			i2, j2 := i+fx, j+fz
			if bad[j2*(ww+1)+i2]-bad[j*(ww+1)+i2]-bad[j2*(ww+1)+i]+bad[j*(ww+1)+i] != 0 {
				continue
			}
			px, pz := (cx0+i)*16+fx*8, (cz0+j)*16+fz*8
			d := Dist2(px, pz, x, z)
			if d > lim {
				continue
			}
			ax, az := r.anchor(px, pz)
			reg := r.labelAt(ax, az)
			if reg == 0 {
				continue
			}
			dd := int32(ISqrt64(d))
			k := first
			for ; k < len(dst); k++ {
				if dst[k].Region == reg {
					break
				}
			}
			if k == len(dst) {
				dst = append(dst, RegionSite{Region: reg, X: px, Z: pz, Dist: dd})
			} else if dd < dst[k].Dist {
				dst[k] = RegionSite{Region: reg, X: px, Z: pz, Dist: dd}
			}
		}
	}
	return dst
}
