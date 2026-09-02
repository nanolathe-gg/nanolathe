// Package movement — collision and occupancy [04 §8.2] C18 C22–C25 [P0-12].
//
// # The occupancy authority [04 R-COLL-01 §4][03 §2.2]
//
// Retail keeps mobile occupancy in the plot cell itself: the 13-byte attribute
// cell's first two `uint16` words are the ground plane (mode-1 movers and the
// building class) and the air plane (mode-2 movers) [03 §2.2][04 R-COLL-01 §4].
// Nanolathe reached this unit with those words written for buildings only:
// movers lived in this file's `OccupancyGrid`, a private single-plane map that
// nothing mirrored into `world.PlotCell`, so every reader the research names
// for those words — the projectile contact test [06 R-DMG-01 §7], the placement
// validator and the extractor sampler — read an empty plane for movers.
//
// The choice made here is (b) of WU-19-20: `OccupancyGrid` stays the store the
// search-time consumers of `docs/SPEC_CONFLICTS.md` SC22 already bind to (the
// request-initialization restamp and the occupant-age gate read
// `OccupantAt`/`FootprintOccupied` on the ground plane, and the commit
// validator reads the same predicate), and it grows a second plane plus a
// reference to the plot so that **every** stamp and clear writes the plot word
// of the same plane in the same call. There is one write path, so the two
// cannot disagree; option (a) — making the plot words the store and the grid a
// view — would have moved construction's pre-creation mobile *reservation*
// (`reservePlacement` writes the plot word for a product that has no unit yet)
// into the mover search/commit predicate, which is a change to SC22's
// search-time rules that this unit is required not to make.
//
// The plane a mover writes is its committed mover mode: 1 ground, 2 air, 0 and
// 3 nothing [04 R-COLL-01 §4]. `Stamp`/`Clear` without a plane are the ground
// plane, which is what the building class and every pre-existing caller mean.
//
// CollisionState and OccupancyGrid are the explicit synchronous-commit surfaces
// that retail scatters across the unit/mover/terrain records. The orchestrator
// will unify these with units.Unit / world.Terrain once those types grow the
// necessary fields. Retail offsets are noted where established so the unification
// is mechanical (I13).
//
// P0-12 [ground collision, pushing, blocked arrival, repath — substantially closed]:
//
//	validator row-major Z outer X inner immediate return + aggregate, same-cell fast path,
//	blocked MaxVelocity/2 cap + fixed trig (8192 table, (prod+4096)>>13) ±524287 clamp without restamp,
//	success Clear+Stamp before next slot → vacated reusable same tick, head-on both block,
//	pipeline one cell per tick.
//	NEGATIVE-BOUNDED [P0-12]: no pushing/slide/yield/priority — absence is contract, do NOT implement.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	write, no mass read, no blockedTicks counter, no repath call. Repath is via path scheduler elsewhere.
//
// TODO(question): yard bit semantic labels 0x20/0x40 etc and factory BMCode 0x22F mode gate name remain [P0-12].
//
// Mapping to retail [04 §8.2][04 §9.1][02 "Movement class record"][03 §2.1] (I13: offsets are identity, not layout):
//
//	X,Z                  world position 16.16 at unit +? (X/Z pair, Fixed) [04 §8.2] C22 C24
//	VX,VZ                horizontal velocity 16.16 at mover +? [04 §8.2] C24 recomputed at blocked
//	Speed                scalar speed word Fixed 16.16 at mover +? [04 §8.2] C24 capped at MaxVelocity/2
//	Heading              heading uint16 0..65535 per circle at +? [04 §5.1][04 §8.2] C24
//	MaxVelocity          definition MaxVelocity Fixed 16.16 [02 "Unit record"] [04 §8.2] C24 via compile_movement
//	FootPrintX/Z         footprint dimensions int16 at moveinfo +? [02 "Movement class record"][04 §6.2][04 §8.2] C25
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	CachedAnchor         committed cached anchor cell pair at mover+? [04 §8.2] C23 same-cell fast path
//	CachedMode           committed mode at mover+? [04 §8.2] C23
//	OldAnchor            old footprint anchor at mover+? [04 §8.2] C24 clamp reference (centre ±0x7FFFF)
//	Blocked              mover blocked bit 2 at mover+? [04 §8.2] C23 C24 rewritten by validator result
//	Dirty                transform dirty at +? [04 §8.2] C23 C24 — marks transform/visibility dirty
//	HalfBias             packed half-cell bias per instance [04 §8.2] C23 — quantize with signed arithmetic (floorDiv)
package movement

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// worldUnitsPerCell is one attribute cell in 16.16 world units: 16 map pixels × 65536 [03 §2.1] C1.
const worldUnitsPerCell int64 = 16 * 65536 // 1048576 [03 §2.1]

