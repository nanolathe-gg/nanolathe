// Collision and occupancy [04 §8.2] C18 C22–C25 [P0-12].
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
// CollisionState and OccupancyGrid are movement's synchronous commit surfaces.
// CollisionState holds the committed mover transform, velocity, cache and stamp
// state; its persisted words are the mover-save source. OccupancyGrid owns the
// ground and air plot-cell writes [04 R-COLL-01 §1][04 R-COLL-01 §4]
// [08 R-SAVE-02 §8].
//
// P0-12 [ground collision, pushing, blocked arrival, repath — substantially closed]:
//
//	validator row-major Z outer X inner immediate return + aggregate, same-cell fast path,
//	blocked MaxVelocity/2 cap + fixed trig (8192 table, (prod+4096)>>13) ±524287 clamp without restamp,
//	success Clear+Stamp before next slot → vacated reusable same tick, head-on both block,
//	pipeline one cell per tick.
//	NEGATIVE-BOUNDED [P0-12]: no pushing/slide/yield/priority — absence is contract, do NOT implement.
//	The bounded set of eight collision- and movement-adjacent routines examined
//	has no second-unit write, no mass read, no blockedTicks counter, no repath
//	call. Repath is via path scheduler elsewhere.
//
// Closed by [04 R-COLL-01 §10], which retires the P0-12 markers that stood
// here: the yard-byte labels and the name of the "mode gate" byte.
//
// The compiled yard byte's meaningful bits are three. Bit 0 is the
// STRUCTURE-YARD MARK, copied into the cell's flag byte on stamp and cleared
// on clear, and read by the placement validator's bit-0 test. Bit 1 is
// SELECTED WHILE THE YARD IS OPEN, bit 2 SELECTED WHILE THE YARD IS CLOSED.
// Against the yard-map letters: `o`, `f`, `w` and `G` carry both selection
// bits; `c`/`C` only the closed bit; `O` only the open bit; `Y`, `y` and `.`
// neither. The 0x20/0x40 the retired marker asked about are not yard-byte
// values at all — nothing in the stamp or the validator masks the yard byte
// with them.
//
// The "mode gate" byte is the definition's `bmcode`: the FBI key, stored as a
// byte, that also selects the yard-map parse and raises status-word bit 29 at
// creation. Its dispatch in the shared validator is `bmcode` zero -> the
// building class, validated by the yard-map placement validator whatever the
// mode; otherwise a mode other than 1 returns legal without scanning a cell,
// and mode 1 runs the per-cell scan. There is no other reader of the byte on
// the commit path. That dispatch lives at the placement caller — this package
// reaches it through world.Terrain.CheckPlacement's Mobile arm — not here.
package movement

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// worldUnitsPerCell is one attribute cell in 16.16 world units: 16 map pixels × 65536 [03 §2.1] C1.
const worldUnitsPerCell int64 = 16 * 65536 // 1048576 [03 §2.1]

// blockedBand is the literal ±0x7FFFF clamp the blocked branch applies around
// the old footprint span centre for every footprint size [04 §8.2] C24,
// [GAP 04-P1-GROUND]. The span centre is oldAnchor*cell + halfSpan.
//
// [04 R-COLL-01 §10] closes the masking marker that stood here. The clamp is a
// per-axis BOUND, not a mask merge: with `c = (f + 2·cachedCell) << 19` and
// `H = 0x7FFFF`, it is `X = min(max(proposedX, c.x − H), c.x + H)`, the same
// for Z, Y untouched — two signed compares and two conditional loads per axis.
// The `(base & ^H) | (proposed & H)` alternative the marker offered was never
// the code's shape; the constant is a DISTANCE, half a cell minus one 16.16
// unit, and the centre±band form below is the retail form. `c` is this
// expression: OldAnchor tracks the committed anchor (CommitSuccess writes both
// from the same proposal), and `oldAnchor*cell + f*cell/2` is
// `(f + 2·oldAnchor) << 19` exactly.
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

	// overlap and ownerState are the overlap protocol's two bindings
	// [04 R-COLL-01 §4]. The grid arbitrates a contested cell from the
	// occupant's owner player state and records the outcome on both units'
	// flag words, neither of which it carries itself. With no binding the
	// occupant keeps every contested cell, which is the branch retail takes
	// for every owner that is not in the displacing state.
	overlap    OverlapUnits
	ownerState func(owner uint8) uint8
	// inOverlapScan guards the clear's overlap scan against re-entry. A
	// restamp only stamps, so it cannot start a second clear; the flag keeps
	// a future writer from turning the scan quadratic by accident.
	inOverlapScan bool
	// scan is the overlap scan's reusable candidate buffer. The scan gathers
	// before it restamps, which is safe because nothing the restamp does moves
	// a unit between sector buckets: retail's restamp never re-links, and its
	// clear never unlinks [04 R-COLL-01 §4A].
	scan []overlapCandidate
	// linkSeq is the grid's link clock: every relink of a unit into a sector
	// record takes the next value, so a bucket's head-first order — most
	// recent relink first — is the descending sequence [04 R-COLL-01 §11].
	linkSeq uint64
}

