// Package movement — collision and occupancy [04 §8.2] C18 C22–C25 [P0-12].
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
type OccupancyGrid struct {
	cells map[Cell]int // cell → occupant ID (pool slot), single occupant per cell [04 §8.2] C22
	rev   uint64       // profile revision [04 §7.4] C18
}

// NewOccupancyGrid returns an empty occupancy grid.
func NewOccupancyGrid() *OccupancyGrid {
	return &OccupancyGrid{cells: make(map[Cell]int)}
}

// Revision returns the current profile revision [04 §7.4] C18.
func (g *OccupancyGrid) Revision() uint64 {
	if g == nil {
		return 0
	}
	return g.rev
}

// Bump increments the revision counter [04 §7.4] C18. Heap entries are not
// purged eagerly — passability recheck is lazy at expansion [04 §7.4] C18.
func (g *OccupancyGrid) Bump() {
	if g == nil {
		return
	}
	g.rev++
}

// BumpRevision is an alias for Bump retained for callers that prefer the
// Revision()/bump naming from the plan [04 §7.4] C18.
func (g *OccupancyGrid) BumpRevision() { g.Bump() }

// IsOccupied reports whether cell is occupied [04 §8.2] C22.
func (g *OccupancyGrid) IsOccupied(c Cell) bool {
	if g == nil || g.cells == nil {
		return false
	}
	_, ok := g.cells[c]
	return ok
}

// OccupantAt returns the occupant ID at cell, if any [04 §8.2] C22.
func (g *OccupancyGrid) OccupantAt(c Cell) (int, bool) {
	if g == nil || g.cells == nil {
		return 0, false
	}
	id, ok := g.cells[c]
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

// Stamp claims the footprint anchored at anchor for id [04 §8.2] C22.
// It first validates that no cell is occupied by another id (row-major
// immediate reject is done by CanOccupy externally); Stamp rechecks and
// returns false without mutating if occupied. On success it bumps the
// revision [04 §7.4] C18. Deterministic: caller iterates slots ascending [I1].
func (g *OccupancyGrid) Stamp(anchor Cell, fx, fz int16, id int) bool {
	if g == nil {
		return false
	}
	if g.cells == nil {
		g.cells = make(map[Cell]int)
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	// Validate first — no partial stamp [04 §8.2] C22.
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if occ, ok := g.cells[c]; ok && occ != id {
				return false
			}
		}
	}
	changed := false
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if occ, ok := g.cells[c]; ok && occ == id {
				continue
			}
			g.cells[c] = id
			changed = true
		}
	}
	if changed {
		g.rev++ // [04 §7.4] C18 dynamic blockers bump revision
	}
	return true
}

// Clear vacates the footprint anchored at anchor for id [04 §8.2] C22.
// Only cells occupied by id are cleared. On change it bumps the revision
// [04 §7.4] C18.
func (g *OccupancyGrid) Clear(anchor Cell, fx, fz int16, id int) bool {
	if g == nil || g.cells == nil {
		return false
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	changed := false
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if occ, ok := g.cells[c]; ok && occ == id {
				delete(g.cells, c)
				changed = true
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

	VX, VZ int32 // velocity 16.16 [04 §8.2] C24 — horizontal components recomputed at blocked
	Speed  int32 // scalar speed word 16.16 [04 §8.2] C24 — capped at MaxVelocity/2

	Heading uint16 // current heading [04 §5.1][04 §8.2] C24

	MaxVelocity int32 // definition MaxVelocity 16.16 [02 "Unit record"] C24 — compiled chain default

	FootPrintX int16 // footprint X [02 "Movement class record"] [04 §8.2] C25
	FootPrintZ int16 // footprint Z [02 "Movement class record"] [04 §8.2] C25

	Mode uint8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	CachedAnchor Cell  // committed cached anchor pair [04 §8.2] C23
	CachedMode   uint8 // committed mode [04 §8.2] C23

	OldAnchor Cell // old footprint anchor for clamp reference [04 §8.2] C24 centre±0x7FFFF

	Blocked bool // mover blocked bit 2 at mover+? [04 §8.2] C23 C24 — rewritten by validator result
	Dirty   bool // transform dirty [04 §8.2] C23 C24 — marks transform/visibility dirty

	// halfBiasX/Z are the packed half-cell biases for anchor quantize [04 §8.2] C23.
	// If zero, HalfBias() derives from footprint as FootPrint*cell/2.
	halfBiasX int32
	halfBiasZ int32
}

// SetHalfBias overrides the packed half-cell bias [04 §8.2] C23. If not set,
// HalfBias is derived from footprint.
func (s *CollisionState) SetHalfBias(bx, bz int32) {
	if s == nil {
		return
	}
	s.halfBiasX = bx
	s.halfBiasZ = bz
}

// HalfBias returns the packed half-cell bias in world units [04 §8.2] C23.
func (s *CollisionState) HalfBias() (int32, int32) {
	if s == nil {
		return 0, 0
	}
	bx, bz := s.halfBiasX, s.halfBiasZ
	if bx == 0 && bz == 0 {
		fx, fz := s.FootPrintX, s.FootPrintZ
		if fx <= 0 {
			fx = 1
		}
		if fz <= 0 {
			fz = 1
		}
		bx = int32(int64(fx) * worldUnitsPerCell / 2)
		bz = int32(int64(fz) * worldUnitsPerCell / 2)
	}
	return bx, bz
}

// QuantizedAnchor quantizes a world X/Z proposal into its footprint anchor
// using signed arithmetic and the instance's packed half-cell bias [04 §8.2] C23.
//
// anchor = floorDiv(proposed + halfBias, worldUnitsPerCell) [03 §2.1] I3
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
	// cap scalar speed at MaxVelocity/2 if higher [04 §8.2] C24
	half := s.MaxVelocity / 2
	if s.Speed > half {
		s.Speed = half
	}
	// recompute horizontal velocity at that speed and heading [04 §8.2] C24
	// via fixed-point trig tables scaled 8192 [04 §5.1]
	sin := numeric.Sin(numeric.Angle(s.Heading)) // scaled 8192 [04 §5.1]
	cos := numeric.Cos(numeric.Angle(s.Heading)) // scaled 8192 [04 §5.1]
	// velocity = speed * sin/cos /8192 with round to nearest before truncation [04 §5.1]
	s.VX = int32((int64(s.Speed)*int64(sin) + 4096) >> 13) // [04 §5.1]
	s.VZ = int32((int64(s.Speed)*int64(cos) + 4096) >> 13) // [04 §5.1]

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

	// proposed position after recomputed velocity [04 §8.2] C24 — add to current committed X/Z
	// Retail adds velocity to old position then clamps; we follow that literal.
	propX := int64(s.X) + int64(s.VX)
	propZ := int64(s.Z) + int64(s.VZ)

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
	// before next slot so later units observe earlier mutations [04 §8.2] C22
	if grid != nil {
		grid.Clear(s.OldAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
		grid.Stamp(proposedAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
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
		s.ApplyBlocked() // [04 §8.2] C24 — includes speed cap, velocity recompute, clamp, dirty, no occupancy change, no second validator
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
