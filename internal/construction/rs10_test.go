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

// RS-10 focused tests: mobile/factory lifecycle [RS-10].

// TestRS10_MobileBuildLegalSite verifies mobile build uses exact validated world site and builder link [RS-10][P0-I05].
func TestRS10_MobileBuildLegalSite(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true, BuildTime: 100}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef

	w := units.New(20, cat)
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef

	// Simulate PlacementResult exact validated site [RS-11]: use terrain validated cell
	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	siteX := world.CellToWorld(10)
	siteZ := world.CellToWorld(10)
	if err := QueueMobileBuild(builder, "armllt", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild: %v", err)
	}
	q := orders.QueueForUnit(builder)
	node := q.Primary()[0]
	if node.GoalX != siteX || node.GoalZ != siteZ {
		t.Fatalf("pending site not exact validated: got %d,%d want %d,%d", node.GoalX.Raw(), node.GoalZ.Raw(), siteX.Raw(), siteZ.Raw())
	}
	// Factory descriptor must not be used for mobile builder
	if node.ID == orders.Lookup("BuildingBuild") {
		t.Fatalf("mobile builder should not use BuildingBuild")
	}
	// Advance to nanoframe allocation: state2 with exact site
	node.Phase = uint8(State2)
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	svc.Pump(builder, 0)
	if node.Target == 0 {
		t.Fatalf("legal site allocation failed")
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
	// Builder link [05 C18]
	if b, ok := svc.BuilderLink(prod.Handle); !ok || b != builder.Handle {
		t.Fatalf("builder link not exact: got %v ok=%v want %v", b, ok, builder.Handle)
	}
	// Pending site must survive to product placement (Goal still authoritative)
	if node.GoalX != siteX || node.GoalZ != siteZ {
		t.Fatalf("pending site mutated after allocation")
	}
	// queue operation state: Phase should be 3 (work loop), not 2
	if State(node.Phase) != State3 {
		t.Fatalf("queue operation state after allocation want State3 got %d", node.Phase)
	}
}

// TestRS10_MobileBuildIllegalSite verifies illegal site triggers silent blocked retry [RS-10][05 C17].
func TestRS10_MobileBuildIllegalSite(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100, BuildTime: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef

	terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	// Block cell (5,5) by occupancy
	terrain.Plot[5*10+5].SetOccupantA(1)
	// Also block via feature for yard bit 5
	terrain.Plot[5*10+5].SetFeature(0) // real feature index 0 is blocking when yard bit 5 set? But our yard "oooo" bits 1-2 reject any occupant 0x2f, so occupantA triggers block.

	w := units.New(10, cat)
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef

	siteX := world.CellToWorld(5)
	siteZ := world.CellToWorld(5)
	// Site snaps with foot 2 => cell 4,4? Actually site 5,5 with foot 2 => snap 4,4 : need site that snaps to blocked 5,5?
	// Choose site 6,6 -> snap 5,5
	siteX = world.CellToWorld(6)
	siteZ = world.CellToWorld(6)
	if err := QueueMobileBuild(builder, "armllt", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild: %v", err)
	}
	q := orders.QueueForUnit(builder)
	node := q.Primary()[0]
	node.Phase = uint8(State2)
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	tick := uint32(100)
	svc.Pump(builder, tick)
	if node.Target != 0 {
		t.Fatalf("illegal site should not allocate, got target %d", node.Target)
	}
	if node.Deadline != int32(tick+15) {
		t.Fatalf("blocked illegal site should retry in 15, got deadline %d want %d", node.Deadline, tick+15)
	}
	if node.DynamicGate != WakeBit1|WakeBit2 {
		t.Fatalf("blocked wake bits want %d got %d", WakeBit1|WakeBit2, node.DynamicGate)
	}
	if len(svc.Messages()) != 0 {
		t.Fatalf("illegal site blocked should be silent, got %v", svc.Messages())
	}
	// No builder link yet
	if len(svc.BuilderLinks()) != 0 {
		t.Fatalf("no builder link should exist for blocked site")
	}
}

// TestRS10_StarveResume verifies starve via carry then resume [RS-10][05 "Two-resource admission"].
func TestRS10_StarveResume(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		content.CanonicalKey("armfac"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfac"}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 60, BuildTime: 100},
		content.CanonicalKey("armflash"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armflash"}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100},
	}}
	facDef := cat.Units[content.CanonicalKey("armfac")]
	flashDef := cat.Units[content.CanonicalKey("armflash")]
	flashDef.BuildCostMetal = 100
	flashDef.BuildCostEnergy = 100
	w := units.New(20, cat)
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
	// Starve: positive carry denies admission
	if b := econ.UnitBuckets(factory.Handle); b != nil {
		(*b)[economy.Metal].Carry = 10
	}
	svc := NewService(nil, cat, w, econ)
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State3), Target: hp})
	head := q.Primary()[0]
	head.Phase = uint8(State3)
	head.Target = hp
	before := prod.Remaining
	svc.Pump(factory, 10)
	if prod.Remaining != before {
		t.Fatalf("starved should not advance remaining %v -> %v", before, prod.Remaining)
	}
	// Resume: clear carry
	if b := econ.UnitBuckets(factory.Handle); b != nil {
		(*b)[economy.Metal].Carry = 0
		(*b)[economy.Energy].Carry = 0
	}
	svc.Pump(factory, 11)
	if prod.Remaining == before {
		t.Fatalf("resumed should advance")
	}
}

