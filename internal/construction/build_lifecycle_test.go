package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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
	w := newConstructionFixtureWorld(8, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State0), Deadline: -1})
	svc := NewService(nil, cat, w, &economy.Service{})
	product.Remaining = 1
	svc.handleGetBuiltOrder(product, q.Primary()[0], 10)
	gb := orders.QueueForUnit(product).Primary()[0]
	if State(gb.Phase) != State1 || gb.Deadline != 310 || gb.DynamicGate != 0x8001 {
		t.Fatalf("state0 retry=%+v", gb)
	}
	// Direct handler invocation locks its phase arithmetic. It is deliberately
	// direct: these phase deadlines count from the product's RELEASE, never
	// from its attach, because the pump cannot reach `GetBuilt` while the
	// product is carried [04 R-FAC-02 §4]. The composed queue behaviour is
	// TestFactoryCarriedGetBuiltWaitsForRelease below.
	if gb.Deadline <= 100 {
		t.Fatal("fixture deadline unexpectedly due")
	}
	if State(gb.Phase) != State1 {
		t.Fatal("state1 advanced before 300-tick deadline")
	}
	svc.handleGetBuiltOrder(product, gb, 310)
	if State(gb.Phase) != State2 || gb.Deadline != 340 || gb.DynamicGate != 0x8001 {
		t.Fatalf("state1 retry=%+v", gb)
	}
	svc.handleGetBuiltOrder(product, gb, 340)
	if State(gb.Phase) != State2 || gb.Deadline != 351 || gb.DynamicGate != 0x8001 {
		t.Fatalf("state2 entry=%+v", gb)
	}
}

// The decay quantum is not truncated. Corrected (2026-09-02): this test
// asserted `-(11*100/60)` truncating to -18 before the reverse helper. The
// wrapper forms `−((float)(buildtime × 11) / buildcostenergy)` in single
// precision and passes it as a float32 — no integer division, which is what
// lets a zero `buildcostenergy` reach the step as −∞ [05 R-WORK-01 §11].
func TestGetBuiltPhase2NegativeWorkQuantumIsSinglePrecision(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.BuildCostEnergy = 60
	product := &units.Unit{Handle: 1, Owner: 0, Alive: true, Def: def, Remaining: 0.5, MaxHealth: 100}
	node := &orders.Node{Phase: uint8(State2)}
	svc := NewService(nil, nil, nil, &economy.Service{})
	if code := svc.handleGetBuiltOrder(product, node, 40); code != 2 {
		t.Fatalf("GetBuilt code=%d, want hold", code)
	}
	// The fraction rises by quantum/buildtime = 11/buildcostenergy.
	want := float32(0.5) + float32(11)/float32(60)
	if diff := product.Remaining - want; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("phase-2 remaining=%v, want %v", product.Remaining, want)
	}
	if node.Deadline != 51 || node.DynamicGate != 0x8001 {
		t.Fatalf("phase-2 deadline=%d gate=%#x, want 51/0x8001", node.Deadline, node.DynamicGate)
	}
}

func TestGetBuiltDeadlineRaisesOnlyOrdinaryBit(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	w := newConstructionFixtureWorld(4, &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}})
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	product.Remaining = 1
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{})
	gb := q.Primary()[0]
	gb.DynamicGate = 0x8000
	gb.Deadline = 1
	sim := rng.NewSimulation(99)
	svc := NewService(nil, nil, w, nil)
	svc.OrderBinding = &orders.QueueBinding{SimRNG: &sim}
	svc.queueForUnit(product)
	q.Pump(product, 1)
	if gb.Phase != uint8(State0) || gb.DynamicGate != 0x8000 || gb.Deadline != -1 || gb.Satisfied != 1 {
		t.Fatalf("0x8000-only deadline state phase=%d gate=%#x deadline=%d satisfied=%#x, want 0/0x8000/-1/1", gb.Phase, gb.DynamicGate, gb.Deadline, gb.Satisfied)
	}
	if sim.Draws() != 0 {
		t.Fatalf("blocked GetBuilt deadline consumed %d RNG draws", sim.Draws())
	}
}

