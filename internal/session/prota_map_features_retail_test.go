//go:build retail

package session

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// proTAMapFeatureMap authors 186 terrain-file DragonsTeeth and DragonsTeeth_Core
// (height 20, forced nodrawundergray) in the ProTA 4.8 archive.
const proTAMapFeatureMap = "slated fate"

// TestProTAMapFeaturesStampOwnerEleven locks the loader half of the package's
// map-owned feature behaviour on authored content
// (research/extensions/prota-engine.md "Map-owned features drawn without line
// of sight"; docs/DESIGN_COMMUNITY_PATCH.md §4.7): under the ProTA profile every
// terrain-file anchor takes placer 11, the committed frame publishes it, and
// a battle save restores it [08 R-SAVE-02 §12]; Strict 3.1 resolves the zero
// table and keeps retail's 10.
func TestProTAMapFeaturesStampOwnerEleven(t *testing.T) {
	fs, cat, limits, profile := loadProTAArchive(t)
	sources := CommunitySources{Content: profile.GameplaySources()}
	enter := func(mode gameplay.Mode) *Session {
		t.Helper()
		cfg := DirectSkirmishConfig(proTAMapFeatureMap)
		cfg.ApplyDefaults()
		cfg.Gameplay = mode
		cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
		s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{CommunitySources: sources, ContentLimits: limits})
		if err != nil {
			t.Fatalf("compose %s skirmish on %s: %v", mode, proTAMapFeatureMap, err)
		}
		return s
	}
	// tally counts the live anchors of nodrawundergray definitions by placer.
	tally := func(s *Session) map[uint8]int {
		out := map[uint8]int{}
		for _, inst := range s.Features.Instances() {
			if inst.Def == nil || !inst.Def.NoDrawUnderGray {
				continue
			}
			out[s.World.PlotAt(int32(inst.CX), int32(inst.CZ)).PlacerNibble()]++
		}
		return out
	}

	strict := enter(gameplay.Strict31)
	if got := tally(strict); got[world.MapOwnedFeaturePlacer] != 0 || got[world.TerrainFeaturePlacer] == 0 {
		t.Fatalf("Strict 3.1 map wall placers = %v, want only %d", got, world.TerrainFeaturePlacer)
	}

	s := enter(gameplay.Modern)
	if !s.EntryCommunity.MapFeatureOwnerEleven {
		t.Fatal("ProTA profile did not enable MapFeatureOwnerEleven in the entry table")
	}
	walls := tally(s)
	if walls[world.MapOwnedFeaturePlacer] == 0 || walls[world.TerrainFeaturePlacer] != 0 {
		t.Fatalf("ProTA map wall placers = %v, want only %d", walls, world.MapOwnedFeaturePlacer)
	}
	advanceProTATicks(t, s, 2)
	published := 0
	for _, f := range s.Snapshot.Current().Features {
		if f.NoDrawUnderGray && f.OwnerKnown && f.Owner == world.MapOwnedFeaturePlacer {
			published++
		}
	}
	if published == 0 {
		t.Fatal("the committed frame published no owner-11 map wall")
	}

	summary := RetailBattleSummary(s, "ProTA map walls", "0", s.Skirmish.UnitLimit)
	inputs, err := s.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("project save: %v", err)
	}
	path := filepath.Join(t.TempDir(), "WALLS.SAV")
	if err := s.WriteRetailSave(path, inputs); err != nil {
		t.Fatalf("write save: %v", err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: fs, Catalog: cat, ContentLimits: limits, CommunitySources: sources,
		Gameplay: gameplay.Modern, SimSeed: 7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("restore save: %v", err)
	}
	if loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("save did not restore a battle")
	}
	dst := loaded.Battle.Session
	if got := tally(dst); got[world.MapOwnedFeaturePlacer] != walls[world.MapOwnedFeaturePlacer] || got[world.TerrainFeaturePlacer] != 0 {
		t.Fatalf("restored map wall placers = %v, want %v", got, walls)
	}
	before, err := s.World.RetailPlayerFeaturesImage()
	if err != nil {
		t.Fatal(err)
	}
	after, err := dst.World.RetailPlayerFeaturesImage()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("restored PlayerFeatures image differs from the saved battle's")
	}
}
