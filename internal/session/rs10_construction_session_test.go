package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Ensure human and AI can construct via ordinary commands after save.
// This smoke check uses the authored mobile-build queue for both producers.
// The old AI factory assertion used an unbound fixture unit; strict factory
// production requires an authored COB QueryBuildInfo binding [04 §5.3][05
// C16], so that fixture could not validly exercise the factory path.
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
	w := newSessionFixtureWorld(20, cat)
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
	// AI ordinary command: a mobile build request (the same typed queue used
	// by the session AI binder).
	if err := construction.QueueMobileBuild(aiUnit, "armllt", world.CellToWorld(8), world.CellToWorld(8), 1, cat); err != nil {
		t.Fatalf("AI QueueMobileBuild: %v", err)
	}
	if orders.QueueForUnit(human).LenPrimary() != 1 || orders.QueueForUnit(aiUnit).LenPrimary() != 1 {
		t.Fatalf("ordinary commands not queued")
	}
	// Pump both, ensure they can produce
	for _, u := range []*units.Unit{human, aiUnit} {
		q := orders.QueueForUnit(u)
		q.Primary()[0].Phase = uint8(construction.State2)
	}
	svc.Pump(human, 0)
	svc.Pump(aiUnit, 0)
	if orders.QueueForUnit(human).Primary()[0].Target == 0 {
		t.Fatalf("human mobile build did not produce nanoframe via ordinary command")
	}
	if orders.QueueForUnit(aiUnit).Primary()[0].Target == 0 {
		t.Fatalf("AI mobile build did not produce nanoframe via ordinary command")
	}
}

func init() {
	_ = numeric.Fixed(0)
	_ = economy.Metal
}