// TestFactoryCarriedGetBuiltWaitsForRelease locks [04 R-FAC-02 §4]'s 2026-09-02
// correction: `GetBuilt` is never visited while the product is carried, and its
// first visit is the release pass.
//
// It used to be TestFactoryCarriedGetBuiltQueueCadence and asserted the
// retracted latency composition — GetBuilt's phase 0 → 1 at tick 301, phase 1 →
// 2 at 331, and a phase-2 decay visit at 351 — which followed from §4's
// withdrawn claim that a hold does not stop the walk. The primary pump reloads
// the head after every result code [04 R-ORD-01 §10]; `BeCarried` phase 1 arms
// a ten-tick deadline and returns 2 on every visit, so the pass ends at
// `BeCarried` and the record behind it is never reached.
func TestFactoryCarriedGetBuiltWaitsForRelease(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.BMCode = true
	def.BuildCostEnergy = 55
	cat.Units[def.CanonicalKey] = def
	w := newConstructionFixtureWorld(8, cat)
	fh, _ := w.Create(newFactoryDef("armlab", 2, 2, 30), 0, 0, 0, 0)
	ph, _ := w.Create(def, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	if !movement.AttachCargo(w, factory.Handle, product.Handle, 0) {
		t.Fatal("attach failed")
	}
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("BeCarried"), orders.Node{Target: factory.Handle})
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State0), Deadline: -1})
	svc := NewService(nil, cat, w, nil)
	sim := rng.NewSimulation(12345)
	svc.OrderBinding = &orders.QueueBinding{SimRNG: &sim}
	svc.getBuiltLinks[product.Handle] = factory.Handle
	svc.queueForUnit(product)
	drawsBefore, stateBefore := sim.Draws(), sim.State
	product.Remaining = 0.5
	for tick := uint32(1); tick <= 351; tick++ {
		q.Pump(product, tick)
	}
	gb := q.Primary()[1]
	// Untouched: never dispatched once in 351 ticks of being carried. The
	// pushed record's own phase 0, no-deadline, no-gate state is still there.
	if State(gb.Phase) != State0 || gb.Deadline != -1 || gb.DynamicGate != 0 || gb.Satisfied != 0 || product.Remaining != 0.5 {
		t.Fatalf("GetBuilt while carried: deadline=%d phase=%d gate=%#x satisfied=%#x remaining=%v, want the untouched pushed record -1/0/0/0/0.5 [04 R-FAC-02 §4]",
			gb.Deadline, gb.Phase, gb.DynamicGate, gb.Satisfied, product.Remaining)
	}
	// BeCarried is what the pass stops at, re-armed ten ticks past its last
	// expiry — `t0 + 1 + 10k`, which for a record pushed at tick 0 is 11, 21,
	// … 351, so the next deadline is 361 [04 R-FAC-02 §4][04 R-ORD-01 §2].
	if be := q.Primary()[0]; be.Deadline != 361 || be.Phase != 1 || be.DynamicGate != 1 {
		t.Fatalf("BeCarried at tick 351 deadline=%d phase=%d gate=%#x, want 361/1/0x1", be.Deadline, be.Phase, be.DynamicGate)
	}
	if sim.Draws() != drawsBefore || sim.State != stateBefore {
		t.Fatalf("the carried wait consumed RNG: state %d->%d draws %d->%d", stateBefore, sim.State, drawsBefore, sim.Draws())
	}

	// Release. The first GetBuilt visit in the product's whole life is the pass
	// in which BeCarried sees a null carrier, completes (code 5) and is
	// unlinked; the head reload [04 R-ORD-01 §10] then dispatches GetBuilt, and
	// with the remaining fraction already 0.0 that visit is the completion arm.
	product.Remaining = 0
	if _, ok := movement.DetachCargo(w, product.Handle); !ok {
		t.Fatal("completion detach failed")
	}
	for tick := uint32(352); tick <= 360; tick++ {
		q.Pump(product, tick)
	}
	if len(q.Primary()) != 2 {
		t.Fatalf("queue before BeCarried's expiry=%v, want both records still linked", q.Primary())
	}
	q.Pump(product, 361)
	for _, n := range q.Primary() {
		if n.ID == orders.Lookup("BeCarried") {
			t.Fatal("BeCarried survived the detach: a null carrier completes it [04 R-ORD-01 §2]")
		}
		if n.ID == orders.Lookup("GetBuilt") {
			t.Fatal("GetBuilt survived the release pass: its first visit is the completion arm [04 R-FAC-02 §4]")
		}
	}
}

