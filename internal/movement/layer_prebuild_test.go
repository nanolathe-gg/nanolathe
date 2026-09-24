package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A registered ground unit's class layer exists at the next tick start, with
// no path request, and building it twice allocates nothing new
// (DESIGN_MOVEMENT_PATH "Full-layer rebuild storage").
func TestRegisteredClassLayerIsBuiltAtTickStart(t *testing.T) {
	sys := NewSystem(syntheticTerrainForIntegrate(), Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255}, NewOccupancyGrid())
	w := newMovementFixtureWorld(2)
	def := setScratchMovement(&content.UnitDef{UnitName: "layer-prebuild-test", FootprintX: 1, FootprintZ: 1, BMCode: 1},
		Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255})
	h, err := w.Create(def, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	sys.EnsureUnit(w.Unit(h))
	if sys.existingLayer(h) != nil {
		t.Fatal("layer built before the tick start")
	}
	sys.BeginTick(1)
	l := sys.existingLayer(h)
	if l == nil {
		t.Fatal("registered unit's class layer was not built at the tick start")
	}
	sys.EndTick(1)
	sys.BeginTick(2)
	if sys.existingLayer(h) != l || len(sys.layerRegistry.Names()) != 1 {
		t.Fatal("a second tick rebuilt or duplicated the class layer")
	}
}

// A structure (movement byte 0) never searches, so registering one builds no
// layer (review: each structure profile had built a full-map layer).
func TestRegisteredStructureBuildsNoLayer(t *testing.T) {
	sys := NewSystem(syntheticTerrainForIntegrate(), Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255}, NewOccupancyGrid())
	w := newMovementFixtureWorld(2)
	def := &content.UnitDef{UnitName: "layer-prebuild-structure", FootprintX: 4, FootprintZ: 4, BMCode: 0}
	h, err := w.Create(def, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	sys.EnsureUnit(w.Unit(h))
	sys.BeginTick(1)
	if n := len(sys.layerRegistry.Names()); n != 0 {
		t.Fatalf("a structure built %d class layers", n)
	}
}
