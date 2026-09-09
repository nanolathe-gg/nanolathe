package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The clear's overlap scan visits its candidates in sector-bucket order
// [04 R-COLL-01 §4A]: sector column ascending in the outer loop, sector row
// ascending in the inner, over the rectangle's sector span grown by one sector
// on every side, with a unit filed in the off-map sector record never reached.
// A sector is eight cells on a side, and the stamp indexes the grid from the
// unit's committed 16.16 position, not from its cell pair.
//
// The other half — a bucket reads back from its head, so in reverse order of
// each unit's most recent relink — is carried by the sector filing the stamp
// maintains on the collision record [04 R-COLL-01 §11]; the last test here
// locks it through a filing-aware fixture.

// sectorFixture is an overlapFixture that also answers the committed position,
// so the scan takes the position path rather than the rectangle-centre
// fallback. Slot-indexed slices, never a map [I1].
type sectorFixture struct {
	*overlapFixture
	posX []int32
	posZ []int32
}

func newSectorFixture(slots int) *sectorFixture {
	return &sectorFixture{
		overlapFixture: newOverlapFixture(slots),
		posX:           make([]int32, slots),
		posZ:           make([]int32, slots),
	}
}

// addAt files a unit with a rectangle and the 16.16 position the stamp would
// have filed its sector from — the centre of its footprint.
func (f *sectorFixture) addAt(id int, owner uint8, anchor Cell, fx, fz int16) {
	f.add(id, owner, anchor, fx, fz)
	f.posX[id] = int32(int64(anchor.X)*worldUnitsPerCell + int64(fx)*worldUnitsPerCell/2)
	f.posZ[id] = int32(int64(anchor.Z)*worldUnitsPerCell + int64(fz)*worldUnitsPerCell/2)
}

func (f *sectorFixture) OverlapPosition(id int) (int32, int32, bool) {
	if id <= 0 || id >= len(f.posX) || !f.rect[id].ok {
		return 0, 0, false
	}
	return f.posX[id], f.posZ[id], true
}

func sectorGrid(f *sectorFixture) (*OccupancyGrid, *world.Terrain) {
	ter := syntheticFlat(64, 64)
	g := NewOccupancyGrid()
	g.AttachPlot(ter)
	g.AttachOverlap(f, func(owner uint8) uint8 { return owner })
	return g, ter
}

// TestOverlapScanOrdersContendersBySectorColumn is the contention case the
// scan's order decides [04 R-COLL-01 §4A]: two intruders were both refused the
// same cell of a host, the host clears it, and the first one restamped takes
// it. Both intruders are anchored in the same *cell* sector (14>>3 and 15>>3
// are both 1); their committed positions are one sector apart, and it is the
// position the stamp files from — so the lower sector column is restamped
// first even though it holds the higher pool slot, which is the order this
// scan used to walk.
func TestOverlapScanOrdersContendersBySectorColumn(t *testing.T) {
	const contested = 15 // cell column 15 is the last of sector column 1

	f := newSectorFixture(16)
	f.addAt(4, activeState, Cell{X: contested, Z: 10}, 1, 1)     // the holder, becomes host
	f.addAt(6, activeState, Cell{X: contested, Z: 10}, 2, 1)     // covers 15..16, sector column 2
	f.addAt(9, activeState, Cell{X: contested - 1, Z: 10}, 2, 1) // covers 14..15, sector column 1
	g, _ := sectorGrid(f)
	f.restamp = func(id int) {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	}

	// The two anchors share a cell sector; the two positions do not. If this
	// ever stops holding the test is no longer testing the position path.
	if sectorOfCell(contested) != sectorOfCell(contested-1) {
		t.Fatalf("the two anchors must share a cell sector for this case [04 R-COLL-01 §4A]")
	}
	sx6, _, ok6 := g.unitSector(6, f.rect[6].anchor, 2, 1, f)
	sx9, _, ok9 := g.unitSector(9, f.rect[9].anchor, 2, 1, f)
	if !ok6 || !ok9 || sx9 != sx6-1 {
		t.Fatalf("sector columns are %d/%d (filed %v/%v), want 9 one column below 6 [04 R-COLL-01 §4A]", sx9, sx6, ok9, ok6)
	}

	g.StampPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: contested, Z: 10}, 2, 1, 6)
	g.StampPlane(PlaneGround, Cell{X: contested - 1, Z: 10}, 2, 1, 9)
	if !f.host[4] || !f.intruder[6] || !f.intruder[9] {
		t.Fatalf("both refused stamps must leave host on 4 and intruder on 6 and 9 [04 R-COLL-01 §4]")
	}

	if !g.ClearPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 4) {
		t.Fatalf("the holder's clear must release its cell [04 R-COLL-01 §4]")
	}

	// The live traversal offers 4, 6, 9 in slot order; the sweep restamps the
	// lower sector column first.
	if len(f.restamped) != 2 || f.restamped[0] != 9 || f.restamped[1] != 6 {
		t.Fatalf("restamped %v, want [9 6] — sector column ascending, not slot ascending [04 R-COLL-01 §4A]", f.restamped)
	}
	if id, ok := g.OccupantAt(Cell{X: contested, Z: 10}); !ok || id != 9 {
		t.Fatalf("the released cell holds (%d,%v), want the first-restamped intruder 9 [04 R-COLL-01 §4A]", id, ok)
	}
	// The loser was arbitrated again on its way past and is an intruder once
	// more; the winner is now the host of the cell it took.
	if !f.intruder[6] || !f.host[9] {
		t.Fatalf("after the scan: 6 intruder=%v, 9 host=%v [04 R-COLL-01 §4]", f.intruder[6], f.host[9])
	}
}

