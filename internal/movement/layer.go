// Per-class stamped passability layers [04 §6.1 R-DOC04-B].
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
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
// The grid belongs to the visibility publisher, not to this package: its
// closed writer census is five — the map loader's allocate-and-zero (or
// all-ones) fill, the bulk wipe-and-rebuild, the phase-5 per-player LOS
// stamp sweep, the two-slot mapping-share routine (not on a single-player
// path) and the saved-game restore, which copies the save's `Mapping` box
// straight into the array — and NO occupancy, unit, feature or construction
// writer, so a movement-side copy would stay all-zero forever and the search's
// bit-miss value would never occur [04 R-PATH-01 §14][03 R-LAYER §1]. The class
// layer therefore holds a VIEW of the publisher's array through this port,
// never an array of its own.
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
	cells                   []uint32
	stampScratch, stampRows []uint8

	// restampTiers holds the per-cell tiers of the rectangle a restamp is
	// classifying, restampW wide from (restampX, restampZ); see
	// fillRestampTiers. Scratch only: it is rewritten by every restamp before
	// it is read.
	restampTiers                 []uint8
	restampX, restampZ, restampW int32

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
	// first request revision at a tick past 30 arms it; the map-load stamp
	// therefore never blocks on occupants — the static layer is terrain and
	// features only [04 §6.1 R-DOC04-B].
	watermark uint32

	// commits records each unit's last occupancy-commit tick, the unit
	// record's occupancy-commit field [04 §6.1 R-DOC04-B]. It is a dense row
	// addressed by handle (see commitWord); the revision pass walks the unit
	// pool slot-ascending and reads it by handle, never by iterating it [I1].
	commits []commitWord

	// fullStamps counts how many times this layer has been rebuilt end to end
	// by stampAll. It is a HOST diagnostic and nothing else: no simulation
	// branch reads it, it consumes no random draw, and a host samples it
	// between ticks to attribute its own timing (docs/SIM_BENCHMARK.md). A
	// full rebuild visits every attribute cell, so a host that sees this move
	// knows the tick it just measured paid for one.
	fullStamps uint64
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
	}
	// No nil guard on t: the struct literal above reads four of its fields, so
	// a nil terrain has already panicked by here. A guard that stands after the
	// dereferences it claims to protect reads as if nil were a supported input.
	l.stampAll()
	return l
}

// classifyCell is the layer's per-cell chain: the profile's terrain chain
// (feature gate, depth gates, medium split, slope tier) plus the occupant-age
// gate, which needs the layer's grid and watermark [04 §6.1 R-DOC04-B
// steps 1-6][04 R-SLOPE-01 §2].
//
// Retail runs the occupant-age gate as step 2, between the feature gate and
// the deep gate; both it and the terrain gates only ever return blocked, so
// testing it after the terrain chain yields the same tier. The watermark is
// zero until the first request revision at a tick past 30 arms it, so the
// map-load stamp never blocks on a MOBILE occupant, and neither does any
// classification during the opening 31 ticks — for movers the static layer is
// terrain and features only. A building takes the mover-null arm below and
// blocks at every watermark, map load included [04 R-PATH-01 §14].
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
			if l.commitTick(h) < l.watermark {
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
//
// It is a plain read. A feature change reaches this layer when it happens, not
// when the layer is next read: retail's stamping service and its footprint
// teardown helper each end by restamping every named class over the changed
// rectangle, synchronously, inside the feature service and in the calling
// phase [03 §5.1.2][03 R-LAYER §2], which is what System.NoteFeatureFootprint
// does through the terrain's ClassRestamp port. The whole-layer classifier
// runs only at map load.
func (l *ClassLayer) Value(x, z int32) uint8 {
	if l == nil || x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	return uint8((l.cells[(z>>4)*l.W+x] >> (uint(z&15) * 2)) & 3)
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
	l.fillRestampTiers(x1, z1, x2, z2, fx, fz)
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			if x+fx >= l.W || z+fz >= l.H {
				l.setValue(x, z, LayerBlocked)
				continue
			}
			l.setValue(x, z, l.classifyTiers(x, z, fx, fz))
		}
	}
}

