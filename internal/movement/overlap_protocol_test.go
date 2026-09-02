package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The overlap protocol of [04 R-COLL-01 §4], per cell, row-major, as visited:
//
//	occupant := unit at the cell's word
//	if occupant's owner is active and in player state 3:
//	        occupant.flags |= intruder;  self.flags |= host;  cell.word := self
//	else:
//	        occupant.flags |= host;      self.flags |= intruder;  (cell keeps occupant)
//
// and, on clear: bit 27 cleared; if bit 26 was set, both cleared and the
// overlap scan runs, restamping the intruders whose rectangle intersects.
//
// These tests lock the arbitration, the two bits, the visit order and the
// scan. They assert relationships on a handful of cells, never a census.

// overlapFixture is a hand-built OverlapUnits: slot-indexed slices, never a
// map, so nothing here can smuggle nondeterministic order into a contract
// these tests then bless [I1].
type overlapFixture struct {
	owner     []uint8
	host      []bool
	intruder  []bool
	rect      []overlapRect
	live      []int
	asked     []int // every identity the protocol arbitrated, in order
	restamped []int
	restamp   func(id int)
}

type overlapRect struct {
	anchor Cell
	fx, fz int16
	ok     bool
}

func newOverlapFixture(slots int) *overlapFixture {
	return &overlapFixture{
		owner:    make([]uint8, slots),
		host:     make([]bool, slots),
		intruder: make([]bool, slots),
		rect:     make([]overlapRect, slots),
	}
}

// add files a unit at a slot: an owner byte, a rectangle, and membership of
// the live traversal.
func (f *overlapFixture) add(id int, owner uint8, anchor Cell, fx, fz int16) {
	f.owner[id] = owner
	f.rect[id] = overlapRect{anchor: anchor, fx: fx, fz: fz, ok: true}
	f.live = append(f.live, id)
}

func (f *overlapFixture) OverlapOwner(id int) (uint8, bool) {
	if id <= 0 || id >= len(f.owner) || !f.rect[id].ok {
		return 0, false
	}
	f.asked = append(f.asked, id)
	return f.owner[id], true
}

func (f *overlapFixture) OverlapFlags(id int) (bool, bool) {
	if id <= 0 || id >= len(f.host) {
		return false, false
	}
	return f.host[id], f.intruder[id]
}

func (f *overlapFixture) SetOverlapFlags(id int, host, intruder bool) {
	if id <= 0 || id >= len(f.host) {
		return
	}
	f.host[id], f.intruder[id] = host, intruder
}

func (f *overlapFixture) OverlapRect(id int) (Cell, int16, int16, bool) {
	if id <= 0 || id >= len(f.rect) || !f.rect[id].ok {
		return Cell{}, 0, 0, false
	}
	r := f.rect[id]
	return r.anchor, r.fx, r.fz, true
}

func (f *overlapFixture) VisitOverlapCandidates(fn func(id int)) {
	for _, id := range f.live {
		fn(id)
	}
}

func (f *overlapFixture) RestampFootprint(id int) {
	f.restamped = append(f.restamped, id)
	if f.restamp != nil {
		f.restamp(id)
	}
}

// activeState and eliminatedState are the two owner rows the protocol
// distinguishes: any playing row versus the state-3 row [04 R-COLL-01 §4].
const (
	activeState     uint8 = 1
	eliminatedState uint8 = eliminatedPlayerState
)

// overlapGrid builds a grid over a small flat map with the fixture bound.
func overlapGrid(f *overlapFixture) (*OccupancyGrid, *world.Terrain) {
	ter := syntheticFlat(24, 24)
	g := NewOccupancyGrid()
	g.AttachPlot(ter)
	g.AttachOverlap(f, func(owner uint8) uint8 { return owner })
	return g, ter
}

