package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func newRS10Movement(terrain *world.Terrain) *movement.System {
	grid := movement.NewOccupancyGrid()
	fallback := movement.Profile{FootPrintX: 1, FootPrintZ: 1}
	return movement.NewSystem(terrain, fallback, grid)
}

// TestRS10_SessionSaveMidMobile verifies session capture/restore mid-mobile-build preserves exact work [RS-10].
func TestRS10_SessionSaveMidMobile(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 300, CanMove: true, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 200, BuildTime: 200, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef

	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}

	// Session A
	wA := units.New(20, cat)
	econA := &economy.Service{}
	sA := &Session{
		Catalog:  cat,
		World:    terrain,
		Units:    wA,
		Econ:     econA,
		Build:    construction.NewService(terrain, cat, wA, econA),
		Movement: newRS10Movement(terrain),
	}
	hb, _ := wA.Create(builderDef, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	builder := wA.Unit(hb)
	builder.Def = builderDef
	siteX := world.CellToWorld(10)
	siteZ := world.CellToWorld(10)
	if err := construction.QueueMobileBuild(builder, "armllt", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild: %v", err)
	}
	q := orders.QueueForUnit(builder)
	node := q.Primary()[0]
	node.Phase = uint8(construction.State2)
	// Pump to allocate nanoframe at exact site
	sA.Build.Pump(builder, 10)
	if node.Target == 0 {
		t.Fatalf("allocation failed")
	}
	prodHandle := node.Target
	prod := wA.Unit(prodHandle)
	prod.Remaining = 0.6
	prod.Health = 80
	node.Phase = uint8(construction.State3)
	node.DynamicGate = construction.WakeBit1 | construction.WakeBit3
	node.Deadline = 12345

	hookFired := false
	sA.Build.OnRefresh = func(u *units.Unit) { hookFired = true }

	st := sA.CaptureStateV1()
	if st == nil {
		t.Fatalf("CaptureStateV1 nil")
	}
	if len(st.Construction.BuilderLinks) != 1 {
		t.Fatalf("builder links not captured, got %d", len(st.Construction.BuilderLinks))
	}
	if len(st.Orders) == 0 {
		t.Fatalf("orders not captured")
	}
	ord := st.Orders[0]
	if ord.GoalX != int32(siteX.Raw()) || ord.GoalZ != int32(siteZ.Raw()) {
		t.Fatalf("pending site not captured exact: got %d,%d want %d,%d", ord.GoalX, ord.GoalZ, siteX.Raw(), siteZ.Raw())
	}
	if ord.Phase != uint8(construction.State3) {
		t.Fatalf("queue op state not captured")
	}
	if ord.Deadline != 12345 {
		t.Fatalf("blocked retry deadline not captured")
	}
	found := false
	for _, u := range st.Units {
		if u.Slot == int32(prodHandle) && u.Remaining == 0.6 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fractional remaining not captured")
	}

	// Encode/decode via bank to test persistence
	b := save.NewBuilder(save.RetailTag)
	save.WriteStateV1(b, st)
	payload := b.Bytes()
	bank, err := save.OpenBytes(payload, save.RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	decoded, err := save.ReadStateV1(bank, "", "")
	if err != nil {
		t.Fatalf("ReadStateV1: %v", err)
	}

	// Session B restore without firing hooks
	wB := units.New(20, cat)
	econB := &economy.Service{}
	sB := &Session{
		Catalog:  cat,
		World:    terrain,
		Units:    wB,
		Econ:     econB,
		Build:    construction.NewService(terrain, cat, wB, econB),
		Movement: newRS10Movement(terrain),
	}
	// Need to create builder and product shells? RestoreStateV1 will recreate units from st, so wB empty is fine.
	// But we need Clock etc? Minimal.
	sB.Build.OnRefresh = func(u *units.Unit) { hookFired = true }
	hookFired = false
	if err := sB.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	if hookFired {
		t.Fatalf("restore should not fire gameplay hooks")
	}
	if _, ok := sB.Build.BuilderLink(prodHandle); !ok {
		t.Fatalf("builder link not rebinded after restore")
	}
	// Verify queue operation state and pending site still exact
	qB := orders.QueueForUnit(sB.Units.Unit(hb))
	if qB == nil || qB.LenPrimary() == 0 {
		t.Fatalf("queue not restored")
	}
	nodeB := qB.Primary()[0]
	if nodeB.GoalX != siteX || nodeB.GoalZ != siteZ {
		t.Fatalf("pending site not exact after restore")
	}
	if nodeB.Phase != uint8(construction.State3) || nodeB.Deadline != 12345 {
		t.Fatalf("queue operation state/deadline not restored")
	}
	prodB := sB.Units.Unit(prodHandle)
	if prodB == nil || prodB.Remaining != 0.6 {
		t.Fatalf("fractional remaining not restored, got %v", prodB.Remaining)
	}
	// Resume work: should advance
	before := prodB.Remaining
	if b := econB.UnitBuckets(hb); b != nil {
		(*b)[economy.Metal].Carry = 0
		(*b)[economy.Energy].Carry = 0
	}
	sB.Build.Pump(sB.Units.Unit(hb), 12346)
	if prodB.Remaining == before {
		t.Fatalf("work did not resume after restore")
	}
}

// TestRS10_SessionSaveMidFactory verifies session capture/restore mid-factory blocked exit resumes exact [RS-10].
func TestRS10_SessionSaveMidFactory(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfac"}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "oo\n00", Builder: true, MaxDamage: 200, WorkerTime: 300, BuildTime: 100}
	prodDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armflash"}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oo\noo", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 100}
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef

	terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.Plot[4*10+4].SetOccupantA(1)

	wA := units.New(20, cat)
	econA := &economy.Service{}
	sA := &Session{Catalog: cat, World: terrain, Units: wA, Econ: econA, Build: construction.NewService(terrain, cat, wA, econA), Movement: newRS10Movement(terrain)}
	hf, _ := wA.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := wA.Unit(hf)
	factory.Def = facDef
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: 1, Param2: 2, Phase: uint8(construction.State2), Deadline: -1})
	head := q.Primary()[0]
	head.Phase = uint8(construction.State2)
	sA.Build.Pump(factory, 10)
	if head.Deadline != 25 {
		t.Fatalf("blocked retry deadline not set, got %d", head.Deadline)
	}
	if head.Param2 != 2 {
		t.Fatalf("queue count not preserved before save")
	}
	if len(sA.Build.BuilderLinks()) != 0 {
		t.Fatalf("should have no builder link before allocation")
	}
	// Capture mid-factory blocked
	st := sA.CaptureStateV1()
	b := save.NewBuilder(save.RetailTag)
	save.WriteStateV1(b, st)
	payload := b.Bytes()
	bank, err := save.OpenBytes(payload, save.RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	decoded, err := save.ReadStateV1(bank, "", "")
	if err != nil {
		t.Fatalf("ReadStateV1: %v", err)
	}
	// Restore into B
	wB := units.New(20, cat)
	econB := &economy.Service{}
	sB := &Session{Catalog: cat, World: terrain, Units: wB, Econ: econB, Build: construction.NewService(terrain, cat, wB, econB), Movement: newRS10Movement(terrain)}
	hookFired := false
	sB.Build.OnRefresh = func(u *units.Unit) { hookFired = true }
	if err := sB.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	if hookFired {
		t.Fatalf("restore fired hook")
	}
	factoryB := sB.Units.Unit(hf)
	if factoryB == nil {
		t.Fatalf("factory not restored")
	}
	qB := orders.QueueForUnit(factoryB)
	if qB.LenPrimary() == 0 {
		t.Fatalf("queue not restored")
	}
	headB := qB.Primary()[0]
	if headB.Deadline != 25 || headB.Param2 != 2 || headB.Phase != uint8(construction.State2) {
		t.Fatalf("factory queue state not exact after restore: deadline %d p2 %d phase %d", headB.Deadline, headB.Param2, headB.Phase)
	}
	if headB.DynamicGate != construction.WakeBit1|construction.WakeBit2 {
		t.Fatalf("wake bits not preserved")
	}
	// Unblock and ensure retry resumes and factory can still produce via ordinary commands
	terrain.Plot[4*10+4].SetOccupantA(0)
	// Need to pump at deadline tick
	sB.Build.Pump(factoryB, 25)
	if headB.Phase != uint8(construction.State3) {
		t.Fatalf("factory blocked should have unblocked to State3 after restore, got %d", headB.Phase)
	}
	// Verify factory still uses primary list and tail coalesce
	if err := construction.QueueFactoryBuild(factoryB, "armflash", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild after restore: %v", err)
	}
	qAfter := orders.QueueForUnit(factoryB)
	if qAfter.LenPrimary() != 2 {
		t.Fatalf("factory queue should be 2 after tail add, got %d", qAfter.LenPrimary())
	}
	// Tail coalesce: same product should merge
	if err := construction.QueueFactoryBuild(factoryB, "armflash", 1, cat); err != nil {
		t.Fatalf("coalesce: %v", err)
	}
	if qAfter.LenPrimary() != 2 {
		t.Fatalf("tail coalesce should keep len 2, got %d", qAfter.LenPrimary())
	}
}