// blockedBand is the literal ±0x7FFFF clamp the blocked branch applies around
// the old footprint span centre for every footprint size [04 §8.2] C24,
// [GAP 04-P1-GROUND]. The span centre is oldAnchor*cell + halfSpan. The mask
// name comes from the 0x7FFFF constant retail masks X and Z with [04 §8.2] C24.
//
// TODO(question): exact masking semantics — whether retail does
// (base & ~0x7FFFF)|(proposed & 0x7FFFF) or literal centre±band clamp.
// We implement centre±band as in openta-go [GAP 04-P1-GROUND] and cite the constant.
//
// Post-merge unification may replace worldUnitsPerCell with world.CellToWorld
// helpers once movement imports world; the constant stays until then.
const blockedBand int32 = 0x7FFFF // [04 §8.2] C24 literal ± band

// floorDiv returns floor(a/b) with sign correction [I3][03 §2.1] — retail's
// arithmetic shift with sign correction, not trunc-toward-zero division.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Cell is a lattice coordinate on the TNT attribute-cell grid [04 §7.1]. Movement
// occupancy is keyed by Cell [04 §8.2] C22; one cell = 16 map pixels.
type Cell struct {
	X int32
	Z int32
}

// OccupancyGrid is the synchronous mobile occupancy lattice [04 §8.2] C22.
//
// Semantics [04 §8.2] C22:
//   - commits synchronously in sweep order: claim-first blocks later movers;
//   - vacated cell reusable in same sweep;
//   - head-on swaps block (no special simultaneous resolution);
//   - one unit's clear/commit/stamp finishes before the next slot, so later
//     units immediately observe earlier same-tick mutations [04 §8.2] C22.
//
// Deterministic iteration [I1]: player 0..9 then pool slot asc is the caller
// order; the grid itself iterates deterministically via sorted keys when
// enumeration is needed. No map iteration leaks into simulation outcome [I1].
//
// Revision [04 §7.4] C18: dynamic blockers bump a profile revision counter
// (expose Revision()/Bump); heap entries are not purged eagerly and
// passability is rechecked lazily at expansion — search already does this;
// the grid is the bump source [04 §7.4] C18.
// OW-3-O: Revision/Bump retained. The lazy-revalidation consumer is the
// search expansion's per-node isPassable recheck [04 §7.4][04 §8.2] C23 C24;
// no eager heap purge or explicit Revision comparison is required. Stamp/Clear
// bump rev for diagnostics and for any future terrain-profile versioning; the
// search implicitly consumes the bump by re-evaluating static passability on
// every expansion after a commit-stage occupancy change [04 §7.4].
type OccupancyGrid struct {
	cells map[Cell]int // ground plane: cell → occupant ID (pool slot) [04 §8.2] C22 [04 R-COLL-01 §4]
	air   map[Cell]int // air plane: mode-2 movers only [04 R-COLL-01 §4]
	rev   uint64       // profile revision [04 §7.4] C18 OW-3-O retained, lazy revalidation via search isPassable
	// plot is the terrain whose 13-byte cells carry the same two planes as
	// their first two words [03 §2.2]. Every stamp and clear writes it in the
	// same call, so the map and the words are one store [04 R-COLL-01 §4].
	// Fixtures that never bind terrain leave it nil and keep the maps alone.
	plot *world.Terrain
}

// Plane selects one of the plot cell's two occupancy words [03 §2.2].
// Mode 1 movers and the building class write the ground plane; mode 2 movers
// write the air plane; modes 0 and 3 write neither [04 R-COLL-01 §4].
type Plane uint8

const (
	// PlaneGround is the cell's first occupancy word [03 §2.2][04 R-COLL-01 §4].
	PlaneGround Plane = 0
	// PlaneAir is the cell's second occupancy word [03 §2.2][04 R-COLL-01 §4].
	PlaneAir Plane = 1
)

// planeForMode maps a committed mover mode to the plane it stamps. The second
// result is false for modes 0 (attached/carried) and 3, which "stamp and clear
// nothing" [04 R-COLL-01 §4].
func planeForMode(mode uint8) (Plane, bool) {
	switch mode & 0x3 {
	case 1:
		return PlaneGround, true
	case 2:
		return PlaneAir, true
	default:
		return PlaneGround, false
	}
}

// NewOccupancyGrid returns an empty occupancy grid.
func NewOccupancyGrid() *OccupancyGrid {
	return &OccupancyGrid{cells: make(map[Cell]int), air: make(map[Cell]int)}
}

// AttachPlot binds the terrain whose plot cells carry the two occupancy words
// [03 §2.2]. NewSystem calls it once at map load; a grid with no terrain keeps
// its maps and writes no words.
func (g *OccupancyGrid) AttachPlot(t *world.Terrain) {
	if g == nil {
		return
	}
	g.plot = t
}

// planeCells returns the map backing one plane, allocating on demand.
func (g *OccupancyGrid) planeCells(plane Plane) map[Cell]int {
	if plane == PlaneAir {
		if g.air == nil {
			g.air = make(map[Cell]int)
		}
		return g.air
	}
	if g.cells == nil {
		g.cells = make(map[Cell]int)
	}
	return g.cells
}