// TestOverlapBitsAreDistinctHighStatusBits guards the two masks against the
// rest of the runtime status word [04 R-COLL-01 §4]: bits 26 and 27, and no
// overlap with the word's other named bits.
func TestOverlapBitsAreDistinctHighStatusBits(t *testing.T) {
	if units.OverlapHostStatus != 1<<26 || units.OverlapIntruderStatus != 1<<27 {
		t.Fatalf("overlap bits are %#x/%#x, want bits 26/27 [04 R-COLL-01 §4]",
			units.OverlapHostStatus, units.OverlapIntruderStatus)
	}
	others := units.BuildingClassStatus | units.ArmedStatus | units.ClassifierEligibleStatus
	if others&(units.OverlapHostStatus|units.OverlapIntruderStatus) != 0 {
		t.Fatalf("overlap bits collide with another status bit [04 R-COLL-01 §4]")
	}
}

// TestOverlapFreeCellTakesTheIdentityWithNoBits is the protocol's first
// outcome: "A free cell simply takes the self identity" — no arbitration, no
// bits [04 R-COLL-01 §4].
func TestOverlapFreeCellTakesTheIdentityWithNoBits(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(7, activeState, Cell{X: 5, Z: 5}, 1, 1)
	g, ter := overlapGrid(f)

	if !g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 7) {
		t.Fatalf("a free cell must be taken [04 R-COLL-01 §4]")
	}
	if id, ok := g.OccupantAt(Cell{X: 5, Z: 5}); !ok || id != 7 {
		t.Fatalf("cell holds (%d,%v), want 7 [04 R-COLL-01 §4]", id, ok)
	}
	if ground, _ := plotWords(t, ter, Cell{X: 5, Z: 5}); ground != 7 {
		t.Fatalf("ground word is %d, want 7 [03 §2.2]", ground)
	}
	if f.host[7] || f.intruder[7] {
		t.Fatalf("an uncontested stamp raises neither bit [04 R-COLL-01 §4]")
	}
}

