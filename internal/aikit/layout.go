package aikit

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Keeping factory exits open. A factory whose exit is walled in — by our
// own rows of generators, by trees, rocks or wrecks — makes units that
// never leave. The executor guards placement (a building that would cut
// an own factory's exit off from open ground is not placed there) and,
// on request, opens a sealed exit by reclaiming the cheapest blockers.
//
// Both work on a local passability picture around one factory, in 2×2-cell
// macro cells (32 world units, so every path found is two cells wide):
// free, removable at a cost (a reclaimable feature, one of our own
// buildings), or blocked (terrain the factory's units cannot cross, a
// permanent feature, the factory itself, anything not ours). The exit is
// open when free macro cells connect the row in front of the factory
// (units leave toward +Z) to the edge of the window, exitRadius macro
// cells out.

// exitRadius is the window half-width in macro cells (1280 world units).
const exitRadius = 40

// exitWindow is the window edge in macro cells.
const exitWindow = 2*exitRadius + 1

const (
	gridFree    int32 = 0
	gridBlocked int32 = -1
	blkPerCell        = 4 // blockers remembered per macro cell
)

// gridBlocker is one removable obstacle: a feature (by its anchor cell) or
// one of our buildings.
type gridBlocker struct {
	feature bool
	cx, cz  int32
	h       pool.Handle
	cost    int32
}

// exitGrid is the scratch picture around one factory; the executor reuses
// its buffers.
type exitGrid struct {
	ox, oz int32 // window origin in macro cells
	fac    pool.Handle
	sealed bool    // no free path from the exit to the window edge
	cost   []int32 // per macro cell: gridFree, gridBlocked or a removable cost
	who    []int32 // blkPerCell blocker indices per macro cell, -1 unused
	blk    []gridBlocker
	seen   []uint32 // visit stamp per macro cell
	reach  []uint32 // stamp of the free region reached from the exit
	stamp  uint32
	// reachStamp is the stamp of the last unobstructed flood.
	reachStamp uint32
	dist       []int32
	prev       []int32
	queue      []int32
	heap       []int64
	seeds      []int32
	built      uint32 // tick the picture was taken
	dd         *gridDedupe
}

// gridDedupe finds a blocker already listed while a grid is built; one is
// shared by the executor's grids (only one is built at a time).
type gridDedupe struct {
	unitStamp []uint32 // by handle
	unitIdx   []int32
	cellStamp []uint32 // by map cell (feature anchors)
	cellIdx   []int32
	gen       uint32
}

// exitClass returns the movement class a factory's units leave with: its
// first ground product's (hovercraft included); false for air plants and
// shipyards, whose units do not walk out.
func (e *executor) exitClass(f *UnitInfo) (MoveClass, bool) {
	for _, p := range f.Builds {
		if mc, ok := MoveClassOf(p); ok && mc.MinDepth <= 0 {
			if mc.FootX > 3 {
				mc.FootX = 3
			}
			if mc.FootZ > 3 {
				mc.FootZ = 3
			}
			return mc, true
		}
	}
	return MoveClass{}, false
}

// buildingCost is what reclaiming one of our buildings to open a path is
// worth avoiding: cheap generators and makers first, extractors reluctantly;
// factories, the commander and anything not ours never.
func (e *executor) buildingCost(u *units.Unit) int32 {
	if u.Owner != e.m.Player || u.Def == nil || u.Def.Commander {
		return gridBlocked
	}
	info := e.table.Of(u.Def)
	if info == nil || info.Role.Any(RoleFactory|RoleCommander) {
		return gridBlocked
	}
	c := 3 + info.Value/50
	if info.Role.Has(RoleExtractor) {
		c *= 4
	}
	if info.Role.Has(RoleDefense) {
		c *= 2
	}
	return c
}

func (g *exitGrid) ensure(n int) {
	if len(g.cost) != n {
		g.cost = make([]int32, n)
		g.who = make([]int32, n*blkPerCell)
		g.seen = make([]uint32, n)
		g.reach = make([]uint32, n)
		g.dist = make([]int32, n)
		g.prev = make([]int32, n)
	}
}

func (d *gridDedupe) ensure(cells int) {
	if len(d.unitStamp) != 1<<16 { // occupancy words hold 16-bit handles
		d.unitStamp = make([]uint32, 1<<16)
		d.unitIdx = make([]int32, 1<<16)
	}
	if len(d.cellStamp) != cells {
		d.cellStamp = make([]uint32, cells)
		d.cellIdx = make([]int32, cells)
	}
	d.gen++
	if d.gen == 0 {
		d.gen = 1
	}
}

// blocker returns the index of a removable blocker, adding it once.
func (g *exitGrid) blocker(b gridBlocker, cellW int32) int32 {
	d := g.dd
	if b.feature {
		i := b.cz*cellW + b.cx
		if d.cellStamp[i] == d.gen {
			return d.cellIdx[i]
		}
		d.cellStamp[i] = d.gen
		d.cellIdx[i] = int32(len(g.blk))
	} else {
		if d.unitStamp[b.h] == d.gen {
			return d.unitIdx[b.h]
		}
		d.unitStamp[b.h] = d.gen
		d.unitIdx[b.h] = int32(len(g.blk))
	}
	g.blk = append(g.blk, b)
	return int32(len(g.blk) - 1)
}

// buildExitGrid fills g for the factory f (a unit that may not exist yet:
// self is 0 for a planned factory) and its seeds; the caller floods it. It
// reports false when the factory has no walking exit. With blockers the
// picture prices every removable obstacle for cheapestOpening; without, a
// macro cell is only free or not (all that flood and cuts read), taken from
// the executor's per-tick cache (macroFree).
func (e *executor) buildExitGrid(g *exitGrid, w *units.World, f *UnitInfo, self pool.Handle, fcx, fcz int32, blockers bool) bool {
	m := e.mapInfo
	t := e.m.Terrain
	if m == nil || t == nil || len(m.cellLo) != int(m.CellW*m.CellH) {
		return false
	}
	mc, ok := e.exitClass(f)
	if !ok {
		return false
	}
	n := exitWindow * exitWindow
	g.ensure(n)
	g.blk = g.blk[:0]
	g.fac = self
	g.ox, g.oz = (fcx>>5)-exitRadius, (fcz>>5)-exitRadius
	if blockers {
		g.dd = &e.dedupe
		g.dd.ensure(int(m.CellW * m.CellH))
		for j := int32(0); j < exitWindow; j++ {
			for i := int32(0); i < exitWindow; i++ {
				k := j*exitWindow + i
				for s := 0; s < blkPerCell; s++ {
					g.who[int(k)*blkPerCell+s] = -1
				}
				g.cost[k] = e.macroCost(g, w, &mc, g.ox+i, g.oz+j, self, k)
			}
		}
	} else {
		e.freeKey(&mc)
		for j := int32(0); j < exitWindow; j++ {
			for i := int32(0); i < exitWindow; i++ {
				c := gridBlocked
				if e.macroFree(w, &mc, g.ox+i, g.oz+j) {
					c = gridFree
				}
				g.cost[j*exitWindow+i] = c
			}
		}
	}
	// The factory's own footprint is solid: its units start in front of it.
	ax, az := fcx/16-f.FootX/2, fcz/16-f.FootZ/2
	g.overlay(ax, az, f.FootX, f.FootZ)
	// Seeds: the macro row just in front of the exit.
	g.seeds = g.seeds[:0]
	sz := (az+f.FootZ+1)>>1 - g.oz
	for x := ax >> 1; x <= (ax+f.FootX-1)>>1; x++ {
		if i := x - g.ox; i >= 0 && i < exitWindow && sz >= 0 && sz < exitWindow {
			g.seeds = append(g.seeds, sz*exitWindow+i)
		}
	}
	return true
}

// macroFree reports whether a macro cell is free for class mc, the way
// macroCost would price it gridFree: every cell on the map, legal for the
// class, and holding no building and no blocking feature. Nothing a batch
// does moves a building or a feature while it is applied, so answers are
// kept for the batch's tick and the class (freeKey).
func (e *executor) macroFree(w *units.World, mc *MoveClass, mx, mz int32) bool {
	m := e.mapInfo
	if mx < 0 || mz < 0 || mx*2+1 >= m.CellW || mz*2+1 >= m.CellH {
		return false
	}
	mw := m.CellW / 2
	i := mz*mw + mx
	f := &e.frees[e.freeSlot]
	if n := int(mw * (m.CellH / 2)); len(f.stamp) != n {
		f.stamp = make([]uint32, n)
		f.val = make([]bool, n)
	}
	if f.stamp[i] == f.gen {
		return f.val[i]
	}
	t := e.m.Terrain
	v := true
	for dz := int32(0); dz < 2 && v; dz++ {
		for dx := int32(0); dx < 2; dx++ {
			cx, cz := mx*2+dx, mz*2+dz
			if !m.cellLegal(cz*m.CellW+cx, mc) || e.buildingAt(w, t.PlotAt(cx, cz)) != nil {
				v = false
				break
			}
			if blocking, _, _, _ := e.featureBlock(cx, cz); blocking {
				v = false
				break
			}
		}
	}
	f.stamp[i], f.val[i] = f.gen, v
	return v
}

// freeSlots is how many classes' macroFree answers the executor keeps at
// once: a placement search asks for its own factory's exit class and for
// the exit classes of the factories guarded around it, and one slot would
// drop each class's answers every time the other is asked for.
const freeSlots = 3

// freeCache holds macroFree's answers for one tick and one class, and the
// free regions exitSealed has labelled under them.
type freeCache struct {
	stamp []uint32
	val   []bool
	gen   uint32
	tick  uint32
	class MoveClass
	used  bool   // the slot holds a class
	asked uint32 // executor.freeSeq when freeKey last chose it

	// comp is, per macro cell, the region (index into regions) of the free
	// cells 4-connected to it, valid where compGen holds gen.
	comp    []int32
	compGen []uint32
	regions []freeRegion
	// seen and seenStamp are exitSealed's visit marks, per macro cell.
	seen      []uint32
	seenStamp uint32
	queue     []int32
}

// freeKey keys macroFree's answers to the tick of the batch being applied
// and to a class: it chooses the class's slot (or the least recently chosen
// one) and drops the slot's answers from an older tick or another class.
func (e *executor) freeKey(mc *MoveClass) {
	e.freeSeq++
	slot := -1
	for i := range e.frees {
		if e.frees[i].used && e.frees[i].class == *mc {
			slot = i
			break
		}
	}
	if slot < 0 {
		slot = 0
		for i := range e.frees {
			if !e.frees[i].used {
				slot = i
				break
			}
			if e.frees[i].asked < e.frees[slot].asked {
				slot = i
			}
		}
	}
	e.freeSlot = slot
	f := &e.frees[slot]
	f.asked = e.freeSeq
	if f.used && f.gen != 0 && f.tick == e.lastTick && f.class == *mc {
		return
	}
	f.used, f.tick, f.class = true, e.lastTick, *mc
	f.gen++
	if f.gen == 0 {
		for i := range f.stamp {
			f.stamp[i] = 0
		}
		for i := range f.compGen {
			f.compGen[i] = 0
		}
		f.gen = 1
	}
	f.regions = f.regions[:0]
}

// freeRegion is a set of free macro cells 4-connected to each other (under
// one freeCache key): its bounding box, or wide when it spans more than a
// window's interior in either direction, in which case its labelling may
// have stopped early and the box is not kept.
type freeRegion struct {
	x0, z0, x1, z1 int32 // macro cells, inclusive
	wide           bool
}

