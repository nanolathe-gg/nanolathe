package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// RX-01: an unbound QueueBuildTyped must be observable, never silent
// [F-P0-004]. Both the mobile-site placement path and the factory-queue path
// count their dropped requests in MissedQueueCallbacks.

func rx01Terrain() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	return &world.Terrain{CellW: 16, CellH: 16, Plot: world.ExpandPlot(attrs, 16, 16), Version: world.VersionCanonical}
}

func rx01Econ() *economy.Service {
	var econ economy.Service
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = 2
	econ.Players[0].StatusHalfwordAt144 = 1
	econ.Players[0].Stock[economy.Energy] = 800
	econ.Players[0].Stock[economy.Metal] = 400
	econ.Players[0].Capacity[economy.Energy] = 1000
	econ.Players[0].Capacity[economy.Metal] = 500
	econ.Players[0].PassProduced[economy.Energy] = 300
	econ.Players[0].PassProduced[economy.Metal] = 10
	return &econ
}

func TestRX01_MissedCallbacks_MobileSitePath(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, CanMove: true, MaxDamage: 100},
			content.CanonicalKey("armsolar"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armsolar")}, UnitName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armsolar"}},
		},
	}
	prof := &Profile{Weight: map[string]int32{content.CanonicalKey("armsolar"): 100}, Limit: map[string]int32{}}
	rng.SeedGlobal(7, 0)
	w := units.New(16, cat)
	h, _ := w.Create(cat.Units[content.CanonicalKey("armcom")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	b := w.Unit(h)
	b.Remaining = 0
	mgr := &Manager{
		Player: 0, Profile: prof, Catalog: cat,
		Strategic:    Strategic{CenterX: world.CellToWorld(8), CenterZ: world.CellToWorld(8), Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("armsolar"): {C0: 40}}},
		OriginX:      world.CellToWorld(2),
		OriginZ:      world.CellToWorld(2),
		SurfaceMetal: 0,
		Factory:      b,
		Terrain:      rx01Terrain(),
	}
	mgr.Strategic.Catalog = cat
	econ := rx01Econ()
	for k := TaskKind(0); k < TaskKindCount; k++ {
		mgr.Deadlines[k] = 0
	}
	for tick := uint32(0); tick < 100; tick++ {
		mgr.Tick(tick, w, econ)
	}
	if mgr.MissedQueueCallbacks() == 0 {
		t.Fatalf("mobile-site request dropped without binder was not counted [F-P0-004]")
	}
}

func TestRX01_MissedCallbacks_FactoryQueuePath(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armfactory"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfactory")}, UnitName: "armfactory", FootprintX: 4, FootprintZ: 4, YardMap: "oooo oooo oooo oooo", Builder: true, CanMove: false, MaxDamage: 100},
			content.CanonicalKey("armflea"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflea")}, UnitName: "armflea", FootprintX: 1, FootprintZ: 1, CanMove: true, MaxVelocity: 30, MaxDamage: 50},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armfactory"): {Builder: "armfactory", Buttons: []string{"armflea"}},
		},
	}
	prof := &Profile{Weight: map[string]int32{content.CanonicalKey("armflea"): 100}, Limit: map[string]int32{}}
	rng.SeedGlobal(9, 0)
	w := units.New(16, cat)
	h, _ := w.Create(cat.Units[content.CanonicalKey("armfactory")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	f := w.Unit(h)
	f.Remaining = 0
	mgr := &Manager{
		Player: 0, Profile: prof, Catalog: cat,
		Strategic:    Strategic{CenterX: world.CellToWorld(8), CenterZ: world.CellToWorld(8), Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{}},
		OriginX:      world.CellToWorld(2),
		OriginZ:      world.CellToWorld(2),
		SurfaceMetal: 0,
		Factory:      f,
		Terrain:      rx01Terrain(),
	}
	mgr.Strategic.Catalog = cat
	econ := rx01Econ()
	for k := TaskKind(0); k < TaskKindCount; k++ {
		mgr.Deadlines[k] = 0
	}
	for tick := uint32(0); tick < 100; tick++ {
		mgr.Tick(tick, w, econ)
	}
	if mgr.MissedQueueCallbacks() == 0 {
		t.Fatalf("factory-queue request dropped without binder was not counted [F-P0-004]")
	}
}
