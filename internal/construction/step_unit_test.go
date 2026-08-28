package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// helper to create a catalog with given defs
func catWithDefs(defs ...*content.UnitDef) *content.Catalog {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	for _, d := range defs {
		if d != nil {
			d.CanonicalKey = content.CanonicalKey(d.UnitName)
			cat.Units[content.CanonicalKey(d.UnitName)] = d
		}
	}
	return cat
}

func TestStepUnit_Isolation(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 200, WorkerTime: 60, BuildTime: 100},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	flashDef := cat.Units[content.CanonicalKey("armflash")]
	flashDef.BuildCostMetal = 100
	flashDef.BuildCostEnergy = 100
	w := units.NewSliced(20, cat)
	hA, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	hB, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builderA := w.Unit(hA)
	builderB := w.Unit(hB)
	builderA.Def = facDef
	builderB.Def = facDef
	// Queue factory builds on both, but put them at state3 with product nanoframes
	prodAHandle, _ := w.Create(flashDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prodA := w.Unit(prodAHandle)
	prodA.Def = flashDef
	prodA.Remaining = 0.5
	prodA.MaxHealth = 100
	prodA.Health = 50
	prodBHandle, _ := w.Create(flashDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prodB := w.Unit(prodBHandle)
	prodB.Def = flashDef
	prodB.Remaining = 0.5
	prodB.MaxHealth = 100
	prodB.Health = 50
	// Queues
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	qA := orders.QueueForUnit(builderA)
	qA.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: prodAHandle})
	qA.Primary()[0].Phase = uint8(State3)
	qA.Primary()[0].Target = prodAHandle
	qB := orders.QueueForUnit(builderB)
	qB.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: prodBHandle})
	qB.Primary()[0].Phase = uint8(State3)
	qB.Primary()[0].Target = prodBHandle

	svc := NewService(nil, cat, w, &economy.Service{})
	// Ensure economy buckets allow admission (carry <=0)
	BucketsA := svc.Economy.UnitBuckets(builderA.Handle)
	if BucketsA != nil {
		(*BucketsA)[economy.Metal].Carry = 0
		(*BucketsA)[economy.Energy].Carry = 0
	}
	BucketsB := svc.Economy.UnitBuckets(builderB.Handle)
	if BucketsB != nil {
		(*BucketsB)[economy.Metal].Carry = 0
		(*BucketsB)[economy.Energy].Carry = 0
	}
	ctxA := TickContext{Tick: 10, World: w, Economy: svc.Economy, Catalog: cat}
	resA := svc.StepUnit(ctxA, hA)
	if resA.Err != nil {
		t.Fatalf("StepUnit A err %v", resA.Err)
	}
	// A should have advanced, B should not
	if prodA.Remaining == 0.5 {
		t.Fatalf("A remaining should have advanced, got %v", prodA.Remaining)
	}
	if prodB.Remaining != 0.5 {
		t.Fatalf("B remaining should stay 0.5 (isolation), got %v", prodB.Remaining)
	}
	// Now step B
	resB := svc.StepUnit(TickContext{Tick: 11, World: w, Economy: svc.Economy, Catalog: cat}, hB)
	if resB.Err != nil {
		t.Fatalf("StepUnit B err %v", resB.Err)
	}
	if prodB.Remaining == 0.5 {
		t.Fatalf("B should have advanced after its own step")
	}
}