// plotWord returns the cell's occupancy word for the plane, or (0,false) when
// no terrain is bound or the cell is off the map [03 §2.2].
func (g *OccupancyGrid) plotWord(plane Plane, c Cell) (int16, bool) {
	if g == nil || g.plot == nil {
		return 0, false
	}
	cell := g.plot.PlotAt(c.X, c.Z)
	if cell == nil {
		return 0, false
	}
	if plane == PlaneAir {
		return cell.OccupantB(), true
	}
	return cell.OccupantA(), true
}

// setPlotWord writes the cell's occupancy word for the plane [03 §2.2].
func (g *OccupancyGrid) setPlotWord(plane Plane, c Cell, v int16) {
	if g == nil || g.plot == nil {
		return
	}
	cell := g.plot.PlotAt(c.X, c.Z)
	if cell == nil {
		return
	}
	if plane == PlaneAir {
		cell.SetOccupantB(v)
		return
	}
	cell.SetOccupantA(v)
}

// occupancyWord narrows a pool slot to the identity the plot word carries.
// The word is 16 bits wide, so an identity past that range cannot be filed;
// the caller keeps it out of both halves rather than aliasing another unit
// [I13][04 R-COLL-01 §4].
func occupancyWord(id int) (int16, bool) {
	if id <= 0 || id > int(^uint16(0)>>1) {
		return 0, false
	}
	return int16(id), true
}

// RectOnMap applies the stamp's bounds test — the validator's steps 1–4 on the
// cached pair — to a footprint rectangle [04 R-COLL-01 §2][04 R-COLL-01 §4].
// An out-of-map rectangle files the unit in the off-map bucket and writes no
// cell at all, which is how an airborne mover leaves the map without occupying
// anything. With no terrain bound (fixtures) every rectangle is on the map.
func (g *OccupancyGrid) RectOnMap(anchor Cell, fx, fz int16) bool {
	if g == nil || g.plot == nil {
		return true
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	// Steps 1–4 are `cellX < 0`, `cellZ < 0`, `cellX + fx >= width`,
	// `cellZ + fz >= height`: the last column and row are never enterable
	// [04 R-COLL-01 §2].
	if anchor.X < 0 || anchor.Z < 0 {
		return false
	}
	if anchor.X+int32(fx) >= g.plot.CellW || anchor.Z+int32(fz) >= g.plot.CellH {
		return false
	}
	return true
}

// Revision returns the current profile revision [04 §7.4] C18 OW-3-O.
func (g *OccupancyGrid) Revision() uint64 {
	if g == nil {
		return 0
	}
	return g.rev
}

// Bump increments the revision counter [04 §7.4] C18 OW-3-O. Heap entries are not
// purged eagerly — passability recheck is lazy at expansion [04 §7.4] C18.
// Retained for diagnostics and future profile versioning; search consumes it
// implicitly via per-expansion isPassable after occupancy changes.
func (g *OccupancyGrid) Bump() {
	if g == nil {
		return
	}
	g.rev++
}

// BumpRevision is an alias for Bump retained for callers that prefer the
// Revision()/bump naming from the plan [04 §7.4] C18 OW-3-O.
func (g *OccupancyGrid) BumpRevision() { g.Bump() }

// IsOccupied reports whether cell is occupied in the ground plane [04 §8.2] C22.
func (g *OccupancyGrid) IsOccupied(c Cell) bool {
	if g == nil || g.cells == nil {
		return false
	}
	_, ok := g.cells[c]
	return ok
}

// OccupantAt returns the ground-plane occupant ID at cell, if any
// [04 §8.2] C22. The commit validator and the class layer's occupant-age gate
// read this predicate and only this one: "Only the ground word is read; the air
// word is never consulted, so a landed or hovering airborne unit never blocks a
// ground mover through this test" [04 R-COLL-01 §2].
func (g *OccupancyGrid) OccupantAt(c Cell) (int, bool) {
	if g == nil || g.cells == nil {
		return 0, false
	}
	id, ok := g.cells[c]
	return id, ok
}

// OccupantAtPlane returns the occupant ID at cell in one plane
// [04 R-COLL-01 §4]. Diagnostics and tests read the air plane through it; the
// simulation's blocking predicate stays OccupantAt.
func (g *OccupancyGrid) OccupantAtPlane(plane Plane, c Cell) (int, bool) {
	if g == nil {
		return 0, false
	}
	var m map[Cell]int
	if plane == PlaneAir {
		m = g.air
	} else {
		m = g.cells
	}
	if m == nil {
		return 0, false
	}
	id, ok := m[c]
	return id, ok
}

// FootprintOccupied reports whether any cell of the footprint anchored at
// anchor with size fx × fz is occupied by an occupant other than ignoreID
// [04 §8.2] C22 C25. fx or fz ≤0 is treated as 1 [profile.go fixture].
// Scan is row-major [04 §8.2] C25 (dz outer, dx inner) with immediate return.
func (g *OccupancyGrid) FootprintOccupied(anchor Cell, fx, fz int16, ignoreID int) bool {
	if g == nil {
		return false
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if id, ok := g.cells[c]; ok && id != ignoreID {
				return true
			}
		}
	}
	return false
}

