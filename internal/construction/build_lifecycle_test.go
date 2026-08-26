package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestQueryBuildInfoRequiresStrictBindingOutsideSyntheticSeam(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	factory := &units.Unit{Def: newFactoryDef("armfac", 2, 2, 30), X: world.CellToWorld(5), Z: world.CellToWorld(5)}
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: "armflash", Param2: 1})
	svc := NewService(nil, cat, nil, nil)
	if _, ok := svc.QueryBuildWorldPosition(factory, trivialModel(1, nil)); ok {
		t.Fatal("missing strict binding accepted production root fallback")
	}
	if _, ok := svc.QueryBuildWorldPosition(factory, nil); ok {
		t.Fatal("missing strict model accepted production fallback")
	}
}

func TestConstructionUsesMappedHierarchicalCompose(t *testing.T) {
	const cell = int64(1 << 20)
	prog := &cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"QueryNanoPiece": 0},
		ScriptsByID: []int{0},
		Pieces:      []string{"vm-child", "vm-root"},
	}
	vm := cob.NewVM(prog)
	bridge := cob.NewCallbackBridge(vm)
	mdl := &model.Model{
		Name: "mapped-hierarchy", Root: 0,
		Pieces: []model.Piece{
			{Name: "model-root", Parent: -1, Translate: [3]numeric.Fixed{numeric.Fixed(cell), 0, 0}, Children: []int{1}},
			{Name: "model-child", Parent: 0, Translate: [3]numeric.Fixed{numeric.Fixed(2 * cell), 0, 0}},
		},
	}
	binding := &cob.Binding{VM: vm, Model: mdl, PieceMap: []int{1, 0}, Callbacks: bridge}
	factory := &units.Unit{Handle: 1, X: world.CellToWorld(10), Y: world.CellToWorld(2), Z: world.CellToWorld(4), Script: vm, ScriptState: &units.ScriptState{VM: vm, Binding: binding}}
	svc := NewService(nil, nil, nil, nil)
	rootPos, composed := binding.ComposePiece(1, 0, 0, 0)
	if !composed || rootPos[0] != numeric.Fixed(cell) || rootPos[1] != 0 || rootPos[2] != 0 {
		t.Fatalf("mapped root composition=%v composed=%t, want root offset", rootPos, composed)
	}
	piece, nanoPos, ok := svc.QueryNanoPiece(factory)
	if !ok || piece != 0 || nanoPos.X() != factory.X.Add(numeric.Fixed(3*cell)) || nanoPos.Y() != factory.Y || nanoPos.Z() != factory.Z {
		t.Fatalf("mapped nano piece=%d pos=(%d,%d,%d), want child hierarchy offset", piece, nanoPos.X().Raw(), nanoPos.Y().Raw(), nanoPos.Z().Raw())
	}
}

func TestGetBuiltRetryStates(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armflash", 1, 1, 1, 100)
	cat.Units[def.CanonicalKey] = def
	w := units.New(8, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State0), Deadline: -1})
	svc := NewService(nil, cat, w, nil)
	product.Remaining = 1
	svc.resolveGetBuilt(product, 10)
	gb := orders.QueueForUnit(product).Primary()[0]
	if State(gb.Phase) != State1 || gb.Deadline != 310 || gb.DynamicGate != WakeBit1 {
		t.Fatalf("state0 retry=%+v", gb)
	}
	svc.resolveGetBuilt(product, 100)
	if State(gb.Phase) != State1 {
		t.Fatal("state1 advanced before 300-tick deadline")
	}
	svc.resolveGetBuilt(product, 310)
	if State(gb.Phase) != State2 || gb.Deadline != 340 || gb.DynamicGate != WakeBit2 {
		t.Fatalf("state1 retry=%+v", gb)
	}
	svc.resolveGetBuilt(product, 340)
	if State(gb.Phase) != State2 || gb.Deadline != -1 || gb.DynamicGate != WakeBit2 {
		t.Fatalf("state2 wake=%+v", gb)
	}
}

