// Factory liveness contracts: a factory whose exit is obstructed must be able
// to free itself again, and the obstruction must not be an object the engine
// created and then forgot how to remove [04 R-FAC-02 §6]
// [05 "Reverse and deconstruction"].
package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestDecayClampKillsTheNanoframe locks the reverse arm's own terminator:
// "If the remaining fraction is clamped to one, the unit kills itself with a
// kind-9 30000 packet — the no-corpse, no-explosion path (severity zero)"
// [05 "Reverse and deconstruction"].
//
// Before this contract the clamp result was discarded, so the frame settled at
// remaining 1.0 and zero health and stayed alive for the rest of the battle.
func TestDecayClampKillsTheNanoframe(t *testing.T) {
	def := newProductDef("decayprod", 1, 1, 40, 100)
	// worker = -(11*buildtime/buildcostenergy) = -100, i.e. a whole fraction
	// per decay visit, so one expired period clamps.
	def.BuildCostEnergy = 11
	cat := exitCatalog(def)
	terrain := exitTerrain(16, 16)
	svc, w := exitService(t, terrain, cat)

	h, err := w.Create(def, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create frame: %v", err)
	}
	frame := w.Unit(h)
	frame.Remaining = 0.5
	frame.MaxHealth = 100
	frame.Health = 50
	q := svc.queueForUnit(frame)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2)})
	node := q.Primary()[0]

	if code := svc.handleGetBuiltOrder(frame, node, 40); code != 2 {
		t.Fatalf("decay visit code=%d, want the ordinary hold", code)
	}
	if frame.Remaining != 1 {
		t.Fatalf("decay left remaining=%v, want the clamp at 1", frame.Remaining)
	}
	if frame.Alive || w.Unit(h) != nil {
		t.Fatalf("clamped frame is still alive [05 \"Reverse and deconstruction\"]")
	}
	kill := svc.LastKill()
	if kill.Damage != Kind9Damage || kill.Severity != 0 || !kill.NoCorpse {
		t.Fatalf("kill packet %+v, want kind-9 %d damage, severity 0, no corpse [05 C21]", kill, Kind9Damage)
	}
	if _, held := svc.PlacementForProduct(h); held {
		t.Fatalf("the killed frame still holds its footprint reservation")
	}
}

// TestAbandonedFrameOnTheExitStopsBlockingTheFactory is the liveness property
// the playtest defect names: a factory whose exit spot is obstructed must free
// itself again rather than sit in the silent 15-tick retry forever.
//
// The obstruction here is the one the engine itself creates and then has to
// remove: a nanoframe whose builder went away. Retail's frame decays and, on
// the clamp, kills itself [05 "Reverse and deconstruction"]; the exit test then
// admits the next product [04 R-FAC-02 §6]. With the clamp result discarded the
// frame stayed alive at zero health on the exit cells and the factory never
// reached state 3 again — no progress and, because state 3 is where the demand
// is raised, no resource consumption either.
func TestAbandonedFrameOnTheExitStopsBlockingTheFactory(t *testing.T) {
	lab := newFactoryDef("stalelab", 4, 4, 300)
	lab.YardMap = "yccy yccy yccy yccy"
	prodDef := exitMobileDef("staleprod", 1, 1)
	prodDef.BuildTime = 100
	prodDef.BuildCostEnergy = 11 // one whole fraction per decay visit
	prodDef.BuildCostMetal = 1
	prodDef.MaxVelocity = 2 * 65536
	prodDef.Acceleration = 65536 / 2
	prodDef.BrakeRate = 65536 / 2
	prodDef.TurnRate = 1000
	cat := exitCatalog(lab, prodDef)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	terrain := exitTerrain(32, 32)
	svc, w := exitService(t, terrain, cat)
	sim := rng.NewSimulation(99)
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

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param1: prodIdx(cat, prodDef.CanonicalKey), Param2: 1, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].Phase = uint8(State2)

	// One admission, then abandon the record while the frame is still partly
	// built. This is the state a factory killed mid-product leaves behind.
	var abandoned pool.Handle
	tick := uint32(0)
	for i := 0; i < 20 && abandoned == 0; i++ {
		tick++
		ctx.Tick = tick
		svc.Economy.TickPlayer(0, tick, w, func() {})
		svc.StepUnit(ctx, fh)
		if head := headNodeForTest(factory); head != nil && head.Target != 0 {
			abandoned = head.Target
		}
	}
	if abandoned == 0 {
		t.Fatal("the lab never allocated its first frame")
	}
	if w.Unit(abandoned).Remaining == 0 {
		t.Fatal("the fixture completed the frame instead of leaving it partly built")
	}
	q.SetPrimary(nil)

	// The exit is now held by a frame with no builder. Queue a fresh product
	// and drive the ordinary per-unit visit for every live unit.
	if err := QueueFactoryBuild(factory, prodDef.CanonicalKey, 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	ordersPump := &orders.Pump{World: w}
	var produced pool.Handle
	const maxTicks = 2000
	for i := 0; i < maxTicks && produced == 0; i++ {
		tick++
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
			if res := svc.StepUnit(ctx, h); res.Completed && res.Product != 0 && res.Product != abandoned {
				produced = res.Product
				sys.EnsureUnit(w.Unit(res.Product))
			}
			sys.StepUnit(h, tick)
		}
		sys.EndTick(tick)
	}
	if produced == 0 {
		stale := w.Unit(abandoned)
		state := "gone"
		if stale != nil {
			state = fmt.Sprintf("alive rem=%.4f hp=%d at cell (%d,%d)", stale.Remaining, stale.Health,
				world.WorldToCell(stale.X), world.WorldToCell(stale.Z))
		}
		node := headNodeForTest(factory)
		nodeTxt := "queue-empty"
		if node != nil {
			nodeTxt = fmt.Sprintf("%s phase=%d deadline=%d gate=%d target=%d",
				orders.DescriptorFor(node.ID).Name, node.Phase, node.Deadline, node.DynamicGate, node.Target)
		}
		var adm []string
		diags := svc.AdmissionDiagnostics()
		if len(diags) > 3 {
			diags = diags[len(diags)-3:]
		}
		for _, d := range diags {
			adm = append(adm, fmt.Sprintf("%s@%d:%s", d.Status, d.Tick, d.Reason))
		}
		t.Fatalf("the lab never produced again in %d ticks: abandoned frame %s; node %s; admissions [%s] — a factory whose exit is obstructed must free itself [04 R-FAC-02 §6][05 \"Reverse and deconstruction\"]",
			maxTicks, state, nodeTxt, strings.Join(adm, " | "))
	}
	if w.Unit(abandoned) != nil {
		t.Fatal("the abandoned frame outlived its own decay clamp")
	}
	_ = units.DeathKilled
}

