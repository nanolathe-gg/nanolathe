package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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

// exitAircraftDef is exitMobileDef's air twin: a canfly product with the flight
// constants the §10.1 integrator needs. Aircraft are not classified against the
// ground lattice, but the factory's own state-2 exit test still validates the
// product's footprint per cell against its definition [04 R-FAC-02 §5], so the
// movement class is kept.
func exitAircraftDef(name string, fx, fz int32) *content.UnitDef {
	d := exitMobileDef(name, fx, fz)
	d.CanFly = true
	d.CanMove = true
	d.CruiseAlt = 60
	d.MaxVelocity = 4 * 65536
	d.Acceleration = 65536 / 4
	d.BrakeRate = 65536 / 8
	d.TurnRate = 500
	return d
}

// TestAircraftProductTakesOffAndFreesTheYard is the air half of the egress
// contract the ground tests above lock. [04 R-AIR-02] composes it from two
// direct traces: `Park` re-identifies a canfly product's record as `VTOL_Move`
// with the product's own position as goal [04 R-FAC-02 §4], whose phase 0 is the
// shared takeoff preamble — mode 1 → 2 plus an initial climb marker at
// `cruisealt / 2` [04 R-AIR-01 §6] — and the mode write is what moves the
// aircraft's occupancy off the ground plane [04 R-COLL-01 §4], which is the
// event [04 R-FAC-02 §6] names for the exit clearing and [04 R-FAC-02 §5] for
// the yard closing.
//
// Before this test the product reached mode 1 and stopped there: nothing in the
// sim ran the preamble, so a finished aircraft sat on its pad forever, holding
// the exit cells and pinning the doors open.
func TestAircraftProductTakesOffAndFreesTheYard(t *testing.T) {
	plant := newFactoryDef("airplant", 4, 4, 300)
	plant.YardMap = "yccy yccy yccy yccy" // 'c' pad lane: stamped only while closed
	air := exitAircraftDef("airprod", 1, 1)
	air.BuildTime = 1
	air.BuildCostEnergy = 1
	air.BuildCostMetal = 1
	cat := exitCatalog(plant, air)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	sys := movement.NewSystem(terrain, movement.Profile{}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	svc.Movement = sys
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}

	fh, err := w.Create(plant, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create plant: %v", err)
	}
	factory := w.Unit(fh)
	bindConstructionFixture(factory, trivialModel(1, [][3]int64{{0, 0, 0}}), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("plant yard refused to open")
	}
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: air.CanonicalKey, Param1: prodIdx(cat, air.CanonicalKey), Param2: 1, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].Phase = uint8(State2)

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	var product pool.Handle
	for i := 1; i <= 60 && product == 0; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, uint32(i), w, func() {})
		if res := svc.StepUnit(ctx, fh); res.Completed && res.Product != 0 {
			product = res.Product
		}
	}
	if product == 0 {
		t.Fatal("plant never completed an aircraft")
	}
	// The session's completion hook: the finished product joins the movement
	// system, which stamps its footprint on the ground plane at mode 1.
	sys.EnsureUnit(w.Unit(product))
	prod := w.Unit(product)
	if prod.Move.Mode != 1 {
		t.Fatalf("completed aircraft mover mode=%d, want the grounded 1 the detach leaves [04 R-FAC-02 §1]", prod.Move.Mode)
	}
	padY := prod.Y
	// The grounded aircraft holds its exit cells, so the doors cannot close.
	if svc.YardOpenTransaction(factory, false) {
		t.Fatal("yard closed over a grounded aircraft still standing on its pad [04 R-FAC-02 §5]")
	}

	// No rally: GetBuilt appends Park, whose canfly arm restarts the record as
	// VTOL_Move at the product's own position [04 R-FAC-02 §4].
	svc.rallyInheritance(factory, prod, 0)
	pq := orders.QueueForUnit(prod)
	if pq == nil || pq.LenPrimary() == 0 {
		t.Fatal("product queue is empty after rally inheritance")
	}

	wantClimb := movement.CruiseAltitudeForCarrier(terrain, prod.X, prod.Z, prod, true)
	sawMode2 := false
	for tick := uint32(61); tick <= 400; tick++ {
		sys.Scheduler.Tick(tick)
		pq.Pump(prod, tick)
		if head := headNodeForTest(prod); head != nil {
			switch orders.DescriptorFor(head.ID).Name {
			case "VTOL_Move", "Park":
				sys.ActivateMove(prod, head)
			}
		}
		sys.BeginTick(tick)
		sys.StepUnit(product, tick)
		sys.EndTick(tick)
		if prod.Move.Mode == 2 {
			sawMode2 = true
		}
		if sawMode2 && movement.AltitudesEqual(prod.Y, wantClimb) {
			break
		}
	}
	if !sawMode2 {
		t.Fatalf("aircraft never reached active locomotion; mover mode=%d [04 R-AIR-01 §6 step 4]", prod.Move.Mode)
	}
	if prod.Move.Mode != 2 {
		t.Fatalf("aircraft mover mode=%d after takeoff, want 2 [04 R-AIR-01 §3]", prod.Move.Mode)
	}
	if prod.Y <= padY {
		t.Fatalf("aircraft did not climb: Y=%d, pad Y=%d [04 R-AIR-02]", prod.Y, padY)
	}
	if !movement.AltitudesEqual(prod.Y, wantClimb) {
		t.Fatalf("aircraft did not reach the initial climb marker: Y=%d want %d within one world unit [04 R-AIR-01 §4][04 R-AIR-01 §6]", prod.Y, wantClimb)
	}
	// The mode write moved the stamp off the ground plane, so nothing of the
	// aircraft is left on the exit and the doors close [04 R-COLL-01 §4]
	// [04 R-FAC-02 §5][04 R-FAC-02 §6].
	rect, ok := svc.PlacementForProduct(fh)
	if !ok {
		t.Fatal("no placement record for the plant")
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			if id, occupied := sys.Grid.OccupantAt(movement.Cell{X: x, Z: z}); occupied && id == int(product) {
				t.Fatalf("airborne aircraft still holds ground cell (%d,%d) [04 R-COLL-01 §4]", x, z)
			}
		}
	}
	if !svc.YardOpenTransaction(factory, false) {
		t.Fatal("yard refused to close after the aircraft took off [04 R-FAC-02 §5]")
	}
	if factory.YardOpen {
		t.Fatal("accepted close did not commit the bit")
	}
}