// overlapCandidate is one unit the clear's overlap scan reaches, tagged with
// the sector record the stamp filed it under and the sequence of its most
// recent relink [04 R-COLL-01 §4A][04 R-COLL-01 §11].
type overlapCandidate struct {
	id     int
	sx, sz int32
	seq    uint64
}

// SectorFiling is a unit's place in retail's sector-bucket structure: the
// sector record the stamp filed it under (or the off-map record) and the link
// sequence of its most recent relink [04 R-COLL-01 §4A][04 R-COLL-01 §11].
// The zero value is unfiled — retail's null record reference — so a fresh
// collision record's first stamp always inserts.
type SectorFiling struct {
	Filed  bool
	OffMap bool
	SX, SZ int32
	Seq    uint64
}

// displaceableOwnerState is the owner player-row CONTROL byte whose units yield
// a contested cell to whoever stamps over it [04 R-COLL-01 §4]. The byte is the
// player row's control byte — the one doc 04 also calls the "player-state byte"
// when it writes "a live unit whose owner's player-state byte is 1 or 2"
// [04 R-MOV-01 §3] — and its value set is exactly {1, 2, 3}: 1 a locally
// controlled human, 2 a computer player, 3 a remote peer
// [05 R-SHARE-01 §1]. Doc 05 names the same three values "the three active
// states", narrows settlement to "the two settling states" 1 and 2, and says
// the third "traverses but never settles"
// [05 "Authoritative settlement order"]. So player state 3 IS control byte 3,
// and economy.Player.ControllerState is the field that carries it.
//
// This constant was called `eliminatedPlayerState` and its comment offered the
// eliminated/watch population as a Supported-inference label, citing the sweep
// gate of [04 R-MOV-03 §1]. That was a conflation of three different bytes and
// is corrected here without changing a value: the sweep gate tests the control
// byte for 1/2/3 AND a separate byte "for the eliminated value 10", and 10 is
// the neutral-side sentinel that recurs as "side index is not the neutral
// value 10" [08 P0-04] — a third byte again. Nothing anywhere makes 3 an
// eliminated value, and elimination on this build is derived from the row's two
// unit counters, not from a state byte at all (economy.PlayerEliminated). The
// name mattered: read beside [04 R-MOV-03 §1] the old one invited a "fix" to
// value 10 on a byte this protocol never reads.
//
// [04 R-COLL-01 §4] still labels the value "the eliminated/watch state" as a
// Supported inference and cites [R-MOV-01 §3], which is the route follower and
// cannot support it. The arithmetic there is Established and is what this
// constant implements; only the label is wrong, and correcting it in the
// research doc belongs to the doc's owner.
const displaceableOwnerState uint8 = 3

// OverlapUnits is the occupancy layer's window onto the facts the overlap
// protocol of [04 R-COLL-01 §4] needs and the grid does not carry: which unit
// an occupant identity names, its flag word's host/intruder bits, the
// rectangle it would stamp, and the deterministic live-unit traversal the
// clear's overlap scan walks. *System implements it; fixtures may leave it
// unbound.
//
// Identities are pool slots, the same numbers the grid files in its planes.
type OverlapUnits interface {
	// OverlapOwner returns the owner byte of the live unit at a pool slot.
	OverlapOwner(id int) (uint8, bool)
	// OverlapFlags reads the unit's host and intruder bits.
	OverlapFlags(id int) (host, intruder bool)
	// SetOverlapFlags writes both bits to the given values.
	SetOverlapFlags(id int, host, intruder bool)
	// OverlapRect returns the cell rectangle the unit occupies at its cached
	// pair — the rectangle the clear scans and the restamp re-stamps.
	OverlapRect(id int) (anchor Cell, fx, fz int16, ok bool)
	// VisitOverlapCandidates visits live unit identities in the sweep's
	// deterministic order (player slot, then pool slot ascending) [I1].
	VisitOverlapCandidates(fn func(id int))
	// RestampFootprint re-runs the stamp loop for one unit at its cached pair
	// through the grid, so the overlap protocol arbitrates every cell again.
	// The building class stamps only the cells its yard map still selects and
	// releases the self-held cells it no longer selects [04 R-COLL-01 §4].
	RestampFootprint(id int)
}

