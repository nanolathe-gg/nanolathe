package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestSessionUsesOnePathScheduler verifies that path requests have one
// canonical scheduler [04 §7.3].
func TestSessionUsesOnePathScheduler(t *testing.T) {
	// Minimal synthetic catalog and terrain for composition helper
	cat := &content.Catalog{
		Units:    map[string]*content.UnitDef{"armflea": {UnitName: "armflea", MaxDamage: 100, FootprintX: 1, FootprintZ: 1, MaxVelocity: 65536, TurnRate: 100}},
		Sides:    []*content.SideDef{{Commander: "armflea"}},
		Maps:     map[string]*content.MapHeader{},
		Movement: map[string]*content.MovementClass{},
		Features: map[string]*content.FeatureDef{},
	}
	terrain := &world.Terrain{
		CellW: 20, CellH: 20, SeaLevel: 0,
		Plot: make([]world.PlotCell, 400),
	}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
	}
	terrain.ApplySchema(nil, 0)
	w, _ := newSlicedWorld(cat)
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Units:   w,
		Mission: &mission.Mission{Type: mission.TypeSkirmish, TerrainKey: "test", Schema: mission.Schema{Name: "test"}},
	}
	// Ensure wind and services via helper
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.Path != s.Movement.Scheduler {
		t.Fatalf("Path alias broken: Path %p != Movement.Scheduler %p", s.Path, s.Movement.Scheduler)
	}
	if s.Path == nil || s.Movement.Scheduler == nil {
		t.Fatalf("scheduler nil")
	}
	if err := s.ValidateComposition(); err != nil {
		// Validation checks alias equality before the first tick.
		t.Fatalf("validate: %v", err)
	}
}