// TestFourAircraftProductsEachLeaveThePad is the air twin of
// TestFourGroundProductsEachLeaveTheYard: a counted run of four canfly products
// out of one plant, driven through the real order pump, the real GetBuilt/Park
// insertion and the real movement follower. An aircraft's release is its mode
// change to airborne, which moves its stamp off the ground plane and clears the
// exit for the next product [04 R-FAC-02 §6][04 R-COLL-01 §4], so a counted run
// of aircraft must never stall on its own predecessor.
func TestFourAircraftProductsEachLeaveThePad(t *testing.T) {
	const productCount = 4
	plant := newFactoryDef("airqueuelab", 4, 4, 300)
	plant.YardMap = "yccy yccy yccy yccy"
	prodDef := exitAircraftDef("airqueueprod", 1, 1)
	prodDef.BuildTime = 60
	prodDef.BuildCostEnergy = 60
	prodDef.BuildCostMetal = 60
	cat := exitCatalog(plant, prodDef)
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

	fh, err := w.Create(plant, 0, world.CellToWorld(12), 0, world.CellToWorld(12))
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
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param1: prodIdx(cat, prodDef.CanonicalKey), Param2: productCount, Phase: uint8(State2), Deadline: -1})
	q.Primary()[0].Phase = uint8(State2)

	ordersPump := &orders.Pump{World: w}
	completed := []pool.Handle{}
	seen := map[pool.Handle]bool{}
	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	const maxTicks = 4000
	lastCompletion := uint32(0)
	for tick := uint32(1); tick <= maxTicks && len(completed) < productCount; tick++ {
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
				sys.EnsureUnit(w.Unit(res.Product))
			}
			sys.StepUnit(h, tick)
		}
		sys.EndTick(tick)
	}

	if len(completed) != productCount {
		var detail []string
		for _, h := range completed {
			u := w.Unit(h)
			detail = append(detail, fmt.Sprintf("%d at cell (%d,%d) mode=%d", h, world.WorldToCell(u.X), world.WorldToCell(u.Z), u.Move.Mode))
		}
		node := headNodeForTest(factory)
		nodeTxt := "queue-empty"
		if node != nil {
			nodeTxt = fmt.Sprintf("%s phase=%d deadline=%d target=%d count=%d",
				orders.DescriptorFor(node.ID).Name, node.Phase, node.Deadline, node.Target, node.Param2)
		}
		t.Fatalf("only %d of %d aircraft completed (last at tick %d); factory node %s; completed=[%s] — the counted run stalled [04 R-FAC-02 §6]",
			len(completed), productCount, lastCompletion, nodeTxt, strings.Join(detail, ", "))
	}
	for _, h := range completed {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			t.Fatalf("aircraft %d is not alive after the run", h)
		}
	}
}