func TestStepUnit_MobileSiteSurvives(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true, BMCode: true, BuildTime: 100}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef
	w := units.NewSliced(20, cat)
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef
	siteX := world.CellToWorld(10)
	siteZ := world.CellToWorld(10)
	if err := QueueMobileBuild(builder, "armllt", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild %v", err)
	}
	q := orders.QueueForUnit(builder)
	if q.LenPrimary() != 1 {
		t.Fatalf("queue len")
	}
	node := q.Primary()[0]
	if node.GoalX != siteX || node.GoalZ != siteZ {
		t.Fatalf("site not stored in node: got %d,%d want %d,%d", node.GoalX.Raw(), node.GoalZ.Raw(), siteX.Raw(), siteZ.Raw())
	}
	if node.BuildDefKey != "armllt" {
		t.Fatalf("BuildDefKey")
	}
	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	svc := NewService(terrain, cat, w, &economy.Service{})
	// Force state2
	node.Phase = uint8(State2)
	ctx := TickContext{Tick: 0, World: w, Economy: svc.Economy, Terrain: terrain, Catalog: cat}
	res := svc.StepUnit(ctx, hb)
	if res.Err != nil {
		t.Fatalf("StepUnit mobile err %v", res.Err)
	}
	if node.Target == 0 {
		t.Fatalf("mobile allocation failed")
	}
	prod := w.Unit(node.Target)
	if prod == nil {
		t.Fatalf("product nil")
	}
	expectedCell := SnapWorldToCell(siteX, siteZ, int(prodDef.FootprintX), int(prodDef.FootprintZ))
	wantX, wantZ := world.PlacementCenter(expectedCell.X, expectedCell.Z, prodDef.FootprintX, prodDef.FootprintZ)
	if prod.X != wantX || prod.Z != wantZ {
		t.Fatalf("product at (%d,%d) want model center (%d,%d), anchor (%d,%d)", prod.X.Raw(), prod.Z.Raw(), wantX.Raw(), wantZ.Raw(), expectedCell.X, expectedCell.Z)
	}
	// GoalX/Z must still equal original site (not overwritten to cell origin) per P0-I05 mobile site authoritative.
	if node.GoalX != siteX || node.GoalZ != siteZ {
		t.Fatalf("mobile site Goal overwritten: got %d,%d want %d,%d", node.GoalX.Raw(), node.GoalZ.Raw(), siteX.Raw(), siteZ.Raw())
	}
	// Verify building builds still use factory exit (not site) – factory-local
	cat2 := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 4, FootprintZ: 4, YardMap: "oooo", Builder: true, MaxDamage: 200, WorkerTime: 30, BuildTime: 100},
		prodDef,
	)
	facDef := cat2.Units[content.CanonicalKey("armfac")]
	w2 := units.NewSliced(20, cat2)
	hf, _ := w2.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := w2.Unit(hf)
	factory.Def = facDef
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	if err := QueueFactoryBuild(factory, "armllt", 1, cat2); err != nil {
		t.Fatalf("QueueFactoryBuild %v", err)
	}
	qf := orders.QueueForUnit(factory)
	nf := qf.Primary()[0]
	nf.Phase = uint8(State2)
	svc2 := NewService(terrain, cat2, w2, &economy.Service{})
	res2 := svc2.StepUnit(TickContext{Tick: 0, World: w2, Economy: svc2.Economy, Terrain: terrain, Catalog: cat2}, hf)
	if res2.Err != nil {
		t.Fatalf("factory StepUnit err %v", res2.Err)
	}
	if nf.Target == 0 {
		t.Fatalf("factory allocation failed")
	}
	prodF := w2.Unit(nf.Target)
	if prodF == nil {
		t.Fatalf("factory product nil")
	}
	// Factory product should be at factory exit snapped cell (5,5) with foot 2 => 4,4 bias, not at mobile site 10,10
	if world.WorldToCell(prodF.X) == expectedCell.X && world.WorldToCell(prodF.Z) == expectedCell.Z {
		t.Fatalf("factory product incorrectly at mobile site")
	}
}