// CanOccupy reports whether the footprint anchored at anchor can be stamped
// for id, considering current occupancy [04 §8.2] C22 C25.
// Row-major, immediate reject, ignore self [04 §8.2] C25.
func (g *OccupancyGrid) CanOccupy(anchor Cell, fx, fz int16, id int) bool {
	return !g.FootprintOccupied(anchor, fx, fz, id)
}

// Stamp claims the ground plane's footprint anchored at anchor for id
// [04 §8.2] C22 [04 R-COLL-01 §4]. It is StampPlane on PlaneGround, which is
// what the building class and every mode-1 mover write.
func (g *OccupancyGrid) Stamp(anchor Cell, fx, fz int16, id int) bool {
	return g.StampPlane(PlaneGround, anchor, fx, fz, id)
}

// StampPlane writes id over the rectangle in one plane, in both the plane's
// map and the plot cell's word for that plane, and reports whether every cell
// of the rectangle now holds id [04 R-COLL-01 §4][03 §2.2].
//
// Order is the section's: the bounds test of [04 R-COLL-01 §2] steps 1–4 on
// the pair first — an out-of-map rectangle writes **no cell** — then the
// per-cell overlap protocol. A free cell takes the identity; a cell that
// already holds a different identity keeps its occupant and the stamp does not
// fail. Retail also records the host/intruder bits on the two units' flag
// words and lets a stamp displace an occupant whose owner is in player state 3.
//
// TODO(T25): the host/intruder flag bits (26/27), the state-3 displacement and
// the sector-bucket overlap scan of [04 R-COLL-01 §4] need unit flag words and
// player state inside the occupancy layer, which it does not have. Placeholder:
// the occupant keeps the cell in every overlap, which is the branch retail
// takes for every owner that is not in state 3. Same gap, same placeholder as
// construction's building stamp.
//
// On a mutation the revision bumps [04 §7.4] C18. Deterministic: the caller
// iterates slots ascending and the scan is row-major [I1][04 §8.2] C25.
func (g *OccupancyGrid) StampPlane(plane Plane, anchor Cell, fx, fz int16, id int) bool {
	if g == nil {
		return false
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	if !g.RectOnMap(anchor, fx, fz) {
		return false // off-map bucket: no cell is written [04 R-COLL-01 §4]
	}
	cells := g.planeCells(plane)
	word, wordFits := occupancyWord(id)
	changed := false
	held := true
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if occ, ok := cells[c]; ok {
				if occ != id {
					held = false // the cell keeps its occupant [04 R-COLL-01 §4]
					continue
				}
			} else {
				cells[c] = id
				changed = true
			}
			if wordFits {
				if cur, ok := g.plotWord(plane, c); ok && (cur == 0 || cur == word) {
					g.setPlotWord(plane, c, word)
				}
			}
		}
	}
	if changed {
		g.rev++ // [04 §7.4] C18 dynamic blockers bump revision
	}
	return held
}

// Clear vacates the footprint anchored at anchor for id [04 §8.2] C22.
// Only cells this identity holds are cleared, in both planes: an identity is
// stamped in exactly one plane at a time [04 R-COLL-01 §4], so a self-owned
// clear over the rectangle is plane-independent and every teardown caller —
// death, load reset, yard close — releases an airborne mover's air word as
// well as a ground mover's ground word. On change it bumps the revision
// [04 §7.4] C18.
func (g *OccupancyGrid) Clear(anchor Cell, fx, fz int16, id int) bool {
	if g == nil {
		return false
	}
	cleared := g.ClearPlane(PlaneGround, anchor, fx, fz, id)
	if g.ClearPlane(PlaneAir, anchor, fx, fz, id) {
		cleared = true
	}
	return cleared
}

// ClearPlane vacates the rectangle in one plane for id, in both the plane's map
// and the plot cell's word [04 R-COLL-01 §4][03 §2.2]. "mode 1 — ground word
// equal to self → 0; mode 2 — air word equal to self → 0."
func (g *OccupancyGrid) ClearPlane(plane Plane, anchor Cell, fx, fz int16, id int) bool {
	if g == nil {
		return false
	}
	var cells map[Cell]int
	if plane == PlaneAir {
		cells = g.air
	} else {
		cells = g.cells
	}
	if cells == nil {
		return false
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	word, wordFits := occupancyWord(id)
	changed := false
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if occ, ok := cells[c]; ok && occ == id {
				delete(cells, c)
				changed = true
			}
			if wordFits {
				if cur, ok := g.plotWord(plane, c); ok && cur == word {
					g.setPlotWord(plane, c, 0)
				}
			}
		}
	}
	if changed {
		g.rev++ // [04 §7.4] C18
	}
	return changed
}

