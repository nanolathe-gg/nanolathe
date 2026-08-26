// Package construction tests for factory lifecycle [PLAN_08 WU-08-5] C16–C19, C21, C22, C24.
package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func prodIdx(cat *content.Catalog, key string) uint32 {
	if cat != nil {
		if idx, ok := cat.UnitDefIndex(key); ok {
			return idx
		}
	}
	// fallback for nil cat or missing - use BuildDefKey directly; Param1 0 plus string handles identity
	return 0
}

// helper to create a trivial model with n pieces for QueryBuildInfo.
func trivialModel(pieceCount int, translations [][3]int64) *model.Model {
	m := &model.Model{
		Pieces: make([]model.Piece, pieceCount),
		Root:   0,
		Name:   "test",
	}
	for i := 0; i < pieceCount; i++ {
		var tx, ty, tz numeric.Fixed
		if i < len(translations) {
			tx = numeric.Fixed(translations[i][0])
			ty = numeric.Fixed(translations[i][1])
			tz = numeric.Fixed(translations[i][2])
		}
		m.Pieces[i] = model.Piece{
			Name:      "piece" + string(rune('0'+i)),
			Parent:    -1,
			Translate: [3]numeric.Fixed{tx, ty, tz},
		}
		if i > 0 {
			m.Pieces[i].Parent = 0
			m.Pieces[0].Children = append(m.Pieces[0].Children, i)
		}
	}
	return m
}

func newTestWorld(cap int) *units.World {
	cat := &content.Catalog{}
	return units.New(cap, cat)
}

func newFactoryDef(name string, footX, footZ int32, workerTime int32) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name,
		FootprintX:       footX,
		FootprintZ:       footZ,
		WorkerTime:       workerTime,
		BuildCostMetal:   100,
		BuildCostEnergy:  100,
		BuildTime:        100,
		MaxDamage:        100,
		YardMap:          "o", // simple yard
		Builder:          true,
	}
}

func newProductDef(name string, footX, footZ int32, metalCost int32, buildTime int32) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name,
		FootprintX:       footX,
		FootprintZ:       footZ,
		BuildCostMetal:   metalCost,
		BuildCostEnergy:  50,
		BuildTime:        buildTime,
		MaxDamage:        200,
		YardMap:          "o",
	}
}

// TestSnapHalfExtentBias verifies C16 half-extent bias vectors [05 "Factory production lifecycle"].
func TestSnapHalfExtentBias(t *testing.T) {
	// Vectors: foot 2x2 => bias 1,1 ; foot 3x3 => bias 1,1 ; foot 1x1 => 0,0 ; foot 4x2 => 2,1
	cases := []struct {
		footX, footZ   int
		worldX, worldZ numeric.Fixed
		wantX, wantZ   int32
	}{
		{2, 2, world.CellToWorld(5), world.CellToWorld(5), 4, 4},     // 5-1=4
		{3, 3, world.CellToWorld(5), world.CellToWorld(5), 4, 4},     // 3/2=1
		{1, 1, world.CellToWorld(5), world.CellToWorld(5), 5, 5},     // 0 bias
		{4, 2, world.CellToWorld(8), world.CellToWorld(8), 6, 7},     // 8-2=6, 8-1=7
		{2, 4, world.CellToWorld(-1), world.CellToWorld(-1), -2, -3}, // negative: WorldToCell(-1) = -1, minus bias
	}
	for _, c := range cases {
		got := SnapWorldToCell(c.worldX, c.worldZ, c.footX, c.footZ)
		if got.X != c.wantX || got.Z != c.wantZ {
			t.Fatalf("SnapWorldToCell foot %dx%d world (%d,%d) => (%d,%d) want (%d,%d)", c.footX, c.footZ, c.worldX.Raw(), c.worldZ.Raw(), got.X, got.Z, c.wantX, c.wantZ)
		}
		bx, bz := snapBias(c.footX, c.footZ)
		if bx != int32(c.footX/2) || bz != int32(c.footZ/2) {
			t.Fatalf("snapBias mismatch")
		}
	}
	// Also test QueryBuildInfo path with model piece offset.
	factory := &units.Unit{Handle: 1, Owner: 0, X: world.CellToWorld(5), Y: 0, Z: world.CellToWorld(5), Def: newFactoryDef("armfac", 2, 2, 300)}
	// Model with piece0 at origin, piece1 at 2 cells east (2097152)
	m := trivialModel(2, [][3]int64{{0, 0, 0}, {2 * 1048576, 0, 0}})
	// Without VM, QueryBuildInfo falls back to piece0 (factory position)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	// Also need factory queue with product so footprint resolved
	svc := NewService(nil, cat, nil, nil)
	svc.AllowSyntheticPlacement = true
	q := orders.QueueForUnit(factory)
	// Push a building build node for armflash
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2)})
	cell, ok := svc.QueryBuildInfo(factory, m)
	if !ok {
		t.Fatalf("QueryBuildInfo failed")
	}
	// Factory at 5,5 with foot 2x2 bias 1 => 4,4 regardless of piece offset fallback 0
	if cell.X != 4 || cell.Z != 4 {
		t.Fatalf("QueryBuildInfo cell (%d,%d) want (4,4)", cell.X, cell.Z)
	}
	// Verify half-extent bias explicitly
	if bx, _ := snapBias(2, 2); bx != 1 {
		t.Fatalf("bias 2=>1")
	}
	position, ok := svc.QueryBuildWorldPosition(factory, m)
	if !ok || position.X() != factory.X || position.Y() != factory.Y || position.Z() != factory.Z {
		t.Fatalf("QueryBuildWorldPosition = (%d,%d,%d), want factory transform (%d,%d,%d)", position.X().Raw(), position.Y().Raw(), position.Z().Raw(), factory.X.Raw(), factory.Y.Raw(), factory.Z.Raw())
	}
}

