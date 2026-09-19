package session

// The `UseOnlyUnits` restriction is a *catalog* removal, and the battle-entry
// catalog compile that follows it rebuilds every builder's list against the
// compacted table: each authored `canbuild<n>` name is resolved through the
// by-name search and a name that is no unit is skipped, not stored
// [02 R-CAT-01 §5 step 6], as is a download item whose `UNITNAME` no longer
// resolves [02 R-CAT-01 §8 step 4]. Removing definitions while leaving the
// compiled menus alone left the restricted mission's build rail still offering
// units the mission forbids, and clicking one asked for a definition the
// battle catalog no longer held.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestUseOnlyRestrictionPrunesBuildMenus(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "camps", "useonly"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The retail files carry one empty section per allowed unit.
	body := "[ARMCOM]\n\t{\n\t}\n[ARMSOLAR]\n\t{\n\t}\n"
	if err := os.WriteFile(filepath.Join(root, "camps", "useonly", "one.tdf"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}

	base := minimalCatalogForStrict()
	for _, name := range []string{"armsolar", "armwin"} {
		def := *base.Units["armcom"]
		def.UnitName = name
		def.CanonicalKey = name
		def.Commander = false
		base.Units[name] = &def
	}
	base.BuildMenus = map[string]*content.BuildMenuPage{
		"armcom": {
			Builder: "ARMCOM",
			// Base CANBUILD prefix, then one download-extended product; the
			// permitted name sits between two forbidden ones so a prune that
			// merely truncates would be caught.
			Buttons:         []string{"ARMWIN", "ARMSOLAR", "CORCOM", "ARMWIN"},
			BaseButtonCount: 3,
			AuthoredButtons: []string{"ARMWIN", "ARMSOLAR", "ARMWIN", "ARMSOLAR"},
		},
		// A builder the restriction removes keeps no list at all.
		"corcom": {Builder: "CORCOM", Buttons: []string{"ARMSOLAR"}, BaseButtonCount: 1},
	}
	base.DownloadPlacements = []content.DownloadMenuPlacement{
		{Builder: "ARMCOM", Product: "ARMSOLAR", Menu: 1, Button: 0, BuilderResolved: true, ProductResolved: true},
		{Builder: "ARMCOM", Product: "ARMWIN", Menu: 1, Button: 1, BuilderResolved: true, ProductResolved: true},
	}

	restricted, err := applyUseOnlyRestriction(fs, base, "camps/useonly/one.tdf")
	if err != nil {
		t.Fatalf("applyUseOnlyRestriction: %v", err)
	}
	page := restricted.BuildMenus["armcom"]
	if page == nil {
		t.Fatal("the permitted builder lost its build menu")
	}
	if got := page.Buttons; len(got) != 1 || got[0] != "ARMSOLAR" {
		t.Fatalf("restricted ARMCOM page %v, want only the permitted ARMSOLAR [02 R-CAT-01 §5 step 6]", got)
	}
	if got := page.AuthoredButtons; len(got) != 2 || got[0] != "ARMSOLAR" || got[1] != "ARMSOLAR" {
		t.Fatalf("authored membership bypassed restriction: %v", got)
	}
	if len(base.BuildMenus["armcom"].AuthoredButtons) != 4 {
		t.Fatal("restriction mutated shared authored membership")
	}
	// The base prefix has to shrink with the list: a stale count would slice
	// download products into the authored page window [07 R-HUD-03 §6].
	if page.BaseButtonCount != 1 {
		t.Fatalf("restricted ARMCOM base count %d, want 1", page.BaseButtonCount)
	}
	if _, ok := restricted.BuildMenus["corcom"]; ok {
		t.Fatal("a builder the restriction removed must not keep a build menu")
	}
	if got := restricted.DownloadPlacementsForPage("ARMCOM", 0); len(got) != 1 || got[0].Product != "ARMSOLAR" {
		t.Fatalf("restricted download placements %v, want only the permitted product [02 R-CAT-01 §8 step 4]", got)
	}
	// The input catalog is shared and must be untouched: the restriction lasts
	// one battle [05 R-SHARE-01 §8].
	if len(base.BuildMenus) != 2 || len(base.BuildMenus["armcom"].Buttons) != 4 || len(base.DownloadPlacements) != 2 {
		t.Fatal("the shared catalog's build menus were mutated by the restriction")
	}
}
