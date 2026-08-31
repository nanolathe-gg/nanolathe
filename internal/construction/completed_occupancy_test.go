package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func completedOccupancyTerrain(w, h int32) *world.Terrain {
	terrain := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return terrain
}

func completedOccupancyRect(t *testing.T, x, z, w, h int32) world.FootprintRect {
	t.Helper()
	extent, err := world.NewFootprintExtent(w, h)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(x, z), extent)
	if err != nil {
		t.Fatal(err)
	}
	return rect
}

func TestCompletedAllOYardRetainsCanonicalGroundWordsUntilRelease(t *testing.T) {
	terrain := completedOccupancyTerrain(12, 12)
	def := &content.UnitDef{UnitName: "extractor", FootprintX: 3, FootprintZ: 3, YardMap: "o", BMCode: false}
	rect := completedOccupancyRect(t, 3, 4, 3, 3)
	u := &units.Unit{Handle: pool.Handle(7), Def: def, Alive: true}
	svc := NewService(terrain, nil, nil, nil)
	if err := svc.reservePlacement(u.Handle, def, rect); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(u.Handle, def, rect)
	svc.applyCompletionPosture(u)

	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := terrain.PlotAt(x, z)
			if got := cell.OccupantA(); got != int16(u.Handle) {
				t.Fatalf("completed extractor cell %d,%d occupant=%d, want %d", x, z, got, u.Handle)
			}
			if !cell.StructureYard() {
				t.Fatalf("completed extractor cell %d,%d lacks structure-yard mark", x, z)
			}
		}
	}
	yard, err := world.ParseYardMap(def.YardMap, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	rules := world.PlacementRules{
		Domain: content.MobilityGround, ProfileResolved: true,
		MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: -10000,
	}
	if _, err := terrain.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: 0}); err == nil {
		t.Fatal("canonical placement query accepted the occupied extractor site")
	}
	if !svc.ReleasePlacement(u.Handle) {
		t.Fatal("completed extractor placement did not release at death/finalization")
	}
	if _, ok := svc.PlacementForProduct(u.Handle); ok {
		t.Fatal("completed extractor placement record survived release")
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := terrain.PlotAt(x, z)
			if cell.OccupantA() != 0 || cell.StructureYard() {
				t.Fatalf("released extractor cell %d,%d = occupant %d yard=%t", x, z, cell.OccupantA(), cell.StructureYard())
			}
		}
	}
}

func TestCompletedMobileStillReleasesPlacement(t *testing.T) {
	terrain := completedOccupancyTerrain(8, 8)
	def := &content.UnitDef{UnitName: "mobile", FootprintX: 2, FootprintZ: 2, BMCode: true}
	rect := completedOccupancyRect(t, 2, 2, 2, 2)
	u := &units.Unit{Handle: pool.Handle(9), Def: def, Alive: true}
	svc := NewService(terrain, nil, nil, nil)
	if err := svc.reservePlacement(u.Handle, def, rect); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(u.Handle, def, rect)
	svc.applyCompletionPosture(u)
	if _, ok := svc.PlacementForProduct(u.Handle); ok {
		t.Fatal("completed mobile retained its placement record")
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			if got := terrain.PlotAt(x, z).OccupantA(); got != 0 {
				t.Fatalf("completed mobile cell %d,%d occupant=%d, want 0", x, z, got)
			}
		}
	}
}