// TestOverlapDisplacesAnEliminatedOccupant is the protocol's second outcome:
// a state-3 owner's unit yields its cell to whoever stamps over it, the
// occupant becomes the intruder and the stamper the host [04 R-COLL-01 §4].
func TestOverlapDisplacesAnEliminatedOccupant(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(4, eliminatedState, Cell{X: 5, Z: 5}, 1, 1)
	f.add(7, activeState, Cell{X: 5, Z: 5}, 1, 1)
	g, ter := overlapGrid(f)

	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)
	if !g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 7) {
		t.Fatalf("a displaced cell is held by the stamper [04 R-COLL-01 §4]")
	}
	if id, _ := g.OccupantAt(Cell{X: 5, Z: 5}); id != 7 {
		t.Fatalf("cell holds %d, want the displacing identity 7 [04 R-COLL-01 §4]", id)
	}
	if ground, _ := plotWords(t, ter, Cell{X: 5, Z: 5}); ground != 7 {
		t.Fatalf("ground word is %d, want 7: the protocol writes cell.word := self [04 R-COLL-01 §4]", ground)
	}
	if !f.intruder[4] || f.host[4] {
		t.Fatalf("the displaced occupant is the intruder (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[4], f.intruder[4])
	}
	if !f.host[7] || f.intruder[7] {
		t.Fatalf("the displacing stamper is the host (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[7], f.intruder[7])
	}
}

// TestOverlapKeepsAnActiveOccupant is the protocol's third outcome: any owner
// that is not in the displacing state keeps the cell; the stamp does not fail,
// it records the bits the other way round [04 R-COLL-01 §4].
func TestOverlapKeepsAnActiveOccupant(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(4, activeState, Cell{X: 5, Z: 5}, 1, 1)
	f.add(7, activeState, Cell{X: 5, Z: 5}, 1, 1)
	g, ter := overlapGrid(f)

	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)
	if g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 7) {
		t.Fatalf("the rectangle is not wholly held when the occupant keeps a cell [04 R-COLL-01 §4]")
	}
	if id, _ := g.OccupantAt(Cell{X: 5, Z: 5}); id != 4 {
		t.Fatalf("cell holds %d, want the occupant 4 [04 R-COLL-01 §4]", id)
	}
	if ground, _ := plotWords(t, ter, Cell{X: 5, Z: 5}); ground != 4 {
		t.Fatalf("ground word is %d, want 4: the cell keeps its occupant [04 R-COLL-01 §4]", ground)
	}
	if !f.host[4] || f.intruder[4] {
		t.Fatalf("the keeping occupant is the host (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[4], f.intruder[4])
	}
	if f.host[7] || !f.intruder[7] {
		t.Fatalf("the refused stamper is the intruder (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[7], f.intruder[7])
	}
}

// TestOverlapArbitratesRowMajorPerCell locks the order the section states:
// "one cell at a time, row-major over the rectangle, each cell arbitrated as
// it is visited — so a rectangle can end half displaced when the occupants
// differ" [04 R-COLL-01 §4].
func TestOverlapArbitratesRowMajorPerCell(t *testing.T) {
	f := newOverlapFixture(16)
	// Four occupants, one per cell of a 2x2 rectangle at (5,5), alternating
	// owner state so the outcome differs cell by cell.
	f.add(1, eliminatedState, Cell{X: 5, Z: 5}, 1, 1)
	f.add(2, activeState, Cell{X: 6, Z: 5}, 1, 1)
	f.add(3, activeState, Cell{X: 5, Z: 6}, 1, 1)
	f.add(4, eliminatedState, Cell{X: 6, Z: 6}, 1, 1)
	f.add(9, activeState, Cell{X: 5, Z: 5}, 2, 2)
	g, _ := overlapGrid(f)

	for id := 1; id <= 4; id++ {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, 1, 1, id)
	}
	f.asked = nil
	if g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 2, 2, 9) {
		t.Fatalf("two of the four cells keep their occupant, so the rectangle is not wholly held [04 R-COLL-01 §4]")
	}
	// dz outer, dx inner: (5,5) (6,5) (5,6) (6,6), i.e. occupants 1,2,3,4.
	want := []int{1, 2, 3, 4}
	if len(f.asked) != len(want) {
		t.Fatalf("arbitrated %v, want one arbitration per contested cell %v [04 R-COLL-01 §4]", f.asked, want)
	}
	for i := range want {
		if f.asked[i] != want[i] {
			t.Fatalf("arbitration order %v, want row-major %v [04 R-COLL-01 §4][04 §8.2 C25]", f.asked, want)
		}
	}
	// Half displaced: the two eliminated owners' cells are the stamper's, the
	// two active owners' cells are not.
	for _, tc := range []struct {
		c    Cell
		want int
	}{
		{Cell{X: 5, Z: 5}, 9},
		{Cell{X: 6, Z: 5}, 2},
		{Cell{X: 5, Z: 6}, 3},
		{Cell{X: 6, Z: 6}, 9},
	} {
		if id, _ := g.OccupantAt(tc.c); id != tc.want {
			t.Fatalf("cell %v holds %d, want %d — the rectangle ends half displaced [04 R-COLL-01 §4]", tc.c, id, tc.want)
		}
	}
}