// TestOverlapScanSkipsTheOffMapSectorRecord: a unit whose own rectangle fails
// the stamp's bounds test is filed in the single off-map record, which is not
// in the sector array, so no sweep reaches it — its intruder bit survives the
// clear that would otherwise have restamped it [04 R-COLL-01 §4A]. The restamp
// would write no cell either way; what the sweep decides is whether the bit is
// consumed.
func TestOverlapScanSkipsTheOffMapSectorRecord(t *testing.T) {
	f := newSectorFixture(16)
	f.addAt(4, activeState, Cell{X: 60, Z: 10}, 1, 1) // the holder, becomes host
	f.addAt(5, activeState, Cell{X: 59, Z: 10}, 2, 1) // on the map, intersects
	f.addAt(8, activeState, Cell{X: 60, Z: 10}, 4, 1) // 60+4 == the map width: off-map
	g, _ := sectorGrid(f)
	f.restamp = func(id int) {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	}

	if g.RectOnMap(Cell{X: 60, Z: 10}, 4, 1) {
		t.Fatalf("the wide rectangle must fail the bounds test [04 R-COLL-01 §2]")
	}

	g.StampPlane(PlaneGround, Cell{X: 60, Z: 10}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: 59, Z: 10}, 2, 1, 5)
	// The off-map unit's stamp writes nothing, so the protocol never raises
	// its bit; retail's off-map filing is what keeps the scan away from it,
	// not the absence of the bit. Raise it by hand to test exactly that.
	f.intruder[8] = true

	if !f.host[4] || !f.intruder[5] {
		t.Fatalf("the refused stamp must leave host on 4 and intruder on 5 [04 R-COLL-01 §4]")
	}
	g.ClearPlane(PlaneGround, Cell{X: 60, Z: 10}, 1, 1, 4)

	if len(f.restamped) != 1 || f.restamped[0] != 5 {
		t.Fatalf("restamped %v, want only the on-map intruder [5] [04 R-COLL-01 §4A]", f.restamped)
	}
	if !f.intruder[8] {
		t.Fatalf("an off-map intruder keeps its bit: no sweep visits the off-map record [04 R-COLL-01 §4A]")
	}
}

// TestOverlapScanSpanReachesOneSectorPastTheRectangle locks the margin: the
// span is the rectangle's own sectors grown by one sector on every side, so a
// candidate filed a whole sector away is still visited when its rectangle
// reaches back into the cleared one [04 R-COLL-01 §4A].
func TestOverlapScanSpanReachesOneSectorPastTheRectangle(t *testing.T) {
	f := newSectorFixture(16)
	f.addAt(4, activeState, Cell{X: 16, Z: 24}, 1, 1) // sector column 2, row 3
	f.addAt(7, activeState, Cell{X: 9, Z: 24}, 8, 1)  // covers 9..16, sector column 1
	g, _ := sectorGrid(f)
	f.restamp = func(id int) {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	}

	sx4, _, _ := g.unitSector(4, f.rect[4].anchor, 1, 1, f)
	sx7, _, _ := g.unitSector(7, f.rect[7].anchor, 8, 1, f)
	if sx7 != sx4-1 {
		t.Fatalf("sector columns are %d/%d, want the candidate one column below [04 R-COLL-01 §4A]", sx7, sx4)
	}

	g.StampPlane(PlaneGround, Cell{X: 16, Z: 24}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: 9, Z: 24}, 8, 1, 7)
	if !f.host[4] || !f.intruder[7] {
		t.Fatalf("the refused stamp must leave host on 4 and intruder on 7 [04 R-COLL-01 §4]")
	}

	g.ClearPlane(PlaneGround, Cell{X: 16, Z: 24}, 1, 1, 4)
	if len(f.restamped) != 1 || f.restamped[0] != 7 {
		t.Fatalf("restamped %v, want [7] — the margin reaches one sector out [04 R-COLL-01 §4A]", f.restamped)
	}
	if id, ok := g.OccupantAt(Cell{X: 16, Z: 24}); !ok || id != 7 {
		t.Fatalf("the released cell holds (%d,%v), want the restamped intruder 7 [04 R-COLL-01 §4A]", id, ok)
	}
}