func TestStepUnit_DistinctDescriptors(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	mobileDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true, BMCode: true}
	mobileDef.CanonicalKey = content.CanonicalKey("armck")
	factoryDef := &content.UnitDef{UnitName: "armfac", FootprintX: 4, FootprintZ: 4, YardMap: "o", Builder: true, MaxDamage: 200, WorkerTime: 30}
	factoryDef.CanonicalKey = content.CanonicalKey("armfac")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = mobileDef
	cat.Units[content.CanonicalKey("armfac")] = factoryDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef
	w := units.NewSliced(20, cat)
	hm, _ := w.Create(mobileDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	mobile := w.Unit(hm)
	mobile.Def = mobileDef
	// Try to enqueue factory descriptor on mobile builder via direct queue push (should fail on StepUnit)
	factoryID := orders.Lookup("BuildingBuild")
	if factoryID == 0 {
		t.Skip("BuildingBuild not found")
	}
	q := orders.QueueForUnit(mobile)
	q.Push(factoryID, orders.Node{BuildDefKey: "armllt", Param1: 1, Param2: 1, Phase: uint8(State0)})
	svc := NewService(nil, cat, w, &economy.Service{})
	res := svc.StepUnit(TickContext{Tick: 0, World: w, Economy: svc.Economy, Catalog: cat}, hm)
	if res.Err == nil {
		t.Fatalf("mobile + factory descriptor should error explicitly")
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("queue should not be silently cleared on descriptor mismatch, len %d", q.LenPrimary())
	}
	// Conversely factory + mobile descriptor
	w2 := units.NewSliced(20, cat)
	hf, _ := w2.Create(factoryDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w2.Unit(hf)
	factory.Def = factoryDef
	mobileID := orders.Lookup("MobileBuild")
	if mobileID == 0 {
		t.Skip("MobileBuild not found")
	}
	q2 := orders.QueueForUnit(factory)
	q2.Push(mobileID, orders.Node{BuildDefKey: "armllt", Param1: 1, Param2: 1, Phase: uint8(State0), GoalX: world.CellToWorld(5), GoalZ: world.CellToWorld(5)})
	svc2 := NewService(exitTerrain(12, 12), cat, w2, &economy.Service{})
	res2 := svc2.StepUnit(TickContext{Tick: 0, World: w2, Economy: svc2.Economy, Catalog: cat}, hf)
	if res2.Err == nil {
		t.Fatalf("factory + mobile descriptor should error")
	}
	if q2.LenPrimary() != 1 {
		t.Fatalf("factory queue should not be cleared on mismatch")
	}
}

func TestStepUnit_ZeroStockWithCarryAdmits(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 60, BuildTime: 100},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	flashDef := cat.Units[content.CanonicalKey("armflash")]
	flashDef.BuildCostMetal = 100
	flashDef.BuildCostEnergy = 100
	w := units.NewSliced(20, cat)
	hf, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(hf)
	factory.Def = facDef
	hp, _ := w.Create(flashDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod := w.Unit(hp)
	prod.Def = flashDef
	prod.Remaining = 0.5
	prod.MaxHealth = 100
	prod.Health = 50
	econ := &economy.Service{}
	// Zero stock, but carry <=0 so admission should succeed per contract: zero stock does NOT forbid first carry-admitted quantum.
	econ.Players[0].Stock[economy.Metal] = 0
	econ.Players[0].Stock[economy.Energy] = 0
	econ.Players[0].Capacity[economy.Metal] = 1000
	econ.Players[0].Capacity[economy.Energy] = 1000
	// Set buckets for factory to carry 0 (allow)
	if b := econ.UnitBuckets(factory.Handle); b != nil {
		(*b)[economy.Metal].Carry = 0
		(*b)[economy.Energy].Carry = 0
	}
	svc := NewService(nil, cat, w, econ)
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: prod.Handle})
	head := q.Primary()[0]
	head.Phase = uint8(State3)
	head.Target = prod.Handle
	before := prod.Remaining
	res := svc.StepUnit(TickContext{Tick: 10, World: w, Economy: econ, Catalog: cat}, hf)
	if res.Err != nil {
		t.Fatalf("err %v", res.Err)
	}
	if prod.Remaining == before {
		t.Fatalf("zero stock with prior accepted carry should still admit work: remaining %v unchanged", before)
	}
}

