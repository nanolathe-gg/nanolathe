//go:build retail

package session

// R07's regression: a campaign that entered under `UseOnlyUnits` must come
// back under the same restriction. Fresh entry applies it; restore staging
// resolved the path and then staged the unrestricted catalog, so saving and
// reloading a mission changed both the permitted unit set and the definition
// index space the save's own unit records were written against.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

const (
	useOnlySeed     = 7
	useOnlyCampaign = "camps/Arm Campaign.tdf"
	useOnlyMission  = useOnlyCampaign + ":MISSION0"
	// A definition the stock AC01 restriction excludes; the review's evidence
	// named it among the types an unrestricted restore let back in.
	useOnlyExcluded = "ARMLAB"
)

func useOnlyPermittedNames(cat *content.Catalog) []string {
	if cat == nil {
		return nil
	}
	return cat.SortedUnitKeys()
}

// TestRetailCampaignRestoreKeepsUseOnlyRestriction is R07's gate.
func TestRetailCampaignRestoreKeepsUseOnlyRestriction(t *testing.T) {
	f := loadRetailFixture(t)
	fullBefore := len(f.cat.Units)
	hashBefore := f.cat.Hash
	if _, ok := f.cat.Unit(useOnlyExcluded); !ok {
		t.Skipf("retail fixture unit %q is absent", useOnlyExcluded)
	}

	src, err := NewMissionWithProgressSeeds(f.fs, f.cat, useOnlyMission, 0, useOnlySeed, useOnlySeed, nil)
	if err != nil {
		t.Skipf("stock campaign %q is unavailable: %v", useOnlyMission, err)
	}
	if strings.TrimSpace(src.Mission.UseOnlyPath) == "" {
		t.Skipf("stock mission %q carries no UseOnlyUnits file", useOnlyMission)
	}
	permitted := useOnlyPermittedNames(src.Catalog)
	if len(permitted) >= fullBefore {
		t.Fatalf("fresh campaign entry did not restrict the catalog: %d of %d definitions", len(permitted), fullBefore)
	}
	if _, ok := src.Catalog.Unit(useOnlyExcluded); ok {
		t.Fatalf("fresh campaign entry admitted %q, which %s excludes", useOnlyExcluded, src.Mission.UseOnlyPath)
	}
	t.Logf("%s permits %d of %d definitions", src.Mission.UseOnlyPath, len(permitted), fullBefore)
	stepRetail(src, 60)

	summary := RetailBattleSummary(src, "useonly", "0", SkirmishDefaultUnitLimit)
	if summary.Gametype != GametypeCampaign {
		t.Fatalf("campaign save wrote gametype %d", summary.Gametype)
	}
	in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "USEONLY.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail battle save: %v", err)
	}

	// The production loader hands the full compiled catalog to the load, the
	// way the UI does; the restriction is the stage's job, not the caller's.
	result, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: useOnlySeed, CRTSeed: useOnlySeed,
		UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail campaign save: %v", err)
	}
	dst := result.Battle.Session

	restored := useOnlyPermittedNames(dst.Catalog)
	if len(restored) != len(permitted) {
		t.Fatalf("restored catalog permits %d definitions, want %d", len(restored), len(permitted))
	}
	for i := range permitted {
		if restored[i] != permitted[i] {
			t.Fatalf("restored permitted name %d = %q, want %q", i, restored[i], permitted[i])
		}
	}
	// Definition identity is the index space the save's unit records and the
	// build menus are written against, so equal names are not enough
	// [05 R-SHARE-01 §8].
	for _, key := range permitted {
		want, got := src.Catalog.Units[key], dst.Catalog.Units[key]
		if got == nil {
			t.Fatalf("restored catalog lost %q", key)
		}
		if got.UnitDefID != want.UnitDefID {
			t.Fatalf("definition %q restored with index %d, want %d", key, got.UnitDefID, want.UnitDefID)
		}
	}
	if dst.Catalog.Hash != src.Catalog.Hash {
		t.Fatalf("restored catalog digest %q, want %q", dst.Catalog.Hash, src.Catalog.Hash)
	}

	// The restriction lasts one battle and must never reach the shared
	// compiled catalog the caller owns [05 R-SHARE-01 §8].
	if dst.Catalog == f.cat {
		t.Fatal("restore staged the caller's shared catalog rather than a detached clone")
	}
	if len(f.cat.Units) != fullBefore || f.cat.Hash != hashBefore {
		t.Fatalf("the caller's catalog was mutated: %d definitions (was %d), digest %q (was %q)", len(f.cat.Units), fullBefore, f.cat.Hash, hashBefore)
	}
	if _, ok := f.cat.Unit(useOnlyExcluded); !ok {
		t.Fatalf("the caller's catalog lost %q", useOnlyExcluded)
	}
}
