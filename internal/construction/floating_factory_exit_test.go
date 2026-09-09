// A water-sited factory's aircraft product must clear its exit despite the
// factory itself standing over deep water [04 §6.4]. This is the seaplane
// platform's (ARMPLAT/CORPLAT) situation: a stationary structure whose own
// authored yard sits on water cells, producing a `canfly` unit.
package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestFloatingFactoryAircraftProductClearsDeepWaterExit locks the aircraft
// domain's water-depth exemption at exactly the site a floating factory
// needs it: PlacementRulesForUnit resolves a `canfly` product's domain to
// MobilityAircraft, and CheckPlacement's `skipAggregates` gate is `true`
// whenever `q.Rules.Domain == content.MobilityAircraft` [04 §6.4] — so the
// slope/water-depth aggregate rejections never run for it, no matter how
// deep the water under the factory's exit is. Without that domain gate this
// factory could never complete a single aircraft product, because every
// exit-spot query samples terrain far below sea level.
//
// A per-def water depth of zero — the aircraft branch of
// PlacementRulesForUnit never populates MaxWaterDepth/MinWaterDepth — would
// otherwise read as "must not be placed in ANY water deeper than zero" if
// the aggregate gate merely fell back to a hostile default instead of being
// skipped outright; this test's terrain is chosen far enough below the
// authored sea level that a placement bug regressing that skip fails loudly
// rather than by coincidence.
func TestFloatingFactoryAircraftProductClearsDeepWaterExit(t *testing.T) {
	const productCount = 2
	plant := newFactoryDef("seaplat", 4, 4, 300)
	plant.YardMap = "yccy yccy yccy yccy"
	prodDef := exitAircraftDef("seaplaneprod", 1, 1)
	prodDef.BuildTime = 60
	prodDef.BuildCostEnergy = 60
	prodDef.BuildCostMetal = 60
	// No ground movement class at all: a real seaplane-platform product
	// (e.g. ARMCA) authors none [02 "Unit record"], and the aircraft branch
	// of PlacementRulesForUnit/validateFactoryProduct must not require one
	// [04 §6.4].
	prodDef.MovementClass = ""
	cat := exitCatalog(plant, prodDef)
	terrain := exitTerrain(32, 32)
	// Deep water: every plot cell sits far below the authored sea level, as a
	// stilted platform's surrounding water would. exitTerrain's default
	// SeaLevel is the struct zero value (0) with every cell at height 10,
	// i.e. dry land; raise the sea and drop the terrain here to model the
	// platform's actual site.
	terrain.SeaLevel = 200
	for i := range terrain.Plot {
		terrain.Plot[i].SetHeight(5)
		terrain.Plot[i].SetMinHeight(5)
		terrain.Plot[i].SetMaxHeight(5)
	}
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
	svc.RegisterOrderHandlers(q)
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
		t.Fatalf("only %d of %d seaplane-platform products completed over deep water (last at tick %d); factory node %s; completed=[%s]; messages=%v",
			len(completed), productCount, lastCompletion, nodeTxt, strings.Join(detail, ", "), svc.Messages())
	}
	for _, h := range completed {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			t.Fatalf("product %d is not alive after the run", h)
		}
	}
}
