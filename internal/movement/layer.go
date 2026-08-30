// Package movement — per-class stamped passability layers [04 §6.1 R-DOC04-B].
//
// One packed 2-bit-per-cell layer is stamped per movement class over the whole
// map (not per unit): the class's classifier chain runs per attribute cell at
// map load with the revision watermark zero, and the owner/building mask is an
// overlay tested separately from the packed terrain value [04 §6.1]. Rectangle
// restamps (dynamic blockers, building occupancy, feature changes) re-run the
// same per-cell chain over a rectangle and rewrite the same packing.
//
// The request revision pass runs at request init before any expansion: it
// advances the class record's revision watermark, re-stamps the footprints of
// recently-committed occupants, and refreshes the requester's commit tick. The
// record and its layer are shared by all requests of the class, so one
// request's revision is observed by the next [04 §6.1 R-DOC04-B][04 §7.3].
//
// Search consumption returns 0 out-of-bounds, 2 on an owner/building-mask bit
// miss, otherwise the packed terrain value; 1 (steep), 2 (mask miss) and 3
// (clear) all expand — only 0 hard-blocks [04 §6.1 R-DOC04-B]. This is the
// SC22 static-layer contract: mobile occupancy is not an A* wall; the dynamic
// channel into the layer is the occupant-age gate plus the revision pass
// [docs/SPEC_CONFLICTS SC22][04 §8.2 R-DOC04-D].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Stamped layer terrain values [04 §6.1 R-DOC04-B]. The classifier yields
// 0 blocked, 1 steep and 3 clear; value 2 never occurs in a stamped layer —
// it is produced only by the search consumer when the requester's bit is
// absent from the owner/building-mask word.
const (
	LayerBlocked  uint8 = 0
	LayerSteep    uint8 = 1
	LayerMaskMiss uint8 = 2 // search consumption only; never stamped [R-DOC04-B]
	LayerClear    uint8 = 3
)

// ClassLayer is one movement class's stamped layer: the class record (the
// embedded Profile), the map dimensions, the packed 2-bit terrain layer, the
// owner/building mask, and the revision watermark [04 §6.1][R-DOC04-A record
// tail: map dimensions, layer pointer, revision watermark].
//
// The record and layer are shared by reference by every path request of the
// class, so a revision armed by one request is observed by the next
// [04 §6.1 R-DOC04-B].
type ClassLayer struct {
	Profile // the class record the layer belongs to [R-DOC04-A]

	W, H    int32          // map dimensions in attribute cells [R-DOC04-A]
	Terrain *world.Terrain // terrain the classifier reads
	Grid    *OccupancyGrid // mobile occupancy the occupant-age gate reads

	// cells packs 2 bits per attribute cell: the dword at index
	// (z>>4)·W + x holds cells z & ~15 .. z|15 of column x, cell z in shift
	// (z&15)·2 [04 §6.1]. Size W · ceil(H/16) dwords.
	cells []uint32

	// owner is the building/owner mask: one 16-bit word per 2×2-cell block,
	// bit per player slot [04 §6.1 R-DOC04-B]. The consumer reads the word at
	// block ((z>>1)+(footZ>>2))·stride + (x>>1)+(footX>>2) with stride
	// W>>1 (W+1)>>1 for odd synthetic widths and tests the requester's bit
	// 1<<player [04 §6.1 R-DOC04-B].
	owner  []uint16
	stride int32

	// watermark is the class record's revision watermark. Zero until the
	// first request revision arms it; the map-load stamp therefore never
	// blocks on occupants — the static layer is terrain and features only
	// [04 §6.1 R-DOC04-B].
	watermark      uint32
	staticRevision uint64 // terrain static-obstacle revision last stamped [04 §7.3]

	// commits records each unit's last occupancy-commit tick, the unit
	// record's occupancy-commit field [04 §6.1 R-DOC04-B]. Lookup-only [I1];
	// the revision pass walks the unit pool slot-ascending, never this map.
	commits map[pool.Handle]uint32
}