// Block is a revision-bumping stamp for dynamic blockers [04 §7.4] C18.
// It is Stamp with revision bump already included; kept as named entry for
// the revision-bump contract tests.
func (g *OccupancyGrid) Block(anchor Cell, fx, fz int16, id int) bool {
	ok := g.Stamp(anchor, fx, fz, id)
	return ok
}

// Unblock is a revision-bumping clear for dynamic blockers [04 §7.4] C18.
func (g *OccupancyGrid) Unblock(anchor Cell, fx, fz int16, id int) bool {
	return g.Clear(anchor, fx, fz, id)
}

// OccupiedCellsSorted returns the occupied cells in deterministic order
// (X asc then Z asc) [I1][04 §8.2] C22 — deterministic iteration for tests/debug.
func (g *OccupancyGrid) OccupiedCellsSorted() []Cell {
	if g == nil || len(g.cells) == 0 {
		return nil
	}
	out := make([]Cell, 0, len(g.cells))
	for c := range g.cells {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Z < out[j].Z
	})
	return out
}

// Count returns the number of occupied cells.
func (g *OccupancyGrid) Count() int {
	if g == nil || g.cells == nil {
		return 0
	}
	return len(g.cells)
}

// ValidateFootprint scans the proposed footprint row-major and returns
// immediately on a rejecting per-cell predicate, with aggregate
// height/depth/slope gates AFTER the scan [04 §8.2] C25.
//
// perCell: returns true if cell passes (not blocked); false means reject
// and scan returns immediately [04 §8.2] C25 row-major immediate.
// aggregate: optional gate after scan; if non-nil and returns false the
// footprint is rejected [04 §8.2] C25 aggregate after scan.
//
// fx or fz ≤0 treated as 1 (profile.go fixture). Deterministic row-major
// (dz outer, dx inner) [I1][04 §8.2] C25.
func ValidateFootprint(anchor Cell, fx, fz int16, perCell func(Cell) bool, aggregate func() bool) bool { // [04 §8.2] C25
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if perCell != nil && !perCell(c) {
				return false // immediate return [04 §8.2] C25
			}
		}
	}
	if aggregate != nil && !aggregate() {
		return false // aggregate gates AFTER scan [04 §8.2] C25
	}
	return true
}

// CollisionState holds the mutable collision/movement commit state.
// See package comment for retail offset mapping. All fixed values are raw
// 16.16 int32 words unless noted. Angles are uint16 0..65535 per circle [04 §5.1].
type CollisionState struct {
	ID int // pool slot asc [01 §6.2] I1 I5

	X, Z int32 // position 16.16 [04 §8.2] C22 C23 C24 — world X/Z
	Y    int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	VX, VY, VZ int32 // velocity 16.16 [04 §8.2] C24 — horizontal components recomputed at blocked
	Speed      int32 // scalar speed word 16.16 [04 §8.2] C24 — capped at MaxVelocity/2

	Heading uint16 // current heading [04 §5.1][04 §8.2] C24

	MaxVelocity int32 // definition MaxVelocity 16.16 [02 "Unit record"] C24 — compiled chain default

	FootPrintX int16 // footprint X [02 "Movement class record"] [04 §8.2] C25
	FootPrintZ int16 // footprint Z [02 "Movement class record"] [04 §8.2] C25

	// Mode is the mover's low two mode bits, mirrored into the unit's flags
	// word [04 §9.1] C23. It is the occupancy PLANE, not a moving/stopped
	// flag: 1 grounded, 2 airborne, 0 attached/carried [04 R-AIR-01 §3]
	// [04 R-COLL-01 §4]. An earlier comment here read "1 stopped, 2 active",
	// which is the vocabulary [04 R-AIR-01 §3] corrected.
	Mode uint8

	CachedAnchor Cell  // committed cached anchor pair [04 §8.2] C23
	CachedMode   uint8 // committed mode [04 §8.2] C23

	OldAnchor Cell // old footprint anchor for clamp reference [04 §8.2] C24 centre±0x7FFFF

	// StampedAnchor/StampedPlane/HasStamp describe where this identity's
	// occupancy currently is. Retail clears at the cached pair because every
	// writer stamps there [04 R-COLL-01 §4]; the airborne commit rewrites the
	// cached pair before the stamp is reconciled, so the rectangle that was
	// added is remembered here and the clear subtracts exactly it.
	StampedAnchor Cell
	StampedPlane  Plane
	HasStamp      bool

	Blocked        bool  // mover blocked bit 2 at mover+? [04 §8.2] C23 C24 — rewritten by validator result
	SavedStateByte uint8 // complete saved state byte; only low mode/blocked groups are consumed [08 R-SAVE-02 §8]
	// These saved mover words have no live consumer in the ground integrator,
	// but are retained verbatim so a restore does not silently discard them.
	LeanX, LeanY, LeanZ int32
	TurnResidual        int16
	LastStampTick       uint32
	LastProposalTick    uint32
	// BlockerID is the dynamic occupant that rejected the last proposal, or -1
	// for static/terrain rejection. It never causes pushing or displacement.
	BlockerID int
	Dirty     bool // transform dirty [04 §8.2] C23 C24 — marks transform/visibility dirty

	// Building and Yard retain the immutable class split and parsed yard bytes
	// used by every initial and teardown occupancy stamp. Mobile units leave
	// Yard nil and continue to use their full rectangular footprint
	// [04 R-COLL-01 §4].
	Building bool
	Yard     []world.YardCell
	YardOpen bool

	// halfBiasX/Z are optional additive anchor-quantisation biases [04
	// R-COLL-01 §1]. A zero pair means derive S-footprint*S, where S is half
	// a cell; callers retain SetHalfBias for authored/saved instance overrides.
	halfBiasX   int32
	halfBiasZ   int32
	halfBiasSet bool
}