func TestGetBuiltCompletionRebindsAndConsumesWatcher(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armflash", 1, 1, 1, 100)
	cat.Units[def.CanonicalKey] = def
	w := newConstructionFixtureWorld(8, cat)
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
	pq.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2), DynamicGate: 0, Deadline: -1})
	pq.Primary()[0].DynamicGate = 0
	hostility := func(*units.Unit, *units.Unit) bool { return true }
	lookup := func(pool.Handle) *units.Unit { return factory }
	pq.SetBinding(&orders.QueueBinding{Hostility: hostility, Lookup: lookup, Economy: &economy.Service{}})
	svc := NewService(nil, cat, w, nil)
	svc.getBuiltLinks[product.Handle] = factory.Handle
	svc.queueForUnit(product)
	product.Remaining = 0
	pq.Pump(product, 10)
	newQ := orders.QueueForUnit(product)
	prim := newQ.Primary()
	for _, n := range prim {
		if n.ID == orders.Lookup("GetBuilt") {
			t.Fatalf("completed queue retained GetBuilt: %v", prim)
		}
	}
	if _, ok := svc.getBuiltLinks[product.Handle]; ok {
		t.Fatal("GetBuilt side-map link survived watcher consumption")
	}
	if binding := newQ.Binding(); binding == nil || binding.Hostility == nil || binding.Lookup == nil || binding.Economy == nil {
		t.Fatal("rally queue hooks were not preserved")
	}
}

func TestBuildingClassGetBuiltCompletesWithoutRallyOrPark(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	factoryDef := newFactoryDef("armfac", 1, 1, 30)
	buildingDef := newProductDef("armsolar", 2, 2, 100, 100)
	buildingDef.BMCode = false
	cat.Units[factoryDef.CanonicalKey] = factoryDef
	cat.Units[buildingDef.CanonicalKey] = buildingDef
	w := newConstructionFixtureWorld(8, cat)
	fh, _ := w.Create(factoryDef, 0, 0, 0, 0)
	ph, _ := w.Create(buildingDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	orders.QueueForUnit(factory).Push(orders.Lookup("QMove"), orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(4)})
	pq := orders.QueueForUnit(product)
	pq.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2), Deadline: -1})
	pq.Primary()[0].DynamicGate = 0
	product.Remaining = 0
	svc := NewService(nil, cat, w, nil)
	svc.getBuiltLinks[product.Handle] = factory.Handle
	refreshes := 0
	svc.OnRefresh = func(got *units.Unit) {
		if got != factory {
			t.Fatalf("refresh unit=%v, want factory", got)
		}
		refreshes++
	}
	svc.queueForUnit(product)
	pq.Pump(product, 10)
	if pq.LenPrimary() != 0 {
		t.Fatalf("building-class completion appended release order: %v", pq.Primary())
	}
	if refreshes != 1 {
		t.Fatalf("builder refresh calls=%d, want 1", refreshes)
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
	w := newConstructionFixtureWorld(12, cat)
	h, err := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	if err != nil {
		t.Fatal(err)
	}
	factory := w.Unit(h)
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State2)})
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.Pump(factory, 0)
	node := q.Primary()[0]
	if node.Target == 0 {
		t.Fatal("factory did not publish product")
	}
	product := w.Unit(node.Target)
	cell := terrain.PlotAt(4, 4)
	if cell == nil || cell.OccupantA() != int16(product.Handle) || cell.Occupied() {
		t.Fatalf("reservation missing: cell=%v occupant=%d", cell, cell.OccupantA())
	}
	if !svc.ReleasePlacement(product.Handle) {
		t.Fatal("unfinished product reservation was not released")
	}
	if cell.OccupantA() != 0 || cell.Occupied() {
		t.Fatalf("reservation leaked after release: occupant=%d", cell.OccupantA())
	}
	// A completed building keeps the same placement record and the ground word
	// on every yard-selected cell [04 R-COLL-01 §3–§4].
	rect, _ := world.NewFootprintRect(world.NewFootprintAnchor(4, 4), mustExtent(2, 2))
	if err := svc.reservePlacement(product.Handle, prodDef, rect); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(product.Handle, prodDef, rect)
	svc.applyCompletionPosture(product)
	if cell.OccupantA() != int16(product.Handle) || cell.Occupied() {
		t.Fatalf("completed building lost its yard-selected ground word: occupant=%d", cell.OccupantA())
	}
	if _, ok := svc.PlacementForProduct(product.Handle); !ok {
		t.Fatal("completed building did not retain its placement record")
	}
	if _, err := terrain.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: []world.YardCell{0x2f, 0x2f, 0x2f, 0x2f}, Self: 0}); err == nil {
		t.Fatal("canonical plot check accepted a completed all-o building")
	}
}