// NewClassLayer allocates the layer for one class over the terrain and stamps
// the whole map [04 §6.1]: the single-cell classifier form runs per attribute
// cell with the watermark zero, so occupants never block at load, then the
// two-direction contagion pass runs [04 §6.1 R-DOC04-B]. Bounds are explicit:
// the layer covers exactly the terrain's attribute-cell lattice.
func NewClassLayer(p Profile, t *world.Terrain, grid *OccupancyGrid) *ClassLayer {
	l := &ClassLayer{
		Profile: p,
		W:       t.CellW,
		H:       t.CellH,
		Terrain: t,
		Grid:    grid,
		cells:   make([]uint32, int(t.CellW)*int((t.CellH+15)>>4)),
		stride:  (t.CellW + 1) >> 1,
		commits: make(map[pool.Handle]uint32),
	}
	if t != nil {
		l.staticRevision = t.StaticObstacleRevision()
	}
	l.owner = make([]uint16, int(l.stride)*int(((t.CellH+1)>>1)+2))
	l.stampAll()
	return l
}

// stampAll runs the single-cell classifier over every attribute cell and packs
// the result [04 §6.1]. Watermark is zero at map load, so the occupant-age
// gate never fires here [04 §6.1 R-DOC04-B].
func (l *ClassLayer) stampAll() {
	for z := int32(0); z < l.H; z++ {
		for x := int32(0); x < l.W; x++ {
			l.setValue(x, z, l.classify(x, z))
		}
	}
}

// classify stamps one candidate anchor for this movement class. The authored
// footprint is classified as one aggregate; a clear result is demoted to
// steep when any cell in the surrounding one-cell ring is non-clear
// [04 R-PATH-01 §2][04 R-MOV-03 §3].
func (l *ClassLayer) classify(cx, cz int32) uint8 {
	fx, fz := l.footprintSize()
	result := l.classifyRect(cx, cz, cx+fx-1, cz+fz-1)
	if result != LayerClear {
		return result
	}
	// The four strips cover the complete ring. Overlapping corner reads do not
	// change the all-clear predicate [04 R-MOV-03 §3].
	if l.classifyRect(cx-1, cz-1, cx+fx, cz-1) != LayerClear ||
		l.classifyRect(cx+fx, cz-1, cx+fx, cz+fz) != LayerClear ||
		l.classifyRect(cx-1, cz+fz, cx+fx, cz+fz) != LayerClear ||
		l.classifyRect(cx-1, cz-1, cx-1, cz+fz) != LayerClear {
		return LayerSteep
	}
	return LayerClear
}

// classifyRect applies immediate feature/occupant rejection, aggregates the
// derived terrain range as min-of-mins/max-of-maxes, then evaluates the depth,
// medium and slope gates once for the rectangle [04 §6.1 R-DOC04-B]
// [04 R-PATH-01 §2]. Coordinates are inclusive.
func (l *ClassLayer) classifyRect(x1, z1, x2, z2 int32) uint8 {
	if l.Terrain == nil {
		return LayerBlocked
	}
	if x1 < 0 || z1 < 0 || x2 < x1 || z2 < z1 || x2 >= l.W || z2 >= l.H {
		return LayerBlocked
	}
	minLow, maxHigh := int32(255), int32(0)
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			cell := l.Terrain.PlotAt(x, z)
			if cell == nil || isFeatureBlocked(l.Terrain, x, z) {
				return LayerBlocked
			}
			if l.Grid != nil {
				if id, ok := l.Grid.OccupantAt(Cell{X: x, Z: z}); ok && l.commits[pool.Handle(id)] < l.watermark {
					return LayerBlocked
				}
			}
			if h := int32(cell.MinHeight()); h < minLow {
				minLow = h
			}
			if h := int32(cell.MaxHeight()); h > maxHigh {
				maxHigh = h
			}
		}
	}
	sea := int32(l.Terrain.SeaLevel)
	if minLow < sea-l.MaxWaterDepth {
		return LayerBlocked
	}
	if maxHigh > sea-l.MinWaterDepth {
		return LayerBlocked
	}
	slope := maxHigh - minLow
	bad, max := l.BadWaterSlope, l.MaxWaterSlope
	if minLow >= sea {
		bad, max = l.BadSlope, l.MaxSlope
	}
	if slope <= int32(bad) {
		return LayerClear
	}
	if slope > int32(max) {
		return LayerBlocked
	}
	return LayerSteep
}

// setValue packs v into cell (x,z) [04 §6.1]: dword (z>>4)·W + x, shift
// (z&15)·2. Caller guarantees bounds.
func (l *ClassLayer) setValue(x, z int32, v uint8) {
	idx := (z>>4)*l.W + x
	shift := uint((z & 15) * 2)
	l.cells[idx] = (l.cells[idx] &^ (3 << shift)) | (uint32(v) << shift)
}

