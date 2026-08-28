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
	watermark uint32

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
	l.owner = make([]uint16, int(l.stride)*int(((t.CellH+1)>>1)+2))
	l.stampAll()
	l.Contagion()
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

// classify is the per-cell passability classifier [04 §6.1 R-DOC04-B], the
// contract for both the map-load single-cell stamp and rectangle restamps.
// Per attribute cell, in order, all comparisons on the derived 2×2 heights
// hmin/hmax (the plot expansion's per-cell derived minimum/maximum):
//
//  1. Feature gate: a resolved blocking feature, a stale feature identity or
//     a void cell blocks; no feature passes.
//  2. Occupant-age gate: the cell's mobile occupant whose last
//     occupancy-commit tick predates the revision watermark blocks.
//  3. Deep gate (signed 32-bit): blocked iff hmin < SeaLevel − MaxWaterDepth,
//     the depth a sign-extended 16-bit record field.
//  4. Shallow gate (signed 32-bit): blocked iff hmax > SeaLevel − MinWaterDepth.
//  5. Medium split (unsigned byte): land iff hmin >= SeaLevel.
//  6. Slope tier (unsigned byte, slope = hmax − hmin): slope <= Bad (land) or
//     BadWater (water) → 3 clear; slope > Max (land) or MaxWater (water) →
//     0 blocked; otherwise → 1 steep. Equality with the bad threshold is
//     clear; equality with the max threshold is steep, not blocked.
func (l *ClassLayer) classify(cx, cz int32) uint8 {
	if l.Terrain == nil {
		return LayerBlocked
	}
	cell := l.Terrain.PlotAt(cx, cz)
	if cell == nil {
		return LayerBlocked // out of bounds [04 §6.1]
	}
	// 1. Feature gate [04 §6.1 R-DOC04-B][04 §6.2][GAP T14].
	if isFeatureBlocked(l.Terrain, cx, cz) {
		return LayerBlocked
	}
	// 2. Occupant-age gate [04 §6.1 R-DOC04-B]: blocks iff the occupant's
	// last occupancy-commit tick predates the revision watermark. The tick is
	// a unit field zero until its first occupancy commit, so a missing record
	// reads as zero. With the watermark zero (map load) the gate never fires.
	if l.Grid != nil {
		if id, ok := l.Grid.OccupantAt(Cell{X: cx, Z: cz}); ok {
			c, have := l.commits[pool.Handle(id)]
			if !have {
				c = 0
			}
			if c < l.watermark {
				return LayerBlocked
			}
		}
	}
	hmin := int32(cell.MinHeight()) // derived 2×2 minimum [fmt tnt][04 §6.1]
	hmax := int32(cell.MaxHeight()) // derived 2×2 maximum [fmt tnt][04 §6.1]
	sea := int32(l.Terrain.SeaLevel)
	// 3. Deep gate — signed 32-bit on the sign-extended 16-bit depth field
	// [04 §6.1 R-DOC04-B]. Equality passes.
	if hmin < sea-l.MaxWaterDepth {
		return LayerBlocked
	}
	// 4. Shallow gate — signed 32-bit [04 §6.1 R-DOC04-B]. Equality passes.
	if hmax > sea-l.MinWaterDepth {
		return LayerBlocked
	}
	// 5. Medium split — unsigned byte: land iff hmin >= SeaLevel
	// [04 §6.1 R-DOC04-B]. hmin/hmax/sea are 0..255 bytes, so plain integer
	// comparison is the unsigned byte comparison.
	slope := hmax - hmin // byte difference, 0..255 [04 §6.1 R-DOC04-B]
	bad, max := l.BadWaterSlope, l.MaxWaterSlope
	if hmin >= sea {
		bad, max = l.BadSlope, l.MaxSlope
	}
	// 6. Slope tier [04 §6.1 R-DOC04-B].
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
	return uint8((l.cells[(z>>4)*l.W+x] >> (uint(z&15) * 2)) & 3)
}

// RestampRect re-runs the per-cell classifier over the rectangle and rewrites
// the packing per cell [04 §6.1 rectangle restamp; R-DOC04-B footprint form:
// the same chain per cell inside the footX×footZ loop]. The rectangle is
// clamped to the map.
//
// TODO(question): the raw trail attaches the two contagion passes to the
// map-load stamp only and does not show one over restamp rectangles; whether a
// restamp re-runs contagion (and over which window) is unresolved. Implemented
// without contagion, matching the trail.
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