func TestGetBuiltCompletionRebindsAndConsumesWatcher(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armflash", 1, 1, 1, 100)
	cat.Units[def.CanonicalKey] = def
	w := units.New(8, cat)
	fh, _ := w.Create(def, 0, 0, 0, 0)
	ph, _ := w.Create(def, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	fq := orders.QueueForUnit(factory)
	moveID, patrolID := orders.Lookup("QMove"), orders.Lookup("QPatrol")
	if moveID == 0 || patrolID == 0 {
		t.Skip("rally descriptors unavailable")
	}
	fq.Push(moveID, orders.Node{GoalX: world.CellToWorld(2), GoalZ: world.CellToWorld(3)})
	fq.Push(patrolID, orders.Node{GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5)})
	pq := orders.QueueForUnit(product)
	pq.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2), Deadline: -1})
	hostility := func(*units.Unit, *units.Unit) bool { return true }
	lookup := func(pool.Handle) *units.Unit { return factory }
	pq.Hostility, pq.Lookup, pq.StockpileEconomy, pq.SecondaryTick = hostility, lookup, &economy.Service{}, 77
	svc := NewService(nil, cat, w, nil)
	svc.getBuiltLinks[product.Handle] = factory.Handle
	svc.resolveGetBuilt(product, 10)
	newQ := orders.QueueForUnit(product)
	prim := newQ.Primary()
	if len(prim) != 2 || prim[0].ID != orders.Lookup("Move_Ground") || prim[1].ID != orders.Lookup("Patrol") {
		t.Fatalf("rebound primary=%v, want inherited Move/Patrol", prim)
	}
	if _, ok := svc.getBuiltLinks[product.Handle]; ok {
		t.Fatal("GetBuilt side-map link survived watcher consumption")
	}
	if newQ.Hostility == nil || newQ.Lookup == nil || newQ.StockpileEconomy == nil || newQ.SecondaryTick != 77 {
		t.Fatal("rally queue hooks were not preserved")
	}
}

func TestStartStopBuildingEdgesAreDeduplicated(t *testing.T) {
	prog := &cob.Program{Code: []uint32{0x10013000, 0x10065000}, Scripts: map[string]int{"StartBuilding": 0, "StopBuilding": 0}, ScriptsByID: []int{0, 0}, Pieces: []string{"base"}}
	vm := cob.NewVM(prog)
	bridge := cob.NewCallbackBridge(vm)
	u := &units.Unit{Script: vm, ScriptState: &units.ScriptState{VM: vm, Binding: &cob.Binding{VM: vm, Model: trivialModel(1, nil), PieceMap: []int{0}, Callbacks: bridge}}}
	svc := &Service{}
	svc.startBuilding(u)
	first := liveThreads(vm)
	svc.startBuilding(u)
	if liveThreads(vm) != first {
		t.Fatalf("duplicate StartBuilding allocated a thread: first=%d now=%d", first, liveThreads(vm))
	}
	svc.stopBuilding(u)
	if liveThreads(vm) != first+1 {
		t.Fatalf("StopBuilding did not fire one deferred edge: before=%d now=%d", first, liveThreads(vm))
	}
	svc.stopBuilding(u)
	if liveThreads(vm) != first+1 {
		t.Fatal("duplicate StopBuilding allocated a thread")
	}
}

func liveThreads(vm *cob.VM) int {
	count := 0
	for i := range vm.Threads {
		if vm.IsThreadAlive(i) {
			count++
		}
	}
	return count
}

func TestFactoryReservationReleaseAndCompletedRetention(t *testing.T) {
	terrain := &world.Terrain{CellW: 12, CellH: 12, Plot: make([]world.PlotCell, 144)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 30)
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := units.New(12, cat)
	h, err := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	if err != nil {
		t.Fatal(err)
	}
	factory := w.Unit(h)
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State2)})
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	svc.Pump(factory, 0)
	node := q.Primary()[0]
	if node.Target == 0 {
		t.Fatal("factory did not publish product")
	}
	product := w.Unit(node.Target)
	cell := terrain.PlotAt(4, 4)
	if cell == nil || cell.OccupantA() != int16(product.Handle) || !cell.Occupied() {
		t.Fatalf("reservation missing: cell=%v occupant=%d occupied=%t", cell, cell.OccupantA(), cell.Occupied())
	}
	if !svc.ReleasePlacement(product.Handle) {
		t.Fatal("unfinished product reservation was not released")
	}
	if cell.OccupantA() != 0 || cell.Occupied() {
		t.Fatalf("reservation leaked after release: occupant=%d occupied=%t", cell.OccupantA(), cell.Occupied())
	}
	// Completed live structures retain their occupancy until removal.
	rect, _ := world.NewFootprintRect(world.NewFootprintAnchor(4, 4), mustExtent(2, 2))
	if err := svc.reservePlacement(product.Handle, rect); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(product.Handle, rect)
	product.Remaining = 0
	if svc.ReleasePlacement(product.Handle) {
		t.Fatal("completed live product released occupancy")
	}
	if cell.OccupantA() != int16(product.Handle) || !cell.Occupied() {
		t.Fatal("completed live product lost its occupancy")
	}
}

func mustExtent(x, z int32) world.FootprintExtent {
	e, err := world.NewFootprintExtent(x, z)
	if err != nil {
		panic(err)
	}
	return e
}

