//go:build retail

package session

// Arm campaign mission 2 (`AC02.ota`, campaign selector `MISSION1`) is the
// named scenario for two battle-entry contracts that had no coverage:
//
//   - the starting-resource words are `[Schema N]` keys, not `[GlobalHeader]`
//     keys [02 R-MAP-01 §5], and the surviving grant writes both the live
//     stocks and the storage bonus on the mission kind
//     [08 R-ENTRY-01 §8 step 5][05 R-ECO-01 §4];
//   - the `UseOnlyUnits` restriction is a catalog removal, and the build menus
//     are rebuilt against the compacted table, so a name the restriction drops
//     is skipped rather than stored [02 R-CAT-01 §5 step 6][08 R-ENTRY-01 §2
//     step 4].
//
// AC02 authors `HumanMetal=1000; HumanEnergy=1000;` in every schema and
// `useonlyunits=AC02.tdf;`, whose file omits `ARMSOLAR`'s stablemates — the
// commander's own build list names units the file does not.

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

const (
	pt6Mission     = "camps/Arm Campaign.tdf:MISSION1"
	pt6Commander   = "ARMCOM"
	pt6StartMetal  = 1000 // AC02 [Schema N] HumanMetal
	pt6StartEnergy = 1000 // AC02 [Schema N] HumanEnergy
)

func TestRetailArmMission02StartingResourcesAndBuildPage(t *testing.T) {
	f := loadRetailFixture(t)
	s, err := NewMissionWithProgressSeeds(f.fs, f.cat, pt6Mission, 0, 7, 7, nil)
	if err != nil {
		t.Skipf("stock campaign %q is unavailable: %v", pt6Mission, err)
	}
	if strings.TrimSpace(s.Mission.UseOnlyPath) == "" {
		t.Skipf("stock mission %q carries no UseOnlyUnits file", pt6Mission)
	}

	human := &s.Econ.Players[0]
	if got := human.Stock[economy.Metal]; got != pt6StartMetal {
		t.Fatalf("human metal stock %v, want %d from the selected schema's HumanMetal [02 R-MAP-01 §5]", got, pt6StartMetal)
	}
	if got := human.Stock[economy.Energy]; got != pt6StartEnergy {
		t.Fatalf("human energy stock %v, want %d from the selected schema's HumanEnergy [02 R-MAP-01 §5]", got, pt6StartEnergy)
	}
	// The bonus is what lets the commander alone hold the opening stock: the
	// mission kind sets the enable flag and stores max(v, 200) per resource
	// [05 R-ECO-01 §4][08 R-ENTRY-01 §8 step 5].
	if !human.StorageBonusEnabled {
		t.Fatal("the mission kind must set the storage-bonus enable flag [05 R-ECO-01 §4]")
	}
	if got := human.StorageBonus[economy.Metal]; got != pt6StartMetal {
		t.Fatalf("metal storage bonus %v, want %d", got, pt6StartMetal)
	}
	if got := human.StorageBonus[economy.Energy]; got != pt6StartEnergy {
		t.Fatalf("energy storage bonus %v, want %d", got, pt6StartEnergy)
	}
	// Capacity carries the bonus, so the opening stock is not clipped by the
	// post-settlement clamp on the first pass.
	if human.Capacity[economy.Metal] < pt6StartMetal {
		t.Fatalf("metal capacity %v is below the opening stock; the bonus never reached the capacity sum [05 R-ECO-01 §4]", human.Capacity[economy.Metal])
	}

	// The restriction removed definitions from the catalog, so the commander's
	// CANBUILD page must not still offer them: retail resolves each authored
	// name through the compacted table and skips a name that is no unit
	// [02 R-CAT-01 §5 step 6].
	page := s.Catalog.BuildMenus[content.CanonicalKey(pt6Commander)]
	if page == nil {
		t.Fatalf("%s has no compiled build menu", pt6Commander)
	}
	if len(page.Buttons) == 0 {
		t.Fatalf("%s build menu is empty", pt6Commander)
	}
	for _, button := range page.Buttons {
		if _, ok := s.Catalog.Unit(button); !ok {
			t.Fatalf("%s build page still offers %q, which %s removed from the catalog [02 R-CAT-01 §5 step 6]", pt6Commander, button, s.Mission.UseOnlyPath)
		}
	}
	t.Logf("%s under %s offers %v", pt6Commander, s.Mission.UseOnlyPath, page.Buttons)
	// AC02's file names ARMSOLAR but not ARMWIN, and the stock ARMCOM CANBUILD
	// list names both: the restriction has to bite on the page, not only on the
	// catalog.
	if !buildPageOffers(page, "ARMSOLAR") {
		t.Fatalf("%s build page lost ARMSOLAR, which AC02.tdf permits: %v", pt6Commander, page.Buttons)
	}
	if buildPageOffers(page, "ARMWIN") {
		t.Fatalf("%s build page still offers ARMWIN, which AC02.tdf does not permit: %v", pt6Commander, page.Buttons)
	}
	// The published command page is the surface the player clicks; it slices
	// the same base list [07 R-HUD-03 §6].
	products := hud.ProductsForPage(page.BaseButtons(), 1, hud.RetailBuildButtonsPerPage)
	for _, product := range products {
		if _, ok := s.Catalog.Unit(product); !ok {
			t.Fatalf("published build page 1 offers %q, which is not in the battle catalog", product)
		}
	}
}

func buildPageOffers(page *content.BuildMenuPage, name string) bool {
	want := content.CanonicalKey(name)
	for _, button := range page.Buttons {
		if content.CanonicalKey(button) == want {
			return true
		}
	}
	return false
}