// regionOf labels the free region holding the free macro cell (mx, mz)
// and returns its index. A region is walked once per key; the walk stops as
// soon as it is wide, leaving the rest unlabelled, so a later cell of the
// same region meets a wide label and is wide too. Two regions never touch,
// so a label met while walking is either the walk's own or a wide region's.
func (e *executor) regionOf(w *units.World, mc *MoveClass, mx, mz int32) int32 {
	f := &e.frees[e.freeSlot]
	mw := e.mapInfo.CellW / 2
	if n := len(f.stamp); len(f.comp) != n {
		f.comp = make([]int32, n)
		f.compGen = make([]uint32, n)
	}
	i := mz*mw + mx
	if f.compGen[i] == f.gen {
		return f.comp[i]
	}
	id := int32(len(f.regions))
	f.regions = append(f.regions, freeRegion{x0: mx, z0: mz, x1: mx, z1: mz})
	r := &f.regions[id]
	f.comp[i], f.compGen[i] = id, f.gen
	f.queue = append(f.queue[:0], i)
	const interior = exitWindow - 2
	for len(f.queue) > 0 && !r.wide {
		k := f.queue[len(f.queue)-1]
		f.queue = f.queue[:len(f.queue)-1]
		x, z := k%mw, k/mw
		for d := 0; d < 4; d++ {
			nx, nz := x, z
			switch d {
			case 0:
				nx--
			case 1:
				nx++
			case 2:
				nz--
			default:
				nz++
			}
			if !e.macroFree(w, mc, nx, nz) {
				continue
			}
			nk := nz*mw + nx
			if f.compGen[nk] == f.gen {
				if f.comp[nk] != id {
					r.wide = true // an unfinished wide region: this one
					break
				}
				continue
			}
			f.comp[nk], f.compGen[nk] = id, f.gen
			f.queue = append(f.queue, nk)
			r.x0, r.x1 = min32(r.x0, nx), max32(r.x1, nx)
			r.z0, r.z1 = min32(r.z0, nz), max32(r.z1, nz)
			if r.x1-r.x0+1 > interior || r.z1-r.z0+1 > interior {
				r.wide = true
				break
			}
		}
	}
	return id
}

// exitSealed is the guard's test of a planned factory's own exit: whether
// the picture buildExitGrid would draw for info centred on the world point
// (fcx, fcz) — no blockers, the factory's own footprint solid — has no free
// path from the row in front of it to the window's edge (flood with an
// empty extra set). It answers without drawing the window: first by the
// free regions (exitPocket), then by the same walk over the tick's
// macroFree answers. ok is false where buildExitGrid would draw nothing (no
// map analysis, no walking exit), which the guard never refuses.
func (e *executor) exitSealed(w *units.World, info *UnitInfo, fcx, fcz int32) (sealed, ok bool) {
	mc, ok := e.exitKey(info)
	if !ok {
		return false, false
	}
	if e.exitPocket(w, info, &mc, fcx, fcz) {
		return true, true
	}
	return e.walkExit(w, info, &mc, fcx, fcz), true
}

// exitKey is the class a planned factory's units leave with, with the free
// cache keyed to it; false where buildExitGrid would draw nothing.
func (e *executor) exitKey(info *UnitInfo) (MoveClass, bool) {
	m := e.mapInfo
	if m == nil || e.m.Terrain == nil || len(m.cellLo) != int(m.CellW*m.CellH) {
		return MoveClass{}, false
	}
	mc, walk := e.exitClass(info)
	if !walk {
		return MoveClass{}, false
	}
	e.freeKey(&mc)
	return mc, true
}

// exitFrame is the window and footprint of a factory centred on (fcx, fcz)
// as buildExitGrid places them, in macro cells: the window's origin, the
// footprint's inclusive span (solid) and the seed row in front of it,
// between seedX0 and seedX1.
type exitFrame struct {
	ox, oz                int32
	fx0, fx1, fz0, fz1    int32
	seedX0, seedX1, seedZ int32
}

func frameExit(info *UnitInfo, fcx, fcz int32) exitFrame {
	ax, az := fcx/16-info.FootX/2, fcz/16-info.FootZ/2
	return exitFrame{
		ox: (fcx >> 5) - exitRadius, oz: (fcz >> 5) - exitRadius,
		fx0: ax >> 1, fx1: (ax + info.FootX - 1) >> 1,
		fz0: az >> 1, fz1: (az + info.FootZ - 1) >> 1,
		seedX0: ax >> 1, seedX1: (ax + info.FootX - 1) >> 1, seedZ: (az + info.FootZ + 1) >> 1,
	}
}

func (fr *exitFrame) inWindow(x, z int32) bool {
	return x >= fr.ox && z >= fr.oz && x < fr.ox+exitWindow && z < fr.oz+exitWindow
}

func (fr *exitFrame) onEdge(x, z int32) bool {
	return x == fr.ox || z == fr.oz || x == fr.ox+exitWindow-1 || z == fr.oz+exitWindow-1
}

func (fr *exitFrame) solid(x, z int32) bool {
	return x >= fr.fx0 && x <= fr.fx1 && z >= fr.fz0 && z <= fr.fz1
}

// exitPocket reports that a planned factory's exit is certainly sealed:
// every free seed lies in a free region that fits strictly inside the
// window's interior, so no path from it reaches the edge whatever the
// footprint blocks (nor does any path when no seed is free). It walks each
// region once per key (regionOf) and is otherwise a few lookups. False
// means only that the regions do not settle it.
func (e *executor) exitPocket(w *units.World, info *UnitInfo, mc *MoveClass, fcx, fcz int32) bool {
	f := &e.frees[e.freeSlot]
	fr := frameExit(info, fcx, fcz)
	for x := fr.seedX0; x <= fr.seedX1; x++ {
		if !fr.inWindow(x, fr.seedZ) || fr.solid(x, fr.seedZ) || !e.macroFree(w, mc, x, fr.seedZ) {
			continue
		}
		r := &f.regions[e.regionOf(w, mc, x, fr.seedZ)]
		if r.wide || r.x0 <= fr.ox || r.z0 <= fr.oz || r.x1 >= fr.ox+exitWindow-1 || r.z1 >= fr.oz+exitWindow-1 {
			return false
		}
	}
	return true
}

// walkExit walks the free macro cells from a planned factory's seeds,
// its footprint solid, within its window: whether none reaches the edge.
func (e *executor) walkExit(w *units.World, info *UnitInfo, mc *MoveClass, fcx, fcz int32) bool {
	f := &e.frees[e.freeSlot]
	fr := frameExit(info, fcx, fcz)
	free := func(x, z int32) bool {
		return fr.inWindow(x, z) && !fr.solid(x, z) && e.macroFree(w, mc, x, z)
	}
	mw := e.mapInfo.CellW / 2
	if n := len(f.stamp); len(f.seen) != n {
		f.seen = make([]uint32, n)
	}
	f.seenStamp++
	if f.seenStamp == 0 {
		for i := range f.seen {
			f.seen[i] = 0
		}
		f.seenStamp = 1
	}
	f.queue = f.queue[:0]
	for x := fr.seedX0; x <= fr.seedX1; x++ {
		if !free(x, fr.seedZ) {
			continue
		}
		if k := fr.seedZ*mw + x; f.seen[k] != f.seenStamp {
			f.seen[k] = f.seenStamp
			f.queue = append(f.queue, k)
		}
	}
	for len(f.queue) > 0 {
		k := f.queue[len(f.queue)-1]
		f.queue = f.queue[:len(f.queue)-1]
		x, z := k%mw, k/mw
		if fr.onEdge(x, z) {
			return false
		}
		for d := 0; d < 4; d++ {
			nx, nz := x, z
			switch d {
			case 0:
				nx--
			case 1:
				nx++
			case 2:
				nz--
			default:
				nz++
			}
			if !free(nx, nz) {
				continue
			}
			nk := nz*mw + nx
			if f.seen[nk] == f.seenStamp {
				continue
			}
			f.seen[nk] = f.seenStamp
			f.queue = append(f.queue, nk)
		}
	}
	return true
}

// macroCost classifies one macro cell (2×2 plot cells).
func (e *executor) macroCost(g *exitGrid, w *units.World, mc *MoveClass, mx, mz int32, self pool.Handle, k int32) int32 {
	m := e.mapInfo
	t := e.m.Terrain
	var c int32
	nb := 0
	for dz := int32(0); dz < 2; dz++ {
		for dx := int32(0); dx < 2; dx++ {
			cx, cz := mx*2+dx, mz*2+dz
			if cx < 0 || cz < 0 || cx >= m.CellW || cz >= m.CellH {
				return gridBlocked
			}
			if !m.cellLegal(cz*m.CellW+cx, mc) {
				return gridBlocked
			}
			cell := t.PlotAt(cx, cz)
			if u := e.buildingAt(w, cell); u != nil {
				if u.Handle == self {
					return gridBlocked
				}
				bc := e.buildingCost(u)
				if bc < 0 {
					return gridBlocked
				}
				bi := g.blocker(gridBlocker{h: u.Handle, cost: bc}, m.CellW)
				if g.addWho(k, bi, &nb) {
					c += bc
				}
			}
			if blocking, removable, ax, az := e.featureBlock(cx, cz); blocking {
				if !removable {
					return gridBlocked
				}
				bi := g.blocker(gridBlocker{feature: true, cx: ax, cz: az, cost: 1}, m.CellW)
				if g.addWho(k, bi, &nb) {
					c++
				}
			}
		}
	}
	return c
}

// addWho records blocker bi on macro cell k once; it reports whether bi
// was new there.
func (g *exitGrid) addWho(k, bi int32, nb *int) bool {
	base := int(k) * blkPerCell
	for s := 0; s < *nb; s++ {
		if g.who[base+s] == bi {
			return false
		}
	}
	if *nb < blkPerCell {
		g.who[base+*nb] = bi
		*nb++
	}
	return true
}

// overlay marks a planned footprint (cells) blocked.
func (g *exitGrid) overlay(ax, az, fx, fz int32) {
	for z := az >> 1; z <= (az+fz-1)>>1; z++ {
		for x := ax >> 1; x <= (ax+fx-1)>>1; x++ {
			i, j := x-g.ox, z-g.oz
			if i >= 0 && j >= 0 && i < exitWindow && j < exitWindow {
				g.cost[j*exitWindow+i] = gridBlocked
			}
		}
	}
}

// onEdge reports whether a macro cell lies on the window's outer ring.
func onEdge(k int32) bool {
	i, j := k%exitWindow, k/exitWindow
	return i == 0 || j == 0 || i == exitWindow-1 || j == exitWindow-1
}

// flood walks free macro cells from the seeds; with extra (a planned
// footprint's macro cells, as window indices) blocked. It reports whether
// the window edge is reached, and marks the cells reached in g.reach when
// extra is nil.
func (g *exitGrid) flood(extra []int32) bool {
	g.stamp++
	if g.stamp == 0 {
		g.stamp = 1
	}
	for _, k := range extra {
		g.seen[k] = g.stamp // treated as visited: never entered
	}
	g.queue = g.queue[:0]
	for _, k := range g.seeds {
		if g.cost[k] == gridFree && g.seen[k] != g.stamp {
			g.seen[k] = g.stamp
			g.queue = append(g.queue, k)
		}
	}
	found := false
	for len(g.queue) > 0 {
		k := g.queue[len(g.queue)-1]
		g.queue = g.queue[:len(g.queue)-1]
		if extra == nil {
			g.reach[k] = g.stamp
		}
		if onEdge(k) {
			found = true
			if extra != nil {
				return true
			}
		}
		i, j := k%exitWindow, k/exitWindow
		for d := 0; d < 4; d++ {
			ni, nj := i, j
			switch d {
			case 0:
				ni--
			case 1:
				ni++
			case 2:
				nj--
			default:
				nj++
			}
			if ni < 0 || nj < 0 || ni >= exitWindow || nj >= exitWindow {
				continue
			}
			nk := nj*exitWindow + ni
			if g.seen[nk] == g.stamp || g.cost[nk] != gridFree {
				continue
			}
			g.seen[nk] = g.stamp
			g.queue = append(g.queue, nk)
		}
	}
	if extra == nil {
		g.reachStamp = g.stamp
	}
	return found
}

