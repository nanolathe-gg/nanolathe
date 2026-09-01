package world_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestGeothermalPlantAcceptsOnlyTheVent is the "where do I build it" contract,
// end to end on a real map [05 "Geothermal requirement"].
//
// The vent has no artwork of its own — `geotherm.gaf` is one frame of one pixel
// — so a player finds it by sweeping the build ghost, and that only works if
// the yard-map rule actually gates the placement. Great Divide carries exactly
// one vent, which makes it the right map to assert both halves on: accepted
// over the vent, rejected two cells away.
//
// It is also the reason the yard map matters more than the footprint: both
// geothermal plants author a four-by-four of `G`, and the rule succeeds on the
// FIRST covered cell that holds a vent, so any overlap is enough.
func TestGeothermalPlantAcceptsOnlyTheVent(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cat, err := content.Compile(fs)
	if err != nil {
		t.Skipf("catalog unavailable: %v", err)
	}
	terrain, err := world.Load(fs, cat, "Great Divide")
	if err != nil {
		t.Skipf("map unavailable: %v", err)
	}
	def := cat.Units["armgeo"]
	if def == nil {
		t.Skip("armgeo absent from the catalog")
	}

	// Find the map's vent by asking the plot, not by hard-coding a cell: the
	// assertion is about the rule, not about one map's layout.
	var ventX, ventZ int32 = -1, -1
	for cz := int32(0); cz < terrain.CellH && ventX < 0; cz++ {
		for cx := int32(0); cx < terrain.CellW; cx++ {
			cell := terrain.Plot[cz*terrain.CellW+cx]
			if !cell.IsRealFeature() {
				continue
			}
			fd, ok := terrain.FeatureDefAt(cell.Feature())
			if ok && fd != nil && fd.Geothermal {
				ventX, ventZ = cx, cz
				break
			}
		}
	}
	if ventX < 0 {
		t.Skip("no geothermal vent on this map")
	}

	yard, err := world.ParseYardMap(def.YardMap, int(def.FootprintX), int(def.FootprintZ))
	if err != nil {
		t.Fatalf("armgeo yard map %q: %v", def.YardMap, err)
	}
	rules, err := world.PlacementRulesForUnit(cat, def)
	if err != nil {
		t.Fatalf("placement rules: %v", err)
	}
	extent, err := world.NewFootprintExtent(def.FootprintX, def.FootprintZ)
	if err != nil {
		t.Fatalf("armgeo footprint %dx%d: %v", def.FootprintX, def.FootprintZ, err)
	}
	check := func(cx, cz int32) error {
		rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cx, cz), extent)
		if err != nil {
			return err
		}
		_, err = terrain.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules})
		return err
	}

	if err := check(ventX, ventZ); err != nil {
		t.Fatalf("the geothermal plant was refused ON the vent at (%d,%d): %v", ventX, ventZ, err)
	}
	// Far enough that the four-by-four cannot reach the vent at all.
	awayX, awayZ := ventX+def.FootprintX+2, ventZ
	if awayX+def.FootprintX <= terrain.CellW {
		if err := check(awayX, awayZ); err == nil {
			t.Fatalf("the geothermal plant was accepted at (%d,%d), clear of the only vent: the requirement is not gating placement", awayX, awayZ)
		}
	}
}
