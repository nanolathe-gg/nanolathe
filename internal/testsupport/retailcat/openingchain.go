package retailcat

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// OpeningChain is one side's authored opening build chain: the side's
// commander and the four definitions an ordinary first minute reaches through
// the authored build-menu pages.
//
// Every field is a catalog key, never a hard-coded unit name.
type OpeningChain struct {
	Commander  string
	Solar      string
	Mex        string
	KbotLab    string
	LabProduct string
}

// SelectOpeningChain walks the authored build menus of side and returns the
// chain an asset-gated test should exercise. It exists so those tests pick
// their fixtures from the compiled corpus instead of naming ARM/CORE units,
// which is what makes the same test meaningful against a modded install
// [02 "Build-menu catalog keys"].
//
// The walk is: the side record's commander, then that commander's own page
// for a solar, a metal extractor and a factory, then the factory's page for
// its first mobile product. Each selection is the FIRST button on the
// authored page satisfying a data-driven predicate, so page order — not a
// name — decides:
//
//   - solar: an ENERGY-category producer that is not storage, wind or tidal
//     [fmt fbi];
//   - metal extractor: `extractsmetal` above zero or a non-zero `makesmetal`;
//   - factory: a builder whose own page produces a mobile kbot;
//   - lab product: a mobile, non-builder kbot on that factory's page.
//
// The test is skipped rather than failed when the side or a link is absent:
// a corpus without an authored chain has nothing for the caller to exercise.
func SelectOpeningChain(t *testing.T, cat *content.Catalog, side int) OpeningChain {
	t.Helper()
	if cat == nil || side < 0 || side >= len(cat.Sides) || cat.Sides[side] == nil {
		t.Skipf("compiled corpus has no side %d", side)
	}
	sd := cat.Sides[side]
	var chain OpeningChain

	commander, ok := cat.Unit(sd.Commander)
	if !ok || commander == nil {
		t.Skipf("side %d commander %q is not in the compiled catalog", side, sd.Commander)
	}
	chain.Commander = commander.CanonicalKey

	page := cat.BuildMenus[commander.CanonicalKey]
	if page == nil || len(page.Buttons) == 0 {
		t.Skipf("commander %q has no authored build-menu page", commander.CanonicalKey)
	}
	solar := firstButton(cat, page, isSolar)
	mex := firstButton(cat, page, isMetalExtractor)
	lab := firstButton(cat, page, func(u *content.UnitDef) bool { return producesKbot(cat, u) })
	if solar == nil || mex == nil || lab == nil {
		t.Skipf("commander page %q lacks a solar/mex/factory candidate", commander.CanonicalKey)
	}
	chain.Solar, chain.Mex, chain.KbotLab = solar.CanonicalKey, mex.CanonicalKey, lab.CanonicalKey

	labPage := cat.BuildMenus[lab.CanonicalKey]
	if labPage == nil || len(labPage.Buttons) == 0 {
		t.Skipf("factory %q has no authored build-menu page", lab.CanonicalKey)
	}
	product := firstButton(cat, labPage, isMobileKbot)
	if product == nil {
		t.Skipf("factory page %q has no mobile kbot product", lab.CanonicalKey)
	}
	chain.LabProduct = product.CanonicalKey
	return chain
}

// firstButton returns the first definition on the page satisfying keep, in
// authored button order [02 "Build-menu catalog keys"].
func firstButton(cat *content.Catalog, page *content.BuildMenuPage, keep func(*content.UnitDef) bool) *content.UnitDef {
	for _, key := range page.Buttons {
		u, ok := cat.Unit(key)
		if !ok || u == nil {
			continue
		}
		if keep(u) {
			return u
		}
	}
	return nil
}

func isSolar(u *content.UnitDef) bool {
	category := content.CanonicalKey(u.Category)
	return strings.Contains(category, "energy") && !strings.Contains(category, "storage") &&
		u.WindGenerator == 0 && u.TidalGenerator == 0
}

func isMetalExtractor(u *content.UnitDef) bool {
	return u.ExtractsMetal > 0 || u.MakesMetal != 0
}

func isMobileKbot(u *content.UnitDef) bool {
	return u.CanMove && !u.Builder && strings.Contains(content.CanonicalKey(u.Category), "kbot")
}

func producesKbot(cat *content.Catalog, u *content.UnitDef) bool {
	if !u.Builder {
		return false
	}
	page := cat.BuildMenus[u.CanonicalKey]
	if page == nil {
		return false
	}
	for _, product := range page.Buttons {
		candidate, ok := cat.Unit(product)
		if ok && candidate != nil && candidate.CanMove &&
			strings.Contains(content.CanonicalKey(candidate.Category), "kbot") {
			return true
		}
	}
	return false
}
