package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestAssistedProductStillCompletesTheFactoryNode is WU-19-132's regression.
//
// [05 R-WORK-01 §1] places the completion transition after every exit of the
// shared construction step — "on both arms and also on the admission-refused
// path" — so the stored fraction's zero test, not whether this particular step
// committed, is what completes a product. Its FIRST line makes a step on an
// already-zero fraction return not-committed, so when an ASSISTING builder
// stores the zero the factory's own next step returns not-committed too.
//
// The factory's state-3 body used to reach the zero test only through the
// committed return. An assisted product therefore left its factory's node
// retrying in state 3 for the rest of the battle: the building edge never
// fell, no successor product was ever allocated, and no `WorkResult` ever
// reported the completion — which is the hook the session uses to give a new
// unit its mover state, so the finished product stood inside the factory
// footprint holding no occupancy and could not be moved out
// [05 "Factory production lifecycle"][04 R-FAC-02 §3].
//
// A commander guarding its own kbot lab is the ordinary opening, so this is
// the common case, not a corner.
func TestAssistedProductStillCompletesTheFactoryNode(t *testing.T) {
	facDef := newFactoryDef("assistlab", 4, 4, 30)
	facDef.YardMap = "yccy yccy yccy yccy" // 'c' pad lane released while open
	prodDef := exitMobileDef("assistprod", 1, 1)
	prodDef.BuildTime = 100
	prodDef.BuildCostEnergy = 1
	prodDef.BuildCostMetal = 1
	helperDef := newFactoryDef("assisthelper", 1, 1, 30000)
	cat := exitCatalog(facDef, prodDef, helperDef)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	svc, w := exitService(t, exitTerrain(24, 24), cat)
	svc.Movement = &movement.System{Grid: movement.NewOccupancyGrid()}
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}
	h, err := w.Create(facDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(h)
	bindConstructionFixture(factory, trivialModel(1, [][3]int64{{0, 0, 0}}), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("factory yard refused to open")
	}
	helperHandle, err := w.Create(helperDef, 0, world.CellToWorld(18), 0, world.CellToWorld(18))
	if err != nil {
		t.Fatalf("create assisting builder: %v", err)
	}
	helper := w.Unit(helperHandle)

	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{
		BuildDefKey: prodDef.CanonicalKey,
		Param1:      prodIdx(cat, prodDef.CanonicalKey),
		Param2:      1,
		Phase:       uint8(State2),
		Deadline:    -1,
	})

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	// One factory visit to allocate the product and enter state 3.
	var product pool.Handle
	for i := 1; i <= 20 && product == 0; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, ctx.Tick, w, func() {})
		svc.StepUnit(ctx, h)
		if node := headNodeForTest(factory); node != nil {
			product = node.Target
		}
	}
	if product == 0 {
		t.Fatal("factory never allocated a product")
	}
	// The assisting builder finishes it. Its own step owns the zero store, and
	// with it the completion transition [05 R-WORK-01 §1].
	prod := w.Unit(product)
	for i := 0; i < 200 && prod.Remaining > 0; i++ {
		svc.Assist(helper, prod, 100)
	}
	if prod.Remaining != 0 {
		t.Fatalf("the assisting builder did not finish the product: remaining %v", prod.Remaining)
	}

	// The factory's own next step returns not-committed on the already-zero
	// fraction; the node must complete anyway and report it.
	reported := false
	for i := 21; i <= 40 && !reported; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, ctx.Tick, w, func() {})
		res := svc.StepUnit(ctx, h)
		if res.Completed && res.Product == product {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the factory never reported the assisted product %d as completed; its node would retry "+
			"in state 3 forever and the product would never receive mover state [05 R-WORK-01 §1]", product)
	}
	if node := headNodeForTest(factory); node != nil && State(node.Phase) == State3 {
		t.Fatalf("the factory node is still in state 3 after the assisted completion")
	}
}

// TestAssistRunsTheCompletionTransitionAfterEveryExit is WU-19-153's half of the
// same contract, at the sibling site.
//
// [05 R-WORK-01 §1] places the completion transition after EVERY exit of the
// shared step, and its FIRST line makes a step on an already-zero fraction
// return not-committed. `Service.Assist` used to run the zero test only behind
// a committed step, which is exactly the shape WU-19-132 had to correct in the
// factory's state-3 body. Assert the transition on the not-committed exit, so a
// frame whose zero was stored elsewhere still receives the posture from a
// helper's own visit.
func TestAssistRunsTheCompletionTransitionAfterEveryExit(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	helperDef := newFactoryDef("assistexithelper", 1, 1, 30)
	helperDef.BMCode = 1
	helperDef.CanMove = true
	prodDef := newProductDef("assistexitprod", 1, 1, 1, 100)
	cat.Units[helperDef.CanonicalKey] = helperDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(8, cat)
	hh, err := w.Create(helperDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := w.Create(prodDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	helper, product := w.Unit(hh), w.Unit(ph)
	// Someone else already stored the zero; this helper's own step therefore
	// takes §1's first line and commits nothing.
	product.Remaining = 0
	product.Health = 0
	product.Flags &^= FlagCompleted

	svc := NewService(nil, cat, w, &economy.Service{})
	if svc.Assist(helper, product, 10) {
		t.Fatal("a step on an already-zero fraction reported committed work [05 R-WORK-01 §1]")
	}
	if product.Flags&FlagCompleted == 0 || product.Health != product.MaxHealth {
		t.Fatalf("the not-committed exit skipped the completion transition: flags %#x health %d/%d [05 R-WORK-01 §1]",
			product.Flags, product.Health, product.MaxHealth)
	}
}