// Value returns the packed terrain value at cell (x,z), 0 out of bounds.
func (l *ClassLayer) Value(x, z int32) uint8 {
	if l == nil || x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	l.syncStaticRevision()
	return uint8((l.cells[(z>>4)*l.W+x] >> (uint(z&15) * 2)) & 3)
}

// StaticRevision returns the terrain revision represented by this layer.
func (l *ClassLayer) StaticRevision() uint64 {
	if l == nil {
		return 0
	}
	l.syncStaticRevision()
	return l.staticRevision
}

// syncStaticRevision refreshes terrain/profile-derived layer cells after a
// blocking feature mutation. Owner/building bits are a separate overlay and
// are deliberately preserved; the completed-structure writer is unresolved
// in this unit [04 §6.1][04 §8.2].
func (l *ClassLayer) syncStaticRevision() {
	if l == nil || l.Terrain == nil {
		return
	}
	revision := l.Terrain.StaticObstacleRevision()
	if revision == l.staticRevision {
		return
	}
	l.stampAll()
	l.staticRevision = revision
}

// RestampRect rewrites each candidate anchor through the footprint classifier
// and clips the anchor rectangle to the layer [04 R-MOV-03 §3].
func (l *ClassLayer) RestampRect(x1, z1, x2, z2 int32) {
	if l == nil {
		return
	}
	if x1 < 0 {
		x1 = 0
	}
	if z1 < 0 {
		z1 = 0
	}
	if x2 > l.W-1 {
		x2 = l.W - 1
	}
	if z2 > l.H-1 {
		z2 = l.H - 1
	}
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			l.setValue(x, z, l.classify(x, z))
		}
	}
}

// restampOccupantRect rewrites every requester anchor whose footprint or
// classifier ring can read the committed occupant rectangle. The traced
// inclusive bounds are [origin-requesterFootprint, origin+occupantSize] on
// each axis; RestampRect supplies the layer-edge clipping
// [04 R-PATH-01 §2][04 R-MOV-03 §3][fmt tdf][fmt fbi].
func (l *ClassLayer) restampOccupantRect(anchor Cell, footX, footZ int16) {
	if l == nil {
		return
	}
	occupantX, occupantZ := int32(footX), int32(footZ)
	if occupantX <= 0 {
		occupantX = 1
	}
	if occupantZ <= 0 {
		occupantZ = 1
	}
	requesterX, requesterZ := l.footprintSize()
	l.RestampRect(
		anchor.X-requesterX,
		anchor.Z-requesterZ,
		anchor.X+occupantX,
		anchor.Z+occupantZ,
	)
}

// Watermark returns the class record's revision watermark [04 §6.1 R-DOC04-B].
func (l *ClassLayer) Watermark() uint32 {
	if l == nil {
		return 0
	}
	return l.watermark
}

// CommitTick returns the recorded last occupancy-commit tick for a unit and
// whether one is recorded.
func (l *ClassLayer) CommitTick(h pool.Handle) (uint32, bool) {
	if l == nil {
		return 0, false
	}
	c, ok := l.commits[h]
	return c, ok
}

// NoteCommit records a unit's last occupancy-commit tick [04 §6.1 R-DOC04-B].
// The production caller is the occupancy commit path (the grid Stamp sites);
// until that wiring lands, the revision pass only sees ticks noted here or by
// its own requester refresh.
func (l *ClassLayer) NoteCommit(h pool.Handle, tick uint32) {
	if l == nil || h == 0 {
		return
	}
	l.commits[h] = tick
}

// revisionWatermark is the request revision pass's corrected watermark
// arithmetic: max(tick, 31) − 30 [04 R-PATH-01 §2]. The first revision
// therefore arms the class layer at 1, so a frozen creation stamp at tick zero
// enters the first crossed window instead of remaining permanently invisible
// to path search.
func revisionWatermark(tick uint32) uint32 {
	if tick < 31 {
		return 1
	}
	return tick - 30
}