// TestClearWithHostBitRunsTheOverlapScan is the clear's order: "flags bit 27
// is cleared, and if bit 26 was set both 26 and 27 are cleared and the overlap
// scan runs: every live unit ... whose own rectangle intersects it is passed
// to the restamp" — and the restamp's gate is bit 27, so only the intruder is
// restamped [04 R-COLL-01 §4].
func TestClearWithHostBitRunsTheOverlapScan(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(4, activeState, Cell{X: 5, Z: 5}, 1, 1) // the holder, becomes host
	f.add(7, activeState, Cell{X: 5, Z: 5}, 1, 1) // the refused stamper, intruder
	f.add(8, activeState, Cell{X: 20, Z: 20}, 1, 1)
	f.intruder[8] = true // an intruder somewhere else: not in the rectangle
	g, _ := overlapGrid(f)
	f.restamp = func(id int) {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	}

	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 7)
	if !f.host[4] || !f.intruder[7] {
		t.Fatalf("the refused stamp must leave host on 4 and intruder on 7 [04 R-COLL-01 §4]")
	}

	if !g.ClearPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4) {
		t.Fatalf("the holder's clear must release its cell [04 R-COLL-01 §4]")
	}
	if f.host[4] || f.intruder[4] {
		t.Fatalf("a clear with bit 26 set clears both bits (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[4], f.intruder[4])
	}
	if len(f.restamped) != 1 || f.restamped[0] != 7 {
		t.Fatalf("restamped %v, want only the intersecting intruder [7] [04 R-COLL-01 §4]", f.restamped)
	}
	if f.intruder[7] {
		t.Fatalf("the restamp clears the intruder bit it is gated on [04 R-COLL-01 §4]")
	}
	if id, ok := g.OccupantAt(Cell{X: 5, Z: 5}); !ok || id != 7 {
		t.Fatalf("cell holds (%d,%v), want the restamped intruder 7 [04 R-COLL-01 §4]", id, ok)
	}
	if !f.intruder[8] {
		t.Fatalf("an intruder outside the rectangle is not in the scan [04 R-COLL-01 §4]")
	}
}

// TestClearWithoutHostBitOnlyClearsTheIntruderBit is the other arm of the same
// step: with bit 26 clear the clear lowers bit 27 and runs no scan
// [04 R-COLL-01 §4].
func TestClearWithoutHostBitOnlyClearsTheIntruderBit(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(4, activeState, Cell{X: 5, Z: 5}, 1, 1)
	f.add(7, activeState, Cell{X: 5, Z: 5}, 1, 1)
	f.intruder[7] = true
	g, _ := overlapGrid(f)

	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)
	f.host[4], f.intruder[4] = false, true
	g.ClearPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)

	if f.intruder[4] || f.host[4] {
		t.Fatalf("bit 27 is cleared unconditionally (host=%v intruder=%v) [04 R-COLL-01 §4]", f.host[4], f.intruder[4])
	}
	if len(f.restamped) != 0 {
		t.Fatalf("restamped %v; the scan runs only when bit 26 was set [04 R-COLL-01 §4]", f.restamped)
	}
}

// TestBuildingStampTakesTheSameProtocol: "The protocol is identical for the
// ground and air planes and for the building class" [04 R-COLL-01 §4]. The
// building path selects its cells by yard byte and stamps each one through the
// same grid call the construction service uses.
func TestBuildingStampTakesTheSameProtocol(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(4, eliminatedState, Cell{X: 5, Z: 5}, 1, 1)
	f.add(5, activeState, Cell{X: 6, Z: 5}, 1, 1)
	f.add(9, activeState, Cell{X: 5, Z: 5}, 2, 1)
	g, ter := overlapGrid(f)
	s := &System{Grid: g}

	g.StampPlane(PlaneGround, Cell{X: 5, Z: 5}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: 6, Z: 5}, 1, 1, 5)

	// A 2x1 yard whose two bytes are both selected in either state ('o' is
	// selected open and closed [R-P0-08]).
	yard, err := world.ParseYardMap("oo", 2, 1)
	if err != nil {
		t.Fatalf("yard: %v", err)
	}
	if !s.stampBuildingGrid(Cell{X: 5, Z: 5}, 2, 1, yard, false, 9) {
		t.Fatalf("the building stamp takes at least the displaced cell [04 R-COLL-01 §4]")
	}
	if id, _ := g.OccupantAt(Cell{X: 5, Z: 5}); id != 9 {
		t.Fatalf("the eliminated owner's cell holds %d, want the building 9 [04 R-COLL-01 §4]", id)
	}
	if id, _ := g.OccupantAt(Cell{X: 6, Z: 5}); id != 5 {
		t.Fatalf("the active owner's cell holds %d, want its occupant 5 [04 R-COLL-01 §4]", id)
	}
	if ground, _ := plotWords(t, ter, Cell{X: 5, Z: 5}); ground != 9 {
		t.Fatalf("ground word is %d, want 9 [04 R-COLL-01 §4]", ground)
	}
	if !f.intruder[4] || !f.host[9] || !f.intruder[9] || !f.host[5] {
		t.Fatalf("bits after a half-displacing building stamp: 4=%v/%v 5=%v/%v 9=%v/%v [04 R-COLL-01 §4]",
			f.host[4], f.intruder[4], f.host[5], f.intruder[5], f.host[9], f.intruder[9])
	}
}

