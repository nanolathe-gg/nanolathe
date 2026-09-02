//go:build retail

package content

import (
	"fmt"
	"testing"
)

// The page-count byte starts from the authored `guis/<unitname>N.GUI` probe
// [02 R-CAT-01 §5 step 5], then download-menu MENU bytes can raise it
// [02 R-CAT-01 §8]. It is never `ceil(len(CANBUILD)/6) + 1`.
//
// The CANBUILD length and authored sources disagree for several builders:
// physical GUI probes establish the initial count, while download records can
// add generated pages. Dividing the flat product list into six-button groups
// cannot distinguish those two cases and is not the retail rule.
func TestBuildPageCountFollowsAuthoredPageWindowsAndDownloads(t *testing.T) {
	fs := mountRetail(t)
	cat, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	pageWindows := func(name string) int {
		n := 0
		for page := 1; page <= 7; page++ {
			info, err := fs.Stat(fmt.Sprintf("guis/%s%d.gui", name, page))
			if err != nil || info.Size == 0 {
				break
			}
			n = page
		}
		return n
	}
	checked := 0
	for key, menu := range cat.BuildMenus {
		if menu == nil || len(menu.Buttons) == 0 {
			continue
		}
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			continue
		}
		want := pageWindows(def.UnitName)
		if want > 0 {
			want++ // the orders page
		}
		for _, placement := range cat.DownloadPlacements {
			if CanonicalKey(placement.Builder) == key && int(placement.Menu) > want {
				want = int(placement.Menu)
			}
		}
		if int(def.BuildPageCount) != want {
			t.Errorf("%s page count = %d, want %d from its authored page windows",
				def.UnitName, def.BuildPageCount, want)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d builders checked; the reference install has 45", checked)
	}
	// Downloads raise these six to 5. This still comes from authored MENU bytes,
	// not from dividing their nineteen or twenty CANBUILD products into pages.
	for _, name := range []string{"armca", "armck", "armcv", "corca", "corck", "corcv"} {
		def, ok := cat.Unit(name)
		if !ok || def == nil {
			t.Fatalf("%s missing from the catalog", name)
		}
		if def.BuildPageCount != 5 {
			t.Errorf("%s page count = %d, want 5 after its authored download MENU raise", name, def.BuildPageCount)
		}
		menu := cat.BuildMenus[CanonicalKey(name)]
		if menu == nil || len(menu.Buttons) <= 18 {
			t.Errorf("%s authors %d products; the overshoot case needs more than three pages' worth", name, len(menu.Buttons))
		}
	}

	// The missing ARM Kbot Lab page is generated from two explicit sparse slot
	// records: MENU=3 is visible page 2, with Warrior at 0 and Flea at 1
	// [02 R-CAT-01 §8][fmt tdf][07 R-HUD-03 §6].
	armlab, ok := cat.Unit("ARMLAB")
	if !ok || armlab == nil {
		t.Fatal("ARMLAB missing from catalog")
	}
	if armlab.BuildPageCount != 3 {
		t.Fatalf("ARMLAB page count = %d, want 3", armlab.BuildPageCount)
	}
	placements := cat.DownloadPlacementsForPage("ARMLAB", 2)
	if len(placements) != 2 {
		t.Fatalf("ARMLAB visible page 2 placements = %#v, want Warrior and Flea", placements)
	}
	byProduct := make(map[string]DownloadMenuPlacement, len(placements))
	for _, placement := range placements {
		byProduct[CanonicalKey(placement.Product)] = placement
	}
	if warrior, ok := byProduct["armwar"]; !ok || warrior.Menu != 3 || warrior.Button != 0 {
		t.Errorf("ARM Warrior placement = %#v, want MENU=3 BUTTON=0", warrior)
	}
	if flea, ok := byProduct["armflea"]; !ok || flea.Menu != 3 || flea.Button != 1 {
		t.Errorf("ARM Flea placement = %#v, want MENU=3 BUTTON=1", flea)
	}
	menu := cat.BuildMenus["armlab"]
	if menu == nil {
		t.Fatal("ARMLAB authoritative product list missing")
	}
	products := make(map[string]bool, len(menu.Buttons))
	for _, product := range menu.Buttons {
		products[CanonicalKey(product)] = true
	}
	if !products["armwar"] || !products["armflea"] {
		t.Fatalf("ARMLAB products omit downloads: %v", menu.Buttons)
	}
}