// AnchorSource resolves a unit's committed footprint rectangle [04 §8.2]
// C23 cached anchor. The revision pass re-stamps the occupant's own rectangle,
// which can be larger than the movement class whose layer is being revised
// [04 R-MOV-03 §3].
type AnchorSource interface {
	CommittedFootprint(h pool.Handle) (anchor Cell, footX, footZ int16, ok bool)
}

// CommittedFootprint adapts the System's collision states to AnchorSource; it
// is the production resolver for the revision pass.
func (s *System) CommittedFootprint(h pool.Handle) (Cell, int16, int16, bool) {
	if s == nil {
		return Cell{}, 0, 0, false
	}
	coll, ok := s.Collisions[h]
	if !ok || coll == nil {
		return Cell{}, 0, 0, false
	}
	fx, fz := coll.FootPrintX, coll.FootPrintZ
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	return coll.CachedAnchor, fx, fz, true
}

// Revise is the request revision pass run before expansion [04 R-PATH-01 §2]
// [04 R-MOV-03 §3]. It advances the shared class watermark and re-stamps the
// cohort whose commit ticks lie in the window just crossed, [old,new). The
// requester is handled separately: its real commit tick is saved, replaced by
// the current tick while its own stale rectangle is refreshed, then restored.
// This keeps self occupancy transparent without falsifying the occupant-age
// clock that later requests observe.
func (l *ClassLayer) Revise(tick uint32, requester pool.Handle, w *units.World, anchors AnchorSource) {
	if l == nil {
		return
	}
	// A feature mutation refreshes the terrain layer before the request's
	// temporary self-commit/restamp. Refreshing lazily on the first expansion
	// would run after the requester commit is restored and can make the unit
	// block its own start cell.
	l.syncStaticRevision()
	oldWatermark := l.watermark
	newWatermark := revisionWatermark(tick)
	requesterCommit, requesterHadCommit := l.commits[requester]
	if requester != 0 {
		l.commits[requester] = tick
	}
	l.watermark = newWatermark
	defer func() {
		if requester == 0 {
			return
		}
		if requesterHadCommit {
			l.commits[requester] = requesterCommit
		} else {
			delete(l.commits, requester)
		}
	}()
	if w == nil || anchors == nil {
		return
	}
	if requester != 0 && requesterCommit < oldWatermark {
		if anchor, fx, fz, ok := anchors.CommittedFootprint(requester); ok {
			l.restampOccupantRect(anchor, fx, fz)
		}
	}
	if newWatermark == oldWatermark {
		return
	}
	// Deterministic full physical unit-pool walk, slots ascending
	// [I1][01 §6.1–§6.2][04 R-MOV-03 §3]. Capacity is the total usable
	// record count excluding the null slot.
	for h := pool.Handle(1); int(h) <= w.Capacity(); h++ {
		if h == requester {
			continue
		}
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue // alive state bit not carried [R-DOC04-B]
		}
		c := l.commits[h] // absent is the unit record's zero-initialized tick
		if c < oldWatermark || c >= newWatermark {
			continue // outside the crossed [old,new) window [04 R-MOV-03 §3]
		}
		anchor, fx, fz, ok := anchors.CommittedFootprint(h)
		if !ok {
			continue
		}
		l.restampOccupantRect(anchor, fx, fz)
	}
}

// ClassLayers is the per-class layer registry: one layer per movement class,
// allocated at first request per class (or eagerly at battle entry by calling
// For over the compiled class table) with explicit bounds [04 §6.1].
type ClassLayers struct {
	terrain *world.Terrain
	grid    *OccupancyGrid
	world   *units.World
	anchors AnchorSource

	byName map[string]*ClassLayer // lookup only; never iterated [I1]
	names  []string               // allocation order, for deterministic inspection
}

// NewClassLayers binds the registry to the battle's terrain, occupancy grid,
// unit world and committed-anchor source.
func NewClassLayers(t *world.Terrain, grid *OccupancyGrid, w *units.World, anchors AnchorSource) *ClassLayers {
	return &ClassLayers{
		terrain: t,
		grid:    grid,
		world:   w,
		anchors: anchors,
		byName:  make(map[string]*ClassLayer),
	}
}

// BindWorld refreshes the unit-pool source used by every later request
// revision. The registry may be allocated by an occupancy commit before the
// session's first unit-sweep bind, so construction-time capture alone is not
// sufficient [01 §6.1–§6.2][04 R-MOV-03 §3].
func (c *ClassLayers) BindWorld(w *units.World) {
	if c == nil {
		return
	}
	c.world = w
}

