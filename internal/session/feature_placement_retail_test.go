// Retail-corpus locks for the mission file's `[features]` pass: the
// centre-referenced anchor of [02 R-MAP-01 §8] against the authored stock
// maps. Skipped when the retail install is absent.
package session

import (
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// otaFeaturePlacements returns the first schema's `[features]` records for a
// compiled map, which is the list the mission loader hands battle entry.
func otaFeaturePlacements(t *testing.T, fs vfs.FSOps, mh *content.MapHeader) []mission.FeaturePlacement {
	t.Helper()
	data, err := fs.ReadFileLimit(mh.LogicalOTA, 1<<24)
	if err != nil {
		return nil
	}
	ota, err := formats.LoadOTA(data)
	if err != nil || len(ota.Schemas) == 0 {
		return nil
	}
	return mission.DecodeFeaturePlacements(ota.Schemas[0].Section)
}

// mapWithMostOTAFeatures picks the stock map carrying the most `[features]`
// records of a named definition, so the lock names a rule rather than a map.
func mapWithMostOTAFeatures(t *testing.T, cat *content.Catalog, fs vfs.FSOps, defKey string) (*content.MapHeader, []mission.FeaturePlacement) {
	t.Helper()
	keys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var best *content.MapHeader
	var bestPlaces []mission.FeaturePlacement
	bestCount := 0
	for _, k := range keys {
		mh := cat.Maps[k]
		if mh == nil {
			continue
		}
		places := otaFeaturePlacements(t, fs, mh)
		n := 0
		for _, fp := range places {
			if fp.IsPlaced() && content.CanonicalKey(fp.Name) == content.CanonicalKey(defKey) {
				n++
			}
		}
		if n > bestCount {
			best, bestPlaces, bestCount = mh, places, n
		}
	}
	return best, bestPlaces
}

// Every stock `[features]` record of a 2x2 non-sprite wall definition anchors
// one cell up-left of its authored cell, and the cell it anchors on carries a
// live blocking feature. A record that does not place at all must have been
// refused by the stamp's own rules — an indestructible feature under the
// footprint, or a footprint leaving the plot — never by the anchor
// [02 R-MAP-01 §8][05 R-FEAT-01 §3][05 R-FEAT-01 §3-A].
func TestStockOTAWallAnchorsAreCentreReferencedRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const wallKey = "dragonsteeth"
	def := cat.Features[content.CanonicalKey(wallKey)]
	if def == nil {
		t.Skipf("compiled corpus has no %q definition", wallKey)
	}
	if def.Filename != "" || def.FootprintX != 2 || def.FootprintZ != 2 {
		t.Skipf("%s is not the authored 2x2 non-sprite wall this lock describes (sprite=%v footprint=%dx%d)",
			wallKey, def.Filename != "", def.FootprintX, def.FootprintZ)
	}
	mh, places := mapWithMostOTAFeatures(t, cat, fs, wallKey)
	if mh == nil {
		t.Skipf("no stock map places %q through its OTA", wallKey)
	}

	terrain, err := world.Load(fs, cat, mh.Name)
	if err != nil {
		t.Fatalf("terrain %q: %v", mh.Name, err)
	}
	svc := features.NewService(terrain, nil, nil, nil)
	svc.PopulateFromTerrain()
	s := &Session{Catalog: cat, World: terrain, Features: svc}
	if err := stampMissionFeatures(s, &mission.Mission{Features: places}); err != nil {
		t.Fatalf("mission feature pass: %v", err)
	}

	placed, refused := 0, 0
	for _, fp := range places {
		if !fp.IsPlaced() || content.CanonicalKey(fp.Name) != content.CanonicalKey(wallKey) {
			continue
		}
		ax, az := fp.X-1, fp.Z-1
		if got, gotZ := missionFeatureAnchor(def, fp.X, fp.Z); got != ax || gotZ != az {
			t.Fatalf("%s authored at (%d,%d) resolved to (%d,%d), want (%d,%d)", wallKey, fp.X, fp.Z, got, gotZ, ax, az)
		}
		if ax < 0 || az < 0 || ax >= terrain.CellW || az >= terrain.CellH {
			continue
		}
		inst := svc.InstanceAt(int(ax), int(az))
		if inst == nil {
			refused++
			continue
		}
		if inst.Def != def || !inst.Def.Blocking {
			t.Fatalf("%s at (%d,%d) anchored a %q, want the blocking wall definition", wallKey, ax, az, inst.Def.CanonicalKey)
		}
		placed++
		// The authored cell is the feature's centre, which for a 2x2 wall is
		// the anchor's south-east footprint member, not the anchor. A member
		// lying in an edge strip is void instead of fringe, because the sweep
		// runs after this pass and converts fringe [03 R-TERR-01 §2].
		cell := terrain.PlotAt(fp.X, fp.Z)
		if cell == nil || (!cell.IsFringe() && cell.Feature() != world.PlotFeatureVoid) {
			t.Fatalf("%s authored cell (%d,%d) is neither a footprint member of the anchor at (%d,%d) nor swept void", wallKey, fp.X, fp.Z, ax, az)
		}
	}
	if placed == 0 {
		t.Fatalf("map %q placed none of its %d %s records", mh.Name, len(places), wallKey)
	}
	if refused*4 > placed {
		t.Fatalf("map %q refused %d of %d %s records, far more than the stamp's own vetoes explain", mh.Name, refused, placed+refused, wallKey)
	}
	t.Logf("map %q: %d %s records placed at the centre-referenced anchor, %d refused by the stamp", mh.Name, placed, wallKey, refused)
}