// cuts reports whether blocking a planned footprint (cells) would cut the
// exit off from the window edge. A sealed exit cannot be cut further.
func (g *exitGrid) cuts(ax, az, fx, fz int32, scratch []int32) (bool, []int32) {
	if g.sealed {
		return false, scratch
	}
	scratch = scratch[:0]
	touches := false
	for z := az >> 1; z <= (az+fz-1)>>1; z++ {
		for x := ax >> 1; x <= (ax+fx-1)>>1; x++ {
			i, j := x-g.ox, z-g.oz
			if i < 0 || j < 0 || i >= exitWindow || j >= exitWindow {
				continue
			}
			k := j*exitWindow + i
			scratch = append(scratch, k)
			if g.reach[k] == g.reachStamp {
				touches = true
			}
		}
	}
	if !touches {
		return false, scratch // nowhere near the open path
	}
	return !g.flood(scratch), scratch
}

// cheapestOpening finds the least-cost path from the exit to the window
// edge through removable cells (Dijkstra on small integer costs) and
// returns the blockers along it, nearest the exit first, at most max.
func (g *exitGrid) cheapestOpening(dst []int32, max int) []int32 {
	n := int32(exitWindow * exitWindow)
	for k := int32(0); k < n; k++ {
		g.dist[k] = -1
		g.prev[k] = -1
	}
	g.heap = g.heap[:0]
	for _, k := range g.seeds {
		if g.cost[k] < 0 {
			continue
		}
		if g.dist[k] < 0 || g.cost[k] < g.dist[k] {
			g.dist[k] = g.cost[k]
			g.heap = heapPush(g.heap, int64(g.cost[k])<<32|int64(k))
		}
	}
	end := int32(-1)
	for len(g.heap) > 0 {
		var top int64
		top, g.heap = heapPop(g.heap)
		k := int32(top & 0xffffffff)
		d := int32(top >> 32)
		if d != g.dist[k] {
			continue
		}
		if onEdge(k) {
			end = k
			break
		}
		i, j := k%exitWindow, k/exitWindow
		for dd := 0; dd < 4; dd++ {
			ni, nj := i, j
			switch dd {
			case 0:
				ni--
			case 1:
				ni++
			case 2:
				nj--
			default:
				nj++
			}
			if ni < 0 || nj < 0 || ni >= exitWindow || nj >= exitWindow {
				continue
			}
			nk := nj*exitWindow + ni
			c := g.cost[nk]
			if c < 0 {
				continue
			}
			if nd := d + c; g.dist[nk] < 0 || nd < g.dist[nk] {
				g.dist[nk] = nd
				g.prev[nk] = k
				g.heap = heapPush(g.heap, int64(nd)<<32|int64(nk))
			}
		}
	}
	if end < 0 {
		return dst
	}
	// Walk back to the exit, then emit blockers exit-first.
	g.queue = g.queue[:0]
	for k := end; k >= 0; k = g.prev[k] {
		g.queue = append(g.queue, k)
	}
	for p := len(g.queue) - 1; p >= 0 && len(dst) < max; p-- {
		k := g.queue[p]
		if g.cost[k] <= 0 {
			continue
		}
		base := int(k) * blkPerCell
		for s := 0; s < blkPerCell && len(dst) < max; s++ {
			bi := g.who[base+s]
			if bi < 0 {
				break
			}
			dup := false
			for _, x := range dst {
				if x == bi {
					dup = true
					break
				}
			}
			if !dup {
				dst = append(dst, bi)
			}
		}
	}
	return dst
}

func heapPush(h []int64, v int64) []int64 {
	h = append(h, v)
	i := len(h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h[p] <= h[i] {
			break
		}
		h[p], h[i] = h[i], h[p]
		i = p
	}
	return h
}

func heapPop(h []int64) (int64, []int64) {
	top := h[0]
	last := len(h) - 1
	h[0] = h[last]
	h = h[:last]
	i := 0
	for {
		l, r, s := 2*i+1, 2*i+2, i
		if l < len(h) && h[l] < h[s] {
			s = l
		}
		if r < len(h) && h[r] < h[s] {
			s = r
		}
		if s == i {
			break
		}
		h[i], h[s] = h[s], h[i]
		i = s
	}
	return top, h
}

// BuildKeep is Build under the base layout rules: a land building goes in
// rows by the conventions of human bases (see "Rows" below), and the exit
// guard refuses any site that would cut one of the owner's factories off
// from open ground (and a factory site whose own exit is closed). The
// point is where the brain wants the building; the rules decide the cell.
func (k *Kit) BuildKeep(builder pool.Handle, product *UnitInfo, x, z, spot, spacing int32) {
	var one [1]pool.Handle
	one[0] = builder
	k.push(Command{Kind: CmdBuild, Product: product, X: x, Z: z, Spot: spot, Spacing: spacing, Keep: true}, one[:])
}

// SetRowNear sets how close to a BuildKeep request point (world units) a
// building of an energy or maker row must stand for the new building to
// extend that row rather than start a new one there; 0 restores the
// default (rowNear, 700). A brain that grows its base outward by moving its
// request points (utility's spread) keeps the old rows from absorbing every
// new building with a smaller radius. It is a setting of the kit's command
// output, so it holds for the host's life once set from a think; set during
// Init, before the kit has an output, it is dropped, like a command.
func (k *Kit) SetRowNear(wu int32) {
	if k.out != nil {
		k.out.rowNear = max(wu, 0)
	}
}

// Unblock asks a builder to open an own factory's exit if it is sealed: the
// executor finds the cheapest way out (reclaimable features and wrecks
// first, then our cheapest buildings) and queues reclaims of up to four
// blockers along it, nearest the exit first. An open exit is left alone.
func (k *Kit) Unblock(builder, factory pool.Handle) {
	var one [1]pool.Handle
	one[0] = builder
	k.push(Command{Kind: CmdUnblock, Target: factory, Spot: -1}, one[:])
}

// guardTTL is how long a factory's picture is reused (ticks); placements
// made since are overlaid on it.
const guardTTL = 300

// pendingTTL is how long a placed site is overlaid on fresh pictures while
// its builder walks there (ticks).
const pendingTTL = 900

// pendingSite is a site the guard accepted recently.
type pendingSite struct {
	cx, cz, fx, fz int32
	g              rowGroup
	tick           uint32
}

// prepareGuard picks the own factories (walking exits only) nearest the
// point a building is being placed around, up to len(guardFacs).
func (e *executor) prepareGuard(x, z int32, tick uint32) {
	e.nGuard = 0
	if e.obs == nil {
		return
	}
	lim := int64(exitRadius*32 + 1100)
	lim *= lim
	var dist [len(e.guardFacs)]int64
	for i := range e.obs.Own {
		u := &e.obs.Own[i]
		if !u.Info.Role.Has(RoleFactory) {
			continue
		}
		if _, ok := e.exitClass(u.Info); !ok {
			continue
		}
		d := Dist2(u.X, u.Z, x, z)
		if d > lim {
			continue
		}
		pos := e.nGuard
		for pos > 0 && dist[pos-1] > d {
			pos--
		}
		if pos >= len(e.guardFacs) {
			continue
		}
		end := e.nGuard
		if end >= len(e.guardFacs) {
			end = len(e.guardFacs) - 1
		}
		for j := end; j > pos; j-- {
			e.guardFacs[j], dist[j] = e.guardFacs[j-1], dist[j-1]
		}
		e.guardFacs[pos], dist[pos] = *u, d
		if e.nGuard < len(e.guardFacs) {
			e.nGuard++
		}
	}
}

// gridFor returns a fresh picture of factory f, reusing a cached one.
func (e *executor) gridFor(f *OwnUnit, w *units.World, tick uint32) *exitGrid {
	oldest := 0
	for i := range e.grids {
		g := &e.grids[i]
		if g.fac == f.H && g.cost != nil && tick-g.built < guardTTL {
			return g
		}
		if e.grids[i].built < e.grids[oldest].built {
			oldest = i
		}
	}
	g := &e.grids[oldest]
	if !e.buildExitGrid(g, w, f.Info, f.H, f.X, f.Z, false) {
		g.fac = 0
		return nil
	}
	g.built = tick
	// Sites accepted recently but not yet framed.
	for i := range e.pending {
		ps := &e.pending[i]
		if ps.tick != 0 && tick-ps.tick < pendingTTL {
			g.overlay(ps.cx, ps.cz, ps.fx, ps.fz)
		}
	}
	g.sealed = !g.flood(nil)
	return g
}

// guardRefuses reports whether a building footprint (cells) would cut an
// own factory's exit off, or is a factory whose own exit would be closed.
func (e *executor) guardRefuses(info *UnitInfo, cx, cz, fx, fz int32, w *units.World, tick uint32) bool {
	if w == nil || !e.keep {
		return false
	}
	span := int32(exitRadius * 32)
	for k := 0; k < e.nGuard; k++ {
		f := &e.guardFacs[k]
		if absI32(cx*16+fx*8-f.X) > span || absI32(cz*16+fz*8-f.Z) > span {
			continue
		}
		g := e.gridFor(f, w, tick)
		if g == nil {
			continue
		}
		var cut bool
		cut, e.cutBuf = g.cuts(cx, cz, fx, fz, e.cutBuf)
		if cut {
			return true
		}
	}
	if info.Role.Has(RoleFactory) {
		// A site search tries thousands of anchors a few cells apart, and
		// where the ground is broken most of them face the same enclosed
		// pocket; exitSealed answers those from the pocket's extent instead
		// of drawing and walking a window per anchor.
		if sealed, ok := e.exitSealed(w, info, cx*16+fx*8, cz*16+fz*8); ok && sealed {
			return true
		}
	}
	return false
}

// sealedSite reports, for a factory site, that the guard would refuse it
// because the factory's own exit is certainly sealed (exitPocket: its seeds
// face enclosed pockets), in a state where running the whole try instead
// could not have changed anything: the guard is on and every own factory's
// picture the guard would consult for this site is already drawn and
// current, so its checks would only read them. The other checks try runs
// first (placement, lanes, corridors) only read, so a site refused here is
// one try refuses, and refusing it early leaves the executor as try would
// have. It is a few lookups per anchor, so it costs little where it
// settles nothing; a search over broken ground meets thousands of anchors
// facing the same pockets.
func (e *executor) sealedSite(info *UnitInfo, cx, cz, fx, fz int32, w *units.World, tick uint32) bool {
	if w == nil || !e.keep {
		return false
	}
	span := int32(exitRadius * 32)
	for k := 0; k < e.nGuard; k++ {
		f := &e.guardFacs[k]
		if absI32(cx*16+fx*8-f.X) > span || absI32(cz*16+fz*8-f.Z) > span {
			continue
		}
		if !e.gridCurrent(f, tick) {
			return false
		}
	}
	mc, ok := e.exitKey(info)
	return ok && e.exitPocket(w, info, &mc, cx*16+fx*8, cz*16+fz*8)
}

// gridCurrent reports whether gridFor would return a cached picture of f
// at tick without drawing one.
func (e *executor) gridCurrent(f *OwnUnit, tick uint32) bool {
	for i := range e.grids {
		g := &e.grids[i]
		if g.fac == f.H && g.cost != nil && tick-g.built < guardTTL {
			return true
		}
	}
	return false
}

// guardPlaced overlays an accepted site on the cached pictures and
// remembers it for pictures taken before its frame appears.
func (e *executor) guardPlaced(cx, cz, fx, fz int32) {
	if !e.keep || e.nGuard == 0 {
		return
	}
	for i := range e.grids {
		g := &e.grids[i]
		if g.cost == nil || g.fac == 0 {
			continue
		}
		g.overlay(cx, cz, fx, fz)
		g.sealed = !g.flood(nil)
	}
}

