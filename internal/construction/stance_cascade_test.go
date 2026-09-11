package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestStandingCopyHelperPropagatesCurrentStance exercises the factory copy
// helper and the subsequent BuildingBuild pipeline. Its first copy is invoked
// manually, so it does not establish that MobileBuild calls that helper.
//
// [04 R-STANCE-01 §6] establishes the mechanism: both fields parse with a
// default of 2, a definition's own creation seeds them straight from its
// definition, and a factory PRODUCT's fields are instead overwritten from the
// builder's current fields at both the state-2 epilogue and the later
// guarded `GetBuilt` copy [04 §3.8][R-P0-09] — the product's own authored
// value is overwritten on that factory-production path.
//
// The asset half of the "why" is not yet in that doc's stock census (which
// aggregates across all 284 files, not per-unit): a direct read of the
// mounted retail install in this session shows ARM's and CORE's commander
// definitions author `standingmoveorder=0` (hold position) while their
// factory definitions author `standingmoveorder=1` (maneuver, the stock
// mobile-definition default the census does report). A commander is not
// itself a factory product, so its own creation seeds hold position straight
// from its definition. The manual copy below models only the helper: retail's
// ground and aircraft MobileBuild paths do not call it [04 R-STANCE-01 §6].
// TestMobileBuiltFactoryKeepsAuthoredStandingOrders covers that distinction
// through placement and completion rather than assuming the call exists.
func TestStandingCopyHelperPropagatesCurrentStance(t *testing.T) {
	commanderDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")},
		UnitName:         "armcom",
		FootprintX:       2, FootprintZ: 2,
		MaxDamage: 100,
		BMCode:    1,
		Builder:   true,
		CanMove:   true,
		// Authored values traced from the retail asset census: the commander
		// starts hold position / fire at will [04 R-STANCE-01 §6].
		StandingMoveOrder: 0,
		StandingFireOrder: 2,
	}
	facDef := newFactoryDef("armlab", 2, 2, 100)
	// Authored value traced from the retail asset census: a stock factory
	// definition authors maneuver / fire at will, same as any other stock
	// mobile-adjacent definition [04 R-STANCE-01 §6].
	facDef.StandingMoveOrder = 1
	facDef.StandingFireOrder = 2
	prodDef := newProductDef("armflash", 2, 2, 100, 50)
	prodDef.MinWaterDepth = -10000 // established land-profile template [04 §6.1]
	prodDef.StandingMoveOrder = 1
	prodDef.StandingFireOrder = 2

	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		commanderDef.CanonicalKey: commanderDef,
		facDef.CanonicalKey:       facDef,
		prodDef.CanonicalKey:      prodDef,
	}}
	w := newConstructionFixtureWorld(10, cat)

	ch, _ := w.Create(commanderDef, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	commander := w.Unit(ch)
	commander.Def = commanderDef
	if got := (commander.Flags & StandingMoveMask) >> 18; got != 0 {
		t.Fatalf("commander's own creation: move = %d, want 0 (hold position, [04 R-STANCE-01 §6])", got)
	}

	// The factory, seeded from its OWN definition, would start maneuver.
	fh, _ := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	factory := w.Unit(fh)
	factory.Def = facDef
	if got := (factory.Flags & StandingMoveMask) >> 18; got != 1 {
		t.Fatalf("factory's own creation (pre-inheritance): move = %d, want 1 (maneuver, its own authored default)", got)
	}

	// Synthetic first hop: invoke the factory helper directly. This is not
	// the mobile placement path [04 R-STANCE-01 §6].
	copyStandingFlags(commander, factory)
	if got := (factory.Flags & StandingMoveMask) >> 18; got != 0 {
		t.Fatalf("factory after direct helper copy: move = %d, want source value 0", got)
	}
	if got := (factory.Flags & StandingFireMask) >> 20; got != 2 {
		t.Fatalf("factory after direct helper copy: fire = %d, want source value 2", got)
	}

	// Hop 2: drive the real factory production pipeline so the product's
	// stance comes from the actual BuildingBuild/allocation code path, not a
	// second manual copyStandingFlags call.
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	q := orders.QueueForUnit(factory)
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	q.Push(bid, orders.Node{BuildDefKey: "armflash", Param1: prodIdx(nil, "armflash"), Param2: 1, Phase: uint8(State2)})
	head := q.Primary()[0]
	head.Phase = uint8(State2)

	svc := NewService(exitTerrain(12, 12), cat, w, &economy.Service{})
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	svc.Pump(factory, 100)
	if head.Target == 0 {
		t.Fatalf("allocation failed; messages=%v phase=%d deadline=%d", svc.Messages(), head.Phase, head.Deadline)
	}
	prod := w.Unit(head.Target)
	if prod == nil {
		t.Fatalf("product nil")
	}

	gotMove := (prod.Flags & StandingMoveMask) >> 18
	gotFire := (prod.Flags & StandingFireMask) >> 20
	if gotMove != 0 || gotFire != 2 {
		t.Fatalf("fresh product stance move/fire = %d/%d, want 0/2 (hold position / fire at will, cascaded from the commander through the freshly built factory) — its own definition authors maneuver (1)", gotMove, gotFire)
	}
}