func TestAcceptedWorkEmitsNanoAndStallDoesNot(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 1, 1, 60)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := units.New(12, cat)
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	product := w.Unit(ph)
	product.Remaining, product.MaxHealth = 0.5, 100
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph})
	svc := NewService(nil, cat, w, nil)
	svc.AllowSyntheticPlacement = true
	svc.ModelForUnit = func(*units.Unit) *model.Model { return trivialModel(1, nil) }
	collector := presentation.NewCollector(presentation.Limits{})
	svc.Presentation = collector
	svc.StepUnit(TickContext{Tick: 7, World: w, Catalog: cat}, h)
	if product.Remaining >= 0.5 || len(collector.Events()) != 1 || collector.Events()[0].Kind != presentation.KindNanolathe {
		t.Fatalf("accepted work remaining=%v events=%v", product.Remaining, collector.Events())
	}
	collector.Reset()
	product.Remaining = 0.5
	econ := &economy.Service{}
	b := econ.UnitBuckets(factory.Handle)
	if b == nil {
		t.Fatal("economy buckets unavailable")
	}
	(*b)[economy.Energy].Carry = 1
	svc.Economy = econ
	svc.StepUnit(TickContext{Tick: 8, World: w, Catalog: cat, Economy: econ}, h)
	if product.Remaining != 0.5 || len(collector.Events()) != 0 {
		t.Fatalf("stalled work changed state/event: remaining=%v events=%v", product.Remaining, collector.Events())
	}
}

func TestAllocatorNanoframeInitializationAndInvalidRollback(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armflash", 1, 1, 1, 100)
	cat.Units[def.CanonicalKey] = def
	factory := &units.Unit{Handle: 1, Owner: 0}
	rect, _ := world.NewFootprintRect(world.NewFootprintAnchor(0, 0), mustExtent(1, 1))
	svc := NewService(nil, cat, nil, nil)
	svc.AllowSyntheticPlacement = true
	svc.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		return &units.Unit{Handle: 9}, nil
	}
	product, err := svc.allocateNanoframe(factory, def, rect, world.NewModelWorldPosition(0, 0, 0))
	if err != nil || product == nil {
		t.Fatalf("allocator product=%v err=%v", product, err)
	}
	if product.Remaining != 1 || product.Health != 0 || product.MaxHealth != def.MaxDamage || product.Flags&FlagInBuildStance != 0 {
		t.Fatalf("allocator nanoframe not initialized: %+v", product)
	}
	if _, ok := svc.BuilderLink(product.Handle); ok {
		t.Fatal("allocator path linked product before epilogue")
	}

	svc.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		// The allocator can race the preflight by installing an occupant before
		// the atomic reservation; construction must roll back the returned unit.
		return &units.Unit{Handle: 10}, nil
	}
	terrain := &world.Terrain{CellW: 2, CellH: 2, Plot: make([]world.PlotCell, 4)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.Plot[0].SetOccupantA(3)
	svc.Terrain = terrain
	if _, err := svc.allocateNanoframe(factory, def, rect, world.NewModelWorldPosition(0, 0, 0)); err == nil {
		t.Fatal("reservation failure was accepted")
	}
	if _, ok := svc.PlacementForProduct(10); ok {
		t.Fatal("reservation failure leaked placement record")
	}
}

func TestCancelCurrentRunsCompletionPostureBeforeCause9(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 1, 1, 30)
	prodDef := newProductDef("armflash", 1, 1, 1, 100)
	prodDef.ActivateWhenBuilt = true
	prodDef.InitCloaked = true
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := units.New(8, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth, product.Health = 0.5, 100, 30
	factory.Flags = FlagActivated | FlagStartBuilding
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 2, Phase: uint8(State3), Target: ph})
	node := q.Primary()[0]
	svc := NewService(nil, cat, w, nil)
	svc.Terrain = &world.Terrain{CellW: 2, CellH: 2, Plot: make([]world.PlotCell, 4)}
	for i := range svc.Terrain.Plot {
		svc.Terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	placement, err := world.NewFootprintRect(world.NewFootprintAnchor(0, 0), mustExtent(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.reservePlacement(ph, placement); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(ph, placement)
	svc.SetBuilderLink(ph, fh)
	svc.getBuiltLinks[ph] = fh
	svc.handleCancelCurrent(factory, node, 4)
	if product.Remaining != 0 || product.Health != product.MaxHealth || product.Flags&FlagCompleted == 0 || product.Flags&FlagInitCloak == 0 || !product.IsCloaked {
		t.Fatalf("cancel completion posture missing: remaining=%v health=%d flags=%x cloaked=%t", product.Remaining, product.Health, product.Flags, product.IsCloaked)
	}
	if factory.Flags&(FlagActivated|FlagStartBuilding) != 0 || product.Alive {
		t.Fatalf("cancel edges/death ordering wrong: factory flags=%x alive=%t", factory.Flags, product.Alive)
	}
	if node.Param2 != 2 {
		t.Fatalf("cancel decremented queued count to %d", node.Param2)
	}
	if _, ok := svc.BuilderLink(ph); ok || len(svc.getBuiltLinks) != 0 {
		t.Fatal("cancel leaked builder/product side-map links")
	}
	if _, ok := svc.PlacementForProduct(ph); ok || svc.Terrain.PlotAt(0, 0).OccupantA() != 0 {
		t.Fatal("cancel leaked product occupancy")
	}
}