// fillRestampTiers runs the per-cell chain once over every cell a restamp's
// classified anchors read — the anchor rectangle grown by the one-cell ring
// on the low sides and by the footprint on the high sides — and keeps the
// tiers in restampTiers.
//
// classify reads each of those cells once per anchor whose footprint or ring
// covers it, up to (fx+2)·(fz+2) times. The chain is a pure read of the
// terrain, the occupant word, the commit row and the watermark, none of which
// a restamp writes, so reading each cell once gives every anchor the same
// tiers; classifyTiers then applies classify's own rule to them. Only the
// anchors the restamp bound leaves to the classifier are covered.
func (l *ClassLayer) fillRestampTiers(x1, z1, x2, z2, fx, fz int32) {
	ax2, az2 := min(x2, l.W-fx-1), min(z2, l.H-fz-1)
	if ax2 < x1 || az2 < z1 {
		l.restampW = 0
		return
	}
	l.restampX, l.restampZ = x1-1, z1-1
	l.restampW = ax2 + fx - l.restampX + 1
	h := az2 + fz - l.restampZ + 1
	size := int(l.restampW) * int(h)
	if cap(l.restampTiers) < size {
		l.restampTiers = make([]uint8, size)
	}
	l.restampTiers = l.restampTiers[:size]
	i := 0
	for z := l.restampZ; z < l.restampZ+h; z++ {
		for x := l.restampX; x < l.restampX+l.restampW; x++ {
			l.restampTiers[i] = l.classifyCell(x, z)
			i++
		}
	}
}

// classifyTiers classifies one anchor fillRestampTiers covered, from the
// tiers it kept. Every covered cell is classified on its own derived pair and
// the anchor takes the MINIMUM tier over the footprint; a clear result is then
// demoted to steep when any cell of the surrounding one-cell ring is non-clear
// [04 R-SLOPE-01 §3][04 R-PATH-01 §2][04 R-MOV-03 §3]. That is the closed form
// of the map-load builder's two separable window minima: 0 iff any footprint
// cell is 0, 3 iff every cell of the (fx+2) × (fz+2) footprint-plus-ring
// rectangle is 3, 1 otherwise [04 R-SLOPE-01 §3 item 1]. A clear footprint
// cannot fail the all-clear test, so the ring test reads that whole rectangle.
//
// The per-anchor form of the same rule is kept as a test reference
// (layer_reference_test.go), which this is tested against.
func (l *ClassLayer) classifyTiers(x, z, fx, fz int32) uint8 {
	stride := int(l.restampW)
	// base addresses the ring's low corner, (x-1, z-1).
	base := int(z-1-l.restampZ)*stride + int(x-1-l.restampX)
	result := LayerClear
	for dz := 1; dz <= int(fz); dz++ {
		row := l.restampTiers[base+dz*stride+1 : base+dz*stride+1+int(fx)]
		for _, v := range row {
			switch v {
			case LayerBlocked:
				return LayerBlocked
			case LayerSteep:
				result = LayerSteep
			}
		}
	}
	if result != LayerClear {
		return result
	}
	for dz := 0; dz < int(fz)+2; dz++ {
		row := l.restampTiers[base+dz*stride : base+dz*stride+int(fx)+2]
		for _, v := range row {
			if v != LayerClear {
				return LayerSteep
			}
		}
	}
	return LayerClear
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
	c := handleRow(l.commits, h)
	return c.tick, c.set
}

// commitWord is one handle's slot in the commit row. The row replaced a
// map[pool.Handle]uint32 read once per pool slot by every revision pass; set
// keeps the map's presence answer, so an absent handle, a forgotten one and
// one never noted all read (0, false) exactly as the absent key did, and a
// tick noted as zero still reads present.
type commitWord struct {
	tick uint32
	set  bool
}

// commitTick is the tick alone, zero for an absent handle — the unit record's
// zero-initialized word, which is what a map read of an absent key answered.
func (l *ClassLayer) commitTick(h pool.Handle) uint32 {
	return handleRow(l.commits, h).tick
}

// NoteCommit records a unit's last occupancy-commit tick [04 §6.1 R-DOC04-B].
// The production caller is the occupancy commit path (the grid Stamp sites);
// until that wiring lands, the revision pass only sees ticks noted here or by
// its own requester refresh.
func (l *ClassLayer) NoteCommit(h pool.Handle, tick uint32) {
	if l == nil || h == 0 {
		return
	}
	setHandleRow(&l.commits, h, commitWord{tick: tick, set: true})
}