func TestFactoryAllocationPreservesAuthoredExitTransform(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 4, 4, 300)
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	w := newTestWorld(8)
	h, _ := w.Create(facDef, 0, world.CellToWorld(5), world.CellToWorld(2), world.CellToWorld(5))
	factory := w.Unit(h)
	factory.Script = nil // explicit synthetic fixture: QueryBuildInfo root fallback
	q := orders.QueueForUnit(factory)
	buildID := orders.Lookup("BuildingBuild")
	if buildID == 0 {
		buildID = orders.Lookup("MobileBuild")
	}
	q.Push(buildID, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(cat, "armflash"), Param2: 1, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].BuildDefKey = "armflash"
	q.Primary()[0].Phase = uint8(State2)
	// The root QueryBuildInfo transform is deliberately offset from the
	// geometric center. Allocation must retain all three authored coordinates;
	// validation uses the independently snapped 2x2 rectangle.
	exitX, exitY, exitZ := int64(12345), int64(54321), int64(-23456)
	svc := NewService(nil, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	svc.ModelForFactory = func(*units.Unit) *model.Model {
		return trivialModel(1, [][3]int64{{exitX, exitY, exitZ}})
	}
	if _, ok := svc.QueryBuildWorldPosition(factory, svc.ModelForFactory(factory)); !ok {
		t.Fatal("query world position failed before pump")
	}
	svc.Pump(factory, 0)
	node := q.Primary()[0]
	prod := w.Unit(node.Target)
	if prod == nil {
		t.Fatalf("factory did not allocate product: node id=%d phase=%d target=%d deadline=%d gate=%d messages=%v", node.ID, node.Phase, node.Target, node.Deadline, node.DynamicGate, svc.Messages())
	}
	wantX := factory.X + numeric.Fixed(exitX)
	wantY := factory.Y + numeric.Fixed(exitY)
	wantZ := factory.Z + numeric.Fixed(exitZ)
	if prod.X != wantX || prod.Y != wantY || prod.Z != wantZ {
		t.Fatalf("product transform=(%d,%d,%d), want authored=(%d,%d,%d)", prod.X.Raw(), prod.Y.Raw(), prod.Z.Raw(), wantX.Raw(), wantY.Raw(), wantZ.Raw())
	}
	extent, _ := world.NewFootprintExtent(2, 2)
	anchor, _ := world.SnapFootprintAnchor(wantX, wantZ, extent)
	if node.GoalX != world.CellToWorld(anchor.CellX()) || node.GoalZ != world.CellToWorld(anchor.CellZ()) {
		t.Fatalf("validation anchor=(%d,%d) not retained separately in node goal", anchor.CellX(), anchor.CellZ())
	}
	record, ok := svc.PlacementForProduct(prod.Handle)
	if !ok || record.Anchor() != anchor {
		t.Fatalf("placement record=%#v ok=%v, want anchor %#v", record, ok, anchor)
	}
}