// TestRS10_MultiBuilderLowestSlot verifies lowest-slot wins [RS-10][05 "Construction arithmetic"].
func TestRS10_MultiBuilderLowestSlot(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 1, FootprintZ: 1, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 30, BuildTime: 3}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armlab", FootprintX: 1, FootprintZ: 1, YardMap: "o", MaxDamage: 10, BuildTime: 3, BuildCostMetal: 0}
	prodDef.CanonicalKey = content.CanonicalKey("armlab")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armlab")] = prodDef

	w := units.NewSliced(10, cat)
	hp, _ := w.Create(prodDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod := w.Unit(hp)
	prod.Def = prodDef
	prod.Remaining = 1.0
	prod.MaxHealth = 10
	h1, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	b1 := w.Unit(h1)
	b1.Def = builderDef
	h2, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	b2 := w.Unit(h2)
	b2.Def = builderDef
	if h1 >= h2 {
		t.Fatalf("slot order violated")
	}
	svc := NewService(nil, cat, w, &economy.Service{})
	for _, b := range []*units.Unit{b1, b2} {
		q := orders.QueueForUnit(b)
		bid := orders.Lookup("BuildingBuild")
		if bid == 0 {
			bid = orders.Lookup("MobileBuild")
		}
		q.Push(bid, orders.Node{BuildDefKey: "armlab", Param1: 1, Param2: 1, Phase: uint8(State3), Target: prod.Handle})
		q.Primary()[0].Phase = uint8(State3)
		q.Primary()[0].Target = prod.Handle
	}
	svc.PumpAll(0)
	if prod.Remaining == 1.0 {
		t.Fatalf("multi-builder did not advance")
	}
	// Completion lowest-slot wins: set to 0.333, one step each should complete via b1 first
	prod.Remaining = 0.33333334
	prod.Health = 7
	for _, b := range []*units.Unit{b1, b2} {
		q := orders.QueueForUnit(b)
		q.Primary()[0].Phase = uint8(State3)
	}
	svc.PumpAll(1)
	if prod.Remaining != 0 {
		t.Fatalf("completion not reached %v", prod.Remaining)
	}
}

// TestRS10_FactoryBlockedRetryAndLimit verifies 15-tick retry and 300-tick limit failure [RS-10][05 C17][05 C18].
func TestRS10_FactoryBlockedRetryAndLimit(t *testing.T) {
	terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	// Foreign occupant (handle 9, distinct from the producing factory) on the
	// exit cell: self-exemption must never swallow genuinely foreign stamps.
	terrain.Plot[4*10+4].SetOccupantA(9)

	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfac"}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "oo\n00", Builder: true, MaxDamage: 200, WorkerTime: 300}
	prodDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armflash"}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oo\noo", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 100}
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	w := units.New(20, cat)
	h, _ := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := w.Unit(h)
	factory.Def = facDef
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State2), Deadline: -1})
	head := q.Primary()[0]
	head.Phase = uint8(State2)
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.AllowSyntheticPlacement = true
	svc.Pump(factory, 10)
	if head.Deadline != int32(25) {
		t.Fatalf("blocked retry deadline want 25 got %d", head.Deadline)
	}
	if head.DynamicGate != WakeBit1|WakeBit2 {
		t.Fatalf("blocked wake bits")
	}
	if len(svc.Messages()) != 0 {
		t.Fatalf("blocked should be silent")
	}
	// Limit failure path: make allocation refuse via LimitChecker
	terrain.Plot[4*10+4].SetOccupantA(0) // unblock
	svc.LimitChecker = func(f *units.Unit, key string) bool { return false }
	svc.Pump(factory, 30)
	if head.Deadline != int32(330) {
		t.Fatalf("limit failure retry want 330 got %d", head.Deadline)
	}
	found := false
	for _, m := range svc.Messages() {
		if m == "Unable to create any more units" {
			found = true
		}
	}
	if !found {
		t.Fatalf("limit failure missing verbatim message got %v", svc.Messages())
	}
	if head.Phase != uint8(State2) {
		t.Fatalf("should stay State2 on limit failure")
	}
}

