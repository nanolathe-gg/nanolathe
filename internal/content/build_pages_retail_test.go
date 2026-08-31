//go:build retail

package content

import (
	"fmt"
	"testing"
)

// The page-count byte is the probe of the authored `guis/<unitname>N.GUI`
// windows [02 R-CAT-01 §5 step 5], never `ceil(len(CANBUILD)/6) + 1`.
//
// The two disagree on six of the reference install's 45 builders: the three
// Arm construction units author nineteen `CANBUILD` products and the three
// Core ones twenty, but all six author only three page windows. Deriving the
// count from the button list claimed a fourth page, and selecting it asked the
// command switch for a window that does not exist — the panel then showed
// neither a build page nor the orders page.
func TestBuildPageCountFollowsAuthoredPageWindows(t *testing.T) {
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
		if int(def.BuildPageCount) != want {
			t.Errorf("%s page count = %d, want %d from its authored page windows",
				def.UnitName, def.BuildPageCount, want)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d builders checked; the reference install has 45", checked)
	}
	// The six builders the CANBUILD arithmetic overshot, named so a regression
	// is reported as itself rather than as a count.
	for _, name := range []string{"armca", "armck", "armcv", "corca", "corck", "corcv"} {
		def, ok := cat.Unit(name)
		if !ok || def == nil {
			t.Fatalf("%s missing from the catalog", name)
		}
		if def.BuildPageCount != 4 {
			t.Errorf("%s page count = %d, want 4 — three authored windows plus the orders page", name, def.BuildPageCount)
		}
		menu := cat.BuildMenus[CanonicalKey(name)]
		if menu == nil || len(menu.Buttons) <= 18 {
			t.Errorf("%s authors %d products; the overshoot case needs more than three pages' worth", name, len(menu.Buttons))
		}
	}
}