// For returns the layer for one movement class, allocating and map-load
// stamping it at first request [04 §6.1]. Each class name owns one record and
// one layer, shared by all requests of the class [04 §6.1 R-DOC04-B].
func (c *ClassLayers) For(name string, p Profile) *ClassLayer {
	if c == nil {
		return nil
	}
	if l, ok := c.byName[name]; ok {
		return l
	}
	l := NewClassLayer(p, c.terrain, c.grid)
	c.byName[name] = l
	c.names = append(c.names, name)
	return l
}

// ReviseFor ensures the class layer, runs the request revision pass on it, and
// returns it — the request-init entry point [04 §6.1 R-DOC04-B][04 §7.3].
func (c *ClassLayers) ReviseFor(name string, p Profile, requester pool.Handle, tick uint32) *ClassLayer {
	l := c.For(name, p)
	l.Revise(tick, requester, c.world, c.anchors)
	return l
}

// Names reports class names in allocation order (diagnostic; deterministic).
func (c *ClassLayers) Names() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.names...)
}

// blockIndex computes the owner/building-mask word index for a search cell
// with the requester's footprint [04 §6.1 R-DOC04-B]:
// ((z>>1)+(footZ>>2))·stride + (x>>1)+(footX>>2). The foot>>2 terms shift the
// block toward the footprint's centre half.
func (l *ClassLayer) blockIndex(x, z int32, footX, footZ int16) int32 {
	bx := (x >> 1) + int32(footX>>2)
	bz := (z >> 1) + int32(footZ>>2)
	return bz*l.stride + bx
}

// SetOwnerRect sets the player's bit in every 2×2 block the footprint
// rectangle anchored at anchor covers. This is the building-occupancy write:
// a building's occupancy commit sets its owner's bits, and its removal clears
// them [04 §6.1 "building occupancy is an overlay"].
//
// Supported inference: the mask's structure (one 16-bit word per 2×2 block,
// bit per player slot, requester's bit tested at search time) is established;
// the write site — set at building occupancy commit, clear at removal — is the
// reading consistent with the owner/building-mask name and the occupancy
// restamp call sites.
func (l *ClassLayer) SetOwnerRect(anchor Cell, footX, footZ int16, player uint8) {
	l.writeOwnerRect(anchor, footX, footZ, player, true)
}

// ClearOwnerRect clears the player's bit over the rectangle's blocks.
func (l *ClassLayer) ClearOwnerRect(anchor Cell, footX, footZ int16, player uint8) {
	l.writeOwnerRect(anchor, footX, footZ, player, false)
}

func (l *ClassLayer) writeOwnerRect(anchor Cell, footX, footZ int16, player uint8, set bool) {
	if l == nil || player > 15 {
		return
	}
	fx, fz := int32(footX), int32(footZ)
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	x1, z1 := anchor.X, anchor.Z
	x2, z2 := anchor.X+fx-1, anchor.Z+fz-1
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			idx := l.blockIndex(x, z, 0, 0)
			if idx < 0 || idx >= int32(len(l.owner)) {
				continue
			}
			if set {
				l.owner[idx] |= 1 << player
			} else {
				l.owner[idx] &^= 1 << player
			}
		}
	}
}

// Passable is the search-side passability test [04 §6.1 R-DOC04-B]:
// out-of-bounds → 0; the requester's bit absent from the coarse
// owner/building-mask word → 2; otherwise the packed terrain value. EVERY
// consumer — the A* expansion and all greedy-ray probes — treats the result as
// passable iff it is nonzero: only the terrain value 0 hard-blocks; steep (1),
// owner-mask miss (2) and clear (3) all expand [04 §6.1 R-DOC04-B].
//
// An index past the mask words (shifted footprint at the map's far corner, a
// degenerate rectangle-retail-reads-a-neighbouring-word case) is treated as a
// bit miss → 2, which is traversable and so cannot invent a wall.
func (l *ClassLayer) Passable(x, z int32, footX, footZ int16, player uint8) uint8 {
	if l == nil || x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	idx := l.blockIndex(x, z, footX, footZ)
	if idx < 0 || idx >= int32(len(l.owner)) || l.owner[idx]&(1<<player) == 0 {
		return LayerMaskMiss
	}
	return l.Value(x, z)
}
