package movement

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Empty extents retain the ordinary mover and cargo transforms, while every
// occupancy writer and clearer visits zero cells [04 R-P0-08-C].
func TestEmptyMobileFootprintCarriedLifecycle(t *testing.T) {
	for _, pair := range [][2]int32{{0, 0}, {0, 2}, {2, 0}} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			terrain := syntheticTerrainForIntegrate()
			sys := NewSystem(terrain, Template(), NewOccupancyGrid())
			w := newMovementFixtureWorld(8)
			sys.BindWorld(w)
			carrierH, err := w.Create(&content.UnitDef{UnitName: "empty-carrier", BMCode: 0, FootprintX: 2, FootprintZ: 2}, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
			if err != nil {
				t.Fatal(err)
			}
			def := &content.UnitDef{UnitName: "empty-cargo", BMCode: 1, FootprintX: pair[0], FootprintZ: pair[1], Upright: true}
			cargoH, err := w.Create(def, 0, numeric.Fixed(40<<16), 0, numeric.Fixed(40<<16))
			if err != nil {
				t.Fatal(err)
			}
			cargo, carrier := w.Unit(cargoH), w.Unit(carrierH)
			sys.Grid.Stamp(Cell{X: 2, Z: 2}, 4, 4, 77)
			before := append([]world.PlotCell(nil), terrain.Plot...)
			revision := sys.Grid.Revision()
			sys.EnsureUnit(cargo)
			coll := handleRow(sys.Collisions, cargoH)
			if coll == nil || coll.FootPrintX != int16(pair[0]) || coll.FootPrintZ != int16(pair[1]) {
				t.Fatalf("collision extents=%+v", coll)
			}
			if coll.CachedAnchor != (Cell{X: int32((40 + 8 - pair[0]*8) / 16), Z: int32((40 + 8 - pair[1]*8) / 16)}) {
				t.Fatalf("anchor=%v", coll.CachedAnchor)
			}
			if !AttachCargoMode(w, carrierH, cargoH, -1, 1) {
				t.Fatal("ordinary attachment refused")
			}
			carrier.X += world.CellToWorld(1)
			sys.SyncCarriedMotion(w)
			if cargo.X != carrier.X || cargo.Z != carrier.Z {
				t.Fatal("empty cargo did not follow carrier")
			}
			if _, ok := DetachCargo(w, cargoH); !ok {
				t.Fatal("detach failed")
			}
			sys.RestampFootprint(int(cargoH))
			sys.StepUnit(cargoH, 1)
			sys.ForgetUnit(cargoH)
			if !reflect.DeepEqual(before, terrain.Plot) || sys.Grid.Revision() != revision {
				t.Fatal("empty lifecycle changed occupancy")
			}
		})
	}
}

func TestEmptyCollisionFootprintBoundsAndWalk(t *testing.T) {
	terrain := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)}
	grid := NewOccupancyGrid()
	grid.AttachPlot(terrain)
	for _, pair := range [][2]int16{{0, 0}, {0, 2}, {2, 0}} {
		edge := Cell{X: 7 - int32(pair[0]), Z: 7 - int32(pair[1])}
		if !grid.RectOnMap(edge, pair[0], pair[1]) || !commitRectInBounds(terrain, edge, pair[0], pair[1]) {
			t.Fatalf("empty edge %v %v rejected", edge, pair)
		}
		edge.X++
		if grid.RectOnMap(edge, pair[0], pair[1]) || commitRectInBounds(terrain, edge, pair[0], pair[1]) {
			t.Fatal("strict empty bound lost")
		}
		if !ValidateFootprint(Cell{}, pair[0], pair[1], func(Cell) bool { t.Fatal("empty footprint visited a cell"); return false }, nil) {
			t.Fatal("empty cell walk rejected")
		}
		// Bounds are still an independent gate even though no cell is visited.
		if ValidateFootprint(Cell{}, pair[0], pair[1], nil, func() bool { return false }) {
			t.Fatal("empty footprint skipped caller bounds")
		}
	}
}

// The retail sector visitor has four strict interval comparisons without an
// empty-axis guard. This is candidate selection, not cell stamping [04 R-P0-08-C].
func TestEmptyOverlapCandidateUsesRetailIntervalComparisons(t *testing.T) {
	outer := Cell{X: 2, Z: 2}
	inner := Cell{X: 3, Z: 3}
	for _, pair := range [][2]int16{{0, 0}, {0, 1}, {1, 0}} {
		if !rectsIntersect(outer, 4, 4, inner, pair[0], pair[1]) || !rectsIntersect(inner, pair[0], pair[1], outer, 4, 4) {
			t.Fatalf("interior degenerate %v was excluded from retail candidate scan", pair)
		}
	}
	if rectsIntersect(outer, 4, 4, Cell{X: 2, Z: 3}, 0, 1) || rectsIntersect(outer, 4, 4, Cell{X: 6, Z: 3}, 0, 1) {
		t.Fatal("zero-width boundary candidate lost strict interval comparison")
	}
	if rectsIntersect(outer, 4, 4, Cell{X: 3, Z: 2}, 1, 0) || rectsIntersect(outer, 4, 4, Cell{X: 3, Z: 6}, 1, 0) {
		t.Fatal("zero-depth boundary candidate lost strict interval comparison")
	}
}