func TestPlacementDispatchUsesProducedDefinitionClass(t *testing.T) {
	terrain := &world.Terrain{CellW: 2, CellH: 1, Plot: make([]world.PlotCell, 2)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.Plot[0].SetOccupantA(9)
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{
			"test": {MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 255, MinWaterDepth: 0},
		},
	}
	prod := newProductDef("dispatch", 1, 1, 1, 1)
	prod.MovementClass = "test"
	prod.BMCode = true // factory-produced mobile product
	cat.Units[prod.CanonicalKey] = prod
	svc := NewService(terrain, cat, nil, nil)
	extent, _ := world.NewFootprintExtent(1, 1)
	rect, _ := world.NewFootprintRect(world.NewFootprintAnchor(0, 0), extent)
	if _, err := validatePlacement(svc, rect, prod, []world.YardCell{0}); err == nil {
		t.Fatal("factory mobile product used building yard path and ignored occupancy")
	}

	prod.BMCode = false // mobile-builder path placing a building product
	if _, err := validatePlacement(svc, rect, prod, []world.YardCell{0}); err != nil {
		t.Fatalf("building product did not use yard path: %v", err)
	}
}

// TestSilentFifteen verifies C17 silent blocked revalidation 15-tick cadence [05 C17].
func TestSilentFifteen(t *testing.T) {
	// Terrain 10x10 with blocked cell at exit spot
	terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
	// Initialize all cells to empty feature sentinel (0xFFFF) otherwise zero defaults to real index 0 => blocked by feature [04 §6.2]
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetOccupied(false)
	}
	// Mark cell (4,4) as mobile-occupied at the factory exit spot: yard bits
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// bit [04 §6.2].
	idx := 4*10 + 4
	terrain.Plot[idx].SetOccupantA(1)

	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	facDef.YardMap = "oo\n00" // 2x2 yard, but product yard matters
	cat.Units[content.CanonicalKey("armfac")] = facDef
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	prodDef.YardMap = "oo\noo" // 2x2 all occupancy-checking?
	cat.Units[content.CanonicalKey("armflash")] = prodDef

	w := newTestWorld(20)
	// Create factory at world position that snaps to (4,4) with foot 2x2 bias 1
	// WorldToCell(factory) = 5, snap 5-1=4
	h, _ := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := w.Unit(h)
	if factory == nil {
		t.Fatalf("factory create failed")
	}
	factory.Def = facDef
	// Queue a build item with state2
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	node := &orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2), Deadline: -1}
	q.Push(bid, *node)
	// Retrieve actual node pointer
	prim := q.Primary()
	if len(prim) == 0 {
		t.Fatalf("queue empty")
	}
	head := prim[0]
	head.Phase = uint8(State2)

	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	// Pump at tick 10: should detect blocked and set retry 15
	tick := uint32(10)
	svc.Pump(factory, tick)
	if head.Deadline != int32(tick+15) {
		t.Fatalf("silent retry deadline %d want %d", head.Deadline, tick+15)
	}
	if head.DynamicGate != WakeBit1|WakeBit2 {
		t.Fatalf("silent retry wake %d want %d (bits 1+2)", head.DynamicGate, WakeBit1|WakeBit2)
	}
	if head.Phase != uint8(State2) {
		t.Fatalf("should stay state2")
	}
	if len(svc.Messages()) != 0 {
		t.Fatalf("silent revalidation must be silent (no message), got %v", svc.Messages())
	}
	// Verify repeats every 15 while obstructed, no timeout
	tick = uint32(25)
	svc.Pump(factory, tick)
	if head.Deadline != int32(tick+15) {
		t.Fatalf("second retry deadline %d want %d", head.Deadline, tick+15)
	}
	if len(svc.Messages()) != 0 {
		t.Fatalf("still silent")
	}
	// Unblock: clear the layer-A occupancy stamp
	terrain.Plot[idx].SetOccupantA(0)
	// Next pump should succeed allocation (nanoframe)
	tick = uint32(40)
	svc.Pump(factory, tick)
	// Should have created product and advanced to state3, message "Starting construction"
	if head.Phase != uint8(State3) {
		t.Fatalf("after unblock should advance to state3, got %d", head.Phase)
	}
	found := false
	for _, m := range svc.Messages() {
		if m == "Starting construction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Starting construction message, got %v", svc.Messages())
	}
	// Product should exist with remaining=1 health=0
	if head.Target == 0 {
		t.Fatalf("product handle not linked")
	}
	prod := w.Unit(head.Target)
	if prod == nil {
		t.Fatalf("product not found")
	}
	if prod.Remaining != 1 || prod.Health != 0 {
		t.Fatalf("nanoframe values remaining=%v health=%d want 1,0", prod.Remaining, prod.Health)
	}
}