func TestStepUnit_SettlementDeniesPausesAndResumes(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 60, BuildTime: 100},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	flashDef := cat.Units[content.CanonicalKey("armflash")]
	flashDef.BuildCostMetal = 100
	w := units.NewSliced(20, cat)
	hf, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(hf)
	factory.Def = facDef
	// Increase workerTime to have quantum 2 (60/30=2)
	// Use existing 60 => quantum 2
	hp, _ := w.Create(flashDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod := w.Unit(hp)
	prod.Def = flashDef
	prod.Remaining = 0.5
	prod.MaxHealth = 100
	prod.Health = 50
	econ := &economy.Service{}
	if b := econ.UnitBuckets(factory.Handle); b != nil {
		// Simulate settlement denies carry: set positive carry before StepUnit
		(*b)[economy.Metal].Carry = 10
		(*b)[economy.Energy].Carry = 0
	}
	svc := NewService(nil, cat, w, econ)
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: prod.Handle})
	head := q.Primary()[0]
	head.Phase = uint8(State3)
	head.Target = prod.Handle
	before := prod.Remaining
	res := svc.StepUnit(TickContext{Tick: 20, World: w, Economy: econ, Catalog: cat}, hf)
	if res.Err != nil {
		t.Fatalf("err %v", res.Err)
	}
	if prod.Remaining != before {
		t.Fatalf("after settlement denies carry, work should pause (remaining %v -> %v)", before, prod.Remaining)
	}
	// Now settlement would have cleared carry? Simulate resources return: set carry <=0
	if b := econ.UnitBuckets(factory.Handle); b != nil {
		(*b)[economy.Metal].Carry = 0
		(*b)[economy.Energy].Carry = 0
	}
	res2 := svc.StepUnit(TickContext{Tick: 21, World: w, Economy: econ, Catalog: cat}, hf)
	if res2.Err != nil {
		t.Fatalf("err %v", res2.Err)
	}
	if prod.Remaining == before {
		t.Fatalf("after resources return, work should resume")
	}
}

func TestStepUnit_CancelBeforeAndAfterNanoframe(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 30, BuildTime: 100},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	w := units.NewSliced(20, cat)
	hf, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(hf)
	factory.Def = facDef
	svc := NewService(nil, cat, w, &economy.Service{})
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	// Cancel before nanoframe: queue node in state2, no Target, interrupt cancel should remove node deterministically and not create product.
	q := orders.QueueForUnit(factory)
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State2)})
	head := q.Primary()[0]
	head.Phase = uint8(State2)
	factory.Pending = InterruptCancel
	res := svc.StepUnit(TickContext{Tick: 10, World: w, Catalog: cat, Economy: svc.Economy}, hf)
	if res.Err != nil {
		t.Fatalf("cancel before err %v", res.Err)
	}
	// Re-fetch queue after step (old q variable is stale after BindQueue)
	qAfter := orders.QueueForUnit(factory)
	if qAfter.LenPrimary() != 0 {
		t.Fatalf("cancel before nanoframe should remove node, len %d", qAfter.LenPrimary())
	}
	// Ensure no product created and no builderLink leaked
	if len(svc.BuilderLinks()) != 0 {
		t.Fatalf("builderLinks leaked before nanoframe cancel")
	}
	// Now test cancel after nanoframe: create product, queue state3, then cancel
	w2 := units.NewSliced(20, cat)
	hf2, _ := w2.Create(facDef, 0, world.CellToWorld(5), numeric.Fixed(0), world.CellToWorld(5))
	factory2 := w2.Unit(hf2)
	factory2.Def = facDef
	bindConstructionFixture(factory2, trivialModel(1, nil), true)
	svc2 := NewService(exitTerrain(12, 12), cat, w2, &economy.Service{})
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State2)})
	head2 := q2.Primary()[0]
	head2.Phase = uint8(State2)
	// Step to allocate nanoframe
	svc2.StepUnit(TickContext{Tick: 20, World: w2, Catalog: cat, Economy: svc2.Economy}, hf2)
	if head2.Target == 0 {
		t.Fatalf("allocation failed for after-nanoframe cancel test")
	}
	prodHandle := head2.Target
	// Now cancel
	factory2.Pending = InterruptCancel
	res2 := svc2.StepUnit(TickContext{Tick: 21, World: w2, Catalog: cat, Economy: svc2.Economy}, hf2)
	if res2.Err != nil {
		t.Fatalf("cancel after err %v", res2.Err)
	}
	if orders.QueueForUnit(factory2).LenPrimary() != 0 {
		t.Fatalf("cancel after should remove node, got %d", orders.QueueForUnit(factory2).LenPrimary())
	}
	prod := w2.Unit(prodHandle)
	if prod != nil && prod.Alive {
		t.Fatalf("product should be dead after cancel")
	}
	if _, ok := svc2.BuilderLink(prodHandle); ok {
		t.Fatalf("builderLink not cleared after cancel after nanoframe")
	}
	// Determinism: repeating cancel before and after yields same results
}

