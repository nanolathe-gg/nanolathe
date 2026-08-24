package world_test

import (
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestEveryMapLoads is the I14 gate for the world layer: fixtures cannot find
// what real map data carries. It also locks the three things Load used to get
// wrong silently — a flat derived floor, an unbound feature table, and
// unstamped fringe anchors — as measurements rather than as assertions about
// one map.
func TestEveryMapLoads(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fileSystem.Close()

	catalog, err := content.Compile(fileSystem)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	keys := make([]string, 0, len(catalog.Maps))
	for key := range catalog.Maps {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var loaded, flatFloor, unbound, fringe, resolved int
	var failures []string
	for _, key := range keys {
		terrain, err := world.Load(fileSystem, catalog, key)
		if err != nil {
			if len(failures) < 5 {
				failures = append(failures, key+": "+err.Error())
			}
			continue
		}
		loaded++
		distinct := map[uint8]bool{}
		for i := range terrain.Plot {
			cell := terrain.Plot[i]
			distinct[cell.MinHeight()] = true
			switch feature := cell.Feature(); {
			case feature == world.PlotFeatureFringe:
				fringe++
				cx := int32(i) % terrain.CellW
				cz := int32(i) / terrain.CellW
				if _, ok := world.ResolveFeature(terrain.Plot, int(terrain.CellW), int(terrain.CellH), int(cx), int(cz)); ok {
					resolved++
				}
			case feature < 0xFFFB:
				if _, ok := terrain.FeatureDefAt(feature); !ok {
					unbound++
				}
			}
		}
		if len(distinct) <= 1 {
			flatFloor++
		}
	}

	if len(failures) > 0 || loaded != len(keys) {
		t.Fatalf("%d of %d maps failed to load: %v", len(keys)-loaded, len(keys), failures)
	}
	// The derived floor pair must vary. It was uniformly zero before, which made
	// CoarseHeightAt a constant [02 "Terrain file"], [03 §2.3].
	if flatFloor != 0 {
		t.Errorf("%d maps have a completely flat derived floor pair", flatFloor)
	}
	// Every feature record in the corpus binds to a catalog definition. A
	// nonzero count is not a bug by itself — [04 §6.2] defines the out-of-range
	// behaviour — but it means the reference install changed.
	if unbound != 0 {
		t.Errorf("%d feature references did not bind to the catalog", unbound)
	}
	// Fringe anchors: see stampFeatureAnchors' TODO(question) for why this is a
	// measured floor rather than 100%.
	if fringe == 0 {
		t.Fatal("no fringe cells found; the corpus should have tens of thousands")
	}
	if ratio := float64(resolved) / float64(fringe); ratio < 0.80 {
		t.Errorf("only %.1f%% of %d fringe cells resolve to an anchor, want >= 80%%",
			100*ratio, fringe)
	}
	t.Logf("%d maps, %d fringe cells, %.1f%% resolved", loaded, fringe, 100*float64(resolved)/float64(fringe))
}