// filingFixture is a sectorFixture that also carries each unit's sector filing,
// so the stamp relinks and the scan orders by relink recency
// [04 R-COLL-01 §11]. Slot-indexed, never a map [I1].
type filingFixture struct {
	*sectorFixture
	filings []SectorFiling
}

func newFilingFixture(slots int) *filingFixture {
	return &filingFixture{sectorFixture: newSectorFixture(slots), filings: make([]SectorFiling, slots)}
}

func (f *filingFixture) OverlapFiling(id int) *SectorFiling {
	if id <= 0 || id >= len(f.filings) || !f.rect[id].ok {
		return nil
	}
	return &f.filings[id]
}

// moveTo re-files a unit at a new anchor and position, as a commit's clear and
// stamp would.
func (f *filingFixture) moveTo(g *OccupancyGrid, id int, anchor Cell) {
	r := f.rect[id]
	g.ClearPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	f.addAt(id, f.owner[id], anchor, r.fx, r.fz)
	f.live = f.live[:len(f.live)-1] // addAt appended the id again
	g.StampPlane(PlaneGround, anchor, r.fx, r.fz, id)
}

// TestOverlapScanOrdersOneSectorByRelinkRecency is the contention case inside
// ONE sector [04 R-COLL-01 §11]: two intruders refused the same cell, both
// filed in the same sector record. The bucket reads from its head, so the one
// that relinked most recently is restamped first and takes the cell — first
// the later-stamped higher slot; then, after the lower slot crosses a sector
// boundary and comes back, the lower slot ahead of a still later arrival.
func TestOverlapScanOrdersOneSectorByRelinkRecency(t *testing.T) {
	const contested = 12 // sector column 1, row 1

	f := newFilingFixture(16)
	f.addAt(4, activeState, Cell{X: contested, Z: 10}, 1, 1) // the holder
	f.addAt(6, activeState, Cell{X: contested, Z: 10}, 2, 1) // same sector as 9
	f.addAt(9, activeState, Cell{X: contested - 1, Z: 10}, 2, 1)
	g, _ := sectorGrid(f.sectorFixture)
	g.AttachOverlap(f, func(owner uint8) uint8 { return owner })
	f.restamp = func(id int) {
		r := f.rect[id]
		g.StampPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
	}

	g.StampPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 4)
	g.StampPlane(PlaneGround, Cell{X: contested, Z: 10}, 2, 1, 6)
	g.StampPlane(PlaneGround, Cell{X: contested - 1, Z: 10}, 2, 1, 9)
	if f.filings[6].SX != f.filings[9].SX || f.filings[6].SZ != f.filings[9].SZ {
		t.Fatalf("both intruders must be filed in one sector: %+v / %+v", f.filings[6], f.filings[9])
	}
	if !(f.filings[9].Seq > f.filings[6].Seq) {
		t.Fatalf("the later stamp must carry the later sequence: %+v / %+v", f.filings[6], f.filings[9])
	}

	// Round one: 9 was linked after 6, so it is nearer the head.
	if !g.ClearPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 4) {
		t.Fatalf("the holder's clear must release its cell [04 R-COLL-01 §4]")
	}
	if len(f.restamped) != 2 || f.restamped[0] != 9 || f.restamped[1] != 6 {
		t.Fatalf("restamped %v, want [9 6] — most recent relink first [04 R-COLL-01 §11]", f.restamped)
	}
	if id, _ := g.OccupantAt(Cell{X: contested, Z: 10}); id != 9 {
		t.Fatalf("the released cell holds %d, want 9", id)
	}

	// A stamp that does not cross a sector boundary keeps the unit's place.
	seq6 := f.filings[6].Seq
	f.moveTo(g, 6, Cell{X: contested, Z: 11})
	if f.filings[6].Seq != seq6 {
		t.Fatalf("a stamp inside the same sector must not relink: %+v", f.filings[6])
	}
	// Crossing into the next sector column and back relinks twice; 6 is now
	// the most recent entry of the shared sector.
	f.moveTo(g, 6, Cell{X: contested + 4, Z: 11})
	f.moveTo(g, 6, Cell{X: contested, Z: 10})
	if !(f.filings[6].Seq > f.filings[9].Seq) {
		t.Fatalf("the sector crossing must relink 6 ahead of 9: %+v / %+v", f.filings[6], f.filings[9])
	}

	// Round two: 9 holds the cell as host with 6 refused again; a fresh
	// intruder 7 is stamped later in the same sector. When 9 releases the
	// cell, 7 is nearer the head than 6.
	f.addAt(7, activeState, Cell{X: contested, Z: 10}, 1, 1)
	g.StampPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 7)
	f.restamped = nil
	if !g.ClearPlane(PlaneGround, Cell{X: contested, Z: 10}, 1, 1, 9) {
		t.Fatalf("9's clear must release its cell")
	}
	if len(f.restamped) != 2 || f.restamped[0] != 7 || f.restamped[1] != 6 {
		t.Fatalf("restamped %v, want [7 6] — 7 was linked after 6's return [04 R-COLL-01 §11]", f.restamped)
	}
}