// Across the whole stock corpus, a mission `[features]` record that resolves
// to a definition and whose anchor is inside the map either produces a live
// feature or is refused by one of the stamp's OWN two rules: a footprint that
// leaves the plot, or an indestructible feature under a covered cell
// [05 R-FEAT-01 §3][05 R-FEAT-01 §3-A]. Nothing may be lost to the edge/lava
// void sweep, because the loader runs this pass before that sweep
// [02 R-MAP-01 §6][03 R-TERR-01 §2].
//
// The census also requires the ordering to be load-bearing: at least one stock
// record must cover a cell the load-time sweep converted, and those covered
// cells must end void again once the pass has run.
func TestStockOTAFeaturesSurviveTheVoidSweepRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	keys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type loss struct {
		mapName string
		name    string
		ax, az  int32
	}
	var unexplained []loss
	placedTotal, overSweptCells, overSweptPlacements := 0, 0, 0
	for _, key := range keys {
		mh := cat.Maps[key]
		if mh == nil {
			continue
		}
		places := otaFeaturePlacements(t, fs, mh)
		if len(places) == 0 {
			continue
		}
		terrain, err := world.Load(fs, cat, mh.Name)
		if err != nil {
			t.Fatalf("terrain %q: %v", mh.Name, err)
		}
		// The sweep's own voids, as the load-time sweep left them. The TNT's
		// authored void marker is a different word, so this set is exactly
		// what the mission pass has to survive.
		swept := make(map[int32]bool)
		for cz := int32(0); cz < terrain.CellH; cz++ {
			for cx := int32(0); cx < terrain.CellW; cx++ {
				if terrain.PlotAt(cx, cz).Feature() == world.PlotFeatureVoid {
					swept[cz*terrain.CellW+cx] = true
				}
			}
		}
		svc := features.NewService(terrain, nil, nil, nil)
		svc.PopulateFromTerrain()
		s := &Session{Catalog: cat, World: terrain, Features: svc}
		if err := stampMissionFeatures(s, &mission.Mission{Features: places}); err != nil {
			t.Fatalf("mission feature pass on %q: %v", mh.Name, err)
		}

		for _, fp := range places {
			if !fp.IsPlaced() || fp.Name == "" {
				continue
			}
			def := cat.Features[content.CanonicalKey(fp.Name)]
			if def == nil {
				continue // the missing-definition policy is not this lock's subject
			}
			ax, az := missionFeatureAnchor(def, fp.X, fp.Z)
			if ax < 0 || az < 0 || ax >= terrain.CellW || az >= terrain.CellH {
				continue
			}
			touched := 0
			for dz := int32(0); dz < def.FootprintZ; dz++ {
				for dx := int32(0); dx < def.FootprintX; dx++ {
					if swept[(az+dz)*terrain.CellW+ax+dx] {
						touched++
					}
				}
			}
			if svc.InstanceAt(int(ax), int(az)) == nil {
				if !stampRefusedByItsOwnRules(terrain, ax, az, def) {
					unexplained = append(unexplained, loss{mh.Name, fp.Name, ax, az})
				}
				continue
			}
			placedTotal++
			if touched == 0 {
				continue
			}
			overSweptPlacements++
			overSweptCells += touched
			// A covered cell inside a strip is fringe when the stamp writes
			// it and void again when the sweep is replayed over it.
			for dz := int32(0); dz < def.FootprintZ; dz++ {
				for dx := int32(0); dx < def.FootprintX; dx++ {
					cx, cz := ax+dx, az+dz
					if !swept[cz*terrain.CellW+cx] || (dx == 0 && dz == 0) {
						continue
					}
					if got := terrain.PlotAt(cx, cz).Feature(); got != world.PlotFeatureVoid {
						t.Fatalf("map %q: %s footprint cell (%d,%d) = %#x after the pass, want the sweep's void %#x",
							mh.Name, fp.Name, cx, cz, got, world.PlotFeatureVoid)
					}
				}
			}
		}
	}
	if len(unexplained) != 0 {
		t.Fatalf("%d stock [features] records were lost for a reason other than the stamp's own rules, first: %+v", len(unexplained), unexplained[0])
	}
	if overSweptPlacements == 0 {
		t.Fatal("no stock record covers a swept cell: the ordering this locks is untested against the corpus")
	}
	t.Logf("stock corpus: %d [features] records placed, %d of them covering %d cells the edge/lava sweep had converted",
		placedTotal, overSweptPlacements, overSweptCells)
}

// stampRefusedByItsOwnRules reports whether the stamp's two refusals — a
// footprint leaving the plot, or an indestructible feature under a covered
// cell, reached through the fringe hop — explain a missing anchor
// [05 R-FEAT-01 §3][05 R-FEAT-01 §3-A].
func stampRefusedByItsOwnRules(t *world.Terrain, ax, az int32, def *content.FeatureDef) bool {
	footX, footZ := def.FootprintX, def.FootprintZ
	if footX <= 0 || footZ <= 0 {
		return true
	}
	if ax+footX > t.CellW || az+footZ > t.CellH {
		return true
	}
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			cell := t.PlotAt(ax+dx, az+dz)
			if cell == nil {
				return true
			}
			bx, bz := ax+dx, az+dz
			if cell.IsFringe() {
				bx += int32(cell.AnchorDXSigned())
				bz += int32(cell.AnchorDZSigned())
				cell = t.PlotAt(bx, bz)
				if cell == nil {
					return true
				}
			}
			if !cell.IsRealFeature() {
				continue
			}
			if d, ok := t.FeatureDefAt(cell.Feature()); ok && d != nil && d.Indestructible {
				return true
			}
		}
	}
	return false
}
