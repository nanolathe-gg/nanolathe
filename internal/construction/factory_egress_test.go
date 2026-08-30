package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestYardCannotCloseOverAProductInTheYard locks [04 R-FAC-02 §5]: "A factory
// therefore cannot close its yard while a released product still stands on a
// `c`/`C` cell". Retail reads one ground word per cell; Nanolathe splits that
// plane into the terrain plot and the movement occupancy grid, and a completed
// mobile product's plot stamp is released at completion, so the grid is the
// only layer still holding the pad. Testing the plot alone admitted the close
// and shut the doors on the product standing in the yard.
func TestYardCannotCloseOverAProductInTheYard(t *testing.T) {
	lab := newFactoryDef("closelab", 4, 4, 300)
	// 'o' is selected in both yard states, 'c' only while closed [04 R-COLL-01 §4].
	lab.YardMap = "yooy occo occo yooy"
	cat := exitCatalog(lab)
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	svc.Movement = &movement.System{Grid: movement.NewOccupancyGrid()}

	h, err := w.Create(lab, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	u := w.Unit(h)
	if err := svc.RegisterBuildingPlacement(u); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(u, true) {
		t.Fatal("empty yard refused to open")
	}
	rect, ok := svc.PlacementForProduct(h)
	if !ok {
		t.Fatal("no placement record")
	}
	// A released product standing on one of the yard's closed-only cells: it
	// holds the grid half of the ground plane, and nothing at all in the plot,
	// which is exactly the state completion leaves behind.
	yard, err := world.ParseYardMap(lab.YardMap, int(rect.Width()), int(rect.Depth()))
	if err != nil {
		t.Fatalf("ParseYardMap: %v", err)
	}
	var padX, padZ int32
	found := false
	for z := rect.MinZ(); z < rect.MaxZ() && !found; z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			y := yard[int((z-rect.MinZ())*rect.Width()+(x-rect.MinX()))]
			if y&0x04 != 0 && y&0x02 == 0 { // 'c': stamped only while closed
				padX, padZ, found = x, z, true
				break
			}
		}
	}
	if !found {
		t.Fatal("fixture yard map has no closed-only cell")
	}
	const productID = 4242
	svc.Movement.Grid.Stamp(movement.Cell{X: padX, Z: padZ}, 1, 1, productID)

	if svc.YardOpenTransaction(u, false) {
		t.Fatalf("yard closed with a product still standing on its pad cell (%d,%d) [04 R-FAC-02 §5]", padX, padZ)
	}
	if !u.YardOpen {
		t.Fatal("a refused transaction must write nothing")
	}
	// The product drives off the pad; the close is admitted on the next attempt.
	svc.Movement.Grid.Clear(movement.Cell{X: padX, Z: padZ}, 1, 1, productID)
	if !svc.YardOpenTransaction(u, false) {
		t.Fatal("yard refused to close after the product left the pad")
	}
	if u.YardOpen {
		t.Fatal("accepted close did not commit the bit")
	}
}

// activateFixtureProgram is bindConstructionFixture's program plus an Activate
// entry point, so a raised activation edge is observable as a started thread.
func activateFixtureProgram() *cob.Program {
	return &cob.Program{
		Code:        []uint32{0x10021001, 0, 0x10023002, 0, 0x10065000},
		Scripts:     map[string]int{"QueryBuildInfo": 0, "QueryNanoPiece": 0, "Activate": 4},
		ScriptsByID: []int{0, 0, 4},
		Pieces:      []string{"base"},
	}
}

func anyThreadAlive(vm *cob.VM) bool {
	for i := 0; i < 16; i++ {
		if vm.IsThreadAlive(i) {
			return true
		}
	}
	return false
}