// SetHalfBias overrides the packed half-cell bias [04 §8.2] C23. If not set,
// HalfBias is derived from footprint.
func (s *CollisionState) SetHalfBias(bx, bz int32) {
	if s == nil {
		return
	}
	s.halfBiasX = bx
	s.halfBiasZ = bz
	s.halfBiasSet = true
}

// HalfBias returns the packed half-cell bias in world units [04 §8.2] C23.
func (s *CollisionState) HalfBias() (int32, int32) {
	if s == nil {
		return 0, 0
	}
	bx, bz := s.halfBiasX, s.halfBiasZ
	if !s.halfBiasSet {
		fx, fz := s.FootPrintX, s.FootPrintZ
		if fx <= 0 {
			fx = 1
		}
		if fz <= 0 {
			fz = 1
		}
		halfCell := int64(worldUnitsPerCell / 2)
		bx = int32(halfCell - int64(fx)*halfCell)
		bz = int32(halfCell - int64(fz)*halfCell)
	}
	return bx, bz
}

// QuantizedAnchor quantizes a world X/Z proposal into its footprint anchor
// using signed arithmetic and the instance's packed half-cell bias [04 §8.2] C23.
//
// anchor = floorDiv(proposed + halfCell - footprint*halfCell, cell)
// [04 R-COLL-01 §1][03 §2.1]. HalfBias carries the additive
// halfCell-footprint*halfCell term.
func QuantizedAnchor(proposedX, proposedZ int32, halfBiasX, halfBiasZ int32) Cell { // [04 §8.2] C23
	return Cell{
		X: int32(floorDiv(int64(proposedX)+int64(halfBiasX), worldUnitsPerCell)),
		Z: int32(floorDiv(int64(proposedZ)+int64(halfBiasZ), worldUnitsPerCell)),
	}
}

// ProposedAnchor computes the quantized proposed anchor for s by adding
// velocity to position and quantizing [04 §8.2] C23.
func (s *CollisionState) ProposedAnchor(proposedMode uint8) Cell { // [04 §8.2] C23
	if s == nil {
		return Cell{}
	}
	propX := s.X + s.VX
	propZ := s.Z + s.VZ
	bx, bz := s.HalfBias()
	// proposedMode does not affect anchor quantize; mode equality is separate [04 §8.2] C23
	_ = proposedMode
	return QuantizedAnchor(propX, propZ, bx, bz)
}

// TryFastPath implements the same-cell fast path [04 §8.2] C23.
//
// If the proposed anchor cell pair and mover mode equal the committed cached
// pair and mode, commit the transform and dirty state WITHOUT calling the
// validator or restamping occupancy [04 §8.2] C23.
//
// Returns true if fast path was taken (validator must be skipped).
func (s *CollisionState) TryFastPath(proposedAnchor Cell, proposedMode uint8, proposedX, proposedZ int32) bool { // [04 §8.2] C23
	if s == nil {
		return false
	}
	if proposedAnchor == s.CachedAnchor && proposedMode == s.CachedMode {
		// commit transform + dirty WITHOUT validator or restamp [04 §8.2] C23
		s.X = proposedX
		s.Z = proposedZ
		s.Dirty = true
		// blocked bit retains previous value — not rewritten [04 §8.2] C23
		return true
	}
	return false
}

// ApplyBlocked implements the blocked result [04 §8.2] C24.
//
//   - No X-only/Z-only fallback, no pushing other units [04 §8.2] C24
//   - Caps scalar speed at MaxVelocity/2 if higher [04 §8.2] C24
//   - Recomputes horizontal velocity at that speed and heading via fixed-point
//     trig tables [04 §5.1][04 §8.2] C24
//   - Clamps X and Z against the OLD footprint boundary using 0x7FFFF band
//     around the span centre for every footprint size [04 §8.2] C24
//   - Commits clamped self position and marks transform dirty WITHOUT
//     clearing/restamping occupancy [04 §8.2] C24
func (s *CollisionState) ApplyBlocked() { // [04 §8.2] C24
	if s == nil {
		return
	}
	s.applyBlockedProposal(s.X+s.VX, s.Z+s.VZ)
}

