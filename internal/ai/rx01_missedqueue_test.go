package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func rx01Terrain() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	return &world.Terrain{CellW: 16, CellH: 16, Plot: world.ExpandPlot(attrs, 16, 16), Version: world.VersionCanonical}
}

func TestUnboundMobileSitePathReportsMissingQueue(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, CanMove: true, MaxDamage: 100},
		content.CanonicalKey("armsolar"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armsolar")}, UnitName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100},
	}}
	w := units.NewSliced(16, cat)
	h, err := w.Create(cat.Units[content.CanonicalKey("armcom")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	b := w.Unit(h)
	b.Remaining = 0
	sim := rng.NewSimulation(7)
	mgr := &Manager{Player: 0, Catalog: cat, Factory: b, Terrain: rx01Terrain(), RNG: &sim}
	res := PlaceWithResult(mgr, "armsolar", mgr.Terrain)
	if res.Valid || res.Reason != ReasonMissingQueue {
		t.Fatalf("unbound mobile placement = valid=%v reason=%v, want missing typed queue", res.Valid, res.Reason)
	}
}

func TestUnboundFactoryQueuePathReportsMissingQueue(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		content.CanonicalKey("armfactory"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfactory")}, UnitName: "armfactory", FootprintX: 4, FootprintZ: 4, YardMap: "oooo oooo oooo oooo", Builder: true, CanMove: false, MaxDamage: 100},
		content.CanonicalKey("armflea"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflea")}, UnitName: "armflea", FootprintX: 1, FootprintZ: 1, BMCode: true, CanMove: true, MaxVelocity: 30, MaxDamage: 50},
	}}
	w := units.NewSliced(16, cat)
	h, err := w.Create(cat.Units[content.CanonicalKey("armfactory")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	f := w.Unit(h)
	f.Remaining = 0
	sim := rng.NewSimulation(9)
	mgr := &Manager{Player: 0, Catalog: cat, Factory: f, Terrain: rx01Terrain(), RNG: &sim}
	res := PlaceWithResult(mgr, "armflea", mgr.Terrain)
	if res.Valid || res.Reason != ReasonMissingQueue {
		t.Fatalf("unbound factory placement = valid=%v reason=%v, want missing typed queue", res.Valid, res.Reason)
	}
}