// TestFourGroundProductsEachLeaveTheYard settles, empirically, the second half
// of the playtest report "units pile up at factory exit instead of making room
// for newly produced units to exit the yard". [04 R-COB-05] establishes that
// the mechanism the report names — COB port 19 — has no engine reader, so if a
// stall were real it would have another cause. This drives one factory's
// counted run of four ground products end to end through the real order pump,
// the real GetBuilt/Park insertion and the real movement follower.
//
// What must hold, and does: production never deadlocks. Every product of the
// run completes, is released, walks clear of the plant's footprint, and stands
// on a cell of its own — the exit test rejects any non-zero ground word with
// self identity 0, so retail cannot stack two products [04 R-FAC-02 §6] — and
// the counted node drops so the doors close behind the last one
// [04 R-FAC-02 §5].
//
// What is NOT asserted, because research says it is retail: the products end up
// queued in a column rather than fanned out. Every no-rally product of one
// factory receives a `Park` rectangle anchored on the same plate, so the
// rectangles are identical [04 R-FAC-02 §4]; the rectangle class's goal-point
// query is the constant middle column of the FAR Z edge, not a per-mover point
// [04 R-MOV-03 §2]; and route acceptance rewrites any route of fewer than three
// points into a straight line at that goal point [04 R-PATH-01 §8]. The first
// product parks on that cell and the rest close up behind it. Retail has no
// push, no stacking and no force-placement [04 R-FAC-02 §6], and no engine
// scatter [04 R-COB-05]: the queue is the behavior, and a rally point is what
// disperses it. [04 R-EGRESS-01] composes that chain; the far-edge goal point
// itself is locked by TestGroundRouteAcceptanceGates in the movement package.
func TestFourGroundProductsEachLeaveTheYard(t *testing.T) {
	const productCount = 4
	lab := newFactoryDef("queuelab", 4, 4, 300)
	lab.YardMap = "yccy yccy yccy yccy" // 'c' pad lane: released while the yard is open
	prodDef := exitMobileDef("queueprod", 1, 1)
	prodDef.BuildTime = 1
	prodDef.BuildCostEnergy = 1
	prodDef.BuildCostMetal = 1
	prodDef.MaxVelocity = 2 * 65536
	prodDef.Acceleration = 65536 / 2
	prodDef.BrakeRate = 65536 / 2
	prodDef.TurnRate = 1000
	cat := exitCatalog(lab, prodDef)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	terrain := exitTerrain(32, 32)
	svc, w := exitService(t, terrain, cat)
	sim := rng.NewSimulation(4242)
	w.SetSimulationRNG(&sim)
	sys := movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	svc.Movement = sys
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}

	fh, err := w.Create(lab, 0, world.CellToWorld(12), 0, world.CellToWorld(12))
	if err != nil {
		t.Fatalf("create lab: %v", err)
	}
	factory := w.Unit(fh)
	bindConstructionFixture(factory, trivialModel(1, [][3]int64{{0, 0, 0}}), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("lab yard refused to open")
	}
	rect, ok := svc.PlacementForProduct(fh)
	if !ok {
		t.Fatal("no placement record for the lab")
	}
	q := orders.QueueForUnit(factory)
	// This fixture builds the queue directly rather than through the service's
	// own admission point, so it must make the registration the session makes
	// for it: the build rows are advanced by StepUnit, not by the pump.
	svc.RegisterOrderHandlers(q)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param1: prodIdx(cat, prodDef.CanonicalKey), Param2: productCount, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].Phase = uint8(State2)

	// The session's per-unit visit, reduced to the three services this contract
	// needs: the order pump, the movement activation boundary, and the two
	// per-unit steps [04 §3.3][04 §8.1].
	ordersPump := &orders.Pump{World: w}
	completed := []pool.Handle{}
	seen := map[pool.Handle]bool{}
	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	const maxTicks = 4000
	lastCompletion := uint32(0)
	for tick := uint32(1); tick <= maxTicks; tick++ {
		ctx.Tick = tick
		svc.Economy.TickPlayer(0, tick, w, func() {})
		sys.Scheduler.Tick(tick)
		sys.BeginTick(tick)
		for _, u := range w.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			h := u.Handle
			ordersPump.PumpUnit(h, tick)
			if head := headNodeForTest(u); head != nil {
				switch orders.DescriptorFor(head.ID).Name {
				case "Move_Ground", "QMove", "Park", "VTOL_Move":
					sys.ActivateMove(u, head)
				}
			}
			if res := svc.StepUnit(ctx, h); res.Completed && res.Product != 0 && !seen[res.Product] {
				seen[res.Product] = true
				completed = append(completed, res.Product)
				lastCompletion = tick
				// The session's completion hook: the finished product joins the
				// movement system, stamping its footprint on the ground plane.
				sys.EnsureUnit(w.Unit(res.Product))
			}
			sys.StepUnit(h, tick)
		}
		sys.EndTick(tick)
		if len(completed) == productCount && allClearOfRect(w, completed, rect) {
			break
		}
	}

	if len(completed) != productCount {
		var detail []string
		for _, h := range completed {
			u := w.Unit(h)
			detail = append(detail, fmt.Sprintf("%d at cell (%d,%d)", h, world.WorldToCell(u.X), world.WorldToCell(u.Z)))
		}
		node := headNodeForTest(factory)
		nodeTxt := "queue-empty"
		if node != nil {
			nodeTxt = fmt.Sprintf("%s phase=%d deadline=%d", orders.DescriptorFor(node.ID).Name, node.Phase, node.Deadline)
		}
		t.Fatalf("only %d of %d products completed (last at tick %d); factory node %s; completed=[%s] — the counted run stalled [04 R-FAC-02 §6]",
			len(completed), productCount, lastCompletion, nodeTxt, strings.Join(detail, ", "))
	}
	for i, h := range completed {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			t.Fatalf("product %d (#%d) is not alive after the run", h, i)
		}
		cx, cz := world.WorldToCell(u.X), world.WorldToCell(u.Z)
		if cx >= rect.MinX() && cx < rect.MaxX() && cz >= rect.MinZ() && cz < rect.MaxZ() {
			t.Fatalf("product %d (#%d) never left the plant footprint: cell (%d,%d) inside [%d,%d)x[%d,%d) [04 R-FAC-02 §4]",
				h, i, cx, cz, rect.MinX(), rect.MaxX(), rect.MinZ(), rect.MaxZ())
		}
	}
	// Two products must never end on the same cell: the exit test rejects any
	// non-zero ground word with self identity 0, so retail cannot stack them
	// [04 R-FAC-02 §6].
	occupied := map[[2]int32]pool.Handle{}
	for _, h := range completed {
		u := w.Unit(h)
		key := [2]int32{world.WorldToCell(u.X), world.WorldToCell(u.Z)}
		if other, clash := occupied[key]; clash {
			t.Fatalf("products %d and %d both stand on cell (%d,%d) [04 R-FAC-02 §6]", other, h, key[0], key[1])
		}
		occupied[key] = h
	}
	// The counted run is exhausted, so the node is dropped and the doors close
	// behind the last product [05 "Factory production lifecycle"][04 R-FAC-02 §5].
	if node := headNodeForTest(factory); node != nil && orders.DescriptorFor(node.ID).Name == "BuildingBuild" {
		t.Fatalf("factory still holds a BuildingBuild node after %d completions: phase=%d deadline=%d", productCount, node.Phase, node.Deadline)
	}
	if !svc.YardOpenTransaction(factory, false) {
		t.Fatal("yard refused to close after every product left the pad [04 R-FAC-02 §5]")
	}
}

// allClearOfRect reports whether every named unit stands outside rect.
func allClearOfRect(w *units.World, handles []pool.Handle, rect world.FootprintRect) bool {
	for _, h := range handles {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			return false
		}
		cx, cz := world.WorldToCell(u.X), world.WorldToCell(u.Z)
		if cx >= rect.MinX() && cx < rect.MaxX() && cz >= rect.MinZ() && cz < rect.MaxZ() {
			return false
		}
	}
	return true
}