// TestStateZeroActivateIsARealEdge locks the state-0 raise of [05 "Factory
// production lifecycle"]. The yard-door handshake is entirely script-owned —
// the engine raises Activate and waits, and nothing but the script writes the
// in-build-stance bit state 1 tests — so the raise has to start the script.
// Nanolathe pins the activation bit true at creation for every definition
// authoring neither `onoffable` nor `activatewhenbuilt` (units.InitEconomyState,
// the economy's stand-in for [05 R-PROD-01 §2]); every stock factory is one, so
// the raise was swallowed and the node waited in state 1 forever.
func TestStateZeroActivateIsARealEdge(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("edgelab", 4, 4, 300)
	cat.Units[facDef.CanonicalKey] = facDef
	w := newTestWorld(8)
	h, err := w.Create(facDef, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	factory := w.Unit(h)
	vm := cob.NewVM(activateFixtureProgram())
	binding := &cob.Binding{VM: vm, Model: trivialModel(1, nil), PieceMap: []int{0}, Callbacks: cob.NewCallbackBridge(vm)}
	factory.Script = vm
	factory.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	// The pinned state a stock factory reaches its first product in.
	factory.Activated = true
	factory.InBuildStance = false
	if anyThreadAlive(vm) {
		t.Fatal("fixture starts with a live thread")
	}
	svc := NewService(exitTerrain(16, 16), cat, w, nil)
	node := &orders.Node{ID: orders.Lookup("BuildingBuild"), Param2: 1, Phase: uint8(State0), Deadline: -1}
	svc.handleState0(factory, node, 10)
	if !anyThreadAlive(vm) {
		t.Fatal("state 0 did not start the factory's Activate script: the yard-door handshake can never begin")
	}
	if !factory.Activated {
		t.Fatal("state 0 left the activation bit low")
	}
	if State(node.Phase) != State1 {
		t.Fatalf("phase=%d, want state 1", node.Phase)
	}
}

// TestStateZeroDoesNotRestartActivateMidProduction is the other half of the
// same contract: the state machine restarts at state 0 within the same pump
// pass for a coalesced count [05 "Factory production lifecycle"], and retail's
// edge machine suppresses that repeat raise. A factory already in the build
// stance must not have its door script restarted under it.
func TestStateZeroDoesNotRestartActivateMidProduction(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("edgelab2", 4, 4, 300)
	cat.Units[facDef.CanonicalKey] = facDef
	w := newTestWorld(8)
	h, _ := w.Create(facDef, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	factory := w.Unit(h)
	vm := cob.NewVM(activateFixtureProgram())
	binding := &cob.Binding{VM: vm, Model: trivialModel(1, nil), PieceMap: []int{0}, Callbacks: cob.NewCallbackBridge(vm)}
	factory.Script = vm
	factory.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	factory.Activated = true
	factory.InBuildStance = true // mid-production: the script is in the stance
	svc := NewService(exitTerrain(16, 16), cat, w, nil)
	node := &orders.Node{ID: orders.Lookup("BuildingBuild"), Param2: 1, Phase: uint8(State0), Deadline: -1}
	svc.handleState0(factory, node, 10)
	if anyThreadAlive(vm) {
		t.Fatal("state 0 restarted Activate on a factory already in the build stance")
	}
}

// TestCompletionIsReportedWhenASuccessorAllocatesInTheSamePass locks the
// completion hand-off. [05 "Factory production lifecycle"] state 4 "returns
// result 0 — the state machine restarts at state 0 within the same pump pass,
// so coalesced counts build back-to-back with no gap", which means the node's
// target is the SUCCESSOR's nanoframe by the time StepUnit reads it back.
// Deriving the completed handle from the post-pump head therefore reported only
// the last product of a run; every earlier one never reached the session's
// completion hook, so it got no mover and could not leave the pad.
func TestCompletionIsReportedWhenASuccessorAllocatesInTheSamePass(t *testing.T) {
	facDef := newFactoryDef("passlab", 4, 4, 3000)
	facDef.YardMap = "yccy yccy yccy yccy" // 'c' pad lane released while open
	prodDef := exitMobileDef("passprod", 1, 1)
	prodDef.BuildTime = 1
	prodDef.BuildCostEnergy = 1
	prodDef.BuildCostMetal = 1
	cat := exitCatalog(facDef, prodDef)
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
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param1: prodIdx(cat, prodDef.CanonicalKey), Param2: 2, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].Phase = uint8(State2)

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	var first pool.Handle
	reported := map[pool.Handle]int{}
	for i := 1; i <= 40; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, uint32(i), w, func() {})
		res := svc.StepUnit(ctx, h)
		if first == 0 {
			if node := headNodeForTest(factory); node != nil && node.Target != 0 {
				first = node.Target
			}
		}
		if res.Completed && res.Product != 0 {
			reported[res.Product]++
		}
	}
	if first == 0 {
		t.Fatal("factory never allocated a first product")
	}
	if reported[first] == 0 {
		t.Fatalf("first product %d completed but was never reported to the session; reported=%v", first, reported)
	}
	if len(reported) < 2 {
		t.Fatalf("a two-count run reported %d distinct completions, want both: %v", len(reported), reported)
	}
}