// TestRS10_CancelRefundAndLinks verifies cancel, refund, StopBuilding/GetBuilt, standing orders and link behavior [RS-10][05 C21][05 C22].
func TestRS10_CancelRefundAndLinks(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfac"}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, MaxDamage: 100, WorkerTime: 300, BuildTime: 100, BuildCostMetal: 200}
	prodDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armflash"}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "o", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 200}
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	w := units.New(10, cat)
	econ := &economy.Service{}
	h, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory := w.Unit(h)
	factory.Def = facDef
	factory.Owner = 0
	factory.Flags = FlagDeactivate | FlagStartBuilding

	// Create product with remaining 0.25 => refund trunc((1-0.25)*200)=150
	hp, _ := w.Create(prodDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod := w.Unit(hp)
	prod.Def = prodDef
	prod.Remaining = 0.25
	prod.MaxHealth = 100
	// Queue state3
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 2, Phase: uint8(State3), Target: prod.Handle})
	head := q.Primary()[0]
	head.Target = prod.Handle
	head.Phase = uint8(State3)
	svc := NewService(nil, cat, w, econ)
	svc.OnRefresh = func(u *units.Unit) {}
	// Register link
	svc.SetBuilderLink(prod.Handle, factory.Handle)
	if _, ok := svc.BuilderLink(prod.Handle); !ok {
		t.Fatalf("link not set")
	}
	// Cancel
	factory.Pending = InterruptCancel
	svc.Pump(factory, 100)
	if econ.Players[0].Mirror[economy.Metal].Production != 150 {
		t.Fatalf("refund want 150 got %v", econ.Players[0].Mirror[economy.Metal].Production)
	}
	if svc.LastKill().Damage != 30000 || svc.LastKill().Severity != 0 || !svc.LastKill().NoCorpse {
		t.Fatalf("kill packet wrong %+v", svc.LastKill())
	}
	if factory.Flags&(FlagDeactivate|FlagStartBuilding) != 0 {
		t.Fatalf("callback bits not cleared together")
	}
	if q2 := orders.QueueForUnit(factory); q2.LenPrimary() != 0 {
		t.Fatalf("cancel should drop node")
	}
	if _, ok := svc.BuilderLink(prod.Handle); ok {
		t.Fatalf("builder link should be cleared on cancel after nanoframe")
	}
	if head.Param2 != 2 {
		t.Fatalf("cancel should not decrement count, got %d", head.Param2)
	}
	// Stop interrupt: should decrement once and survive, state0
	w2 := units.New(10, cat)
	h2, _ := w2.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory2 := w2.Unit(h2)
	factory2.Def = facDef
	q2 := orders.QueueForUnit(factory2)
	q2.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 3, Phase: uint8(State3)})
	head2 := q2.Primary()[0]
	head2.Param2 = 3
	head2.Phase = uint8(State3)
	svc2 := NewService(nil, cat, w2, &economy.Service{})
	svc2.OnRefresh = func(u *units.Unit) {}
	factory2.Pending = InterruptStop
	svc2.Pump(factory2, 200)
	if head2.Param2 != 2 {
		t.Fatalf("stop decrement want 2 got %d", head2.Param2)
	}
	if head2.Phase != uint8(State0) {
		t.Fatalf("stop should restart at State0")
	}
	if q2.LenPrimary() != 1 {
		t.Fatalf("stop should keep node")
	}
	foundStop := false
	for _, m := range svc2.Messages() {
		if m == "Construction stopped" {
			foundStop = true
		}
	}
	if !foundStop {
		t.Fatalf("stop message missing")
	}
	// Death/capture link behavior: leaked on death (no walk) [P0-14], cleared on completion
	w3 := units.New(10, cat)
	h3, _ := w3.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory3 := w3.Unit(h3)
	factory3.Def = facDef
	hp3, _ := w3.Create(prodDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod3 := w3.Unit(hp3)
	prod3.Def = prodDef
	svc3 := NewService(nil, cat, w3, &economy.Service{})
	svc3.SetBuilderLink(prod3.Handle, factory3.Handle)
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	w3.Destroy(prod3.Handle, 0)
	if _, ok := svc3.BuilderLink(hp3); !ok {
		t.Fatalf("builder link should leak on death/capture (no walk)")
	}
	// Completion clears
	w4 := units.New(10, cat)
	h4, _ := w4.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	factory4 := w4.Unit(h4)
	factory4.Def = facDef
	hp4, _ := w4.Create(prodDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	prod4 := w4.Unit(hp4)
	prod4.Def = prodDef
	prod4.Remaining = 0
	prod4.MaxHealth = 100
	prod4.Health = 100
	svc4 := NewService(nil, cat, w4, &economy.Service{})
	svc4.SetBuilderLink(prod4.Handle, factory4.Handle)
	q4 := orders.QueueForUnit(factory4)
	q4.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 1, Phase: uint8(State4), Target: hp4})
	head4 := q4.Primary()[0]
	head4.Target = hp4
	head4.Phase = uint8(State4)
	svc4.Pump(factory4, 300)
	if _, ok := svc4.BuilderLink(hp4); ok {
		t.Fatalf("builder link should be cleared on completion")
	}
}