// execUnblock applies an Unblock.
func (e *executor) execUnblock(c *Command, b *batch, tick uint32, w *units.World) bool {
	if c.count < 1 {
		e.stats.Failed++
		return false
	}
	u := e.actorOK(w, b.actors[c.first], b.inst[c.first])
	if u == nil {
		e.stats.Stale++
		e.stats.Reasons[FailNoActor]++
		return false
	}
	f := w.Unit(c.Target)
	if f == nil || f.Dying || f.Owner != e.m.Player || f.Def == nil {
		e.stats.Stale++
		e.stats.Reasons[FailTarget]++
		return false
	}
	info := e.table.Of(f.Def)
	g := &e.selfGrid
	if info == nil || !e.buildExitGrid(g, w, info, f.Handle, int32(int64(f.X)>>16), int32(int64(f.Z)>>16), true) {
		e.stats.Failed++
		e.stats.Reasons[FailResolve]++
		return false
	}
	g.sealed = !g.flood(nil)
	if !g.sealed {
		return true // open: nothing to do
	}
	e.blkBuf = g.cheapestOpening(e.blkBuf[:0], 4)
	if len(e.blkBuf) == 0 {
		e.stats.Failed++
		e.stats.Reasons[FailNoSite]++
		return false
	}
	q := orders.BindQueueBinding(u, e.m.OrderBinding)
	if q == nil {
		e.stats.Failed++
		return false
	}
	issued := false
	for _, bi := range e.blkBuf {
		blk := &g.blk[bi]
		var ok bool
		if blk.feature {
			ok = e.reclaimFeature(u, q, blk.cx, blk.cz, tick, !issued)
		} else if t := w.Unit(blk.h); t != nil {
			ok = e.reclaimUnit(u, q, t, tick, !issued)
		}
		issued = issued || ok
	}
	if !issued {
		e.stats.Failed++
		e.stats.Reasons[FailResolve]++
		return false
	}
	e.stats.Unblocks++
	return true
}

// Rows. With the guard on, land buildings follow the conventions of human
// base layouts (tools/ai-layout-bench; ~1,900 human land 1v1 player-games):
//
//   - energy buildings touch each other, and so do makers and storage: a
//     same-class neighbour is either flush (gap 0, sharing an edge) or a
//     street away, never 1–2 cells (too narrow for any mover, 2×2 is the
//     smallest, so a small gap only wastes ground);
//   - a block (flush same-class buildings) is a row: at most 8 buildings,
//     24 cells long and 10 deep (two buildings);
//   - streets between blocks are at least 3 cells (a 3×3 tank passes);
//   - nothing comes within 3 cells of a factory, and its front (+Z, where
//     units leave) is kept clear over its full width plus a cell for 12
//     cells — of buildings and of blocking trees and rocks;
//   - other buildings (radar, towers) keep 2 cells from anything.
//
// A row building first tries to extend a nearby row of its class (along
// the row before a second rank), nearest the requested point; failing
// that it starts a new row as near the point as the rules allow. Where the
// rows go — factories forward, energy and makers on the flanks, growing
// outward — is the brain's request point.

const (
	rowMaxLong  = 24   // cells
	rowMaxShort = 10   // cells: two buildings deep
	rowMaxCount = 8    // buildings per block
	streetMin   = 3    // cells between blocks
	looseGap    = 2    // cells between any other building and its neighbours
	factoryGap  = 3    // cells between a factory and anything
	factoryLane = 12   // cells kept clear in front (+Z) of a factory
	featureLane = 8    // of which free of blocking features
	rowNear     = 700  // world units: rows this close to the request point may be extended (default; Kit.SetRowNear)
	rowGather   = 1400 // world units: buildings this close are taken into account
	rowSearchR  = 40   // cells: how far from the request point a new row may start
	rowDeepCost = 160  // world units of distance a second rank is worth avoiding
)

// rowGroup is a building's class for the layout rules.
type rowGroup uint8

const (
	grpLoose rowGroup = iota // radar, towers, anything else
	grpEnergy
	grpMaker // makers and storage
	grpFactory
	grpExtractor
)

func groupOf(info *UnitInfo) rowGroup {
	switch r := info.Role; {
	case r.Has(RoleFactory):
		return grpFactory
	case r.Has(RoleExtractor):
		return grpExtractor
	case r.Any(RoleDefense | RoleRadar | RoleSonar | RoleJammer):
		return grpLoose
	case r.Has(RoleEnergy):
		return grpEnergy
	case r.Any(RoleMetalMaker | RoleStorage):
		return grpMaker
	}
	return grpLoose
}

// rowBld is one building (or accepted site) on the cell grid.
type rowBld struct {
	x0, z0, x1, z1 int32 // cells, half-open
	g              rowGroup
	blk            int32 // block index, -1 for none
}

// rowBlock is a set of flush same-class buildings.
type rowBlock struct {
	x0, z0, x1, z1 int32
	n              int32
	unit           int32 // the largest footprint side among its members
}

// rowCand is a candidate anchor with its cost.
type rowCand struct {
	cx, cz int32
	cost   int64
}

// rowState is the executor's scratch for one placement.
type rowState struct {
	blds  []rowBld
	blks  []rowBlock
	cands []rowCand
	touch []int32
	// near lists, per bucket of candidate anchors in a new-row search, the
	// buildings that can be within rowMaxLong of a candidate there
	// (bucketRows); nearAt[b]..nearAt[b+1] index near.
	near   []int32
	nearAt []int32
	// Placement answers for one site search (validAtSearch): per anchor of
	// the new-row window from (vx0, vz0), the answer where vStamp holds vGen;
	// and the new-row rules' answers (rulesAt), where rStamp does.
	vStamp   []uint32
	vOK      []bool
	rStamp   []uint32
	rOK      []bool
	vGen     uint32
	vx0, vz0 int32
}

// rowWindow is the edge, in anchors, of a new-row search's window.
const rowWindow = 2*rowSearchR + 1

// resetValid starts a site search's placement answers for the window of
// anchors from (x0, z0).
func (rs *rowState) resetValid(x0, z0 int32) {
	if len(rs.vStamp) != rowWindow*rowWindow {
		rs.vStamp = make([]uint32, rowWindow*rowWindow)
		rs.vOK = make([]bool, rowWindow*rowWindow)
		rs.rStamp = make([]uint32, rowWindow*rowWindow)
		rs.rOK = make([]bool, rowWindow*rowWindow)
	}
	rs.vGen++
	if rs.vGen == 0 {
		clear(rs.vStamp)
		clear(rs.rStamp)
		rs.vGen = 1
	}
	rs.vx0, rs.vz0 = x0, z0
}

// validAtSearch is validAt within one site search: the placement validator
// only reads the world, and nothing changes the world while a search runs
// (it places nothing until it returns), so an anchor asked about twice — a
// factory's second pass asks about every anchor again — gets the first
// answer. Anchors outside the window are asked every time.
func (e *executor) validAtSearch(p *placeDef, cx, cz int32) bool {
	rs := &e.rows
	i, j := cx-rs.vx0, cz-rs.vz0
	if i < 0 || j < 0 || i >= rowWindow || j >= rowWindow || rs.vStamp == nil {
		return e.validAt(p, cx, cz)
	}
	k := j*rowWindow + i
	if rs.vStamp[k] == rs.vGen {
		return rs.vOK[k]
	}
	ok := e.validAt(p, cx, cz)
	rs.vStamp[k], rs.vOK[k] = rs.vGen, ok
	return ok
}

// rectGap is the Chebyshev edge-to-edge gap between two rectangles in cells
// (the width of the free channel between them; 0 when they touch, at an
// edge or a corner), and whether they overlap.
func rectGap(a, b *rowBld) (gap int32, overlap bool) {
	gx := b.x0 - a.x1
	if v := a.x0 - b.x1; v > gx {
		gx = v
	}
	gz := b.z0 - a.z1
	if v := a.z0 - b.z1; v > gz {
		gz = v
	}
	if a.x0 < b.x1 && b.x0 < a.x1 && a.z0 < b.z1 && b.z0 < a.z1 {
		return 0, true
	}
	if gx < 0 {
		gx = 0
	}
	if gz < 0 {
		gz = 0
	}
	gap = gx
	if gz > gap {
		gap = gz
	}
	return gap, false
}

// laneOf is the rectangle kept clear in front of a factory rectangle.
func laneOf(f *rowBld) rowBld {
	return rowBld{x0: f.x0 - 1, z0: f.z1, x1: f.x1 + 1, z1: f.z1 + factoryLane}
}

func intersects(a, b *rowBld) bool {
	return a.x0 < b.x1 && b.x0 < a.x1 && a.z0 < b.z1 && b.z0 < a.z1
}

// gatherRows collects the owner's buildings (and sites accepted but not yet
// framed) within rowGather of (x, z), and groups flush same-class
// neighbours into blocks.
func (e *executor) gatherRows(rs *rowState, x, z int32, tick uint32) {
	rs.blds = rs.blds[:0]
	rs.blks = rs.blks[:0]
	lim := int64(rowGather) * rowGather
	if e.obs != nil {
		for i := range e.obs.Own {
			u := &e.obs.Own[i]
			if u.Info.Role.Has(RoleMobile) || Dist2(u.X, u.Z, x, z) > lim {
				continue
			}
			fx, fz := u.Info.FootX, u.Info.FootZ
			ax, az := u.X/16-fx/2, u.Z/16-fz/2
			rs.blds = append(rs.blds, rowBld{x0: ax, z0: az, x1: ax + fx, z1: az + fz, g: groupOf(u.Info), blk: -1})
		}
	}
	for i := range e.pending {
		ps := &e.pending[i]
		if ps.tick == 0 || tick-ps.tick >= pendingTTL {
			continue
		}
		c := rowBld{x0: ps.cx, z0: ps.cz, x1: ps.cx + ps.fx, z1: ps.cz + ps.fz, g: ps.g, blk: -1}
		dup := false
		for j := range rs.blds {
			if _, ov := rectGap(&c, &rs.blds[j]); ov {
				dup = true // already framed there
				break
			}
		}
		if !dup {
			rs.blds = append(rs.blds, c)
		}
	}
	rs.groupBlocks()
}

// groupBlocks groups flush same-class energy or maker neighbours into blocks.
func (rs *rowState) groupBlocks() {
	for i := range rs.blds {
		a := &rs.blds[i]
		if a.g != grpEnergy && a.g != grpMaker || a.blk >= 0 {
			continue
		}
		id := int32(len(rs.blks))
		a.blk = id
		rs.blks = append(rs.blks, rowBlock{x0: a.x0, z0: a.z0, x1: a.x1, z1: a.z1, n: 1, unit: maxSide(a)})
		rs.touch = append(rs.touch[:0], int32(i))
		for len(rs.touch) > 0 {
			k := rs.touch[len(rs.touch)-1]
			rs.touch = rs.touch[:len(rs.touch)-1]
			for j := range rs.blds {
				b := &rs.blds[j]
				if b.blk >= 0 || b.g != a.g {
					continue
				}
				if g, _ := rectGap(&rs.blds[k], b); g == 0 {
					b.blk = id
					bk := &rs.blks[id]
					bk.x0, bk.z0 = min32(bk.x0, b.x0), min32(bk.z0, b.z0)
					bk.x1, bk.z1 = max32(bk.x1, b.x1), max32(bk.z1, b.z1)
					bk.n++
					if v := maxSide(b); v > bk.unit {
						bk.unit = v
					}
					rs.touch = append(rs.touch, int32(j))
				}
			}
		}
	}
}