// ForgetCommit drops a unit's mirrored occupancy-commit tick. Retail holds the
// clock in one word on the mover structure [04 R-PATH-01 §14], which
// finalisation frees with the mover; the next unit allocated into the slot gets
// a zero-initialized word, which is what an absent entry means here (the
// revision pass reads "absent is the unit record's zero-initialized tick").
// Retaining the entry would hand a reused pool slot the previous occupant's
// clock, exactly the inheritance finalisation's unlink prevents for the sector
// filing [04 R-COLL-01 §11 item 1]; a unit whose creation stamps normally
// overwrites it at once, but a creation path that stamps no cell does not, and
// that unit would then read FRESH to the occupant-age gate on someone else's
// clock. It also keeps the map from growing for the length of a battle.
func (l *ClassLayer) ForgetCommit(h pool.Handle) {
	if l == nil || h == 0 {
		return
	}
	if int(h) < len(l.commits) {
		l.commits[h] = commitWord{}
	}
}

// revisionWatermark is the request revision pass's watermark arithmetic: the
// current tick clamped UP to 30 and then reduced by 30 — zero for every tick up
// to and including 30, tick−30 after that, the clamp being an unsigned strict
// comparison against 30 [04 §6.1 R-DOC04-B][04 R-PATH-01 §2].
//
// So no mobile occupant blocks by occupant age during the opening 31 ticks of a
// battle, and nothing is lost by that: a unit whose commit tick is frozen at
// zero is still caught exactly once, by the first revision that moves the
// watermark off zero, because the crossed window [old, new) is INCLUSIVE at its
// lower bound and [0, new) therefore contains a zero stamp. (This function used
// to return 1 below tick 31 and call that a correction, on the argument that a
// frozen zero stamp would otherwise stay permanently invisible to path search.
// That argument belonged to an earlier one-sided cohort test, not to the
// two-sided window Revise implements.)
func revisionWatermark(tick uint32) uint32 {
	if tick < 30 {
		return 0
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

// CommitTickSource resolves a unit's last occupancy-commit tick. Retail keeps
// ONE such word, on the mover structure, read by every class record
// [04 R-PATH-01 §14]; this build mirrors it per layer, so a layer allocated
// after units have already committed needs the real word to start from.
type CommitTickSource interface {
	LastCommitTick(h pool.Handle) (uint32, bool)
}

// LastCommitTick adapts the System's collision states to CommitTickSource: the
// collision record is where this build keeps the mover's one occupant-age clock
// [04 R-PATH-01 §14]. A handle with no record has no clock, which is what a
// finalised or never-created unit is.
func (s *System) LastCommitTick(h pool.Handle) (uint32, bool) {
	if s == nil {
		return 0, false
	}
	coll := handleRow(s.Collisions, h)
	if coll == nil {
		return 0, false
	}
	return coll.LastStampTick, true
}

// HasMover adapts the System's collision states to MoverSource: a live
// non-building collision record is the mover [04 R-PATH-01 §14].
func (s *System) HasMover(h pool.Handle) bool {
	if s == nil {
		return false
	}
	if int(h) >= len(s.Collisions) {
		return false
	}
	coll := handleRow(s.Collisions, h)
	if coll == nil {
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
	if int(h) >= len(s.Collisions) {
		return Cell{}, 0, 0, false
	}
	coll := handleRow(s.Collisions, h)
	if coll == nil {
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
	oldWatermark := l.watermark
	newWatermark := revisionWatermark(tick)
	// The requester's word is saved whole, presence included, and put back
	// as it was: a requester that had no entry has none again afterwards.
	saved := handleRow(l.commits, requester)
	requesterCommit := saved.tick
	if requester != 0 {
		setHandleRow(&l.commits, requester, commitWord{tick: tick, set: true})
	}
	l.watermark = newWatermark
	defer func() {
		if requester == 0 {
			return
		}
		l.commits[requester] = saved
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
	// record count excluding the null slot; it is read once, since nothing
	// in the walk allocates or frees a unit.
	capacity := w.Capacity()
	for h := pool.Handle(1); int(h) <= capacity; h++ {
		if h == requester {
			continue
		}
		// The window test runs before the liveness read. Both are pure reads
		// and a slot is restamped only when it passes both, so the order is
		// storage only; testing the row first skips the unit lookup for the
		// many slots whose tick is outside the window.
		c := l.commitTick(h) // absent is the unit record's zero-initialized tick
		if c < oldWatermark || c >= newWatermark {
			continue // outside the crossed [old,new) window [04 R-MOV-03 §3]
		}
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue // alive state bit not carried [R-DOC04-B]
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
	ticks   CommitTickSource
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
	// The committed-anchor adapter, the mover predicate and the occupant-age
	// clock are the same System object in production; take the other ports from
	// it when it implements them [04 R-PATH-01 §14].
	if m, ok := anchors.(MoverSource); ok {
		c.movers = m
	}
	if t, ok := anchors.(CommitTickSource); ok {
		c.ticks = t
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
	c.seedCommits(l)
	if c.movers != nil {
		l.stampAll()
	}
	c.byName[name] = l
	c.names = append(c.names, name)
	return l
}

// seedCommits gives a freshly allocated layer the occupant-age clocks the units
// already carry. Retail has no equivalent step because it has no equivalent
// state: its thirty-two class records are all created with the map and every one
// of them reads the SAME word, the one on each mover [04 R-PATH-01 §14]
// [04 R-COLL-01 §4]. This build mirrors that word per layer, and layers are
// allocated at the first path request of their class, so without this a class
// whose first request comes at tick T starts with an empty mirror and reads
// every unit that has ever committed as carrying tick zero: its first revision
// then walls and restamps units that committed within the last 30 ticks, which
// retail's shared word leaves passable.
//
// The walk is the unit pool slot-ascending, never a map range [I1]. Handles with
// no collision record have no clock, and a clock of zero is exactly what an
// absent entry already means, so neither is written.
func (c *ClassLayers) seedCommits(l *ClassLayer) {
	if c == nil || l == nil || c.ticks == nil || c.world == nil {
		return
	}
	for h := pool.Handle(1); int(h) <= c.world.Capacity(); h++ {
		if tick, ok := c.ticks.LastCommitTick(h); ok && tick != 0 {
			l.NoteCommit(h, tick)
		}
	}
}

// Existing returns the layer already allocated for one movement class, or nil.
// It is the read-only counterpart of For, for callers that must not allocate
// (and map-load stamp) a layer as a side effect of asking [04 §6.1].
func (c *ClassLayers) Existing(name string) *ClassLayer {
	if c == nil {
		return nil
	}
	return c.byName[name]
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

// forEachLayer visits every allocated layer in allocation order [I1]. It is the
// walk the per-unit occupancy bookkeeping uses; unlike Names it allocates
// nothing, which matters because the footprint clear runs it per commit.
func (c *ClassLayers) forEachLayer(fn func(*ClassLayer)) {
	if c == nil || fn == nil {
		return
	}
	for _, name := range c.names {
		if l := c.byName[name]; l != nil {
			fn(l)
		}
	}
}

// FullStampCount is the total number of end-to-end layer rebuilds across every
// allocated class layer, in allocation order [I1]. Host diagnostic only; it
// allocates nothing and reads no clock.
func (c *ClassLayers) FullStampCount() uint64 {
	var total uint64
	c.forEachLayer(func(l *ClassLayer) { total += l.fullStamps })
	return total
}

// noteFootprintClear is the class-layer half of the footprint clear
// [04 R-COLL-01 §4]: after the cell loop and the overlap step, "for a unit with
// a mover, each of the sixteen class-layer records whose watermark exceeds the
// mover's last-stamp tick reclassifies the rectangle ([R-PATH-01 §2]'s
// footprint-aware classifier over the rectangle), and the last-stamp tick is
// set to the current tick; for a unit without a mover every active layer
// reclassifies it."
//
// The gate is the occupant-age gate's own comparison read from the other side:
// a layer whose watermark has passed this mover's last-stamp tick is exactly a
// layer that classified the unit's cells BLOCKED [04 R-PATH-01 §14], so the
// cells it is now vacating are baked into that layer as a wall. Nothing else
// rewrites them — the request revision pass walks live units only and visits an
// occupant just once, at its current rectangle [04 R-MOV-03 §3] — so without
// this step every place a unit parked long enough to be baked in and then left,
// died in, or was picked up from, keeps blocking path search for the rest of
// the battle.
//
// Callers pass the rectangle that was actually cleared (the cached pair, since
// "every writer stamps at the unit's cached pair") and whether the unit has a
// mover; the tick write is the mover's one last-stamp word, which this build
// mirrors per layer, so it goes to every layer through noteOccupancyCommit.
func (s *System) noteFootprintClear(h pool.Handle, anchor Cell, footX, footZ int16, hasMover bool) {
	if s == nil || h == 0 || s.layerRegistry == nil {
		return
	}
	s.layerRegistry.forEachLayer(func(l *ClassLayer) {
		if hasMover {
			commit, _ := l.CommitTick(h)
			if l.Watermark() <= commit {
				return // this layer never saw the occupant as stale
			}
			// The clear's tick write lands before the reclassification, so an
			// anchor whose footprint still covers cells this unit holds
			// elsewhere reads a fresh occupant rather than a stale one.
			l.NoteCommit(h, s.tick)
		}
		l.restampOccupantRect(anchor, footX, footZ)
	})
	if hasMover {
		s.noteOccupancyCommit(h, s.tick)
	}
}

// NoteStructureStamp is the STAMP half of the same class-layer maintenance,
// for the one stamp class that runs it: "for the building class the
// derived-height recompute over the grown rectangle and a reclassification of
// the rectangle in every active class layer follow" [04 R-COLL-01 §4]. It is
// the exported entry a building stamp needs, because that stamp is written by
// internal/construction — the yard map decides which cells of the rectangle
// the occupant word takes, and that selection lives with the placement record,
// not with the mover state this package keeps.
//
// Two things are deliberate. It runs over EVERY active layer with no watermark
// gate: the gate on the clear side is the occupant-age comparison, and a
// building has no mover to compare, which is the same reason it hard-blocks
// unconditionally in classifyCell [04 R-PATH-01 §14] — noteFootprintClear
// reaches the same arm through its hasMover=false path. And it writes no
// commit tick: the stamp's tick write is the mover's word, and a building's
// never advances after creation.
//
// The caller runs it AFTER its cell writes, which is where §4 puts it — the
// classifier reads the occupant word back out of the grid, so a reclassify
// that ran first would bake the pre-stamp verdict in. Without this call the
// cells a building's yard newly claims or releases keep whatever a layer baked
// in until something else reclassifies them, and nothing else does: the
// request revision pass walks live units through the occupant-age window and a
// building never enters it.
func (s *System) NoteStructureStamp(anchor Cell, footX, footZ int16) {
	if s == nil || s.layerRegistry == nil {
		return
	}
	s.layerRegistry.forEachLayer(func(l *ClassLayer) {
		l.restampOccupantRect(anchor, footX, footZ)
	})
}

// NoteFeatureFootprint is the movement half of retail's feature stamper. The
// single stamping service and the footprint teardown helper each END by
// restamping every NAMED movement class over the changed footprint rectangle,
// synchronously, inside the feature service and in the calling phase
// [03 §5.1.2][03 R-LAYER §2]. internal/world's ClassRestamp port carries the
// call across the package boundary; the geometry is the ring-aware anchor
// rectangle of [04 R-MOV-03 §3], the same shared restamp the unit and building
// commit sites already use.
//
// Layers are visited in the registry's fixed allocation order [I1]. A class
// whose layer has never been allocated is skipped, because its first
// allocation stamps the whole map from the plot as it then stands.
func (s *System) NoteFeatureFootprint(anchorX, anchorZ int32, footX, footZ int16) {
	if s == nil || s.layerRegistry == nil {
		return
	}
	anchor := Cell{X: anchorX, Z: anchorZ}
	s.layerRegistry.forEachLayer(func(l *ClassLayer) {
		l.restampOccupantRect(anchor, footX, footZ)
	})
}

// noteRequestRelease is the request release's re-wall [04 R-PATH-01 §14
// correction]. The release runs when a search ends by ANY route — a published
// route, the empty publication of heap exhaustion, and every early exit of
// [04 R-PATH-01 §4] — and on the requester's OWN class layer it compares the
// requester's commit tick against that layer's watermark: when the tick is
// below it, the requester's own footprint rectangle is reclassified. It writes
// nothing to the tick.
//
// This undoes the request revision pass's temporary transparency. Revise
// refreshes the requester's tick to the current tick while it restamps its own
// rectangle — so the requester never blocks its own start cell — and then
// restores the real tick. Without the release re-wall that rectangle stays
// passable to everyone else's searches for the rest of the battle, because the
// revision window [old, new) never revisits an old stamp tick; a mover routed
// into it is then stopped only by the commit validator, "which is the
// difference between a follower that idles and one that circles"
// [04 R-ORDER-02 §1 item 1].
func (s *System) noteRequestRelease(requester pool.Handle) {
	if s == nil || requester == 0 || s.layerRegistry == nil {
		return
	}
	name := s.classKeyFor(requester)
	if name == "" {
		name = scratchLayerKey(s.ProfileFor(requester))
	}
	l := s.layerRegistry.Existing(name)
	if l == nil {
		return // no request of this class has ever allocated a layer
	}
	commit, _ := l.CommitTick(requester)
	if commit >= l.Watermark() {
		return // still fresh in this layer's eyes; nothing was made passable
	}
	anchor, fx, fz, ok := s.CommittedFootprint(requester)
	if !ok {
		return
	}
	l.restampOccupantRect(anchor, fx, fz)
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
