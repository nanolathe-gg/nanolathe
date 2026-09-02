// Package movement — per-class stamped passability layers [04 §6.1 R-DOC04-B].
//
// One packed 2-bit-per-cell layer is stamped per movement class over the whole
// map (not per unit): the class's classifier chain runs per attribute cell at
// map load with the revision watermark zero, and the per-player MAPPING WORD
// GRID is a separate array tested before the packed terrain value [04 §6.1]
// [04 R-PATH-01 §2]. Rectangle restamps (dynamic blockers, building occupancy,
// feature changes) re-run the same per-cell chain over a rectangle and rewrite
// the same packing.
//
// The request revision pass runs at request init before any expansion: it
// advances the class record's revision watermark, re-stamps the footprints of
// recently-committed occupants, and refreshes the requester's commit tick. The
// record and its layer are shared by all requests of the class, so one
// request's revision is observed by the next [04 §6.1 R-DOC04-B][04 §7.3].
//
// Search consumption returns 0 out-of-bounds, 2 when the requesting player's
// slot bit is absent from the mapping word — the block is UNEXPLORED — and
// otherwise the packed terrain value; 1 (steep), 2 (unmapped) and 3 (clear)
// all expand — only 0 hard-blocks [04 §6.1 R-DOC04-B][04 R-PATH-01 §2]. Retail
// units therefore path optimistically straight through fog. This is the
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
// it is produced only by the search consumer when the requesting player's slot
// bit is absent from the mapping word for the block [04 R-PATH-01 §2].
const (
	LayerBlocked  uint8 = 0
	LayerSteep    uint8 = 1
	LayerUnmapped uint8 = 2 // search consumption only; never stamped [R-DOC04-B]
	LayerClear    uint8 = 3
)

// MappingWordSource reads one word of the per-player mapping word grid at a
// TILE coordinate pair — one 16-bit word per 2×2-cell tile, bits 0..9 one per
// player slot, ORed by the phase-5 LOS stamp sweep and never cleared
// [03 R-LAYER §1]. ok is false when no grid is bound or the index falls outside
// the allocation.
//
// The grid belongs to the visibility publisher, not to this package: it has
// exactly three writers — the map-load zero (or all-ones) fill, the bulk
// wipe-and-rebuild and the phase-5 per-player LOS stamp — and NO
// occupancy-commit writer, so a movement-side copy would stay all-zero forever
// and the search's bit-miss value would never occur [04 R-PATH-01 §14]
// [03 R-LAYER §1 "Confirmation (2026-09-02, RWU-19-30)"]. The class layer
// therefore holds a VIEW of the publisher's array through this port, never an
// array of its own.
type MappingWordSource func(tileX, tileZ int32) (uint16, bool)

// ClassLayer is one movement class's stamped layer: the class record (the
// embedded Profile), the map dimensions, the packed 2-bit terrain layer, a view
// of the visibility publisher's mapping word grid, and the revision watermark
// [04 §6.1][R-DOC04-A record tail: map dimensions, layer pointer, revision
// watermark].
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

	// mapping is the view of the visibility publisher's per-player mapping word
	// grid the search's coarse test reads [04 R-PATH-01 §2][04 R-PATH-01 §14].
	// Nil means "no grid bound", which makes Passable fall through to the
	// terrain value rather than invent a word; production binds it through the
	// registry.
	mapping MappingWordSource

	// movers answers whether an occupant has a mover structure, which the
	// occupant-age gate needs: a building has none [04 R-PATH-01 §14].
	movers MoverSource

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
		commits: make(map[pool.Handle]uint32),
	}
	if t != nil {
		l.staticRevision = t.StaticObstacleRevision()
	}
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

