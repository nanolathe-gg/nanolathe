package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The terrain and selected mission request feature models independently of
// unit corpses [05 R-FEAT-01 §1][02 "Cross-reference failure policy"].
func TestBattleEntryValidatesRequestedFeatureModels(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "placerfixture.tnt"), placerFixtureTNT(), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	cat := &content.Catalog{
		Features: map[string]*content.FeatureDef{
			"wallfixture": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wallfixture"}},
			"unused":      {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "unused"}, Object: "missing"},
		},
		Maps: map[string]*content.MapHeader{"placerfixture": {Schemas: []content.MapSchema{{Name: "Schema 0"}}}},
	}
	m := &mission.Mission{TerrainKey: "placerfixture", Schema: mission.Schema{Name: "Schema 0"}}
	if _, err := loadTerrainStrict(fs, cat, m, community.Features{}); err != nil {
		t.Fatalf("unrequested feature blocked entry: %v", err)
	}
	for _, source := range []string{"terrain successor", "mission"} {
		t.Run(source, func(t *testing.T) {
			if source == "terrain successor" {
				cat.Features["wallfixture"].FeatureDead = "unused"
				defer func() { cat.Features["wallfixture"].FeatureDead = "" }()
			} else {
				m.Features = []mission.FeaturePlacement{{Name: "unused", X: 10, Z: 10}}
				defer func() { m.Features = nil }()
			}
			if _, err := loadTerrainStrict(fs, cat, m, community.Features{}); err == nil || !strings.Contains(err.Error(), "objects3d/missing.3do") {
				t.Fatalf("requested model error = %v", err)
			}
		})
	}
}

func TestBattleRestoreValidatesSavedFeatureModels(t *testing.T) {
	bank, deps := restoreRNGFixture(t)
	// The saved tree is not requested by the base terrain or a unit corpse.
	// A changed provider must still fail before the restored world is adopted.
	deps.Catalog.Features["tree1"].Object = "missing_saved_feature"
	if _, err := StageRetailBattle(bank, deps); err == nil || !strings.Contains(err.Error(), "objects3d/missing_saved_feature.3do") {
		t.Fatalf("saved feature model error = %v", err)
	}
}