// TestNanoframeCreationValues verifies C18 values [05 C18].
func TestNanoframeCreationValues(t *testing.T) {
	w := newTestWorld(10)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	prodDef := newProductDef("armflash", 2, 2, 100, 50)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	h, _ := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := w.Unit(h)
	factory.Def = facDef
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2)})
	head := q.Primary()[0]
	head.Phase = uint8(State2)
	svc := NewService(nil, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	// Explicit synthetic seam: no terrain is available in this fixture.
	svc.Pump(factory, 100)
	if head.Target == 0 {
		t.Fatalf("allocation failed")
	}
	prod := w.Unit(head.Target)
	if prod == nil {
		t.Fatalf("prod nil")
	}
	if prod.Remaining != 1 {
		t.Fatalf("remaining %v want 1", prod.Remaining)
	}
	if prod.Health != 0 {
		t.Fatalf("health %d want 0", prod.Health)
	}
	if prod.InBuildStance {
		t.Fatalf("build stance not cleared")
	}
	if prod.MaxHealth != int32(prodDef.MaxDamage) {
		t.Fatalf("maxHealth %d want %d", prod.MaxHealth, prodDef.MaxDamage)
	}
	// GetBuilt queued on product
	pq := orders.QueueForUnit(prod)
	if pq.LenPrimary() == 0 {
		t.Fatalf("GetBuilt not queued on product")
	}
	foundGetBuilt := false
	getBuiltID := orders.Lookup("GetBuilt")
	for _, n := range pq.Primary() {
		if n.ID == getBuiltID {
			foundGetBuilt = true
			if n.Param2 != 0 {
				t.Fatalf("GetBuilt queued mode zero count expected 0 got %d", n.Param2)
			}
		}
	}
	if !foundGetBuilt {
		t.Fatalf("GetBuilt not found in product queue")
	}
	// Builder link
	if b, ok := svc.BuilderLink(prod.Handle); !ok || b != factory.Handle {
		t.Fatalf("builder link not registered: got %v ok=%v want %v", b, ok, factory.Handle)
	}
	// Standing-order bits copy
	factory.Flags = StandingMoveMask | StandingFireMask
	// Recreate to test copy: need new product
	w2 := newTestWorld(10)
	h2, _ := w2.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory2 := w2.Unit(h2)
	factory2.Def = facDef
	factory2.Flags = StandingMoveMask | StandingFireMask
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2)})
	head2 := q2.Primary()[0]
	head2.Phase = uint8(State2)
	svc2 := NewService(nil, cat, w2, &economy.Service{})
	svc2.AllowSyntheticPlacement = true
	svc2.Pump(factory2, 200)
	prod2 := w2.Unit(head2.Target)
	if prod2.Flags&(StandingMoveMask|StandingFireMask) != (StandingMoveMask | StandingFireMask) {
		t.Fatalf("standing order bits not copied %b", prod2.Flags)
	}
	// Start-building edge raised
	if factory2.Flags&FlagStartBuilding == 0 {
		t.Fatalf("start-building edge not raised")
	}
	// Allocator-refusal path: test 300 tick retry
	cat2 := &content.Catalog{Units: map[string]*content.UnitDef{}}
	cat2.Units[content.CanonicalKey("armfac")] = facDef
	// product def not in catalog? Actually add but limit checker will refuse
	cat2.Units[content.CanonicalKey("armflash")] = prodDef
	w3 := newTestWorld(10)
	h3, _ := w3.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory3 := w3.Unit(h3)
	factory3.Def = facDef
	q3 := orders.QueueForUnit(factory3)
	q3.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2)})
	head3 := q3.Primary()[0]
	head3.Phase = uint8(State2)
	svc3 := NewService(nil, cat2, w3, &economy.Service{})
	svc3.AllowSyntheticPlacement = true
	svc3.LimitChecker = func(f *units.Unit, key string) bool { return false }
	svc3.Pump(factory3, 300)
	if len(svc3.Messages()) == 0 || svc3.Messages()[len(svc3.Messages())-1] != "Unable to create any more units" {
		t.Fatalf("expected verbatim Unable to create any more units, got %v", svc3.Messages())
	}
	if head3.Deadline != int32(300+300) {
		t.Fatalf("allocator refusal retry deadline %d want %d", head3.Deadline, 600)
	}
	if head3.Phase != uint8(State2) {
		t.Fatalf("should stay state2 on allocator failure")
	}
}