func TestStepUnit_CompletionExactlyOnce(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 60, BuildTime: 2, BuildCostMetal: 10},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 2, BuildCostMetal: 10},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	flashDef := cat.Units[content.CanonicalKey("armflash")]
	flashDef.BuildTime = 2
	flashDef.MaxDamage = 100
	w := units.NewSliced(20, cat)
	hf, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(hf)
	factory.Def = facDef
	// Set workerTime 60 => quantum 2, buildTime 2 => delta 1 each tick => should complete in one step from Remaining 1? But need nanoframe then work.
	// Create directly state3 product with Remaining 0.2 to test completion in one step: worker 60 => quantum2 buildTime2 => delta 1 => Remaining 0.2 ->0
	hp, _ := w.Create(flashDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod := w.Unit(hp)
	prod.Def = flashDef
	prod.Remaining = 0.2
	prod.MaxHealth = 100
	prod.Health = 80
	svc := NewService(nil, cat, w, &economy.Service{})
	if b := svc.Economy.UnitBuckets(factory.Handle); b != nil {
		(*b)[economy.Metal].Carry = 0
		(*b)[economy.Energy].Carry = 0
	}
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: hp})
	head := q.Primary()[0]
	head.Phase = uint8(State3)
	head.Target = hp
	res := svc.StepUnit(TickContext{Tick: 30, World: w, Economy: svc.Economy, Catalog: cat}, hf)
	if res.Err != nil {
		t.Fatalf("err %v", res.Err)
	}
	if !res.Completed {
		t.Fatalf("should have completed")
	}
	if res.Product != hp {
		t.Fatalf("product handle %d want %d", res.Product, hp)
	}
	if res.DefKey != "armflash" {
		t.Fatalf("defkey %q want armflash", res.DefKey)
	}
	if res.Owner != factory.Owner {
		t.Fatalf("owner mismatch")
	}
	if prod.Remaining != 0 {
		t.Fatalf("remaining %v want 0", prod.Remaining)
	}
	if prod.Health != prod.MaxHealth {
		t.Fatalf("health not initialized exactly once: %d want %d", prod.Health, prod.MaxHealth)
	}
	// Second step should not re-complete
	res2 := svc.StepUnit(TickContext{Tick: 31, World: w, Economy: svc.Economy, Catalog: cat}, hf)
	if res2.Completed {
		t.Fatalf("completion should be exactly once, second step should not report completed")
	}
	if prod.Health != prod.MaxHealth {
		t.Fatalf("health changed on second step")
	}
}

func TestStepUnit_NoPresentationCalls(t *testing.T) {
	cat := catWithDefs(
		&content.UnitDef{UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 30, BuildTime: 100},
		&content.UnitDef{UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 10},
	)
	facDef := cat.Units[content.CanonicalKey("armfac")]
	w := units.NewSliced(20, cat)
	hf, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(hf)
	factory.Def = facDef
	svc := NewService(nil, cat, w, &economy.Service{})
	called := false
	svc.OnRefresh = func(u *units.Unit) { called = true }
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State2)})
	head := q.Primary()[0]
	head.Phase = uint8(State2)
	svc.StepUnit(TickContext{Tick: 0, World: w, Economy: svc.Economy, Catalog: cat}, hf)
	if called {
		t.Fatalf("StepUnit should not call presentation OnRefresh")
	}
}