func mustExtent(x, z int32) world.FootprintExtent {
	e, err := world.NewFootprintExtent(x, z)
	if err != nil {
		panic(err)
	}
	return e
}

// TestReservePlacementYardGatesOccupancy locks the reservePlacement contract
// [R-P0-08]: a product's yard map decides which cells reject a foreign
// occupant (bits 1-2). An open yard cell (e.g. a building's 'y' corner) is
// passable and may coexist with another unit, matching the placement validator;
// the solid 'o' cells still reject. Previously reservePlacement rejected any
// foreign occupant in any cell, so a valid site that touched another building's
// open yard corner was refused forever after validation passed.
func TestReservePlacementYardGatesOccupancy(t *testing.T) {
	terrain := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	svc := NewService(terrain, nil, nil, nil)
	// 3x2 product with yard "yoy/ooo": the top-left 'y' cell is open (no bits
	// 1-2), every other cell ('o') requires occupancy clearance. Non-square
	// dimensions lock the row-major (dz*fx+dx) yard indexing.
	def := &content.UnitDef{UnitName: "tst", FootprintX: 3, FootprintZ: 2, YardMap: "yoy ooo", BMCode: false}
	def.CanonicalKey = content.CanonicalKey("tst")
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(1, 1), mustExtent(3, 2))
	if err != nil {
		t.Fatal(err)
	}
	// A foreign occupant in a solid cell must reject.
	terrain.PlotAt(2, 1).SetOccupantA(99) // local (1,0) 'o'
	if err := svc.reservePlacement(7, def, rect); err == nil {
		t.Fatal("solid yard cell with foreign occupant was admitted")
	}
	terrain.PlotAt(2, 1).SetOccupantA(0)
	// A foreign occupant in the open corner cell must be tolerated, matching
	// the yard-gated validator; only the solid cells are stamped.
	terrain.PlotAt(1, 1).SetOccupantA(99) // local (0,0) 'y'
	if err := svc.reservePlacement(7, def, rect); err != nil {
		t.Fatalf("open yard cell with foreign occupant was rejected: %v", err)
	}
	if got := terrain.PlotAt(1, 1).OccupantA(); got != 99 {
		t.Fatalf("open yard cell occupant was overwritten: %d", got)
	}
	if got := terrain.PlotAt(2, 1).OccupantA(); got != 7 {
		t.Fatalf("solid yard cell was not stamped: %d", got)
	}
	if got := terrain.PlotAt(1, 2).OccupantA(); got != 7 {
		t.Fatalf("second row solid cell was not stamped: %d", got)
	}
}

