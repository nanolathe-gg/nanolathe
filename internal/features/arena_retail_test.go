//go:build retail

package features_test

import (
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestRestingSpritesDoNotConsumeTheArena is the stock-asset half of R05's
// regression. The live-instance arena is 2048 slots and its occupants are 3D
// definitions and ACTIVE sprite event records only; a map's resting sprite
// anchors take no slot at all [05 R-FEAT-01 §2][05 R-FEAT-01 §3 steps 4-5].
//
// "forest base 129" is the shipped map that proves it: it carries thousands of
// resting tree anchors and no 3D anchors, so a service that charged every
// anchor against the arena stopped populating partway through and then refused
// every wreck for the rest of the battle.
//
// The assertions are relationships, not a census: every real anchor the plot
// carries reaches publication, and a 3D wreck can still be stamped afterwards.
func TestRestingSpritesDoNotConsumeTheArena(t *testing.T) {
	catalog, fileSystem := retailcat.Shared(t)
	key := content.CanonicalKey("forest base 129")
	if _, ok := catalog.Maps[key]; !ok {
		t.Skipf("map %q absent from this install", key)
	}
	terrain, err := world.Load(fileSystem, catalog, key)
	if err != nil {
		t.Fatalf("load map: %v", err)
	}

	// Count the anchors the plot actually carries, and how many of them are
	// sprite (filename-based) definitions.
	anchors, sprites := 0, 0
	w, h := int(terrain.CellW), int(terrain.CellH)
	for cz := 0; cz < h; cz++ {
		for cx := 0; cx < w; cx++ {
			cell := terrain.PlotAt(int32(cx), int32(cz))
			if cell == nil || !cell.IsRealFeature() {
				continue
			}
			def, bound := terrain.FeatureDefAt(cell.Feature())
			if !bound || def == nil {
				continue
			}
			anchors++
			if def.Filename != "" {
				sprites++
			}
		}
	}
	if anchors <= features.FeatureAnimSlots {
		t.Skipf("map carries %d anchors, which does not exceed the %d-slot arena", anchors, features.FeatureAnimSlots)
	}

	sim := rng.SimulationFromState(1)
	service := features.NewService(terrain, &sim, nil, nil)
	populated := service.PopulateFromTerrain()
	if populated != anchors {
		t.Fatalf("populated %d instances for %d plot anchors; resting sprites must not be charged against the arena", populated, anchors)
	}
	if got := len(service.Instances()); got != anchors {
		t.Fatalf("published %d features for %d plot anchors", got, anchors)
	}
	if sprites != anchors {
		t.Logf("map carries %d 3D anchors alongside %d sprite anchors", anchors-sprites, sprites)
	}

	// A wreck must still be placeable after all that clutter. Find an empty
	// cell and a 3D corpse definition, and stamp it.
	wreck := first3DFeature(catalog)
	if wreck == nil {
		t.Skip("no 3D feature definition in this catalog")
	}
	cx, cz, ok := firstEmptyCell(terrain, wreck)
	if !ok {
		t.Skip("no empty cell wide enough for the wreck")
	}
	if service.PlaceAt(cx, cz, wreck) == nil {
		t.Fatalf("wreck %q refused at empty cell (%d,%d) on a map of %d resting anchors", wreck.CanonicalKey, cx, cz, anchors)
	}
}

// first3DFeature returns the lowest-keyed 3D definition — an authored model and
// no sprite source, which is flag bit 0 clear [05 R-FEAT-01 §15]. The catalog
// map is walked through a sorted key list so the choice is deterministic (I1).
func first3DFeature(catalog *content.Catalog) *content.FeatureDef {
	keys := make([]string, 0, len(catalog.Features))
	for key := range catalog.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		def := catalog.Features[key]
		if def == nil || def.Object == "" || def.Filename != "" || def.Indestructible {
			continue
		}
		if def.FootprintX <= 0 || def.FootprintZ <= 0 {
			continue
		}
		return def
	}
	return nil
}

// firstEmptyCell scans row-major for a footprint-sized rectangle of empty plot
// cells, which is where a stamp needs no teardown at all.
func firstEmptyCell(terrain *world.Terrain, def *content.FeatureDef) (int, int, bool) {
	w, h := int(terrain.CellW), int(terrain.CellH)
	fx, fz := int(def.FootprintX), int(def.FootprintZ)
	for cz := 0; cz+fz <= h; cz++ {
	next:
		for cx := 0; cx+fx <= w; cx++ {
			for dz := 0; dz < fz; dz++ {
				for dx := 0; dx < fx; dx++ {
					cell := terrain.PlotAt(int32(cx+dx), int32(cz+dz))
					if cell == nil || !cell.IsEmpty() {
						continue next
					}
				}
			}
			return cx, cz, true
		}
	}
	return 0, 0, false
}