func maxSide(b *rowBld) int32 {
	if b.x1-b.x0 > b.z1-b.z0 {
		return b.x1 - b.x0
	}
	return b.z1 - b.z0
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// rowRules checks a candidate rectangle against the layout rules; for a row
// building it also reports the resulting block's size and whether it
// would be deeper than one rank.
func (rs *rowState) rowRules(r *rowBld) (ok, joins, deep bool) {
	return rs.rowRulesAmong(r, nil, false)
}

// rowRulesAmong is rowRules over the buildings idx lists (every building
// when all). A building more than rowMaxLong from the candidate takes no
// part in the rules, so any list holding every building within that gap
// gives rowRules's answer.
func (rs *rowState) rowRulesAmong(r *rowBld, idx []int32, subset bool) (ok, joins, deep bool) {
	var bx0, bz0, bx1, bz1, n, unit int32 = r.x0, r.z0, r.x1, r.z1, 1, maxSide(r)
	rs.touch = rs.touch[:0]
	count := len(rs.blds)
	if subset {
		count = len(idx)
	}
	for q := 0; q < count; q++ {
		j := q
		if subset {
			j = int(idx[q])
		}
		b := &rs.blds[j]
		g, ov := rectGap(r, b)
		if ov {
			return false, false, false
		}
		if g > rowMaxLong {
			continue
		}
		switch {
		case r.g == grpFactory || b.g == grpFactory:
			if g < factoryGap {
				return false, false, false
			}
			if b.g == grpFactory {
				if l := laneOf(b); intersects(r, &l) {
					return false, false, false
				}
			}
			if r.g == grpFactory {
				if l := laneOf(r); intersects(b, &l) {
					return false, false, false
				}
			}
		case (r.g == grpEnergy || r.g == grpMaker) && b.g == r.g:
			if g == 0 {
				// Flush (or corner to corner, as in a second rank): the
				// building joins that block.
				dup := false
				for _, t := range rs.touch {
					if t == b.blk {
						dup = true
						break
					}
				}
				if !dup && b.blk >= 0 {
					rs.touch = append(rs.touch, b.blk)
				}
			} else if g < streetMin {
				return false, false, false
			}
		case (r.g == grpEnergy || r.g == grpMaker) && (b.g == grpEnergy || b.g == grpMaker):
			if g < streetMin {
				return false, false, false
			}
		default:
			if g < looseGap {
				return false, false, false
			}
		}
	}
	if len(rs.touch) == 0 {
		return true, false, false
	}
	for _, t := range rs.touch {
		bk := &rs.blks[t]
		bx0, bz0 = min32(bx0, bk.x0), min32(bz0, bk.z0)
		bx1, bz1 = max32(bx1, bk.x1), max32(bz1, bk.z1)
		n += bk.n
		if bk.unit > unit {
			unit = bk.unit
		}
	}
	long, short := bx1-bx0, bz1-bz0
	if short > long {
		long, short = short, long
	}
	if n > rowMaxCount || long > rowMaxLong || short > rowMaxShort {
		return false, true, false
	}
	return true, true, short > unit
}

// findRowSite places a land building under the layout rules near (x, z).
func (e *executor) findRowSite(info *UnitInfo, p *placeDef, x, z int32, w *units.World, tick uint32) (int32, int32, bool) {
	rs := &e.rows
	e.gatherRows(rs, x, z, tick)
	g := groupOf(info)
	fx, fz := p.footX, p.footZ
	rs.resetValid(x/16-fx/2-rowSearchR, z/16-fz/2-rowSearchR)
	spotLanes := false // a factory's first search keeps its lane off metal spots
	try := func(cx, cz int32) bool {
		if e.overlapsSpot(cx, cz, fx, fz) || !e.validAtSearch(p, cx, cz) {
			return false
		}
		if g == grpFactory && !spotLanes && e.laneSpot(cx, cz, fx, fz) {
			return false
		}
		if g == grpFactory && !e.laneFree(info, cx, cz, fx, fz) {
			return false
		}
		if e.inCorridor(info, cx, cz, fx, fz) {
			return false
		}
		return !e.guardRefuses(info, cx, cz, fx, fz, w, tick)
	}
	// A factory whose own exit would be sealed is refused before anything
	// else is looked at, once looking at it can change nothing (sealedSite).
	var refuse func(cx, cz int32) bool
	if g == grpFactory {
		refuse = func(cx, cz int32) bool { return e.sealedSite(info, cx, cz, fx, fz, w, tick) }
	}
	// Extend a nearby row of the same class: flush on either side along the
	// row first, then a second rank.
	if g == grpEnergy || g == grpMaker {
		near := int32(rowNear)
		if e.rowNear > 0 {
			near = e.rowNear
		}
		rs.extendCands(g, fx, fz, x, z, near)
		for len(rs.cands) > 0 {
			bi := 0
			for i := range rs.cands {
				if rs.cands[i].cost < rs.cands[bi].cost {
					bi = i
				}
			}
			c := rs.cands[bi]
			rs.cands[bi] = rs.cands[len(rs.cands)-1]
			rs.cands = rs.cands[:len(rs.cands)-1]
			if try(c.cx, c.cz) {
				e.placedRow(c.cx, c.cz, fx, fz, g, tick)
				e.stats.RowExtend++
				return c.cx, c.cz, true
			}
		}
	}
	// A new row (or a factory, or a loose building): the nearest anchor to
	// the request point that keeps the rules. A factory is searched for
	// twice: first with its lane off every metal spot, then, if that finds
	// nothing, with a spot allowed in it (the spot's extractor is then
	// refused: spotSite).
	cx0, cz0 := x/16-fx/2, z/16-fz/2
	rs.bucketRows(cx0-rowSearchR, cz0-rowSearchR, fx, fz)
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			if g != grpFactory {
				break
			}
			spotLanes = true
		}
		if cx, cz, ok := e.newRowSite(rs, g, fx, fz, cx0, cz0, tick, refuse, try); ok {
			return cx, cz, true
		}
	}
	return 0, 0, false
}

// newRowSite is findRowSite's new-row search: the nearest anchor to
// (cx0, cz0) in rings out to rowSearchR that keeps the row rules (over the
// buckets bucketRows listed) and passes try. refuse, when set, rejects an
// anchor before the rules; it must only refuse anchors try would, and
// change nothing try would not.
func (e *executor) newRowSite(rs *rowState, g rowGroup, fx, fz, cx0, cz0 int32, tick uint32, refuse, try func(cx, cz int32) bool) (int32, int32, bool) {
	for r := int32(0); r <= rowSearchR; r++ {
		for j := -r; j <= r; j++ {
			for i := -r; i <= r; i++ {
				if absI32(i) != r && absI32(j) != r {
					continue
				}
				cx, cz := cx0+i, cz0+j
				if refuse != nil && refuse(cx, cz) {
					continue
				}
				if !rs.rulesAt(g, fx, fz, cx0, cz0, i, j) {
					continue
				}
				if try(cx, cz) {
					e.placedRow(cx, cz, fx, fz, g, tick)
					if g == grpEnergy || g == grpMaker {
						e.stats.RowNew++
					}
					return cx, cz, true
				}
			}
		}
	}
	return 0, 0, false
}

// rulesAt is the new-row rules' answer for the anchor (cx0+i, cz0+j) of a
// search from (cx0, cz0), over the buckets bucketRows listed. The rules
// read only the gathered buildings, which a search does not change until
// it places, so a factory's second pass takes the first pass's answers.
func (rs *rowState) rulesAt(g rowGroup, fx, fz, cx0, cz0, i, j int32) bool {
	cx, cz := cx0+i, cz0+j
	k := int32(-1)
	if wi, wj := cx-rs.vx0, cz-rs.vz0; wi >= 0 && wj >= 0 && wi < rowWindow && wj < rowWindow && rs.rStamp != nil {
		k = wj*rowWindow + wi
		if rs.rStamp[k] == rs.vGen {
			return rs.rOK[k]
		}
	}
	c := rowBld{x0: cx, z0: cz, x1: cx + fx, z1: cz + fz, g: g}
	b := ((j+rowSearchR)/rowBucket)*rowBuckets + (i+rowSearchR)/rowBucket
	ok, _, _ := rs.rowRulesAmong(&c, rs.near[rs.nearAt[b]:rs.nearAt[b+1]], true)
	if k >= 0 {
		rs.rStamp[k], rs.rOK[k] = rs.vGen, ok
	}
	return ok
}

// extendCands lists the anchors (rs.cands) where a g-class building of
// footprint fx×fz would extend a row of its class under the rules: flush
// against a building of the class whose centre lies within near world
// units of the request point (x, z), costed by the distance from the
// request point plus rowDeepCost for a second rank.
func (rs *rowState) extendCands(g rowGroup, fx, fz, x, z, near int32) {
	rs.cands = rs.cands[:0]
	lim := int64(near) * int64(near)
	for j := range rs.blds {
		b := &rs.blds[j]
		if b.g != g || Dist2(b.x0*16+(b.x1-b.x0)*8, b.z0*16+(b.z1-b.z0)*8, x, z) > lim {
			continue
		}
		for k := 0; k < 8; k++ {
			var cx, cz int32
			switch k {
			case 0:
				cx, cz = b.x1, b.z0
			case 1:
				cx, cz = b.x0-fx, b.z0
			case 2:
				cx, cz = b.x1, b.z1-fz
			case 3:
				cx, cz = b.x0-fx, b.z1-fz
			case 4:
				cx, cz = b.x0, b.z1
			case 5:
				cx, cz = b.x0, b.z0-fz
			case 6:
				cx, cz = b.x1-fx, b.z1
			default:
				cx, cz = b.x1-fx, b.z0-fz
			}
			r := rowBld{x0: cx, z0: cz, x1: cx + fx, z1: cz + fz, g: g}
			ok, joins, deep := rs.rowRules(&r)
			if !ok || !joins {
				continue
			}
			cost := int64(Dist(cx*16+fx*8, cz*16+fz*8, x, z))
			if deep {
				cost += rowDeepCost
			}
			rs.cands = append(rs.cands, rowCand{cx: cx, cz: cz, cost: cost})
		}
	}
}

// rowBucket is the edge, in candidate anchors, of one bucket of a new-row
// search's (2·rowSearchR+1)² candidates.
const (
	rowBucket  = 8
	rowBuckets = (2*rowSearchR + rowBucket) / rowBucket
)

// bucketRows lists, for each bucket of new-row candidates (anchors from
// (ax, az), footprint fx×fz), every gathered building that can come within
// rowMaxLong of one of them: a building's gap to a candidate anchored at cx
// is at most rowMaxLong along x exactly when b.x0−fx−rowMaxLong ≤ cx ≤
// b.x1+rowMaxLong, and likewise along z.
func (rs *rowState) bucketRows(ax, az, fx, fz int32) {
	const nb = rowBuckets * rowBuckets
	if cap(rs.nearAt) < nb+1 {
		rs.nearAt = make([]int32, nb+1)
	}
	rs.nearAt = rs.nearAt[:nb+1]
	for i := range rs.nearAt {
		rs.nearAt[i] = 0
	}
	span := func(lo, hi, origin int32) (int32, int32, bool) {
		b0, b1 := (lo-origin)/rowBucket, (hi-origin)/rowBucket
		if lo-origin < 0 {
			b0 = 0
		}
		if hi < origin || b0 >= rowBuckets {
			return 0, 0, false
		}
		if b1 >= rowBuckets {
			b1 = rowBuckets - 1
		}
		return b0, b1, true
	}
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			// Offsets from counts, then fill.
			var sum int32
			for i := 0; i < nb; i++ {
				c := rs.nearAt[i]
				rs.nearAt[i] = sum
				sum += c
			}
			rs.nearAt[nb] = sum
			if cap(rs.near) < int(sum) {
				rs.near = make([]int32, sum)
			}
			rs.near = rs.near[:sum]
		}
		for j := range rs.blds {
			b := &rs.blds[j]
			x0, x1, okx := span(b.x0-fx-rowMaxLong, b.x1+rowMaxLong, ax)
			z0, z1, okz := span(b.z0-fz-rowMaxLong, b.z1+rowMaxLong, az)
			if !okx || !okz {
				continue
			}
			for bz := z0; bz <= z1; bz++ {
				for bx := x0; bx <= x1; bx++ {
					k := bz*rowBuckets + bx
					if pass == 0 {
						rs.nearAt[k]++
					} else {
						rs.near[rs.nearAt[k]] = int32(j)
						rs.nearAt[k]++
					}
				}
			}
		}
	}
	// The fill advanced each start to the next bucket's; shift back.
	for i := nb; i > 0; i-- {
		rs.nearAt[i] = rs.nearAt[i-1]
	}
	rs.nearAt[0] = 0
}