// TestRallyInheritanceOrdering verifies C19 ordering [05 "Rally inheritance"].
func TestRallyInheritanceOrdering(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	w := newTestWorld(20)
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	factory.Def = facDef
	// Build factory queue: BuildingBuild head + QMove + QPatrol + QMove in order
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	qMoveID := orders.Lookup("QMove")
	qPatrolID := orders.Lookup("QPatrol")
	if qMoveID == 0 || qPatrolID == 0 {
		t.Skip("QMove/QPatrol not in table")
	}
	// head is building
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State3)})
	// Now add rally nodes after head (they will be after active marker)
	q.Push(qMoveID, orders.Node{GoalX: world.CellToWorld(1), GoalZ: world.CellToWorld(1)})
	q.Push(qPatrolID, orders.Node{GoalX: world.CellToWorld(2), GoalZ: world.CellToWorld(2)})
	q.Push(qMoveID, orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(3)})
	// Verify queue order: head BuildingBuild then QMove(1) QPatrol(2) QMove(3) in traversal order?
	prim := q.Primary()
	if len(prim) != 4 {
		t.Fatalf("factory queue len %d want 4", len(prim))
	}
	// Create product via allocation path
	svc := NewService(nil, cat, w, &economy.Service{})
	// Directly call rallyInheritance to test ordering
	hp, _ := w.Create(prodDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	prod := w.Unit(hp)
	// Ensure product queue empty
	pqBefore := orders.QueueForUnit(prod).LenPrimary()
	if pqBefore != 0 {
		t.Fatalf("product queue not empty")
	}
	svc.rallyInheritance(factory, prod)
	pq := orders.QueueForUnit(prod)
	primProd := pq.Primary()
	// Should have 3 inherited nodes in queue-traversal order: Move, Patrol, Move
	// The active marker moves to each inserted node [04 §3.3][05 "Queue
	// insertion"], so traversal yields FIFO 1,2,3.
	if len(primProd) != 3 {
		// If product already had GetBuilt from earlier, it may have 1+3; but we created product directly without GetBuilt, so expect 3
		t.Fatalf("product rally len %d want 3, got IDs %v", len(primProd), func() []orders.ID {
			var ids []orders.ID
			for _, n := range primProd {
				ids = append(ids, n.ID)
			}
			return ids
		}())
	}
	moveID := orders.Lookup("Move_Ground")
	patrolID := orders.Lookup("Patrol")
	if primProd[0].ID != moveID || primProd[1].ID != patrolID || primProd[2].ID != moveID {
		t.Fatalf("rally order mismatch: got %v %v %v want Move Patrol Move", primProd[0].ID, primProd[1].ID, primProd[2].ID)
	}
	// Traversal is FIFO because the marker moves to the inserted node.
	if primProd[0].GoalX != world.CellToWorld(1) || primProd[1].GoalX != world.CellToWorld(2) || primProd[2].GoalX != world.CellToWorld(3) {
		t.Fatalf("rally position copy failed: got %v %v %v want 1,2,3", primProd[0].GoalX.Raw(), primProd[1].GoalX.Raw(), primProd[2].GoalX.Raw())
	}
	// Test none => parks
	w2 := newTestWorld(20)
	h2, _ := w2.Create(facDef, 0, 0, 0, 0)
	factory2 := w2.Unit(h2)
	factory2.Def = facDef
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State3)})
	hp2, _ := w2.Create(prodDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	prod2 := w2.Unit(hp2)
	svc.rallyInheritance(factory2, prod2)
	pq2 := orders.QueueForUnit(prod2)
	foundPark := false
	parkID := orders.Lookup("Park")
	for _, n := range pq2.Primary() {
		if n.ID == parkID {
			foundPark = true
		}
	}
	if !foundPark {
		t.Fatalf("product should park when no rally nodes")
	}
}

