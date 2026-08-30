package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func constructionOrderGateFixture(t *testing.T) (*Manager, *units.World, *units.Unit, *economy.Service, *rng.Simulation, *int) {
	t.Helper()
	builderDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gate-builder"},
		UnitName:         "gate-builder",
		Side:             "ARM",
		Builder:          true,
		BMCode:           true,
		CanMove:          true,
		MaxDamage:        100,
	}
	productDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gate-product"},
		UnitName:         "gate-product",
		Side:             "ARM",
		FootprintX:       2,
		FootprintZ:       2,
		YardMap:          "o",
		MaxDamage:        100,
		MaxSlope:         255,
		MaxWaterSlope:    255,
		MaxWaterDepth:    10000,
		MinWaterDepth:    -10000,
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			builderDef.CanonicalKey: builderDef,
			productDef.CanonicalKey: productDef,
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			builderDef.CanonicalKey: {Buttons: []string{productDef.CanonicalKey}},
		},
	}
	w := newAIFixtureWorld(4, cat)
	h, err := w.Create(builderDef, 1, world.CellToWorld(32), 0, world.CellToWorld(32))
	if err != nil {
		t.Fatal(err)
	}
	builder := w.Unit(h)
	builder.Remaining = 0
	terrain := placementTerrain(64, 64, 0)
	sim := rng.NewSimulation(7)
	submissions := 0
	m := &Manager{
		Player:            1,
		Profile:           &Profile{Weight: map[string]int32{productDef.CanonicalKey: 100}, Limit: map[string]int32{}},
		Catalog:           cat,
		Terrain:           terrain,
		RNG:               &sim,
		GroupConstruction: []pool.Handle{h},
		QueueBuildTyped: func(BuildRequest) error {
			submissions++
			return nil
		},
	}
	m.Strategic = Strategic{
		CenterX:         builder.X,
		CenterZ:         builder.Z,
		Radius:          37,
		Counts:          map[string]int32{productDef.CanonicalKey: 0},
		ClassVectors:    map[string]ClassVector{productDef.CanonicalKey: {C0: 100}},
		Catalog:         cat,
		LandRegion:      PlacementRegion{CellW: 20, CellH: 20},
		WaterRegion:     PlacementRegion{CellW: 20, CellH: 20},
		setupDrawsReady: true,
	}
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	return m, w, builder, econ, &sim, &submissions
}

func TestConstructionCurrentOrderStaticBitSuppressesBeforeSelection(t *testing.T) {
	m, w, builder, econ, sim, submissions := constructionOrderGateFixture(t)
	q := orders.QueueForUnit(builder)
	// An otherwise inert identity with only static bit 3 proves that pass 1
	// reads the current record mask itself [08 R-AI-01 §3].
	q.Push(orders.Lookup("Park"), orders.Node{StaticGate: 0x8})
	beforeState, beforeDraws, beforeRadius := sim.State, sim.Draws(), m.Strategic.Radius

	m.doConstruction(90, w, econ)

	if sim.State != beforeState || sim.Draws() != beforeDraws {
		t.Fatalf("suppressed builder advanced selection RNG: state=%d/%d draws=%d/%d", sim.State, beforeState, sim.Draws(), beforeDraws)
	}
	if m.Strategic.Radius != beforeRadius {
		t.Fatalf("suppressed builder changed placement radius = %d, want %d", m.Strategic.Radius, beforeRadius)
	}
	if *submissions != 0 {
		t.Fatalf("suppressed builder submitted %d construction requests", *submissions)
	}
}

func TestConstructionCurrentOrderWithoutStaticBitRemainsEligible(t *testing.T) {
	m, w, builder, econ, sim, submissions := constructionOrderGateFixture(t)
	q := orders.QueueForUnit(builder)
	// Dynamic bit 3 is not the static-mask gate [08 R-AI-01 §3].
	q.Push(orders.Lookup("Park"), orders.Node{DynamicGate: 0x8})

	m.doConstruction(90, w, econ)

	if sim.Draws() == 0 {
		t.Fatal("eligible builder did not reach selection and placement RNG")
	}
	if m.Strategic.Radius != 0 {
		t.Fatalf("eligible placement radius = %d, want success reset 0", m.Strategic.Radius)
	}
	if *submissions != 1 {
		t.Fatalf("eligible builder submitted %d construction requests, want 1", *submissions)
	}
}

func TestConstructionWithoutCurrentOrderRemainsEligible(t *testing.T) {
	m, w, builder, econ, sim, submissions := constructionOrderGateFixture(t)
	if orders.QueueOfUnit(builder) != nil {
		t.Fatal("fixture unexpectedly materialized an idle order queue")
	}

	m.doConstruction(90, w, econ)

	if sim.Draws() == 0 || m.Strategic.Radius != 0 || *submissions != 1 {
		t.Fatalf("idle builder was not eligible: draws=%d radius=%d submissions=%d", sim.Draws(), m.Strategic.Radius, *submissions)
	}
}

func TestConstructionMobileBuildMaskSuppressesSelection(t *testing.T) {
	m, w, builder, econ, sim, submissions := constructionOrderGateFixture(t)
	q := orders.QueueForUnit(builder)
	q.Push(orders.Lookup("MobileBuild"), orders.Node{})
	current := q.Head()
	if current == nil {
		t.Fatal("MobileBuild fixture has no current primary order")
	}
	// Canonical descriptor mask [04 "Order descriptor table"].
	if current.StaticGate != 0x100508 {
		t.Fatalf("MobileBuild static mask = %#x, want 0x100508", current.StaticGate)
	}
	beforeState, beforeDraws, beforeRadius := sim.State, sim.Draws(), m.Strategic.Radius

	m.doConstruction(90, w, econ)

	if sim.State != beforeState || sim.Draws() != beforeDraws || m.Strategic.Radius != beforeRadius || *submissions != 0 {
		t.Fatalf("MobileBuild current order did not suppress pass 1: state=%d/%d draws=%d/%d radius=%d/%d submissions=%d", sim.State, beforeState, sim.Draws(), beforeDraws, m.Strategic.Radius, beforeRadius, *submissions)
	}
}