// TestRestampFootprintReleasesDeselectedBuildingCells locks the restamp's
// building arm: "for the building class a cell the yard map no longer selects
// that holds the self identity is released to 0" [04 R-COLL-01 §4].
func TestRestampFootprintReleasesDeselectedBuildingCells(t *testing.T) {
	f := newOverlapFixture(16)
	f.add(9, activeState, Cell{X: 5, Z: 5}, 2, 1)
	f.intruder[9] = true
	g, ter := overlapGrid(f)
	f.restamp = nil

	// 'c' is selected only while the yard is CLOSED, 'o' in both states
	// [04 R-COLL-01 §4][R-P0-08]. Stamp closed, then reopen and restamp.
	yard, err := world.ParseYardMap("oc", 2, 1)
	if err != nil {
		t.Fatalf("yard: %v", err)
	}
	coll := &CollisionState{
		ID: 9, FootPrintX: 2, FootPrintZ: 1, Building: true,
		Yard: yard, YardOpen: false, CachedAnchor: Cell{X: 5, Z: 5}, CachedMode: 1,
	}
	s := &System{Grid: g, Collisions: map[pool.Handle]*CollisionState{9: coll}}
	f.restamp = s.RestampFootprint

	if !s.stampBuildingGrid(Cell{X: 5, Z: 5}, 2, 1, yard, false, 9) {
		t.Fatalf("the closed yard stamps both cells [04 R-COLL-01 §4]")
	}
	coll.YardOpen = true
	g.Restamp(9)

	if id, ok := g.OccupantAt(Cell{X: 5, Z: 5}); !ok || id != 9 {
		t.Fatalf("the still-selected cell holds (%d,%v), want 9 [04 R-COLL-01 §4]", id, ok)
	}
	if id, ok := g.OccupantAt(Cell{X: 6, Z: 5}); ok {
		t.Fatalf("the deselected cell still holds %d; the restamp releases it [04 R-COLL-01 §4]", id)
	}
	if ground, _ := plotWords(t, ter, Cell{X: 6, Z: 5}); ground != 0 {
		t.Fatalf("the deselected cell's ground word is %d, want 0 [04 R-COLL-01 §4]", ground)
	}
	if f.intruder[9] {
		t.Fatalf("the restamp clears the intruder bit it is gated on [04 R-COLL-01 §4]")
	}
}

// TestOverlapProtocolIsInertWithoutABinding: a grid with no overlap window
// keeps its pre-protocol behavior — the occupant keeps every contested cell,
// which is the branch retail takes for every owner that is not in the
// displacing state [04 R-COLL-01 §4]. Every movement fixture depends on it.
func TestOverlapProtocolIsInertWithoutABinding(t *testing.T) {
	ter := syntheticFlat(16, 16)
	g := NewOccupancyGrid()
	g.AttachPlot(ter)

	g.StampPlane(PlaneGround, Cell{X: 4, Z: 4}, 1, 1, 3)
	if g.StampPlane(PlaneGround, Cell{X: 4, Z: 4}, 1, 1, 6) {
		t.Fatalf("an unbound grid keeps the occupant [04 R-COLL-01 §4]")
	}
	if id, _ := g.OccupantAt(Cell{X: 4, Z: 4}); id != 3 {
		t.Fatalf("cell holds %d, want the occupant 3 [04 R-COLL-01 §4]", id)
	}
}