// classify stamps one candidate anchor for this movement class. Every covered
// cell is classified on its own derived pair and the anchor takes the MINIMUM
// tier over the footprint; a clear result is then demoted to steep when any
// cell of the surrounding one-cell ring is non-clear [04 R-SLOPE-01 §3]
// [04 R-PATH-01 §2][04 R-MOV-03 §3].
//
// This is the closed form of the map-load builder's two separable window
// minima: 0 iff any footprint cell is 0, 3 iff every cell of the
// (fx+2) × (fz+2) footprint-plus-ring rectangle is 3, 1 otherwise
// [04 R-SLOPE-01 §3 item 1]. Corrected by WU-19-46: the footprint used to be
// classified on one min-of-mins/max-of-maxes height span, a form that belongs
// to the structure placement validator alone and that judged a 2×2 class on
// the height range of a 3×3 corner window.
//
// The bound is the map-load builder's: 0 exactly when the footprint leaves the
// map. RestampRect adds the restamp's stricter one on top [04 R-SLOPE-01 §3].
func (l *ClassLayer) classify(cx, cz int32) uint8 {
	fx, fz := l.footprintSize()
	if l.Terrain == nil || cx < 0 || cz < 0 || cx+fx > l.W || cz+fz > l.H {
		return LayerBlocked
	}
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

// classifyRect runs the per-cell chain on each cell of an inclusive rectangle
// and returns the MINIMUM tier over those cells [04 R-SLOPE-01 §3 item 2]: a
// blocked cell returns immediately, a steep cell lowers a running clear to
// steep. Cells outside the map are tier 0, so a rectangle leaving the map is
// blocked.
func (l *ClassLayer) classifyRect(x1, z1, x2, z2 int32) uint8 {
	if l.Terrain == nil || x2 < x1 || z2 < z1 {
		return LayerBlocked
	}
	result := LayerClear
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			switch l.classifyCell(x, z) {
			case LayerBlocked:
				return LayerBlocked
			case LayerSteep:
				result = LayerSteep
			}
		}
	}
	return result
}