func (s *CollisionState) applyBlockedProposal(proposedX, proposedZ int32) {
	if s == nil {
		return
	}
	// The boundary clamp consumes the proposal that failed validation. Speed
	// limiting affects the next movement proposal, not this position [04
	// R-COLL-01 §2].
	propX := int64(proposedX)
	propZ := int64(proposedZ)
	// cap scalar speed at MaxVelocity/2 if higher [04 §8.2] C24; the division
	// truncates toward zero, which Go's int32 `/` already does.
	half := s.MaxVelocity / 2
	if s.Speed > half {
		s.Speed = half
		// Recompute horizontal velocity only when the strict half-speed cap
		// fires. The blocked branch writes it with the SAME negated form as the
		// ordinary position step [04 R-COLL-01 §1] "The blocked branch":
		//
		//	vx = -sinq(heading, half);  vy = 0;  vz = -cosq(heading, half)
		//
		// where sinq/cosq are the 512-entry table lookups with the 0x1000
		// round-to-nearest addend of [04 R-MOV-01 §4] and `heading` is the
		// heading the steering step just turned — a blocked unit keeps turning
		// toward its waypoint at full turn rate while its speed is capped.
		// (PLAN_16 WU-16-1 step 1 cites this as [04 R-MOV-01 §8]; that section
		// is the mover-modes correction. The velocity rewrite is owned by
		// [04 R-COLL-01 §1], with [R-MOV-01 §7]'s blocked-mover correction
		// pointing at the same trig table.)
		//
		// Nothing reads these components before they are overwritten: StepUnit
		// re-derives VX/VZ from the steer delta at the top of the next tick, and
		// within this tick the proposal was already formed. The flip is for
		// contract consistency with the step that produced the proposal, not a
		// live defect — an unnegated copy here would silently become one the
		// moment a reader lands between the two writes.
		sin := numeric.Sin(numeric.Angle(s.Heading))
		cos := numeric.Cos(numeric.Angle(s.Heading))
		s.VX = -int32((int64(sin)*int64(s.Speed) + 0x1000) >> 13)
		s.VZ = -int32((int64(cos)*int64(s.Speed) + 0x1000) >> 13)
	}

	// clamp X and Z against OLD footprint boundary using 0x7FFFF [04 §8.2] C24
	// centre = oldAnchor*cell + halfSpan for every footprint size [GAP 04-P1-GROUND]
	fx, fz := s.FootPrintX, s.FootPrintZ
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	halfSpanX := int64(fx) * worldUnitsPerCell / 2
	halfSpanZ := int64(fz) * worldUnitsPerCell / 2
	centreX := int64(s.OldAnchor.X)*worldUnitsPerCell + halfSpanX
	centreZ := int64(s.OldAnchor.Z)*worldUnitsPerCell + halfSpanZ
	band := int64(blockedBand) // 0x7FFFF [04 §8.2] C24

	if propX < centreX-band {
		propX = centreX - band
	} else if propX > centreX+band {
		propX = centreX + band
	}
	if propZ < centreZ-band {
		propZ = centreZ - band
	} else if propZ > centreZ+band {
		propZ = centreZ + band
	}
	s.X = int32(propX)
	s.Z = int32(propZ)
	s.Dirty = true   // mark dirty WITHOUT clearing/restamping [04 §8.2] C24
	s.Blocked = true // rewrite blocked bit 2 with blocked result [04 §8.2] C23 C24
	// no occupancy clear/stamp — occupancy untouched [04 §8.2] C24
}