// laneSpot reports whether a factory footprint's kept lane (laneOf), or a
// cell around it, covers a metal spot: the rows keep buildings out of the
// lane, but an extractor stands wherever its spot is (a cell off it at
// most), and one built there walls the pad in. Measured before this rule:
// 4 of 277 factories in 18 40-minute mirrors faced an extractor; a first
// factory so placed made one constructor that never left its pad.
func (e *executor) laneSpot(cx, cz, fx, fz int32) bool {
	return e.overlapsSpot(cx-2, cz+fz, fx+4, factoryLane+1)
}

// inOwnLane reports whether a footprint intersects the kept lane of one of
// the owner's factories: standing or framed (the latest observation), or
// accepted and not yet framed (pending).
func (e *executor) inOwnLane(cx, cz, fx, fz int32, tick uint32) bool {
	c := rowBld{x0: cx, z0: cz, x1: cx + fx, z1: cz + fz}
	if e.obs != nil {
		for i := range e.obs.Own {
			u := &e.obs.Own[i]
			if !u.Info.Role.Has(RoleFactory) {
				continue
			}
			ffx, ffz := u.Info.FootX, u.Info.FootZ
			f := rowBld{x0: u.X/16 - ffx/2, z0: u.Z/16 - ffz/2}
			f.x1, f.z1 = f.x0+ffx, f.z0+ffz
			if l := laneOf(&f); intersects(&c, &l) {
				return true
			}
		}
	}
	for i := range e.pending {
		ps := &e.pending[i]
		if ps.g != grpFactory || ps.tick == 0 || tick-ps.tick >= pendingTTL {
			continue
		}
		f := rowBld{x0: ps.cx, z0: ps.cz, x1: ps.cx + ps.fx, z1: ps.cz + ps.fz}
		if l := laneOf(&f); intersects(&c, &l) {
			return true
		}
	}
	return false
}

// laneFree reports whether a factory site leaves its units room: the
// front lane (full width plus a cell, featureLane deep) must be ground
// they can drive on — no cliff, no deep water — and it and factoryGap cells
// on the other three sides must be clear of blocking features (trees,
// rocks, wrecks). Humans plan factories around trees; a factory whose front
// is a cliff or a thicket makes units that never get off its pad.
func (e *executor) laneFree(info *UnitInfo, cx, cz, fx, fz int32) bool {
	m := e.mapInfo
	mc, walk := e.exitClass(info)
	x0, x1 := cx-factoryGap, cx+fx+factoryGap
	z0, z1 := cz-factoryGap, cz+fz+featureLane
	for z := z0; z < z1; z++ {
		for x := x0; x < x1; x++ {
			if x >= cx && x < cx+fx && z >= cz && z < cz+fz {
				continue // the footprint itself: placement checks it
			}
			front := z >= cz+fz && x >= cx-1 && x < cx+fx+1
			if z >= cz+fz+factoryGap && !front {
				continue // beyond the side margin, outside the front lane
			}
			if x < 0 || z < 0 || x >= m.CellW || z >= m.CellH {
				if front {
					return false
				}
				continue
			}
			if front && walk && !m.cellLegal(z*m.CellW+x, &mc) {
				return false
			}
			if blocking, _, _, _ := e.featureBlock(x, z); blocking {
				return false
			}
		}
	}
	return true
}

// corridorSide is the clear run (cells) a building must leave on one side
// of it across a terrain passage: with walls nearer than this on two
// opposite sides, the site stands in a ramp, pass or gully.
const corridorSide = 8

// inCorridor reports whether a site stands in a terrain passage: for some
// row (or column) of its footprint, ground that the nearby factories'
// units cannot cross lies within corridorSide cells on both sides. A
// radar or tower in a ramp halves the ramp; late in long games the army
// queues there back to the factories. Map edges count as open ground.
func (e *executor) inCorridor(info *UnitInfo, cx, cz, fx, fz int32) bool {
	m := e.mapInfo
	if m == nil {
		return false
	}
	var cls [len(e.guardFacs) + 1]MoveClass
	n := 0
	add := func(u *UnitInfo) {
		mc, ok := e.exitClass(u)
		if !ok {
			return
		}
		for i := 0; i < n; i++ {
			if cls[i] == mc {
				return
			}
		}
		cls[n] = mc
		n++
	}
	if info.Role.Has(RoleFactory) {
		add(info)
	}
	for k := 0; k < e.nGuard; k++ {
		add(e.guardFacs[k].Info)
	}
	for i := 0; i < n; i++ {
		if m.corridor(&cls[i], cx, cz, fx, fz) {
			return true
		}
	}
	return false
}

// corridor is inCorridor's test for one movement class.
func (m *MapInfo) corridor(c *MoveClass, cx, cz, fx, fz int32) bool {
	run := func(x, z, dx, dz int32) int32 {
		for n := int32(0); n < corridorSide; n++ {
			if x < 0 || z < 0 || x >= m.CellW || z >= m.CellH {
				return corridorSide
			}
			if !m.cellLegal(z*m.CellW+x, c) {
				return n
			}
			x, z = x+dx, z+dz
		}
		return corridorSide
	}
	for z := cz; z < cz+fz; z++ {
		if run(cx-1, z, -1, 0) < corridorSide && run(cx+fx, z, 1, 0) < corridorSide {
			return true
		}
	}
	for x := cx; x < cx+fx; x++ {
		if run(x, cz-1, 0, -1) < corridorSide && run(x, cz+fz, 0, 1) < corridorSide {
			return true
		}
	}
	return false
}

// placedRow remembers an accepted row site until its frame appears.
func (e *executor) placedRow(cx, cz, fx, fz int32, g rowGroup, tick uint32) {
	e.guardPlaced(cx, cz, fx, fz)
	e.pending[e.nextPending] = pendingSite{cx: cx, cz: cz, fx: fx, fz: fz, g: g, tick: tick}
	e.nextPending = (e.nextPending + 1) % len(e.pending)
}

// Clear orders a builder to reclaim features around (x, z) within radius:
// blocking features on streets and in factory lanes first (nearest first),
// then wrecks and other features holding metal (most metal per distance),
// up to four. With nothing to reclaim it fails and the builder keeps its
// orders.
func (k *Kit) Clear(builder pool.Handle, x, z, radius int32) {
	var one [1]pool.Handle
	one[0] = builder
	k.push(Command{Kind: CmdClear, X: x, Z: z, Count: radius, Spot: -1}, one[:])
}

// clearPick is one feature a Clear may reclaim.
type clearPick struct {
	cx, cz int32
	score  int64
	metal  int32
}

// execClear applies a Clear.
func (e *executor) execClear(c *Command, b *batch, tick uint32, w *units.World) bool {
	m := e.mapInfo
	t := e.m.Terrain
	u := e.actorOK(w, b.actors[c.first], b.inst[c.first])
	if u == nil {
		e.stats.Stale++
		e.stats.Reasons[FailNoActor]++
		return false
	}
	if m == nil || t == nil || u.Def == nil || !u.Def.CanReclamate {
		e.stats.Failed++
		e.stats.Reasons[FailResolve]++
		return false
	}
	radius := c.Count
	if radius < 64 {
		radius = 64
	}
	ux, uz := int32(int64(u.X)>>16), int32(int64(u.Z)>>16)
	cx0, cz0 := (c.X-radius)/16, (c.Z-radius)/16
	cx1, cz1 := (c.X+radius)/16, (c.Z+radius)/16
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= m.CellW {
		cx1 = m.CellW - 1
	}
	if cz1 >= m.CellH {
		cz1 = m.CellH - 1
	}
	var picks [4]clearPick
	n := 0
	lim := int64(radius) * int64(radius)
	for cz := cz0; cz <= cz1; cz++ {
		for cx := cx0; cx <= cx1; cx++ {
			cell := t.PlotAt(cx, cz)
			if cell == nil || !cell.IsRealFeature() {
				continue // anchors only: each feature once
			}
			if Dist2(cx*16+8, cz*16+8, c.X, c.Z) > lim {
				continue
			}
			def, ok := t.FeatureDefAt(cell.Feature())
			if !ok || def == nil || !def.Reclaimable || def.Indestructible {
				continue
			}
			d := int64(Dist(cx*16+8, cz*16+8, ux, uz))
			var sc int64
			switch {
			case def.Blocking && e.inFactoryLane(cx, cz):
				sc = 1<<40 - d // in the way: first, nearest first
			case def.Metal > 0:
				sc = int64(def.Metal) * 1000 / (d + 100)
			default:
				continue
			}
			pos := n
			for pos > 0 && picks[pos-1].score < sc {
				pos--
			}
			if pos >= len(picks) {
				continue
			}
			end := n
			if end >= len(picks) {
				end = len(picks) - 1
			}
			for j := end; j > pos; j-- {
				picks[j] = picks[j-1]
			}
			picks[pos] = clearPick{cx: cx, cz: cz, score: sc, metal: def.Metal}
			if n < len(picks) {
				n++
			}
		}
	}
	if n == 0 {
		e.stats.Failed++
		e.stats.Reasons[FailNoSite]++
		return false
	}
	q := orders.BindQueueBinding(u, e.m.OrderBinding)
	if q == nil {
		e.stats.Failed++
		return false
	}
	issued := false
	for i := 0; i < n; i++ {
		if e.reclaimFeature(u, q, picks[i].cx, picks[i].cz, tick, !issued) {
			issued = true
			e.stats.ClearMetal += picks[i].metal
		}
	}
	if !issued {
		e.stats.Failed++
		e.stats.Reasons[FailResolve]++
		return false
	}
	e.stats.Clears++
	return true
}

// inFactoryLane reports whether a cell lies in the front lane of one of the
// owner's factories (from the latest observation).
func (e *executor) inFactoryLane(cx, cz int32) bool {
	if e.obs == nil {
		return false
	}
	c := rowBld{x0: cx, z0: cz, x1: cx + 1, z1: cz + 1}
	for i := range e.obs.Own {
		u := &e.obs.Own[i]
		if !u.Info.Role.Has(RoleFactory) {
			continue
		}
		fx, fz := u.Info.FootX, u.Info.FootZ
		f := rowBld{x0: u.X/16 - fx/2, z0: u.Z/16 - fz/2}
		f.x1, f.z1 = f.x0+fx, f.z0+fz
		if l := laneOf(&f); intersects(&c, &l) {
			return true
		}
	}
	return false
}

