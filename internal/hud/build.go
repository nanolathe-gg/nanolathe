package hud

import (
	"github.com/nanolathe/nanolathe/internal/content"
)

// RetailBuildButtonsPerPage is the stock builder-page product count. The
// CANBUILD sequence assigns buttons 1..6 to page one, 7..12 to page two, and
// so on; the executable's DOWNLOADMENU records carry the authored page and
// gadget slot explicitly [fmt tdf][07 §9].
const RetailBuildButtonsPerPage = 6

// Build law [07 §9][02 "Build-menu catalog keys"][R-P0-03]:
// Build pages driven by CANBUILD + per-builder GUI files; button name and unit
// definition stay data-driven; GUI may not invent products absent from authored
// build list; page encoding (page&7)<<23 bits 23-25 with bit 22 paged indicator;
// MOBILEBUILD (0xE) requires non-empty build list.

// IsMobileBuilder reports a mobile builder that should arm placement [07 §9][04 §3.4] 0xE.
func IsMobileBuilder(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	return def.Builder && def.CanMove
}

// IsFactoryBuilder reports an immobile builder (factory) that queues directly [07 §9].
func IsFactoryBuilder(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	return def.Builder && !def.CanMove
}

// BuildProductsFor returns the authored build list for a builder key [02 "Build-menu catalog keys"].
// It is the sole source of product names; GUI may not invent products absent here [R-P0-03].
func BuildProductsFor(cat *content.Catalog, builderKey string) []string {
	if cat == nil || builderKey == "" {
		return nil
	}
	if pm, ok := cat.BuildMenus[builderKey]; ok && pm != nil {
		out := make([]string, len(pm.Buttons))
		copy(out, pm.Buttons)
		return out
	}
	return nil
}

// ValidateBuildProduct reports whether product is in the builder's authored list [R-P0-03].
func ValidateBuildProduct(cat *content.Catalog, builderKey, product string) bool {
	if cat == nil {
		return false
	}
	ck := content.CanonicalKey(product)
	if ck == "" {
		return false
	}
	for _, b := range BuildProductsFor(cat, builderKey) {
		if content.CanonicalKey(b) == ck {
			return true
		}
	}
	return false
}

// NextPage returns flags with page incremented data-driven with guard [07 §9] C10.
// It uses pageCount from authored build list (len Buttons split by perPage) and
// never invents a page beyond count-1. count <=1 means no paging.
func NextPage(flags uint32, count int) uint32 {
	if count <= 1 {
		return flags
	}
	cur := 0
	if IsPaged(flags) {
		cur = DecodePage(flags)
	}
	cur = ClampPage(cur+1, count)
	return EncodePageBits(flags, cur)
}

// PrevPage returns flags with page decremented [07 §9] C10.
func PrevPage(flags uint32, count int) uint32 {
	if count <= 1 {
		return flags
	}
	cur := 0
	if IsPaged(flags) {
		cur = DecodePage(flags)
	}
	cur = ClampPage(cur-1, count)
	return EncodePageBits(flags, cur)
}

// PageCountFromButtons computes page count from button count and perPage size
// data-driven. perPage is the GUI's authored product count; callers without a
// generated page mapping use RetailBuildButtonsPerPage.
func PageCountFromButtons(n, perPage int) int {
	if n <= 0 {
		return 0
	}
	if perPage <= 0 {
		perPage = RetailBuildButtonsPerPage
	}
	return (n + perPage - 1) / perPage
}

// ProductsForPage slices the authored button list for the given page data-driven.
func ProductsForPage(all []string, page, perPage int) []string {
	if len(all) == 0 {
		return nil
	}
	if perPage <= 0 {
		perPage = RetailBuildButtonsPerPage
	}
	cnt := PageCountFromButtons(len(all), perPage)
	page = ClampPage(page, cnt)
	start := page * perPage
	if start >= len(all) {
		return nil
	}
	end := start + perPage
	if end > len(all) {
		end = len(all)
	}
	return all[start:end]
}
