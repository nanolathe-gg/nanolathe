//go:build retail

package world_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestAcidWaterWordsReachTheTerrain checks the wiring the per-unit sweep's
// step-9 water damage depends on [04 R-MOV-03 §1][04 §9.2]: the mission's
// `waterdoesdamage` and `waterdamage` are carried from the compiled map header
// onto the terrain record, beside the sea-level byte they are tested against.
//
// The two named maps are a census result over the 275 stock OTAs, not a guess:
// ten author `waterdoesdamage=1` (the acid maps, the two gasplant maps, the
// two verdigris seas, "checker ponds", "cleanup on kral" and "surface
// meltdown"), and most of the rest author a `waterdamage` amount with the flag
// at zero, which is inert because the step needs BOTH words nonzero.
func TestAcidWaterWordsReachTheTerrain(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fileSystem.Close()

	maps, err := content.CompileMaps(fileSystem)
	if err != nil {
		t.Fatalf("compile maps: %v", err)
	}
	features, err := content.CompileFeatures(fileSystem)
	if err != nil {
		t.Fatalf("compile features: %v", err)
	}
	catalog := &content.Catalog{Maps: maps, Features: features}

	for _, tc := range []struct {
		key                          string
		waterDoesDamage, waterDamage int32
	}{
		{"Acid Pools", 1, 10},   // an acid map: the step fires
		{"Ashap Plateau", 0, 0}, // authors neither word: the step is inert
	} {
		terrain, err := world.Load(fileSystem, catalog, tc.key)
		if err != nil {
			t.Fatalf("load %q: %v", tc.key, err)
		}
		if terrain.WaterDoesDamage != tc.waterDoesDamage || terrain.WaterDamage != tc.waterDamage {
			t.Fatalf("%q water words = (%d, %d), want (%d, %d) [fmt ota][04 §9.2]",
				tc.key, terrain.WaterDoesDamage, terrain.WaterDamage, tc.waterDoesDamage, tc.waterDamage)
		}
	}
}