// TestRefundArithmetic verifies C21 inverted special-mode pairing [05 C21].
func TestRefundArithmetic(t *testing.T) {
	// direct arithmetic test: refund = trunc((1 - remaining) * metalCost)
	remaining := float32(0.25)
	metalCost := int32(200)
	refund := float32(int32((1 - remaining) * float32(metalCost)))
	if refund != 150 {
		t.Fatalf("refund trunc %v want 150", refund)
	}
	// Now test special-mode pairing via handleCancelCurrent
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	prodDef := newProductDef("armflash", 2, 2, 200, 100)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef

	// Helper to run cancel with mode
	runCancel := func(special bool, mode int) float32 {
		w := newTestWorld(10)
		econ := &economy.Service{}
		// Init player 0
		econ.Players[0].Mirror[economy.Metal].Production = 0
		h, _ := w.Create(facDef, 0, 0, 0, 0)
		factory := w.Unit(h)
		factory.Def = facDef
		factory.Owner = 0
		hp, _ := w.Create(prodDef, 0, 0, 0, 0)
		prod := w.Unit(hp)
		prod.Def = prodDef
		prod.Remaining = remaining
		prod.MaxHealth = 100
		// Queue node with product link
		q := orders.QueueForUnit(factory)
		bid := orders.Lookup("BuildingBuild")
		if bid == 0 {
			bid = orders.Lookup("MobileBuild")
		}
		q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 2, Phase: uint8(State3), Target: prod.Handle})
		head := q.Primary()[0]
		head.Target = prod.Handle
		head.Phase = uint8(State3)
		// Set special state func
		svcIsSpecial := func(owner uint8) bool { return special }
		svc := NewService(nil, cat, w, econ)
		svc.IsSpecialSecondState = svcIsSpecial
		svc.ModeSelector = mode
		svc.OnRefresh = func(u *units.Unit) {}
		svc.handleCancelCurrent(factory, head, 100)
		return econ.Players[0].Mirror[economy.Metal].Production
	}

	// Normal add
	got := runCancel(false, 0)
	if got != 150 {
		t.Fatalf("normal refund got %v want 150", got)
	}
	// Special mode 0 subtracts 7/10 => refund * -0.7 = -105
	got = runCancel(true, 0)
	if got != -105 {
		t.Fatalf("special mode 0 got %v want -105", got)
	}
	// Special mode 1 subtracts 1/2 => -75
	got = runCancel(true, 1)
	if got != -75 {
		t.Fatalf("special mode 1 got %v want -75", got)
	}
	// Other fallback adds => 150
	got = runCancel(true, 2)
	if got != 150 {
		t.Fatalf("special mode 2 fallback got %v want 150", got)
	}
	// Special-mode selector now lives on Service; nothing to reset.
}

// TestKind9Kill verifies C21 kill packet 30000 unscaled + severity-zero-no-corpse [05 C21][06 §9.1].
func TestKind9Kill(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	w := newTestWorld(10)
	econ := &economy.Service{}
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	factory.Def = facDef
	factory.Flags = FlagDeactivate | FlagStartBuilding
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	// Test with product attached
	hp, _ := w.Create(prodDef, 0, 0, 0, 0)
	prod := w.Unit(hp)
	prod.Def = prodDef
	prod.Remaining = 0.5
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 2, Phase: uint8(State3), Target: prod.Handle})
	head := q.Primary()[0]
	head.Target = prod.Handle
	svc := NewService(nil, cat, w, econ)
	svc.OnRefresh = func(u *units.Unit) {}
	svc.handleCancelCurrent(factory, head, 100)
	if svc.LastKill().Damage != 30000 {
		t.Fatalf("kind-9 damage %d want 30000", svc.LastKill().Damage)
	}
	if svc.LastKill().Severity != 0 {
		t.Fatalf("severity %d want 0", svc.LastKill().Severity)
	}
	if !svc.LastKill().NoCorpse {
		t.Fatalf("expected no corpse for cause-9")
	}
	if factory.Flags&(FlagDeactivate|FlagStartBuilding) != 0 {
		t.Fatalf("deactivate+start-building bits not lowered together, flags %b", factory.Flags)
	}
	// Node should be dropped without decrementing remaining count (2 stays 2? But node removed)
	if q2 := orders.QueueForUnit(factory); q2.LenPrimary() != 0 {
		t.Fatalf("node should be dropped, len %d", q2.LenPrimary())
	}
	// Verify product dead, no corpse: product Alive false
	if prod.Alive {
		t.Fatalf("product should be dead")
	}
	// Test with no product attached => same epilogue, node still drops
	w2 := newTestWorld(10)
	h2, _ := w2.Create(facDef, 0, 0, 0, 0)
	factory2 := w2.Unit(h2)
	factory2.Def = facDef
	factory2.Flags = FlagDeactivate | FlagStartBuilding
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2), Target: 0})
	head2 := q2.Primary()[0]
	head2.Target = 0
	svc2 := NewService(nil, cat, w2, econ)
	svc2.OnRefresh = func(u *units.Unit) {}
	svc2.handleCancelCurrent(factory2, head2, 100)
	if svc2.LastKill().Damage != 30000 {
		t.Fatalf("no-product kill damage %d", svc2.LastKill().Damage)
	}
	if q2b := orders.QueueForUnit(factory2); q2b.LenPrimary() != 0 {
		t.Fatalf("no-product node should drop")
	}
	if head2.Param2 != 1 {
		t.Fatalf("node count should not decrement on cancel, got %d want 1", head2.Param2)
	}
}

