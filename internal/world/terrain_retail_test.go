//go:build retail

package world_test

import (
	"runtime"
	"sort"
	"sync"
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

	maps, err := content.CompileMaps(fileSystem)
	if err != nil {
		t.Fatalf("compile maps: %v", err)
	}
	features, err := content.CompileFeatures(fileSystem)
	if err != nil {
		t.Fatalf("compile features: %v", err)
	}
	catalog := &content.Catalog{Maps: maps, Features: features}
	keys := make([]string, 0, len(catalog.Maps))
	for key := range catalog.Maps {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	type mapResult struct {
		key             string
		err             error
		flatFloor       bool
		unbound, fringe int
		resolved        int
	}
	inspect := func(key string) mapResult {
		result := mapResult{key: key}
		terrain, err := world.Load(fileSystem, catalog, key)
		if err != nil {
			result.err = err
			return result
		}
		distinct := map[uint8]bool{}
		for i := range terrain.Plot {
			cell := terrain.Plot[i]
			distinct[cell.MinHeight()] = true
			switch feature := cell.Feature(); {
			case feature == world.PlotFeatureFringe:
				result.fringe++
				cx := int32(i) % terrain.CellW
				cz := int32(i) / terrain.CellW
				if _, ok := world.ResolveFeature(terrain.Plot, int(terrain.CellW), int(terrain.CellH), int(cx), int(cz)); ok {
					result.resolved++
				}
			case feature < 0xFFFB:
				if _, ok := terrain.FeatureDefAt(feature); !ok {
					result.unbound++
				}
			}
		}
		result.flatFloor = len(distinct) <= 1
		return result
	}

	// Each map load is independent. A small worker pool keeps this corpus gate
	// representative without making it wait on one map at a time.
	workerCount := runtime.GOMAXPROCS(0)
	if workerCount > 4 {
		workerCount = 4
	}
	if workerCount > len(keys) {
		workerCount = len(keys)
	}
	jobs := make(chan string)
	results := make(chan mapResult, len(keys))
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for key := range jobs {
				results <- inspect(key)
			}
		}()
	}
	go func() {
		for _, key := range keys {
			jobs <- key
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	var loaded, flatFloor, unbound, fringe, resolved int
	errorsByKey := make(map[string]error)
	for result := range results {
		if result.err != nil {
			errorsByKey[result.key] = result.err
			continue
		}
		loaded++
		if result.flatFloor {
			flatFloor++
		}
		unbound += result.unbound
		fringe += result.fringe
		resolved += result.resolved
	}
	var failures []string
	for _, key := range keys {
		if err, ok := errorsByKey[key]; ok && len(failures) < 5 {
			failures = append(failures, key+": "+err.Error())
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
