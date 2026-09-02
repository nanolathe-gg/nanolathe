// The external test package is the only place in internal/combat that can
// import internal/movement — internal/movement imports internal/combat, so an
// in-package test file would close the cycle.
package combat_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestStamperWritesTheCellsTheContactScanReads is the seam between WU-19-20 and
// WU-19-12: the occupancy stamper writes the ground word of every cell of a
// mover's footprint rectangle and nothing outside it [04 R-COLL-01 §4], and the
// contact scan of [06 R-DMG-01 §7] reads exactly one such word per cell. This
// test drives the production stamper — movement.System.EnsureUnit, the creation
// writer of the stamp census — and asserts the rectangle, so the direct
// SetOccupantA/SetOccupantB fixtures in contact_cell_test.go are writing what
// production writes rather than a shape of their own.
func TestStamperWritesTheCellsTheContactScanReads(t *testing.T) {
	const cells = 32
	ter := &world.Terrain{CellW: cells, CellH: cells}
	ter.Plot = make([]world.PlotCell, cells*cells)
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	sys := movement.NewSystem(ter, movement.Template(), movement.NewOccupancyGrid())

	// A 3×2 footprint: the rectangle is three cells across and two deep
	// [04 §6.3], and the anchor is the quantized north-west cell of
	// [04 R-COLL-01 §1].
	def := &content.UnitDef{UnitName: "stampfixture", MaxDamage: 100, Limit: -1,
		FootprintX: 3, FootprintZ: 2, CanMove: true, MaxVelocity: int32(numeric.FixedFromInt(1))}
	// The centre of the rectangle whose anchor is (8,8) for a 3×2 footprint:
	// anchor = (p + 8 − 8·f) >> 20 in world units [04 R-COLL-01 §1]. The unit
	// record is built directly so the test needs neither a COB provider nor a
	// pool slice; EnsureUnit reads only the handle, definition and position.
	const h = pool.Handle(7)
	u := &units.Unit{
		Handle: h,
		Def:    def,
		Owner:  1,
		Alive:  true,
		X:      world.CellToWorld(8) + numeric.Fixed(3*8*65536) - numeric.Fixed(8*65536),
		Y:      numeric.Fixed(0),
		Z:      world.CellToWorld(8) + numeric.Fixed(2*8*65536) - numeric.Fixed(8*65536),
	}
	sys.EnsureUnit(u)

	anchor, ok := stampedAnchor(ter, int16(h))
	if !ok {
		t.Fatal("EnsureUnit wrote no ground word; WU-19-20's stamp is the prerequisite for the contact scan")
	}
	for cz := int32(0); cz < cells; cz++ {
		for cx := int32(0); cx < cells; cx++ {
			inRect := cx >= anchor[0] && cx < anchor[0]+3 && cz >= anchor[1] && cz < anchor[1]+2
			cell := ter.PlotAt(cx, cz)
			if got := cell.OccupantA(); inRect != (got == int16(h)) {
				t.Fatalf("cell (%d,%d): ground word = %d, inside rectangle = %v", cx, cz, got, inRect)
			}
			if got := cell.OccupantB(); got != 0 {
				t.Fatalf("cell (%d,%d): a mode-1 mover must not touch the air word, got %d", cx, cz, got)
			}
		}
	}
}

// stampedAnchor returns the north-west cell holding id in the ground word.
func stampedAnchor(ter *world.Terrain, id int16) ([2]int32, bool) {
	for cz := int32(0); cz < ter.CellH; cz++ {
		for cx := int32(0); cx < ter.CellW; cx++ {
			if ter.PlotAt(cx, cz).OccupantA() == id {
				return [2]int32{cx, cz}, true
			}
		}
	}
	return [2]int32{}, false
}