func TestEmptyBuildingYardUsesDefaultOccupiedParserBuffer(t *testing.T) {
	terrain := completedOccupancyTerrain(8, 8)
	def := &content.UnitDef{UnitName: "emptyyard", FootprintX: 2, FootprintZ: 2, BMCode: false}
	rect := completedOccupancyRect(t, 2, 3, 2, 2)
	yard, err := buildingYard(def, rect)
	if err != nil {
		t.Fatalf("empty building yard: %v", err)
	}
	if len(yard) != 4 {
		t.Fatalf("empty building yard length = %d, want 4", len(yard))
	}
	for i, cell := range yard {
		if cell != world.YardCell(0x2f) {
			t.Fatalf("empty building yard cell %d = %#x, want default o (0x2f)", i, cell)
		}
	}
	svc := NewService(terrain, nil, nil, nil)
	if err := svc.reservePlacement(pool.Handle(10), def, rect); err != nil {
		t.Fatalf("reserve empty building yard: %v", err)
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := terrain.PlotAt(x, z)
			if cell.OccupantA() != 10 || !cell.StructureYard() {
				t.Fatalf("default yard cell %d,%d = occupant %d yard=%t, want occupied by 10", x, z, cell.OccupantA(), cell.StructureYard())
			}
		}
	}
}

func TestYardOpenTransactionPreflightsBeforeBitAndRestamp(t *testing.T) {
	terrain := completedOccupancyTerrain(10, 10)
	def := &content.UnitDef{UnitName: "factory", FootprintX: 3, FootprintZ: 1, YardMap: "cCc", BMCode: false}
	rect := completedOccupancyRect(t, 3, 3, 3, 1)
	u := &units.Unit{Handle: pool.Handle(11), Def: def, Alive: true}
	svc := NewService(terrain, nil, nil, nil)
	if err := svc.reservePlacement(u.Handle, def, rect); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(u.Handle, def, rect)
	svc.applyCompletionPosture(u)

	if !svc.YardOpenTransaction(u, true) || !u.YardOpen {
		t.Fatal("unoccupied closed yard refused open transaction")
	}
	for x := rect.MinX(); x < rect.MaxX(); x++ {
		cell := terrain.PlotAt(x, rect.MinZ())
		if cell.OccupantA() != 0 || !cell.StructureYard() {
			t.Fatalf("open c/C cell %d = occupant %d yard=%t", x, cell.OccupantA(), cell.StructureYard())
		}
	}

	blocked := terrain.PlotAt(rect.MinX()+1, rect.MinZ())
	blocked.SetOccupantA(77)
	if svc.YardOpenTransaction(u, false) {
		t.Fatal("close transaction accepted a foreign occupant on the released pad")
	}
	if !u.YardOpen || blocked.OccupantA() != 77 {
		t.Fatalf("denied close mutated state: open=%t occupant=%d", u.YardOpen, blocked.OccupantA())
	}
	blocked.SetOccupantA(0)
	if !svc.YardOpenTransaction(u, false) || u.YardOpen {
		t.Fatal("cleared pad did not accept close transaction")
	}
	for x := rect.MinX(); x < rect.MaxX(); x++ {
		if got := terrain.PlotAt(x, rect.MinZ()).OccupantA(); got != int16(u.Handle) {
			t.Fatalf("closed c/C cell %d occupant=%d, want %d", x, got, u.Handle)
		}
	}
}

func TestReleasePlacementPreservesForeignGroundWord(t *testing.T) {
	terrain := completedOccupancyTerrain(8, 8)
	def := &content.UnitDef{UnitName: "factory", FootprintX: 2, FootprintZ: 1, YardMap: "oo", BMCode: false}
	rect := completedOccupancyRect(t, 2, 2, 2, 1)
	u := &units.Unit{Handle: pool.Handle(13), Def: def, Alive: true}
	svc := NewService(terrain, nil, nil, nil)
	svc.recordPlacement(u.Handle, def, rect)
	svc.stampBuilding(u.Handle, placementRecord{rect: rect, def: def}, false)
	terrain.PlotAt(3, 2).SetOccupantA(91)
	if !svc.ReleasePlacement(u.Handle) {
		t.Fatal("building placement was not released")
	}
	if terrain.PlotAt(2, 2).OccupantA() != 0 || terrain.PlotAt(3, 2).OccupantA() != 91 {
		t.Fatalf("release cleared foreign word: own=%d foreign=%d", terrain.PlotAt(2, 2).OccupantA(), terrain.PlotAt(3, 2).OccupantA())
	}
}
