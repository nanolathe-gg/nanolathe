package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestP28FactoryProductKeepsSampledHeadingAndAxisAlignedFootprint(t *testing.T) {
	sim := rng.NewSimulation(67)
	expected := rng.NewSimulation(67)
	unitWorld := units.NewSliced(4, nil)
	unitWorld.SetSimulationRNG(&sim)
	factoryDef := &content.UnitDef{UnitName: "factory", BuildAngle: 0, MaxDamage: 100, Limit: -1}
	factoryHandle, err := unitWorld.Create(factoryDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	// A zero-span factory skips the bounded invocation and still consumes the
	// common initializer's full-domain draw.
	_ = expected.Uint32n(0x10000)

	terrain := &world.Terrain{CellW: 12, CellH: 12, Plot: make([]world.PlotCell, 12*12)}
	service := NewService(terrain, nil, unitWorld, nil)
	productDef := &content.UnitDef{
		UnitName: "product", BuildAngle: 4096, MaxDamage: 200, Limit: -1,
		FootprintX: 3, FootprintZ: 2, YardMap: "oooooo",
	}
	extent, err := world.NewFootprintExtent(3, 2)
	if err != nil {
		t.Fatalf("extent: %v", err)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(4, 5), extent)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	before := sim.Draws()
	product, err := service.allocateNanoframe(unitWorld.Unit(factoryHandle), productDef, rect,
		world.NewModelWorldPosition(numeric.FixedFromInt(5), 0, numeric.FixedFromInt(6)))
	if err != nil {
		t.Fatalf("allocateNanoframe: %v", err)
	}
	draw := expected.Uint32n(4096)
	wantHeading := uint16(int32(int16(uint16(draw))) - int32(uint16(4096)>>1) + 32768)
	_ = expected.Uint32n(0x10000)
	if product.Move.Heading != wantHeading {
		t.Fatalf("factory product heading = %d, want sampled %d", product.Move.Heading, wantHeading)
	}
	if sim.State != expected.State || sim.Draws()-before != 2 {
		t.Fatalf("product stream = state %d delta %d, want state %d delta 2", sim.State, sim.Draws()-before, expected.State)
	}
	stored, ok := service.PlacementForProduct(product.Handle)
	if !ok || stored.MinX() != 4 || stored.MinZ() != 5 || stored.Width() != 3 || stored.Depth() != 2 {
		t.Fatalf("heading changed axis-aligned footprint: %+v, present=%t", stored, ok)
	}
	for z := int32(5); z < 7; z++ {
		for x := int32(4); x < 7; x++ {
			if got := terrain.PlotAt(x, z).OccupantA(); got != int16(product.Handle) {
				t.Fatalf("occupancy %d,%d = %d, want product %d", x, z, got, product.Handle)
			}
		}
	}
}

func TestP28FactoryState2FailuresDoNotAdvanceAngleStream(t *testing.T) {
	setup := func(t *testing.T, seed uint32) (*Service, *units.Unit, *orders.Node, *rng.Simulation, *world.Terrain) {
		t.Helper()
		sim := rng.NewSimulation(seed)
		unitWorld := units.NewSliced(4, nil)
		unitWorld.SetSimulationRNG(&sim)
		factoryDef := newFactoryDef("factory", 2, 2, 300)
		factoryDef.BuildAngle = 0
		factoryDef.YardMap = "oooo"
		productDef := newProductDef("product", 2, 2, 100, 100)
		productDef.BuildAngle = 4096
		productDef.YardMap = "oooo"
		catalog := &content.Catalog{Units: map[string]*content.UnitDef{
			content.CanonicalKey(factoryDef.UnitName): factoryDef,
			content.CanonicalKey(productDef.UnitName): productDef,
		}}
		h, err := unitWorld.Create(factoryDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
		if err != nil {
			t.Fatalf("create factory: %v", err)
		}
		factory := unitWorld.Unit(h)
		bindConstructionFixture(factory, trivialModel(1, nil), true)
		q := orders.QueueForUnit(factory)
		buildID := orders.Lookup("BuildingBuild")
		q.Push(buildID, orders.Node{BuildDefKey: "product", Param2: 1, Phase: uint8(State2), Deadline: -1})
		node := q.Primary()[0]
		terrain := &world.Terrain{CellW: 12, CellH: 12, Plot: make([]world.PlotCell, 12*12)}
		for i := range terrain.Plot {
			terrain.Plot[i].SetFeature(world.PlotFeatureNone)
			terrain.Plot[i].SetOccupied(false)
		}
		return NewService(terrain, catalog, unitWorld, &economy.Service{}), factory, node, &sim, terrain
	}

	t.Run("blocked placement", func(t *testing.T) {
		svc, factory, node, sim, terrain := setup(t, 71)
		terrain.Plot[4*12+4].SetOccupantA(9)
		beforeState, beforeDraws := sim.State, sim.Draws()
		svc.Pump(factory, 10)
		if node.Phase != uint8(State2) || node.Deadline != 25 {
			t.Fatalf("blocked state = phase %d deadline %d, want state2/25", node.Phase, node.Deadline)
		}
		if sim.State != beforeState || sim.Draws() != beforeDraws {
			t.Fatalf("blocked placement advanced stream: (%d,%d) -> (%d,%d)", beforeState, beforeDraws, sim.State, sim.Draws())
		}
	})

	t.Run("allocator refusal", func(t *testing.T) {
		svc, factory, node, sim, _ := setup(t, 73)
		svc.LimitChecker = func(*units.Unit, string) bool { return false }
		beforeState, beforeDraws := sim.State, sim.Draws()
		svc.Pump(factory, 20)
		if node.Phase != uint8(State2) || node.Deadline != 320 {
			t.Fatalf("refusal state = phase %d deadline %d, want state2/320", node.Phase, node.Deadline)
		}
		if sim.State != beforeState || sim.Draws() != beforeDraws {
			t.Fatalf("allocator refusal advanced stream: (%d,%d) -> (%d,%d)", beforeState, beforeDraws, sim.State, sim.Draws())
		}
	})
}