// OverlapPositions is the optional half of the overlap binding: the committed
// 16.16 position whose sector index the stamp files a unit under
// [04 R-COLL-01 §4A]. It is separate from OverlapUnits because the position is
// only needed to order the clear's overlap scan, and a fixture that never
// exercises the sector sweep should not have to supply one — without it the
// scan falls back on the cached rectangle's centre, which is where a unit
// standing on its cached pair is. *System implements it.
type OverlapPositions interface {
	// OverlapPosition returns the unit's committed X and Z in 16.16.
	OverlapPosition(id int) (x, z int32, ok bool)
}

// OverlapFilings is the optional third part of the overlap binding: the
// per-unit sector filing the stamp relinks and the clear's overlap scan orders
// by [04 R-COLL-01 §11]. Without it the scan derives each candidate's sector
// from its position and keeps the live-unit order inside one sector. *System
// implements it over the collision record, which a forgotten unit takes with
// it, so a reused pool slot starts unfiled exactly as retail's finalisation
// unlink leaves a slot.
type OverlapFilings interface {
	// OverlapFiling returns the unit's filing, or nil for an identity with
	// no collision record.
	OverlapFiling(id int) *SectorFiling
}

// AttachOverlap binds the overlap protocol's unit window and the owner
// player-state reader [04 R-COLL-01 §4]. The session composes both; a grid
// with neither keeps its pre-protocol behavior (the occupant keeps the cell).
func (g *OccupancyGrid) AttachOverlap(u OverlapUnits, ownerState func(owner uint8) uint8) {
	if g == nil {
		return
	}
	g.overlap = u
	g.ownerState = ownerState
}

// AttachOverlapBinding installs this System as its grid's overlap window and
// binds the owner player-state reader the session owns [04 R-COLL-01 §4].
// The state byte is the player row's, never the unit's own owner byte, which
// is the slot number [06 R-DMG-01 §8].
func (s *System) AttachOverlapBinding(ownerState func(owner uint8) uint8) {
	if s == nil || s.Grid == nil {
		return
	}
	s.Grid.AttachOverlap(s, ownerState)
}

// displaceable reports the overlap protocol's one branch condition: the
// occupant's owner is active and in player state 3 [04 R-COLL-01 §4].
func (g *OccupancyGrid) displaceable(occupant int) bool {
	if g == nil || g.overlap == nil || g.ownerState == nil || occupant <= 0 {
		return false
	}
	owner, ok := g.overlap.OverlapOwner(occupant)
	if !ok {
		return false
	}
	return g.ownerState(owner) == displaceableOwnerState
}

// raiseOverlap ORs the named bits into a unit's flag word. Retail raises one
// bit per side per contested cell and never clears one here; the clear and the
// restamp are the only routines that lower them [04 R-COLL-01 §4].
func (g *OccupancyGrid) raiseOverlap(id int, host, intruder bool) {
	if g == nil || g.overlap == nil || id <= 0 {
		return
	}
	h, i := g.overlap.OverlapFlags(id)
	nh, ni := h || host, i || intruder
	if nh == h && ni == i {
		return
	}
	g.overlap.SetOverlapFlags(id, nh, ni)
}