// TestStopInterrupt verifies C22 survival + single decrement [05 C22].
func TestStopInterrupt(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	w := newTestWorld(10)
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	factory.Def = facDef
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 3, Phase: uint8(State3)})
	head := q.Primary()[0]
	head.Param2 = 3
	head.Phase = uint8(State3)
	refreshCount := 0
	svc := NewService(nil, cat, w, &economy.Service{})
	svc.OnRefresh = func(u *units.Unit) { refreshCount++ }
	// Simulate stop interrupt via Pump with pending bit
	factory.Pending = InterruptStop
	svc.Pump(factory, 100)
	found := false
	for _, m := range svc.Messages() {
		if m == "Construction stopped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Construction stopped message, got %v", svc.Messages())
	}
	if head.Param2 != 2 {
		t.Fatalf("stop should decrement once 3->2 got %d", head.Param2)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("stop node should survive, len %d", q.LenPrimary())
	}
	if head.Phase != uint8(State0) {
		t.Fatalf("stop should restart at state0, got %d", head.Phase)
	}
	if refreshCount == 0 {
		t.Fatalf("should refresh interface")
	}
	// Test single decrement when count 1 => 0 but still survives
	head.Param2 = 1
	factory.Pending = InterruptStop
	svc.Pump(factory, 200)
	if head.Param2 != 0 {
		t.Fatalf("1->0 decrement got %d", head.Param2)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("node should still survive at 0 after stop")
	}
}

// TestConstructionArithmeticCarry verifies C24 fractional carry [05 "Construction arithmetic"].
func TestConstructionArithmeticCarry(t *testing.T) {
	// WorkerQuantum
	if got := WorkerQuantum(90); got != 3 {
		t.Fatalf("WorkerQuantum 90 => %d want 3", got)
	}
	if got := WorkerQuantum(29); got != 0 {
		t.Fatalf("WorkerQuantum 29 => %d want 0", got)
	}
	// RemainingStep
	if got := RemainingStep(1.0, 3, 30); got != 0.9 {
		t.Fatalf("RemainingStep 1-3/30 => %v want 0.9", got)
	}
	if got := RemainingStep(0.1, 10, 10); got != 0 {
		t.Fatalf("clamp to 0 got %v", got)
	}
	// HealthGain difference-of-truncations preserves sub-health progress
	// Example from spec: maxDamage 10, worker 1, buildTime 3 => delta 0.333
	// Step1 old 1.0 new 0.666... => trunc10 - trunc6.666 =10-6=4
	// Step2 old 0.666 new 0.333 => 6-3=3
	// Step3 old 0.333 new 0 => 3-0=3 total 10
	maxDamage := int32(10)
	buildTime := int32(3)
	worker := int32(1)
	old := float32(1.0)
	totalGain := int32(0)
	for i := 0; i < 3; i++ {
		nv, hg, _, _ := ConstructionStep(old, worker, buildTime, maxDamage, 0, 0)
		totalGain += hg
		old = nv
		if i == 0 && hg != 4 {
			t.Fatalf("step1 hg %d want 4", hg)
		}
		if i == 1 && hg != 3 {
			t.Fatalf("step2 hg %d want 3", hg)
		}
	}
	if totalGain != 10 {
		t.Fatalf("total health gain %d want 10", totalGain)
	}
	if old != 0 {
		t.Fatalf("final remaining %v want 0", old)
	}
	// Test multiple builders: order matters via stable iteration, but arithmetic per builder is same helper called sequentially
	// Simulate two builders each worker 15 (quantum 0? actually 45/30=1) with buildTime 30
	// Builder1: old1->new1, Builder2: old new1->new2 ; total delta = 2*worker/buildTime
	totalOld := float32(1.0)
	worker1 := WorkerQuantum(45) // 1
	worker2 := WorkerQuantum(45)
	nv1, _, _, _ := ConstructionStep(totalOld, worker1, 30, 100, 100, 100)
	nv2, _, _, _ := ConstructionStep(nv1, worker2, 30, 100, 100, 100)
	expected := 1.0 - float32(2)/30.0
	if nv2 != expected {
		// Allow float32 epsilon
		diff := nv2 - expected
		if diff < -0.0001 || diff > 0.0001 {
			t.Fatalf("two-builder remaining %v want %v", nv2, expected)
		}
	}
	// Test resource demands proportional to delta
	oldR := float32(1.0)
	_, _, eDem, mDem := ConstructionStep(oldR, 3, 30, 100, 300, 200)
	// delta 0.1 => eDem 30, mDem 20 (float32 epsilon)
	if diff := eDem - 30; diff < -0.001 || diff > 0.001 {
		t.Fatalf("demands e %v want 30", eDem)
	}
	if diff := mDem - 20; diff < -0.001 || diff > 0.001 {
		t.Fatalf("demands m %v want 20", mDem)
	}
	// Test admission failure does not advance (already covered in handleState3)
}