// Passages. Human players put static defenses where attacks must come
// through: the ramps, passes and gaps on the land routes from the enemy's
// start to their base and extractors, and they cover such a choke from
// beside it on their own side rather than standing in it. Chokes reads the
// public terrain once (a brain's Init, during the host's preparation) for one
// movement class and finds, on the routes from the likely enemy starts to
// the owner's start and to the metal spots on the owner's side, the
// narrow places the routes squeeze through, which ground each one closes
// off, and tower sites beside its mouth on the owner's side that the
// passage rule (inCorridor) would accept.
//
// The analysis works on macro cells of chokeCell×chokeCell plot cells:
//
//  1. A macro cell is open when a unit of the class can stand anchored in
//     it, in the owner's start region; the rest is wall, and so is the
//     map edge.
//  2. Clearance is the number of macro steps (8-neighbour) from each open
//     cell to the nearest wall: about half a passage's width at its middle.
//  3. Routes are cheapest paths from the enemy starts whose step cost rises
//     as clearance falls, so they keep to the middle of passages.
//  4. A choke is a point on a route whose clearance is at most chokeMaxClr,
//     the least within chokeWin steps either way (the owner-side end of a
//     narrow stretch), with the passage opening out to at least twice that
//     clearance (and three more steps) within chokeOpen steps on both sides.
//  5. Closing its cross-section (walked from wall to wall across the route)
//     shows what it guards: the open ground no enemy start reaches then.
//     A choke that guards neither the start nor a spot is dropped.
//  6. Sites: walking back from the choke toward the owner's side, the first
//     places where a 3×3 building stands on the guarded ground, as far from
//     the route as the passage allows, outside every given class's passage
//     test, within chokeSiteR steps of the choke — up to two on each side.

const (
	chokeCell    = 2   // plot cells per macro cell edge (32 world units)
	chokeMaxClr  = 6   // macro steps: a choke's clearance is at most this (≤ ~380 wu across)
	chokeWin     = 6   // macro steps along the route: the narrowest point this far either way
	chokeOpen    = 40  // macro steps along the route in which the passage must open out
	chokeMerge   = 6   // macro cells: chokes closer than this are one (192 wu)
	chokeSiteR   = 14  // macro steps back from the choke in which its sites are sought (448 wu)
	chokeFar     = 600 // permille: starts at least this share of the farthest start's distance are enemy starts
	chokeFwd     = 650 // permille of the way to the enemy start: chokes beyond belong to the enemy
	chokeTargets = 32  // our side's spots routed to, nearest our start first
	chokeSiteGap = 160 // world units between two sites on one side (a tower gap)
	chokeBuckets = 128 // route queue ring (above the dearest step, 14×7)
	// MaxChokes is the most chokes one analysis keeps (a macro cell's
	// guard mask is one byte).
	MaxChokes = 8
)

// chokeDirs are eight directions over half a turn (cos, sin × 1000).
var chokeDirs = [8][2]int64{{1000, 0}, {924, 383}, {707, 707}, {383, 924}, {0, 1000}, {-383, 924}, {-707, 707}, {-924, 383}}

// Choke is one narrow place on the land routes into the owner's side.
type Choke struct {
	X, Z   int32 // the narrowest point on the route (world units)
	Width  int32 // about how wide the passage is there (world units)
	Along  int32 // permille of the way from the owner's start to the enemy start its route came from
	Home   bool  // it guards the owner's start
	Spots  int32 // metal spots on the owner's side it guards
	Routes int32 // routes (the start's and the spots') that pass through it
	// Sites are tower sites beside its mouth on the guarded side (world
	// units, footprint centres of a 3×3 building): up to two on each side
	// of the route, all chokeSiteGap apart; NSites of them.
	Sites  [4][2]int32
	NSites int32
}

// ChokeMap is the result of one analysis.
type ChokeMap struct {
	W, H   int32 // macro grid
	Chokes []Choke
	guard  []uint8 // per macro cell: bit i set when choke i guards it
	// Reach is the route class's region map and Home the region of the
	// owner's start in it (0 when the start stands nowhere).
	Reach *Reach
	Home  int32
}

// Guards returns the mask of chokes that guard the macro cell holding (x, z).
func (c *ChokeMap) Guards(x, z int32) uint8 {
	if c == nil || len(c.guard) == 0 {
		return 0
	}
	mx, mz := x/(16*chokeCell), z/(16*chokeCell)
	if mx < 0 || mz < 0 || mx >= c.W || mz >= c.H {
		return 0
	}
	return c.guard[mz*c.W+mx]
}

// chokeCand is a choke found on one route.
type chokeCand struct {
	k      int32 // macro cell
	clr    int32
	along  int32
	home   bool
	routes int32
	path   int32 // route index the site search walks
	idx    int32 // position on that route
}

// InPassage reports whether a footprint (anchor cell, size in cells) stands
// in a terrain passage for class c — the test inCorridor applies near
// factories.
func (m *MapInfo) InPassage(c *MoveClass, cx, cz, fx, fz int32) bool {
	if m == nil || len(m.cellLo) != int(m.CellW*m.CellH) {
		return false
	}
	return m.corridor(c, cx, cz, fx, fz)
}