// classifyCell is the layer's per-cell chain: the profile's terrain chain
// (feature gate, depth gates, medium split, slope tier) plus the occupant-age
// gate, which needs the layer's grid and watermark [04 §6.1 R-DOC04-B
// steps 1-6][04 R-SLOPE-01 §2].
//
// Retail runs the occupant-age gate as step 2, between the feature gate and
// the deep gate; both it and the terrain gates only ever return blocked, so
// testing it after the terrain chain yields the same tier. The watermark is
// zero until the first request revision arms it, so the map-load stamp never
// blocks on a MOBILE occupant — for movers the static layer is terrain and
// features only. A building takes the mover-null arm below and blocks at every
// watermark, map load included [04 R-PATH-01 §14].
func (l *ClassLayer) classifyCell(x, z int32) uint8 {
	tier := LayerClear
	switch l.Profile.classifyCell(l.Terrain, x, z) {
	case ClassBlocked:
		return LayerBlocked
	case ClassSteep:
		tier = LayerSteep
	}
	if l.Grid != nil {
		if id, ok := l.Grid.OccupantAt(Cell{X: x, Z: z}); ok {
			// The gate reads the OCCUPANT's mover, not the unit record: the
			// occupancy-commit tick is a word on the mover structure, so the
			// test is `mover == null || mover.commitTick < watermark`
			// [04 R-PATH-01 §14], correcting [R-DOC04-B]'s "a cell's mobile
			// occupant whose last occupancy-commit tick predates the class
			// record's revision watermark blocks" and its rider that a
			// building's frozen commit tick is eventually passed by the
			// watermark. A building has no mover, so it takes the null arm and
			// hard-blocks UNCONDITIONALLY — from the first classification that
			// finds it in the occupant word, whatever the watermark, and
			// without waiting for a request revision to arm one.
			h := pool.Handle(id)
			if l.movers != nil && !l.movers.HasMover(h) {
				return LayerBlocked
			}
			if l.commits[h] < l.watermark {
				return LayerBlocked
			}
		}
	}
	return tier
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
	// The restamp's own bound, stricter than the map-load window's: a
	// rectangle whose extent reaches column W−1 or row H−1 is 0 outright
	// [04 R-SLOPE-01 §3 item 2][04 R-COLL-01 §2 steps 3-4]. In retail both
	// bounds agree on a loaded map because those strips are voided
	// [03 R-TERR-01 §2].
	fx, fz := l.footprintSize()
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			if x+fx >= l.W || z+fz >= l.H {
				l.setValue(x, z, LayerBlocked)
				continue
			}
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

// MoverSource answers whether a unit carries a mover structure. Retail's
// occupancy-commit tick is a word on that structure, so a unit with no mover
// has no tick to compare and the occupant-age gate blocks it outright
// [04 R-PATH-01 §14]. Buildings are exactly the units with no mover
// [04 R-COLL-01 §1 "a building has no mover"]; Nanolathe gives every unit a
// CollisionState and distinguishes the two by its Building flag.
type MoverSource interface {
	HasMover(h pool.Handle) bool
}

// HasMover adapts the System's collision states to MoverSource: a live
// non-building collision record is the mover [04 R-PATH-01 §14].
func (s *System) HasMover(h pool.Handle) bool {
	if s == nil {
		return false
	}
	coll, ok := s.Collisions[h]
	if !ok || coll == nil {
		return false
	}
	return !coll.Building
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
	movers  MoverSource
	mapping MappingWordSource

	byName map[string]*ClassLayer // lookup only; never iterated [I1]
	names  []string               // allocation order, for deterministic inspection
}

// NewClassLayers binds the registry to the battle's terrain, occupancy grid,
// unit world and committed-anchor source.
func NewClassLayers(t *world.Terrain, grid *OccupancyGrid, w *units.World, anchors AnchorSource) *ClassLayers {
	c := &ClassLayers{
		terrain: t,
		grid:    grid,
		world:   w,
		anchors: anchors,
		byName:  make(map[string]*ClassLayer),
	}
	// The committed-anchor adapter and the mover predicate are the same
	// System object in production; take the second port from it when it
	// implements one [04 R-PATH-01 §14].
	if m, ok := anchors.(MoverSource); ok {
		c.movers = m
	}
	return c
}

// BindMappingWord installs the visibility publisher's mapping-word view on the
// registry and on every layer already allocated [04 R-PATH-01 §14]. A nil
// source is ignored so a caller with no binding cannot silently unbind a live
// grid. Iteration is over the allocation-order slice, never the map [I1].
func (c *ClassLayers) BindMappingWord(src MappingWordSource) {
	if c == nil || src == nil {
		return
	}
	c.mapping = src
	for _, name := range c.names {
		if l := c.byName[name]; l != nil {
			l.mapping = src
		}
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
	if name == "" {
		name = scratchLayerKey(p)
	}
	if l, ok := c.byName[name]; ok {
		return l
	}
	l := NewClassLayer(p, c.terrain, c.grid)
	// A new layer inherits both registry ports before its first classification
	// so its map-load stamp already sees buildings [04 R-PATH-01 §14].
	l.movers = c.movers
	l.mapping = c.mapping
	if c.movers != nil {
		l.stampAll()
	}
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

// mappingTile is the tile pair the search's coarse test reads in the mapping
// word grid [04 R-PATH-01 §2 step 2]: bx = (x>>1) + (FootPrintX>>2),
// bz = (z>>1) + (FootPrintZ>>2) — half-resolution coordinates offset by a
// quarter of the class's authored footprint. It is the same pair the aircraft
// landing test forms [04 R-AIR-01 §6a][04 R-AIR-01 §14.2]; the stride multiply
// belongs to whoever holds the grid, so the pair is what crosses the port.
func mappingTile(x, z int32, footX, footZ int16) (bx, bz int32) {
	return (x >> 1) + int32(footX>>2), (z >> 1) + int32(footZ>>2)
}

// Passable is the search-side passability test [04 §6.1 R-DOC04-B] in the exact
// order [04 R-PATH-01 §2] gives:
//
//  1. cell outside the class record's stamped extent (unsigned, so negatives
//     fail too) -> 0;
//  2. mapping-block index outside `mapWidth>>1` x `mapHeight>>1` -> 0;
//  3. requesting player's slot bit ABSENT from that block's mapping word -> 2,
//     without reading the terrain layer at all;
//  4. otherwise the stamped two-bit terrain value 0/1/3.
//
// Value 2 means "this block is unexplored by the requesting player", and EVERY
// consumer — the A* expansion and all greedy-ray probes — treats the result as
// passable iff it is nonzero: only the terrain value 0 hard-blocks, so steep
// (1), unmapped (2) and clear (3) all expand. Retail units path optimistically
// straight through fog and consult the terrain layer only where their owner has
// already mapped the ground [04 R-PATH-01 §2].
//
// With no grid bound the terrain value is returned rather than a word being
// invented; that fallback is stricter than retail and bounded — it only makes
// the search refuse ground it would otherwise cross blind.
func (l *ClassLayer) Passable(x, z int32, footX, footZ int16, player uint8) uint8 {
	if l == nil || x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	bx, bz := mappingTile(x, z, footX, footZ)
	if bx < 0 || bz < 0 || bx >= l.W>>1 || bz >= l.H>>1 {
		return LayerBlocked
	}
	if l.mapping != nil {
		// airMappingBitSet is this package's shared mapping-word bit test: ten
		// usable slot bits, the reader's own owner slot [03 R-LAYER §1].
		if word, ok := l.mapping(bx, bz); ok && !airMappingBitSet(word, player) {
			return LayerUnmapped
		}
	}
	return l.Value(x, z)
}