// CommitSuccess commits a successful validation [04 §8.2] C22 C25.
//
// It clears the old footprint, commits X/Y/Z, packed anchor and low mode bits,
// stamps the new footprint, marks transform dirty, and would call the coverage
// wrapper for visibility (visibility update is presentation of LOS mask and is
// left to the caller; Dirty marks the transform) [04 §8.2] C22. Clear/commit/stamp
// finishes before next slot [04 §8.2] C22. On grid mutation the revision bumps [04 §7.4] C18.
func (s *CollisionState) CommitSuccess(proposedAnchor Cell, proposedMode uint8, proposedX, proposedY, proposedZ int32, grid *OccupancyGrid) { // [04 §8.2] C22 C25
	if s == nil {
		return
	}
	// clear old footprint, stamp new — one unit's clear/commit/stamp finishes
	// before next slot so later units observe earlier mutations [04 §8.2] C22.
	// The plane is the committed mode's: ground for mode 1, air for mode 2,
	// neither for modes 0 and 3 [04 R-COLL-01 §4]. The clear runs at the
	// rectangle that was actually stamped and the stamp at the new pair, which
	// is steps (1) and (4) of the success branch [04 R-COLL-01 §1].
	if grid != nil {
		if s.HasStamp {
			grid.ClearPlane(s.StampedPlane, s.StampedAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
			s.HasStamp = false
		} else {
			grid.Clear(s.OldAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
		}
		if plane, stamps := planeForMode(proposedMode); stamps {
			grid.StampPlane(plane, proposedAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
			s.StampedAnchor = proposedAnchor
			s.StampedPlane = plane
			s.HasStamp = grid.RectOnMap(proposedAnchor, s.FootPrintX, s.FootPrintZ)
		}
	}
	s.X = proposedX
	s.Y = proposedY
	s.Z = proposedZ
	s.CachedAnchor = proposedAnchor
	s.CachedMode = proposedMode & 0x3 // low mode bits only [04 §9.1]
	s.OldAnchor = proposedAnchor
	s.Mode = proposedMode & 0x3
	s.Blocked = false
	s.Dirty = true
}

// CommitOne attempts to commit a single mover's proposal deterministically
// [04 §8.2] C22–C25. It encodes the decision tree:
//
//  1. Quantize proposed anchor with signed arithmetic + halfBias [04 §8.2] C23.
//  2. Same-cell fast path: anchor+mode == cached ⇒ commit without validator/restamp [04 §8.2] C23.
//  3. Otherwise call validator once row-major [04 §8.2] C25 and rewrite blocked bit [04 §8.2] C23.
//  4. Blocked ⇒ ApplyBlocked (no fallback, speed cap at MaxVelocity/2, recompute velocity,
//     clamp with 0x7FFFF, dirty without occupancy change) [04 §8.2] C24.
//  5. Success ⇒ CommitSuccess (clear/commit/stamp synchronously) [04 §8.2] C22 C25.
//
// perCell validates each footprint cell; aggregate validates after scan [04 §8.2] C25.
// grid is the occupancy source for perCell's occupancy checks and for stamping;
// if nil no occupancy mutations are performed but the state transitions still occur.
// proposedMode is the low mode bits for this tick [04 §9.1] (typically s.Mode).
// Returns (fastPath, blocked) for test spies.
func (s *CollisionState) CommitOne(grid *OccupancyGrid, proposedMode uint8, perCell func(Cell) bool, aggregate func() bool) (fastPath bool, blocked bool) { // [04 §8.2] C22–C25
	if s == nil {
		return false, false
	}
	propX := s.X + s.VX
	propZ := s.Z + s.VZ
	propY := s.Y // ground Y not integrated here; kept for CommitSuccess signature
	bx, bz := s.HalfBias()
	propAnchor := QuantizedAnchor(propX, propZ, bx, bz)

	// fast path [04 §8.2] C23
	if s.TryFastPath(propAnchor, proposedMode&0x3, propX, propZ) {
		return true, false
	}

	// validator once, rewrite blocked bit 2 [04 §8.2] C23
	valid := ValidateFootprint(propAnchor, s.FootPrintX, s.FootPrintZ, perCell, aggregate) // [04 §8.2] C25
	s.Blocked = !valid
	if !valid {
		s.applyBlockedProposal(propX, propZ) // [04 §8.2] C24 — includes speed cap, velocity recompute, clamp, dirty, no occupancy change, no second validator
		return false, true
	}
	// success [04 §8.2] C22 C25
	s.CommitSuccess(propAnchor, proposedMode&0x3, propX, propY, propZ, grid)
	return false, false
}

// CommitSweep commits a slice of movers synchronously in deterministic slot order
// [04 §8.2] C22 I1. It sorts states by ID ascending (pool slot asc) and runs
// CommitOne for each, so claim-first blocks later movers, vacated cells are
// reusable in same sweep, and head-on swaps block [04 §8.2] C22. One unit's
// clear/commit/stamp finishes before the next slot [04 §8.2] C22.
//
// perCellFactory and aggregateFactory are injected per-mover predicates so
// movement remains profile-independent per plan (path profile-independent
// passability reaches you as injected funcs). For occupancy tests, perCell
// should check grid.CanOccupy or grid.IsOccupied.
func CommitSweep(states []*CollisionState, grid *OccupancyGrid, perCellFactory func(*CollisionState) func(Cell) bool, aggregateFactory func(*CollisionState) func() bool) {
	if len(states) == 0 {
		return
	}
	// deterministic iteration: slot ascending [I1][01 §6.2]
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	for _, s := range states {
		if s == nil {
			continue
		}
		var perCell func(Cell) bool
		var aggregate func() bool
		if perCellFactory != nil {
			perCell = perCellFactory(s)
		}
		if aggregateFactory != nil {
			aggregate = aggregateFactory(s)
		}
		// proposedMode is current mode unless caller injects variation; use s.Mode
		s.CommitOne(grid, s.Mode, perCell, aggregate)
	}
}