func TestAcceptedWorkEmitsNanoAndStallDoesNot(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 1, 1, 60)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(12, cat)
	h, _ := w.Create(facDef, 0, 0, 0, 0)
	factory := w.Unit(h)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	product := w.Unit(ph)
	bindConstructionFixture(factory, trivialModel(1, nil), false)
	product.Remaining, product.MaxHealth = 0.5, 100
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph})
	svc := NewService(nil, cat, w, &economy.Service{})
	svc.ModelForUnit = func(*units.Unit) *model.Model { return trivialModel(1, nil) }
	collector := frame.NewEventBuffer(frame.Limits{})
	svc.Presentation = collector
	svc.StepUnit(TickContext{Tick: 7, World: w, Catalog: cat}, h)
	if product.Remaining >= 0.5 || len(collector.Events()) != 1 || collector.Events()[0].Kind != frame.KindNanolathe {
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
	svc := NewService(exitTerrain(2, 2), cat, nil, nil)
	svc.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		return &units.Unit{Handle: 9}, nil
	}
	product, err := svc.allocateNanoframe(factory, def, rect, world.NewModelWorldPosition(0, 0, 0))
	if err != nil || product == nil {
		t.Fatalf("allocator product=%v err=%v", product, err)
	}
	if product.Remaining != 1 || product.Health != 0 || product.MaxHealth != def.MaxDamage || product.InBuildStance {
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

func TestFactoryAttachGateFailureRollsBackAllocation(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, Movement: map[string]*content.MovementClass{
		"testground": {FootprintX: 1, FootprintZ: 1, MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: -10000},
	}}
	facDef := newFactoryDef("armfac", 1, 1, 30)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	prodDef.BMCode = true
	prodDef.MovementClass = "testground"
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(12, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	other, _ := w.Create(prodDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Attachment.Cargo = []pool.Handle{other}
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State2)})
	svc := NewService(exitTerrain(12, 12), cat, w, nil)
	svc.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		return product, nil
	}
	liveBefore, createdBefore := w.LiveCountForPlayer(0), w.CreatedCountForPlayer(0)
	svc.Pump(factory, 7)
	// Corrected. This assertion used to require a death latch on the rejected
	// product. [04 R-FAC-02 §3] lists three abnormal ends and only two of them
	// are deaths: "a product freed by pool exhaustion or limit never existed",
	// and successEpilogue's own contract calls its failure "an explicit rejected
	// allocation" [04 R-FAC-02 §1]. Latching a death here filed a kill record and
	// a death cause for a unit retail never created.
	if w.Unit(ph) != nil {
		t.Fatalf("attach rejection left the allocated slot occupied: phase=%d target=%d admissions=%+v messages=%v", q.Primary()[0].Phase, q.Primary()[0].Target, svc.admissions, svc.messages)
	}
	if product.Dying || product.DeathCause != 0 || product.LastDamageCause != 0 {
		t.Fatalf("attach rejection killed the product (dying=%v cause=%d kind=%d); a never-existed product is freed [04 R-FAC-02 §3]", product.Dying, product.DeathCause, product.LastDamageCause)
	}
	if live, created := w.LiveCountForPlayer(0), w.CreatedCountForPlayer(0); live != liveBefore-1 || created != createdBefore-1 {
		t.Fatalf("counters after the rollback live=%d created=%d, want the pre-allocation %d/%d [08 R-SKIR-01 §3][05 R-SHARE-01 §8]", live, created, liveBefore-1, createdBefore-1)
	}
	if q.Primary()[0].Target != 0 || State(q.Primary()[0].Phase) == State3 {
		t.Fatalf("attach rejection published accepted factory state: %+v", q.Primary()[0])
	}
	if _, ok := svc.PlacementForProduct(ph); ok {
		t.Fatal("attach rejection leaked product placement")
	}
	if _, ok := svc.BuilderLink(ph); ok {
		t.Fatal("attach rejection leaked builder link")
	}
}

func TestCancelCurrentRunsCompletionPostureBeforeCause9(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 1, 1, 30)
	prodDef := newProductDef("armflash", 1, 1, 1, 100)
	prodDef.ActivateWhenBuilt = true
	// `init_cloaked` is deliberately authored here to lock the negative half of
	// the completion transition's contract: the transition never reads it and
	// writes neither cloak bit [04 R-SPEC-01 §12][05 R-ECO-01 §9] (RWU-19-26).
	prodDef.InitCloaked = true
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(8, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth, product.Health = 0.5, 100, 30
	factory.Activated = true
	factory.Flags = FlagStartBuilding
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
	if err := svc.reservePlacement(ph, nil, placement); err != nil {
		t.Fatal(err)
	}
	svc.recordPlacement(ph, nil, placement)
	svc.SetBuilderLink(ph, fh)
	svc.getBuiltLinks[ph] = fh
	svc.handleCancelCurrent(factory, node, 4)
	if product.Remaining != 0 || product.Health != product.MaxHealth || product.Flags&FlagCompleted == 0 {
		t.Fatalf("cancel completion posture missing: remaining=%v health=%d flags=%x", product.Remaining, product.Health, product.Flags)
	}
	// The transition must not raise bit 14 of the instance flag word: the old
	// FlagInitCloak wrote the death latch under a cloak name
	// [04 R-SPEC-01 §12] (RWU-19-26).
	if product.Flags&0x00004000 != 0 {
		t.Fatalf("completion raised instance flag bit 14 on a non-isfeature product: flags=%x", product.Flags)
	}
	if factory.Activated || factory.Flags&FlagStartBuilding != 0 || product.Alive {
		t.Fatalf("cancel edges/death ordering wrong: factory activated=%t flags=%x alive=%t", factory.Activated, factory.Flags, product.Alive)
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