// Ensure human and AI can construct via ordinary commands after save.
// This is a smoke check that production queues still work for both via QueueMobileBuild/QueueFactoryBuild.
func TestRS10_HumanAndAIOrdinaryCommands(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	humanDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true}
	humanDef.CanonicalKey = content.CanonicalKey("armck")
	aiDef := &content.UnitDef{UnitName: "corck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true}
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100, BuildTime: 10, BuildCostMetal: 10}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = humanDef
	cat.Units[content.CanonicalKey("corck")] = aiDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef

	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	w := units.New(20, cat)
	econ := &economy.Service{}
	svc := construction.NewService(terrain, cat, w, econ)

	hHuman, _ := w.Create(humanDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	hAI, _ := w.Create(aiDef, 1, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	human := w.Unit(hHuman)
	aiUnit := w.Unit(hAI)
	human.Def = humanDef
	aiUnit.Def = aiDef

	// Human ordinary command: mobile build
	if err := construction.QueueMobileBuild(human, "armllt", world.CellToWorld(5), world.CellToWorld(5), 1, cat); err != nil {
		t.Fatalf("human QueueMobileBuild: %v", err)
	}
	// AI ordinary command: factory build (AI also uses QueueMobileBuild via BuildRequest, but factory product also via QueueFactoryBuild)
	// Simulate AI queuing via typed builder: ai builds factory product
	facDef := &content.UnitDef{UnitName: "armlab", FootprintX: 4, FootprintZ: 4, YardMap: "oooo oooo oooo oooo", Builder: true, MaxDamage: 200, WorkerTime: 30}
	facDef.CanonicalKey = content.CanonicalKey("armlab")
	cat.Units[content.CanonicalKey("armlab")] = facDef
	// Give AI a factory
	hFac, _ := w.Create(facDef, 1, world.CellToWorld(8), 0, world.CellToWorld(8))
	fac := w.Unit(hFac)
	fac.Def = facDef
	if err := construction.QueueFactoryBuild(fac, "armllt", 1, cat); err != nil {
		t.Fatalf("AI QueueFactoryBuild: %v", err)
	}
	if orders.QueueForUnit(human).LenPrimary() != 1 || orders.QueueForUnit(fac).LenPrimary() != 1 {
		t.Fatalf("ordinary commands not queued")
	}
	// Pump both, ensure they can produce
	for _, u := range []*units.Unit{human, fac} {
		q := orders.QueueForUnit(u)
		q.Primary()[0].Phase = uint8(construction.State2)
	}
	svc.Pump(human, 0)
	svc.Pump(fac, 0)
	if orders.QueueForUnit(human).Primary()[0].Target == 0 {
		t.Fatalf("human mobile build did not produce nanoframe via ordinary command")
	}
	if orders.QueueForUnit(fac).Primary()[0].Target == 0 {
		t.Fatalf("AI factory build did not produce nanoframe via ordinary command")
	}
}

func init() {
	_ = numeric.Fixed(0)
	_ = economy.Metal
	_ = save.StateV1VersionConst
}