// Contagion runs the two-direction contagion pass [04 §6.1 R-DOC04-B]: a row
// scan then a column scan, each demoting a clear (3) cell to 1 (steep) when a
// 4-neighbour inside the scan window is not 3. Neighbours outside the map are
// not in the window, so map-edge cells are not demoted for the edge. Each
// sweep evaluates its neighbours against the layer as it stood at the sweep's
// start, so demotion marks the cells edging an obstruction without cascading
// across the map.
//
// TODO(question): whether each sweep reads pre-sweep or in-progress values is
// unresolved; the sweep-anchored reading implemented here keeps the documented
// effect — passable cells edging an obstruction become the steep tier.
func (l *ClassLayer) Contagion() {
	if l == nil {
		return
	}
	// Row sweep: horizontal neighbours against the row sweep's start state.
	rowSnap := append([]uint32(nil), l.cells...)
	value := func(cells []uint32, x, z int32) uint8 {
		return uint8((cells[(z>>4)*l.W+x] >> (uint(z&15) * 2)) & 3)
	}
	for z := int32(0); z < l.H; z++ {
		for x := int32(0); x < l.W; x++ {
			if value(l.cells, x, z) != LayerClear {
				continue
			}
			if (x > 0 && value(rowSnap, x-1, z) != LayerClear) ||
				(x+1 < l.W && value(rowSnap, x+1, z) != LayerClear) {
				l.setValue(x, z, LayerSteep)
			}
		}
	}
	// Column sweep: vertical neighbours against the column sweep's start
	// state (which includes the row sweep's demotions).
	colSnap := append([]uint32(nil), l.cells...)
	for x := int32(0); x < l.W; x++ {
		for z := int32(0); z < l.H; z++ {
			if value(l.cells, x, z) != LayerClear {
				continue
			}
			if (z > 0 && value(colSnap, x, z-1) != LayerClear) ||
				(z+1 < l.H && value(colSnap, x, z+1) != LayerClear) {
				l.setValue(x, z, LayerSteep)
			}
		}
	}
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

// revisionWatermark is the request revision pass's watermark arithmetic
// [04 §6.1 R-DOC04-B]: max(tick, 30) − 30. The first revision (any tick below
// 30) arms the watermark at zero; from tick 30 on the armed window is the
// preceding 30 ticks, so an occupant is stale — blocked by the gate — once its
// last commit predates the window.
func revisionWatermark(tick uint32) uint32 {
	if tick < 30 {
		return 0
	}
	return tick - 30
}

// AnchorSource resolves a unit's committed footprint anchor cell [04 §8.2]
// C23 cached anchor. The revision pass re-stamps footprints at the committed
// anchor, the same rectangle occupancy commits maintain.
type AnchorSource interface {
	CommittedAnchor(h pool.Handle, footX, footZ int16) (Cell, bool)
}

// CommittedAnchor adapts the System's collision states to AnchorSource; it is
// the production resolver for the revision pass.
func (s *System) CommittedAnchor(h pool.Handle, footX, footZ int16) (Cell, bool) {
	if s == nil {
		return Cell{}, false
	}
	coll, ok := s.Collisions[h]
	if !ok || coll == nil {
		return Cell{}, false
	}
	return coll.CachedAnchor, true
}

// Revise is the request revision pass [04 §6.1 R-DOC04-B], run at request
// init before any expansion:
//
//   - the class record's revision watermark is set to max(tick, 30) − 30;
//   - the requesting unit's own commit tick is refreshed, so its own footprint
//     re-stamps below and the requester never blocks itself;
//   - every unit carrying the alive state bit (bit 28 of the retail status
//     word; units.Unit.Alive here) whose last occupancy-commit tick falls in
//     the watermark window has its footprint rectangle re-stamped into the
//     layer.
//
// The window is the armed 30-tick window ending at the request tick: commits
// with tick >= watermark. The classifier's occupant-age gate then gives the
// documented effect — units that committed within the last 30 ticks do not
// block the layer (their footprints are re-stamped and pass the gate), while
// an occupant whose commit tick predates the watermark blocks any cell it
// occupies that is re-stamped afterwards [04 §6.1 R-DOC04-B]. Footprint
// extents are the layer's own class record's — the layer is class-uniform.
func (l *ClassLayer) Revise(tick uint32, requester pool.Handle, w *units.World, anchors AnchorSource) {
	if l == nil {
		return
	}
	l.watermark = revisionWatermark(tick)
	if requester != 0 {
		l.commits[requester] = tick
	}
	if w == nil || anchors == nil {
		return
	}
	fx, fz := l.footprintSize()
	// Deterministic unit-pool walk, slots ascending [I1][04 §6.1 R-DOC04-B].
	for h := pool.Handle(1); int(h) <= w.Capacity(); h++ {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue // alive state bit not carried [R-DOC04-B]
		}
		c, ok := l.commits[h]
		if !ok || c < l.watermark {
			continue // outside the window: not re-stamped
		}
		anchor, ok := anchors.CommittedAnchor(h, int16(fx), int16(fz))
		if !ok {
			continue
		}
		l.RestampRect(anchor.X, anchor.Z, anchor.X+fx-1, anchor.Z+fz-1)
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