// Chokes analyzes the land routes for class route (its units' footprint is
// capped at three cells) from the enemy starts — the starts at least
// chokeFar of the farthest start's distance from the owner's (m.HomeX,
// m.HomeZ) — to the owner's start and to the metal spots nearer it than to
// any enemy start. Sites must stand outside the passage test of every class
// in site (the owner's factories' exit classes). It allocates and walks the
// whole map: call it from a brain's Init.
func (m *MapInfo) Chokes(route MoveClass, site []MoveClass) *ChokeMap {
	out := &ChokeMap{}
	if m == nil || m.CellW <= 0 || m.CellH <= 0 || len(m.cellLo) != int(m.CellW*m.CellH) {
		return out
	}
	if route.FootX > 3 {
		route.FootX = 3
	}
	if route.FootZ > 3 {
		route.FootZ = 3
	}
	r := m.Reach(route)
	home := r.At(m.HomeX, m.HomeZ)
	out.Reach, out.Home = r, home
	if home == 0 {
		return out
	}
	W := (m.CellW + chokeCell - 1) / chokeCell
	H := (m.CellH + chokeCell - 1) / chokeCell
	n := W * H
	out.W, out.H = W, H
	open := make([]bool, n)
	for mz := int32(0); mz < H; mz++ {
		for mx := int32(0); mx < W; mx++ {
			for dz := int32(0); dz < chokeCell && !open[mz*W+mx]; dz++ {
				for dx := int32(0); dx < chokeCell; dx++ {
					if r.labelAt(mx*chokeCell+dx, mz*chokeCell+dz) == home {
						open[mz*W+mx] = true
						break
					}
				}
			}
		}
	}
	macro := func(x, z int32) int32 {
		mx, mz := x/(16*chokeCell), z/(16*chokeCell)
		if mx < 0 || mz < 0 || mx >= W || mz >= H {
			return -1
		}
		return mz*W + mx
	}
	// The nearest open macro cell to a world point, within a few steps.
	near := func(x, z int32) int32 {
		k := macro(x, z)
		if k < 0 {
			return -1
		}
		if open[k] {
			return k
		}
		cx, cz := k%W, k/W
		for ring := int32(1); ring <= 4; ring++ {
			for j := -ring; j <= ring; j++ {
				for i := -ring; i <= ring; i++ {
					if absI32(i) != ring && absI32(j) != ring {
						continue
					}
					x2, z2 := cx+i, cz+j
					if x2 >= 0 && z2 >= 0 && x2 < W && z2 < H && open[z2*W+x2] {
						return z2*W + x2
					}
				}
			}
		}
		return -1
	}
	hk := near(m.HomeX, m.HomeZ)
	if hk < 0 {
		return out
	}
	// Enemy starts: the farthest from ours and those nearly as far.
	var far int64
	for _, s := range m.Starts {
		if d := Dist2(s[0], s[1], m.HomeX, m.HomeZ); d > far {
			far = d
		}
	}
	var srcs []int32
	var srcXZ [][2]int32
	for _, s := range m.Starts {
		d := Dist2(s[0], s[1], m.HomeX, m.HomeZ)
		if d < 400*400 || int64(ISqrt64(d))*1000 < int64(ISqrt64(far))*chokeFar {
			continue
		}
		if k := near(s[0], s[1]); k >= 0 {
			srcs = append(srcs, k)
			srcXZ = append(srcXZ, s)
		}
	}
	if len(srcs) == 0 {
		return out
	}
	// Clearance: breadth-first from every wall (and the map edge).
	clr := make([]int32, n)
	q := make([]int32, 0, n)
	for k := int32(0); k < n; k++ {
		if !open[k] {
			q = append(q, k)
		} else {
			clr[k] = -1
		}
	}
	for k := int32(0); k < n; k++ {
		x, z := k%W, k/W
		if open[k] && (x == 0 || z == 0 || x == W-1 || z == H-1) {
			clr[k] = 1
			q = append(q, k)
		}
	}
	for h := 0; h < len(q); h++ {
		k := q[h]
		x, z := k%W, k/W
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				x2, z2 := x+dx, z+dz
				if x2 < 0 || z2 < 0 || x2 >= W || z2 >= H {
					continue
				}
				k2 := z2*W + x2
				if clr[k2] < 0 {
					clr[k2] = clr[k] + 1
					q = append(q, k2)
				}
			}
		}
	}
	centre := func(k int32) (int32, int32) {
		return (k%W)*16*chokeCell + 8*chokeCell, (k/W)*16*chokeCell + 8*chokeCell
	}
	// Routes: cheapest paths from the enemy starts, dearer where narrow,
	// around the blocked cells.
	dist := make([]int32, n)
	prev := make([]int32, n)
	from := make([]int32, n) // the source each cell's route started at
	blocked := make([]bool, n)
	// Dial's bucket queue: a step costs at most 14×7 < chokeBuckets, so
	// every pending distance lies within one turn of the ring.
	var bk [chokeBuckets][]int32
	routes := func() {
		for k := range dist {
			dist[k], prev[k], from[k] = -1, -1, -1
		}
		pending := 0
		for i, k := range srcs {
			if dist[k] != 0 && !blocked[k] {
				dist[k], from[k] = 0, int32(i)
				bk[0] = append(bk[0], k)
				pending++
			}
		}
		for d := int32(0); pending > 0; d++ {
			b := &bk[d%chokeBuckets]
			for len(*b) > 0 {
				k := (*b)[len(*b)-1]
				*b = (*b)[:len(*b)-1]
				pending--
				if dist[k] != d {
					continue
				}
				x, z := k%W, k/W
				for dz := int32(-1); dz <= 1; dz++ {
					for dx := int32(-1); dx <= 1; dx++ {
						if dx == 0 && dz == 0 {
							continue
						}
						x2, z2 := x+dx, z+dz
						if x2 < 0 || z2 < 0 || x2 >= W || z2 >= H {
							continue
						}
						k2 := z2*W + x2
						if !open[k2] || blocked[k2] {
							continue
						}
						step := int32(10)
						if dx != 0 && dz != 0 {
							// No corner cutting past a wall.
							if !open[z*W+x2] || !open[z2*W+x] || blocked[z*W+x2] || blocked[z2*W+x] {
								continue
							}
							step = 14
						}
						c := clr[k2]
						nd := d + step*(c+6)/c
						if dist[k2] < 0 || nd < dist[k2] {
							dist[k2], prev[k2], from[k2] = nd, k, from[k]
							bk[nd%chokeBuckets] = append(bk[nd%chokeBuckets], k2)
							pending++
						}
					}
				}
			}
		}
	}
	routes()
	// Targets: our start, then our side's spots (nearer us than any enemy
	// start), in spot order.
	targets := []int32{hk}
	for i := range m.Spots {
		sp := &m.Spots[i]
		if sp.Water {
			continue
		}
		dh := Dist2(sp.X, sp.Z, m.HomeX, m.HomeZ)
		ours := true
		for _, s := range srcXZ {
			if Dist2(sp.X, sp.Z, s[0], s[1]) <= dh {
				ours = false
				break
			}
		}
		if !ours {
			continue
		}
		if k := near(sp.X, sp.Z); k >= 0 && dist[k] >= 0 {
			targets = append(targets, k)
		}
	}
	if len(targets) > chokeTargets+1 {
		// The spots nearest our start (a metal map offers hundreds).
		sp := targets[1:]
		sort.SliceStable(sp, func(a, b int) bool {
			ax, az := centre(sp[a])
			bx, bz := centre(sp[b])
			da, db := Dist2(ax, az, m.HomeX, m.HomeZ), Dist2(bx, bz, m.HomeX, m.HomeZ)
			if da != db {
				return da < db
			}
			return sp[a] < sp[b]
		})
		targets = targets[:chokeTargets+1]
	}
	if dist[hk] < 0 {
		return out // no land route from an enemy start
	}
	var paths [][]int32
	// dir is the route's direction toward the enemy at a candidate (×1000).
	dir := func(c *chokeCand) (int64, int64) {
		p := paths[c.path]
		i := int(c.idx)
		ax, az := centre(p[max(i-2, 0)])
		bx, bz := centre(p[min(i+2, len(p)-1)])
		dx, dz := int64(bx-ax), int64(bz-az)
		l := ISqrt64(dx*dx + dz*dz)
		if l == 0 {
			return 0, 0
		}
		return dx * 1000 / l, dz * 1000 / l
	}
	// cross is the shortest line through macro cell k, of eight directions,
	// that meets a wall both ways within 3×chokeMaxClr steps: its length in
	// steps and direction, or -1 when none does (not a narrow place).
	crossL := make([]int16, n)
	crossA := make([]int8, n)
	for k := range crossL {
		crossL[k] = -2
	}
	cross := func(k int32) (int64, int) {
		if crossL[k] != -2 {
			return int64(crossL[k]), int(crossA[k])
		}
		cx0, cz0 := centre(k)
		best, bestA := int64(-1), -1
		for a := range chokeDirs {
			ux, uz := chokeDirs[a][0], chokeDirs[a][1]
			tot := int64(1)
			ok := true
			for _, side := range [2]int64{1, -1} {
				hit := false
				for s := int64(1); s <= 3*chokeMaxClr && (best < 0 || tot+s <= best); s++ {
					k2 := macro(int32(int64(cx0)+side*ux*s*16*chokeCell/1000), int32(int64(cz0)+side*uz*s*16*chokeCell/1000))
					if k2 < 0 || !open[k2] {
						hit = true
						tot += s - 1
						break
					}
				}
				if !hit {
					ok = false
					break
				}
			}
			if ok && (best < 0 || tot < best) {
				best, bestA = tot, a
			}
		}
		crossL[k], crossA[k] = int16(best), int8(bestA)
		return best, bestA
	}
	// section lists a candidate's cross-section (its cross line) into sec;
	// false when it has none. Steps of one macro cell make an 8-connected
	// line, which the 4-neighbour flood below cannot cross.
	var sec []int32
	section := func(c *chokeCand) bool {
		sec = sec[:0]
		_, a := cross(c.k)
		if a < 0 {
			return false
		}
		cx0, cz0 := centre(c.k)
		ux, uz := chokeDirs[a][0], chokeDirs[a][1]
		sec = append(sec, c.k)
		for _, side := range [2]int64{1, -1} {
			for s := int64(1); s <= 3*chokeMaxClr; s++ {
				k := macro(int32(int64(cx0)+side*ux*s*16*chokeCell/1000), int32(int64(cz0)+side*uz*s*16*chokeCell/1000))
				if k < 0 || !open[k] {
					break
				}
				sec = append(sec, k)
			}
		}
		return true
	}
	along := func(k int32) int32 {
		x, z := centre(k)
		s := srcXZ[0]
		if f := from[k]; f >= 0 {
			s = srcXZ[f]
		}
		ex, ez := int64(s[0]-m.HomeX), int64(s[1]-m.HomeZ)
		l2 := ex*ex + ez*ez
		if l2 == 0 {
			return 0
		}
		return int32((int64(x-m.HomeX)*ex + int64(z-m.HomeZ)*ez) * 1000 / l2)
	}
	// Rounds: find the chokes on the current routes, close them, and route
	// again, so a second (third) way in is found too.
	var ch []chokeCand
	for round := 0; round < 4; round++ {
		if round > 0 {
			routes()
		}
		n0 := len(ch)
		for ti, t := range targets {
			if dist[t] < 0 {
				continue
			}
			var p []int32
			for k := t; k >= 0; k = prev[k] {
				p = append(p, k)
			}
			pi := int32(len(paths))
			paths = append(paths, p)
			// Narrow stretches: runs of route steps with clearance at most
			// chokeMaxClr. Each yields its owner-side end — the narrowest
			// step within chokeWin of where the route enters it — when the
			// route was open before it (clearance at least twice that, and
			// three more, within chokeOpen steps toward the target).
			for i := 1; i+1 < len(p); {
				if clr[p[i]] > chokeMaxClr {
					i++
					continue
				}
				a := i
				for i+1 < len(p) && clr[p[i]] <= chokeMaxClr {
					i++
				}
				// The owner-most place whose cross-section is within a step
				// of the narrowest near the owner-side end.
				minL := int64(-1)
				for j := a; j <= i && j <= a+chokeOpen; j++ {
					if l, _ := cross(p[j]); l >= 0 && (minL < 0 || l < minL) {
						minL = l
					}
				}
				if minL < 0 || minL > 2*chokeMaxClr+1 {
					continue
				}
				best := a
				for j := a; j <= i && j <= a+chokeOpen; j++ {
					if l, _ := cross(p[j]); l >= 0 && l <= minL+1 {
						best = j
						break
					}
				}
				c := clr[p[best]]
				need := max32(2*c, c+3)
				var ours int32
				for j := a - 1; j >= 0 && j >= a-chokeOpen; j-- {
					ours = max32(ours, clr[p[j]])
				}
				if ours < need {
					continue
				}
				al := along(p[best])
				if al > chokeFwd {
					continue
				}
				// One choke per place: the narrowest within chokeMerge.
				cand := chokeCand{k: p[best], clr: c, along: al, home: ti == 0, routes: 1, path: pi, idx: int32(best)}
				merged := false
				for j := range ch {
					o := &ch[j]
					if absI32(o.k%W-cand.k%W) > chokeMerge || absI32(o.k/W-cand.k/W) > chokeMerge {
						continue
					}
					o.routes++
					o.home = o.home || cand.home
					if j >= n0 && cand.clr < o.clr {
						o.k, o.clr, o.along, o.path, o.idx = cand.k, cand.clr, cand.along, cand.path, cand.idx
					}
					merged = true
					break
				}
				if !merged {
					ch = append(ch, cand)
				}
			}
		}
		if len(ch) == n0 {
			break
		}
		for j := n0; j < len(ch); j++ {
			if section(&ch[j]) {
				for _, k := range sec {
					blocked[k] = true
				}
			}
		}
	}
	sort.SliceStable(ch, func(a, b int) bool {
		x, y := &ch[a], &ch[b]
		if x.home != y.home {
			return x.home
		}
		if x.routes != y.routes {
			return x.routes > y.routes
		}
		if x.clr != y.clr {
			return x.clr < y.clr
		}
		return x.k < y.k
	})
	if len(ch) > 2*MaxChokes {
		ch = ch[:2*MaxChokes] // each is tested by a flood of the whole map
	}
	out.guard = make([]uint8, n)
	seen := make([]uint32, n)
	shut := make([]uint32, n)
	var stamp uint32
	// flood marks (seen = stamp) what the enemy starts reach with the
	// sections shut under this stamp closed (4-neighbour moves).
	flood := func() {
		q = q[:0]
		for _, k := range srcs {
			if shut[k] != stamp && seen[k] != stamp {
				seen[k] = stamp
				q = append(q, k)
			}
		}
		for h := 0; h < len(q); h++ {
			k := q[h]
			x, z := k%W, k/W
			for d := 0; d < 4; d++ {
				x2, z2 := x, z
				switch d {
				case 0:
					x2--
				case 1:
					x2++
				case 2:
					z2--
				default:
					z2++
				}
				if x2 < 0 || z2 < 0 || x2 >= W || z2 >= H {
					continue
				}
				k2 := z2*W + x2
				if !open[k2] || shut[k2] == stamp || seen[k2] == stamp {
					continue
				}
				seen[k2] = stamp
				q = append(q, k2)
			}
		}
	}
	guarded := func(k int32) bool { return open[k] && seen[k] != stamp && shut[k] != stamp }
	// accept records a choke guarding the ground guarded() describes, with
	// sites beside its mouth; false when it guards nothing or has no site.
	accept := func(c *chokeCand) bool {
		if len(out.Chokes) >= MaxChokes {
			return false
		}
		ck := Choke{Width: (2*c.clr - 1) * 16 * chokeCell, Along: c.along, Routes: c.routes}
		ck.X, ck.Z = centre(c.k)
		ck.Home = guarded(hk)
		for _, t := range targets[1:] {
			if guarded(t) {
				ck.Spots++
			}
		}
		if !ck.Home && ck.Spots == 0 {
			return false
		}
		p := paths[c.path]
		i := int(c.idx)
		dx, dz := dir(c)
		for _, side := range [2]int64{1, -1} {
			found := 0
			for j := i - 1; j >= 0 && j >= i-chokeSiteR && found < 2; j-- {
				pk := p[j]
				px, pz := centre(pk)
				for off := clr[pk] - 1; off >= 2; off-- {
					x := int32(int64(px) + side*(-dz)*int64(off)*16*chokeCell/1000)
					z := int32(int64(pz) + side*dx*int64(off)*16*chokeCell/1000)
					k := macro(x, z)
					if k < 0 || !guarded(k) || clr[k] < 2 || !m.siteStands(x, z, site) {
						continue
					}
					// Sites stand a tower gap apart.
					near := false
					for q := int32(0); q < ck.NSites; q++ {
						near = near || Dist2(x, z, ck.Sites[q][0], ck.Sites[q][1]) < chokeSiteGap*chokeSiteGap
					}
					if near {
						continue
					}
					ck.Sites[ck.NSites] = [2]int32{x, z}
					ck.NSites++
					found++
					break
				}
			}
		}
		if ck.NSites == 0 {
			return false
		}
		bit := uint8(1) << uint(len(out.Chokes))
		for k := int32(0); k < n; k++ {
			if guarded(k) {
				out.guard[k] |= bit
			}
		}
		out.Chokes = append(out.Chokes, ck)
		return true
	}
	// A choke that alone closes off our start or spots guards them; the
	// rest are tried together (a base with two ramps is guarded by both).
	var joint []int
	for ci := range ch {
		c := &ch[ci]
		stamp++
		if !section(c) {
			continue
		}
		for _, k := range sec {
			shut[k] = stamp
		}
		flood()
		if !accept(c) {
			joint = append(joint, ci)
		}
	}
	if len(joint) > 1 && len(out.Chokes) < MaxChokes {
		stamp++
		for _, ci := range joint {
			if section(&ch[ci]) {
				for _, k := range sec {
					shut[k] = stamp
				}
			}
		}
		flood()
		for _, ci := range joint {
			c := &ch[ci]
			// A member is an entrance: its owner side lies in the jointly
			// guarded ground.
			p := paths[c.path]
			if o := p[max(int(c.idx)-3, 0)]; guarded(o) {
				accept(c)
			}
		}
	}
	return out
}

// siteStands reports whether a 3×3 building centred at (x, z) stands on
// ground every class can cross and outside every class's passage test.
func (m *MapInfo) siteStands(x, z int32, site []MoveClass) bool {
	cx, cz := x/16-1, z/16-1
	if cx < 1 || cz < 1 || cx+3 >= m.CellW-1 || cz+3 >= m.CellH-1 {
		return false
	}
	for i := range site {
		c := &site[i]
		for j := cz; j < cz+3; j++ {
			for k := cx; k < cx+3; k++ {
				if !m.cellLegal(j*m.CellW+k, c) {
					return false
				}
			}
		}
		if m.corridor(c, cx, cz, 3, 3) {
			return false
		}
	}
	return true
}