// ArbitrateOverlap applies the overlap protocol to one cell and reports
// whether self takes it [04 R-COLL-01 §4]:
//
//	occupant := unit at the cell's word
//	if occupant's owner is active and in player state 3:
//	        occupant.flags |= intruder;  self.flags |= host;  cell.word := self
//	else:
//	        occupant.flags |= host;      self.flags |= intruder;  (cell keeps occupant)
//
// A free cell, or one this identity already holds, is taken with no bits
// raised. Stamping a held cell never fails the stamp; it only decides which of
// the two identities the cell names. The protocol is identical for the ground
// and air planes and for the building class, which is why the building stamp
// in internal/construction arbitrates its plot word through this same call.
func (g *OccupancyGrid) ArbitrateOverlap(occupant, self int) bool {
	if self <= 0 {
		return false
	}
	if occupant <= 0 || occupant == self {
		return true
	}
	if g == nil {
		// No grid is no window on the two units, so the cell keeps its
		// occupant — the branch retail takes for every owner that is not in
		// the displacing state.
		return false
	}
	if g.displaceable(occupant) {
		g.raiseOverlap(occupant, false, true)
		g.raiseOverlap(self, true, false)
		return true
	}
	g.raiseOverlap(occupant, true, false)
	g.raiseOverlap(self, false, true)
	return false
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
// per-cell overlap protocol of ArbitrateOverlap above, one cell at a time as
// it is visited: a free cell takes the identity; a contested cell whose
// occupant's owner is in the displacing player state yields to this identity
// and both flag words record it; any other contested cell keeps its occupant,
// records the bits the other way round, and does **not** fail the stamp. A
// rectangle can therefore end half displaced when its occupants differ, which
// is the order the section names [04 R-COLL-01 §4].
//
// The boolean result is unchanged: it is "every cell of the rectangle now
// holds id", which the callers use as "the stamp took the whole footprint",
// not as a success flag for the protocol.
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
	// The sector relink runs on every stamp call ahead of everything else,
	// the off-map filing included [04 R-COLL-01 §4A][04 R-COLL-01 §11].
	g.fileUnit(id, anchor, fx, fz)
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
			displaced := false
			if occ, ok := cells[c]; ok && occ != id {
				// One contested cell, arbitrated as it is visited
				// [04 R-COLL-01 §4].
				if !g.ArbitrateOverlap(occ, id) {
					held = false // the cell keeps its occupant
					continue
				}
				displaced = true
				cells[c] = id
				changed = true
			} else if !ok {
				cells[c] = id
				changed = true
			}
			if wordFits {
				// A displaced cell's word is rewritten unconditionally — the
				// protocol's `cell.word := self`. Otherwise the word is only
				// taken when it is free or already ours, which leaves
				// construction's pre-creation placement reservation (a word
				// written for a product that has no unit yet, so no identity
				// the protocol could arbitrate) standing.
				if cur, ok := g.plotWord(plane, c); ok && (displaced || cur == 0 || cur == word) {
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

// fileUnit is the stamp's sector relink [04 R-COLL-01 §4A] steps 1–4, kept as
// the two words [04 R-COLL-01 §11] names: the record the committed position
// selects (the off-map record when the cell rectangle fails the bounds test)
// and the link sequence of the relink. A unit whose record is unchanged keeps
// its place; one that changed takes the next sequence. The restamp never
// relinks, which the scan guard covers: a restamp only runs inside the scan.
func (g *OccupancyGrid) fileUnit(id int, anchor Cell, fx, fz int16) {
	if g.inOverlapScan || g.overlap == nil {
		return
	}
	filings, ok := g.overlap.(OverlapFilings)
	if !ok {
		return
	}
	f := filings.OverlapFiling(id)
	if f == nil {
		return
	}
	positions, _ := g.overlap.(OverlapPositions)
	sx, sz, onMap := g.unitSector(id, anchor, fx, fz, positions)
	if f.Filed && f.OffMap == !onMap && (!onMap || (f.SX == sx && f.SZ == sz)) {
		return
	}
	g.linkSeq++
	*f = SectorFiling{Filed: true, OffMap: !onMap, SX: sx, SZ: sz, Seq: g.linkSeq}
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
	// "Then flags bit 27 is cleared, and if bit 26 was set both 26 and 27 are
	// cleared and the overlap scan runs" — the section's clear order, after
	// the cell loop [04 R-COLL-01 §4].
	g.releaseOverlap(id, anchor, fx, fz)
	return changed
}

// releaseOverlap is the clear's flags step and, when this identity was a host,
// the overlap scan [04 R-COLL-01 §4]. Clear releases cells, so an intruder
// that was refused one of them may now claim it: every live unit whose own
// rectangle intersects this one is passed to the restamp, which acts only on
// the units whose intruder bit is raised.
//
// Clear() calls this once per plane; the second call finds bit 26 already
// clear and only re-clears bit 27, so the scan runs once per clear.
func (g *OccupancyGrid) releaseOverlap(id int, anchor Cell, fx, fz int16) {
	if g == nil || g.overlap == nil || id <= 0 {
		return
	}
	host, intruder := g.overlap.OverlapFlags(id)
	if !host && !intruder {
		return
	}
	if !host {
		g.overlap.SetOverlapFlags(id, false, false) // bit 27 cleared
		return
	}
	g.overlap.SetOverlapFlags(id, false, false) // bit 26 was set: both cleared
	g.overlapScan(id, anchor, fx, fz)
}

// overlapScan is the clear's sector-bucket scan [04 R-COLL-01 §4]: "every live
// unit (and every unit on a live unit's cargo list) filed in a sector bucket
// touching the rectangle whose own rectangle intersects it is passed to the
// restamp". The rectangle is the clearing unit's own at its cached pair; the
// rectangle the clear was called with is the fallback for an identity the
// binding does not know.
//
// A carried unit is on the cargo list retail also walks; its mover mode is 0,
// which stamps and clears nothing, so passing it to the restamp writes no cell
// either way [04 R-COLL-01 §4].
//
// The sweep is the sector one [04 R-COLL-01 §4A]: sector column ascending in
// the outer loop, sector row ascending in the inner, over the rectangle's
// sector span grown by one sector on every side, skipping a sector index
// outside the grid. A unit filed in the off-map sector record is never reached,
// because that record is not in the grid array — so an intruder whose own
// rectangle has left the map keeps its intruder bit instead of having it
// cleared by a restamp that would write no cell anyway.
//
// The correction the previous text needed: it said "a bucket's own order is its
// insertion order, which is untraced" and walked the sweep's live-unit order
// for the whole scan. Both halves were wrong. The bucket order is *not*
// insertion order — the stamp head-inserts, so a bucket reads back in reverse
// order of linking — and the sector sweep itself is fully determined by each
// candidate's own position, which is what this now walks.
//
// Within one sector the order is the bucket's, read from its head: reverse
// order of each unit's most recent relink [04 R-COLL-01 §11]. A `TODO(question)`
// stood here saying this build fell back on live-unit order inside a sector
// because the relink history lived at stamp sites this package's grid did not
// see. It does see them: every stamp site calls StampPlane, which relinks
// through fileUnit; the filing lives on the collision record, so ForgetUnit
// drops it with the record; and the cargo detach push is mirrored by
// CommitSuccess at the first post-carry commit. The sequence is stable across
// units the binding cannot file (no filing: the live-unit order, as before).
func (g *OccupancyGrid) overlapScan(clearing int, anchor Cell, fx, fz int16) {
	if g == nil || g.overlap == nil || g.inOverlapScan {
		return
	}
	if a, rx, rz, ok := g.overlap.OverlapRect(clearing); ok {
		anchor, fx, fz = a, rx, rz
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	// The span: the rectangle's own sectors, one sector of margin on every
	// side, and the "column start past column end" early return retail takes
	// before it enters either loop [04 R-COLL-01 §4A].
	sxLo, sxHi := sectorOfCell(anchor.X)-1, sectorOfCell(anchor.X+int32(fx))+1
	szLo, szHi := sectorOfCell(anchor.Z)-1, sectorOfCell(anchor.Z+int32(fz))+1
	if sxLo > sxHi {
		return
	}
	g.inOverlapScan = true
	defer func() { g.inOverlapScan = false }()

	positions, _ := g.overlap.(OverlapPositions)
	filings, _ := g.overlap.(OverlapFilings)
	g.scan = g.scan[:0]
	g.overlap.VisitOverlapCandidates(func(id int) {
		if id == clearing || id <= 0 {
			return
		}
		a, cfx, cfz, ok := g.overlap.OverlapRect(id)
		if !ok || !rectsIntersect(anchor, fx, fz, a, cfx, cfz) {
			return
		}
		var sx, sz int32
		var filed bool
		var seq uint64
		if f := g.filingOf(filings, id); f != nil {
			// The record the stamp filed the unit under, as it stands
			// [04 R-COLL-01 §11]; the off-map record is in no sweep.
			sx, sz, filed, seq = f.SX, f.SZ, !f.OffMap, f.Seq
		} else {
			sx, sz, filed = g.unitSector(id, a, cfx, cfz, positions)
		}
		if !filed || sx < sxLo || sx > sxHi || sz < szLo || sz > szHi {
			return
		}
		g.scan = append(g.scan, overlapCandidate{id: id, sx: sx, sz: sz, seq: seq})
	})
	// Column-major over the sectors; inside one sector the bucket from its
	// head, i.e. the most recent relink first [04 R-COLL-01 §11]. SliceStable
	// keeps the live-unit order between candidates with no filing; the visit
	// order it sorts is itself deterministic, so the result is [I1].
	sort.SliceStable(g.scan, func(i, j int) bool {
		if g.scan[i].sx != g.scan[j].sx {
			return g.scan[i].sx < g.scan[j].sx
		}
		if g.scan[i].sz != g.scan[j].sz {
			return g.scan[i].sz < g.scan[j].sz
		}
		return g.scan[i].seq > g.scan[j].seq
	})
	for _, c := range g.scan {
		g.Restamp(c.id)
	}
	g.scan = g.scan[:0]
}

// filingOf returns a candidate's filing when the binding files it and the
// stamp has filed it at least once.
func (g *OccupancyGrid) filingOf(filings OverlapFilings, id int) *SectorFiling {
	if filings == nil {
		return nil
	}
	f := filings.OverlapFiling(id)
	if f == nil || !f.Filed {
		return nil
	}
	return f
}

// sectorCellShift converts a cell coordinate to its occupancy sector index: a
// sector is 8 cells on a side, 128 world units [04 R-COLL-01 §4A][04 R-AIR-01 §5].
// The shift is arithmetic, so a negative coordinate floors to the sector below
// zero rather than toward it — the grid's own bounds test then drops it.
const sectorCellShift = 3

// sectorWorldShift converts a 16.16 world coordinate to the same sector index:
// 128 world units is 0x800000 in 16.16 [04 R-COLL-01 §4A].
const sectorWorldShift = 23

func sectorOfCell(c int32) int32 { return c >> sectorCellShift }

// unitSector reports which sector record the stamp filed this identity under,
// and whether it is in the grid at all [04 R-COLL-01 §4A]. The stamp indexes
// the grid from the unit's committed 16.16 position, not from its cell pair;
// the off-map decision is the other way round — it is the cell rectangle's
// bounds test, the same one StampPlane applies before it writes a cell. A unit
// that fails it is filed in the one off-map record, which is not in the grid
// array and which no sweep ever visits.
//
// With no position binding (fixtures) the position is taken to be the centre of
// the cached rectangle, which is where a unit standing on its cached pair is.
func (g *OccupancyGrid) unitSector(id int, anchor Cell, fx, fz int16, positions OverlapPositions) (sx, sz int32, filed bool) {
	if !g.RectOnMap(anchor, fx, fz) {
		return 0, 0, false
	}
	if positions != nil {
		if x, z, ok := positions.OverlapPosition(id); ok {
			return x >> sectorWorldShift, z >> sectorWorldShift, true
		}
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	cx := int64(anchor.X)*worldUnitsPerCell + int64(fx)*worldUnitsPerCell/2
	cz := int64(anchor.Z)*worldUnitsPerCell + int64(fz)*worldUnitsPerCell/2
	return int32(cx >> sectorWorldShift), int32(cz >> sectorWorldShift), true
}

// Restamp is the section's restamp [04 R-COLL-01 §4]: gated on flags bit 27,
// it clears the bit and re-runs the stamp loop at the cached pair with the
// overlap protocol. Its callers are the overlap scan above (an intruder
// re-claims cells its host just released), the yard-open port write, and the
// save loader's post-load pass — the two writers that raise bit 27 to request
// one.
func (g *OccupancyGrid) Restamp(id int) {
	if g == nil || g.overlap == nil || id <= 0 {
		return
	}
	host, intruder := g.overlap.OverlapFlags(id)
	if !intruder {
		return
	}
	g.overlap.SetOverlapFlags(id, host, false)
	g.overlap.RestampFootprint(id)
}

// rectsIntersect reports whether two cell rectangles share a cell. Both are
// half-open in each axis, as every footprint rectangle in this file is.
func rectsIntersect(a Cell, afx, afz int16, b Cell, bfx, bfz int16) bool {
	if afx <= 0 {
		afx = 1
	}
	if afz <= 0 {
		afz = 1
	}
	if bfx <= 0 {
		bfx = 1
	}
	if bfz <= 0 {
		bfz = 1
	}
	return a.X < b.X+int32(bfx) && b.X < a.X+int32(afx) &&
		a.Z < b.Z+int32(bfz) && b.Z < a.Z+int32(afz)
}

// ReleaseCellIfSelf clears one cell of one plane when it holds id, in both the
// plane's map and the plot word. It is the restamp's building-class release —
// "a cell the yard map no longer selects that holds the self identity is
// released to 0" — and deliberately does not run the clear's flags step, which
// belongs to a whole-unit clear [04 R-COLL-01 §4].
func (g *OccupancyGrid) ReleaseCellIfSelf(plane Plane, c Cell, id int) {
	if g == nil || id <= 0 {
		return
	}
	cells := g.planeCells(plane)
	if occ, ok := cells[c]; ok && occ == id {
		delete(cells, c)
		g.rev++ // [04 §7.4] C18
	}
	if word, wordFits := occupancyWord(id); wordFits {
		if cur, ok := g.plotWord(plane, c); ok && cur == word {
			g.setPlotWord(plane, c, 0)
		}
	}
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

// CollisionState holds the mutable collision/movement commit state. All fixed
// values are raw 16.16 int32 words unless noted. Angles are uint16 0..65535
// per circle [04 §5.1].
type CollisionState struct {
	ID int // pool slot asc [01 §6.2] I1 I5

	X, Z int32 // position 16.16 [04 §8.2] C22 C23 C24 — world X/Z
	Y    int32 // Y 16.16; the signed high word is height [04 §8.1][04 R-MOV-01 §4] C21 — kept for commit

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

	// Filing is where the stamp filed this unit in the sector-bucket
	// structure [04 R-COLL-01 §4A][04 R-COLL-01 §11]. It lives on the
	// collision record so that ForgetUnit drops it: a reused pool slot starts
	// unfiled, which is what retail's finalisation unlink leaves behind.
	Filing SectorFiling

	// airSector is the coarse air-sector record selected by this collision
	// record's completed footprint stamp. It is separate from Filing: Filing is
	// optional overlap-sweep bookkeeping, while air-sector readers include
	// grounded targets that need the canonical stamp result even when no overlap
	// binding is installed [04 R-COLL-01 §4][04 R-AIR-01 §5].
	airSector *airSector
	airOffMap bool
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
	// The success branch, in the section's order [04 R-COLL-01 §1]: (1) clear
	// the old footprint at the rectangle that was actually stamped, (2) write
	// the proposed X/Y/Z, (3) write the new cell pair and mode mirror, (4)
	// stamp the new footprint at the new pair and mode, (5) dirty. One unit's
	// clear/commit/stamp finishes before the next slot so later units observe
	// earlier mutations [04 §8.2] C22. The plane is the committed mode's:
	// ground for mode 1, air for mode 2, neither for modes 0 and 3
	// [04 R-COLL-01 §4]. The position write precedes the stamp because the
	// stamp files the unit's sector from the committed position
	// [04 R-COLL-01 §4A] step 2.
	if grid != nil {
		if s.HasStamp {
			grid.ClearPlane(s.StampedPlane, s.StampedAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
			s.HasStamp = false
		} else {
			grid.Clear(s.OldAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
		}
	}
	// A carried unit's mirror is 0 (the carried-position setter's mode). Its
	// first commit that stamps again is where retail's detach push lands: the
	// released cargo is head-inserted into the record its carried stamps kept
	// current, so it becomes the most recent entry of that sector even when
	// the sector did not change [04 R-COLL-01 §11] item 2. Unfiling here makes
	// the stamp below relink unconditionally. (A cargo released onto the very
	// cell and mode it was picked up from takes the same-cell fast path and
	// never reaches this branch; it keeps its old place.)
	if s.CachedMode == 0 && proposedMode&0x3 != 0 {
		s.Filing = SectorFiling{}
	}
	s.X = proposedX
	s.Y = proposedY
	s.Z = proposedZ
	s.CachedAnchor = proposedAnchor
	s.CachedMode = proposedMode & 0x3
	s.OldAnchor = proposedAnchor
	s.Mode = proposedMode & 0x3
	if grid != nil {
		if plane, stamps := planeForMode(proposedMode); stamps {
			grid.StampPlane(plane, proposedAnchor, s.FootPrintX, s.FootPrintZ, s.ID)
			s.StampedAnchor = proposedAnchor
			s.StampedPlane = plane
			s.HasStamp = grid.RectOnMap(proposedAnchor, s.FootPrintX, s.FootPrintZ)
		}
	}
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

// --- the overlap protocol's unit window [04 R-COLL-01 §4] ---
//
// The grid arbitrates a contested cell from the occupant's owner player state
// and records the outcome on both units' flag words; neither fact lives in the
// grid. These six methods are the System's implementation of OverlapUnits, and
// they are the only place internal/movement reads or writes bits 26 and 27.

// OverlapOwner returns the owner byte of the live unit at a pool slot.
func (s *System) OverlapOwner(id int) (uint8, bool) {
	u := s.overlapUnit(id)
	if u == nil {
		return 0, false
	}
	return u.Owner, true
}

// OverlapFlags reads the unit's host and intruder bits [04 R-COLL-01 §4].
func (s *System) OverlapFlags(id int) (bool, bool) {
	u := s.overlapUnit(id)
	if u == nil {
		return false, false
	}
	return u.Flags&units.OverlapHostStatus != 0, u.Flags&units.OverlapIntruderStatus != 0
}

// SetOverlapFlags writes both bits to the given values [04 R-COLL-01 §4].
func (s *System) SetOverlapFlags(id int, host, intruder bool) {
	u := s.overlapUnit(id)
	if u == nil {
		return
	}
	u.Flags &^= units.OverlapHostStatus | units.OverlapIntruderStatus
	if host {
		u.Flags |= units.OverlapHostStatus
	}
	if intruder {
		u.Flags |= units.OverlapIntruderStatus
	}
}

// OverlapRect returns the cell rectangle the unit occupies at its cached pair.
// For the building class that is the whole extent; which of its cells are
// actually held is the yard map's business, and the restamp applies it.
func (s *System) OverlapRect(id int) (Cell, int16, int16, bool) {
	if s == nil {
		return Cell{}, 0, 0, false
	}
	coll := s.Collisions[pool.Handle(id)]
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

// OverlapPosition returns the unit's committed 16.16 X and Z — the pair whose
// sector index the stamp files it under [04 R-COLL-01 §4A].
func (s *System) OverlapPosition(id int) (int32, int32, bool) {
	if s == nil {
		return 0, 0, false
	}
	coll := s.Collisions[pool.Handle(id)]
	if coll == nil {
		return 0, 0, false
	}
	return coll.X, coll.Z, true
}

// OverlapFiling returns the unit's sector filing on its collision record
// [04 R-COLL-01 §11], or nil when it has none.
func (s *System) OverlapFiling(id int) *SectorFiling {
	if s == nil {
		return nil
	}
	coll := s.Collisions[pool.Handle(id)]
	if coll == nil {
		return nil
	}
	return &coll.Filing
}

// VisitOverlapCandidates visits live unit identities in the sweep's order —
// players 0..9 ascending, then pool slot ascending [I1][01 §6.2]. It is the
// candidate *population*, not the visit order: the clear's overlap scan sorts
// what it gathers into retail's sector sweep and keeps this order only inside
// one sector [04 R-COLL-01 §4A].
func (s *System) VisitOverlapCandidates(fn func(id int)) {
	if s == nil || s.world == nil || fn == nil {
		return
	}
	s.world.VisitActiveSlots(func(v units.SlotVisit) {
		fn(int(v.Handle))
	})
}

// RestampFootprint re-runs the stamp loop for one unit at its cached pair
// through the grid, so the overlap protocol arbitrates every cell again
// [04 R-COLL-01 §4]. The building class stamps only the cells its yard map
// still selects and releases the self-held cells it no longer selects; a mover
// stamps the plane of its committed mode, and modes 0 and 3 stamp nothing.
func (s *System) RestampFootprint(id int) {
	if s == nil || s.Grid == nil {
		return
	}
	h := pool.Handle(id)
	coll := s.Collisions[h]
	if coll == nil {
		return
	}
	fx, fz := coll.FootPrintX, coll.FootPrintZ
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	if coll.Building {
		if len(coll.Yard) != int(fx)*int(fz) {
			return
		}
		for dz := int32(0); dz < int32(fz); dz++ {
			for dx := int32(0); dx < int32(fx); dx++ {
				c := Cell{X: coll.CachedAnchor.X + dx, Z: coll.CachedAnchor.Z + dz}
				if coll.Yard[int(dz)*int(fx)+int(dx)].Selects(coll.YardOpen) {
					s.Grid.StampPlane(PlaneGround, c, 1, 1, coll.ID)
					continue
				}
				s.Grid.ReleaseCellIfSelf(PlaneGround, c, coll.ID)
			}
		}
		coll.StampedAnchor = coll.CachedAnchor
		coll.StampedPlane = PlaneGround
		coll.HasStamp = s.Grid.RectOnMap(coll.CachedAnchor, fx, fz)
		return
	}
	plane, stamps := planeForMode(coll.CachedMode)
	if !stamps {
		return
	}
	s.Grid.StampPlane(plane, coll.CachedAnchor, fx, fz, coll.ID)
	// The restamp is at the cached pair, which is where every writer stamps
	// [04 R-COLL-01 §4], so that pair is now where this identity's occupancy
	// is and the next clear must subtract exactly it.
	coll.StampedAnchor = coll.CachedAnchor
	coll.StampedPlane = plane
	coll.HasStamp = s.Grid.RectOnMap(coll.CachedAnchor, fx, fz)
}

// overlapUnit resolves an occupancy identity to its live unit. An identity the
// unit world does not hold — a construction placement reservation for a
// product that has no unit yet — has no flag word to write, which is exactly
// what a false second result from OverlapOwner reports.
func (s *System) overlapUnit(id int) *units.Unit {
	if s == nil || id <= 0 {
		return nil
	}
	u := s.unitFor(pool.Handle(id))
	if u == nil || !u.Alive {
		return nil
	}
	return u
}