// TestStateGates verifies state0/state1 gates [05 "Factory production lifecycle"].
func TestStateGates(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	facDef.YardMap = "oo\n00"
	cat.Units[content.CanonicalKey("armfac")] = facDef
	w := newTestWorld(10)
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	factory.Def = facDef
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	// State0 with positive count should raise activate and go to state1
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State0)})
	head := q.Primary()[0]
	head.Phase = uint8(State0)
	svc := NewService(nil, cat, w, &economy.Service{})
	svc.Pump(factory, 10)
	if factory.Flags&FlagActivated == 0 {
		t.Fatalf("state0 positive should raise activate")
	}
	if head.Phase != uint8(State1) {
		t.Fatalf("state0 positive should advance to state1, got %d", head.Phase)
	}
	// State0 with nonpositive should lower and free
	w2 := newTestWorld(10)
	h2, _ := w2.Create(facDef, 0, 0, 0, 0)
	factory2 := w2.Unit(h2)
	factory2.Def = facDef
	factory2.Flags = FlagActivated
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 0, Phase: uint8(State0)})
	head2 := q2.Primary()[0]
	head2.Phase = uint8(State0)
	head2.Param2 = 0
	svc2 := NewService(nil, cat, w2, &economy.Service{})
	svc2.Pump(factory2, 10)
	q2b := orders.QueueForUnit(factory2)
	if q2b.LenPrimary() != 0 {
		t.Fatalf("state0 nonpositive should free node, got len %d", q2b.LenPrimary())
	}
	if factory2.Flags&FlagActivated != 0 {
		t.Fatalf("should lower activate")
	}
	// State1 waits for in-build-stance
	w3 := newTestWorld(10)
	h3, _ := w3.Create(facDef, 0, 0, 0, 0)
	factory3 := w3.Unit(h3)
	factory3.Def = facDef
	// Scriptless factory approximation [05 C18][RX-05]: a factory with no COB
	// (or no Activate script) cannot raise the script-owned yard-door stance,
	// so the service sets it on its behalf. Real retail factories always carry
	// scripts; synthetic fixtures do not.
	factory3.InBuildStance = false
	q3 := orders.QueueForUnit(factory3)
	q3.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State1)})
	head3 := q3.Primary()[0]
	head3.Phase = uint8(State1)
	svc3 := NewService(nil, cat, w3, &economy.Service{})
	svc3.Pump(factory3, 10)
	if !factory3.InBuildStance {
		t.Fatalf("scriptless factory should have stance self-set (documented approximation)")
	}
	if head3.Phase != uint8(State2) {
		t.Fatalf("state1 scriptless should advance to state2, got %d", head3.Phase)
	}

	// Script-owned handshake [05 C18]: a factory whose COB exposes Activate
	// must wait for the stance bit (wake bit 2) and advance only when set.
	w4 := newTestWorld(10)
	h4, _ := w4.Create(facDef, 0, 0, 0, 0)
	factory4 := w4.Unit(h4)
	factory4.Def = facDef
	// Keep the legacy flag clear so the classifier eligibility bit installed by
	// the allocator cannot satisfy the compatibility fallback.
	factory4.Flags &^= FlagInBuildStance
	factory4.InBuildStance = false
	factory4.SetScript(cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{"Activate": 0}, Pieces: []string{"base"}}))
	q4 := orders.QueueForUnit(factory4)
	q4.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State1)})
	head4 := q4.Primary()[0]
	head4.Phase = uint8(State1)
	svc4 := NewService(nil, cat, w4, &economy.Service{})
	svc4.Pump(factory4, 10)
	if head4.Phase != uint8(State1) {
		t.Fatalf("state1 with Activate script but no stance should stay")
	}
	if head4.DynamicGate != WakeBit2 {
		t.Fatalf("state1 wait wake bit2")
	}
	factory4.InBuildStance = true
	svc4.Pump(factory4, 11)
	if head4.Phase != uint8(State2) {
		t.Fatalf("state1 with in-stance should advance to state2")
	}
}
