package hud

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
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

// ProductArmsPlacement reports whether clicking this product's build gadget arms
// the placement latch rather than queueing the product immediately [07 §9].
//
// Retail tests the **product**, not the builder: the build-button handler arms
// MOBILEBUILD and stores the product's definition id only when the product's
// authored `BMcode` is zero, and otherwise falls through to the immediate queue
// path. BMcode zero is the building class — the same test that decides whether a
// definition carries a yard map at all [04 §6.2]. Keying on the builder's
// mobility instead happens to agree across the stock corpus, where factories
// build mobile units and mobile builders build structures, but it is not the
// contract.
func ProductArmsPlacement(def *content.UnitDef) bool {
	return def != nil && !def.BMCode
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

// BuildProductForSlot resolves the product authored at one build page's Nth
// button gadget: page is the 1-based authored page number (`<unit>1.GUI` is
// page 1, matching ProductsForPage/BuilderPageCount) and slotOrdinal is the
// zero-based position of the gadget among the page's build-product buttons,
// counted in gadget file order after paging and order-control gadgets are
// excluded — never the gadget's own authored `name` field.
//
// Per-unit `<unit>N.GUI` panels are hand-authored templates, and their build
// gadgets keep whatever unit name the panel artist typed while laying out the
// slot; the engine does not read that name for identity. Retail's product-page
// assembly "matches the builder definition and page, then patches the product
// into the named gadget slot" from the authored CANBUILD sequence, which "maps
// entries 1-6 to page one, 7-12 to page two, and so on" [07 §9 "Product-page
// assembly is closed"]. Measured directly against the patched reference
// install, `guis/armplat1.gui`'s first build gadget is authored `name=ARMCSA`
// while `sidedata.tdf` authors `canbuild1=ARMCA` for the same builder — a
// resolver keyed on the gadget's name drops that button (no product answers
// to "ARMCSA"), and worse, `guis/armplat1.gui`'s fourth build gadget is
// authored `name=ARMSFIG`, which collides with `canbuild3` instead of the
// slot's own `canbuild4=ARMHAWK`, binding the wrong product to a live button.
// Ordinal position is the field the two authoring passes cannot disagree on.
//
// A slot beyond the authored list (a short final page) or an unresolved
// builder returns "", false: the caller must not invent a product for an
// unfilled button [R-P0-03].
func BuildProductForSlot(cat *content.Catalog, builderKey string, page, slotOrdinal int) (string, bool) {
	if slotOrdinal < 0 {
		return "", false
	}
	products := ProductsForPage(BuildProductsFor(cat, builderKey), page, RetailBuildButtonsPerPage)
	if slotOrdinal >= len(products) || products[slotOrdinal] == "" {
		return "", false
	}
	return products[slotOrdinal], true
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
// It is the NEXT gadget's move, which never returns to page 0
// [07 R-HUD-03 §6]. count <=1 means no paging.
func NextPage(flags uint32, count int) uint32 {
	if count <= 1 {
		return flags
	}
	return EncodePageBits(flags, NextPageButton(DecodePage(flags), count))
}

// PrevPage returns flags with page decremented — the PREV gadget's move
// [07 §9][07 R-HUD-03 §6].
func PrevPage(flags uint32, count int) uint32 {
	if count <= 1 {
		return flags
	}
	return EncodePageBits(flags, PrevPageButton(DecodePage(flags), count))
}

// The page cycle [07 R-HUD-03 §6]. Page 0 is the orders state and pages
// 1..count-1 are the authored build pages, so the producers below are three
// different walks over the same range:
//
//   - the `.` and `,` keys cycle through every page, page 0 included, and wrap
//     at both ends: `.` from the last page returns to page 0, `,` from page 0
//     goes to the last page. That is modular arithmetic over 0..count-1.
//   - the NEXT and PREV gadgets never return to page 0: NEXT from the last page
//     wraps to page 1, and PREV from page 1 goes to the last page.
//   - a digit selects page digit-1 outright and does nothing at all when that
//     page does not exist. It is the only producer that can refuse.
//
// Each takes and returns a page number rather than a flag word, so the caller
// keeps the committed page as the one identity it acts on [I6].

// NextPageKey is the `.` key's move: the next page, wrapping past the last one
// back to the orders page [07 R-HUD-03 §6].
func NextPageKey(page, count int) int {
	if count <= 0 || page < 0 || page >= count-1 {
		return 0
	}
	return page + 1
}

// PrevPageKey is the `,` key's move: the previous page, wrapping past the
// orders page back to the last one [07 R-HUD-03 §6].
func PrevPageKey(page, count int) int {
	if count <= 0 {
		return 0
	}
	if page <= 0 || page > count-1 {
		return count - 1
	}
	return page - 1
}

// NextPageButton is the NEXT gadget's move. Unlike the `.` key it never returns
// to page 0: from the last page it wraps to page 1 [07 R-HUD-03 §6].
func NextPageButton(page, count int) int {
	if count <= 1 {
		return 0
	}
	if page <= 0 || page >= count-1 {
		return 1
	}
	return page + 1
}

// PrevPageButton is the PREV gadget's move. From page 1 — and from the orders
// page — it goes to the last page rather than to page 0 [07 R-HUD-03 §6].
func PrevPageButton(page, count int) int {
	if count <= 1 {
		return 0
	}
	if page <= 1 || page > count-1 {
		return count - 1
	}
	return page - 1
}

// DigitPage is the digit rule: digit d selects page d-1 when that page exists,
// and otherwise selects nothing [07 R-HUD-03 §6]. The second result reports
// whether the digit named a page at all; false is a no-op, not a clamp, which
// is what separates the digits from the keys and the gadgets.
func DigitPage(digit, count int) (int, bool) {
	if digit < 1 || digit > 9 || count <= 0 {
		return 0, false
	}
	page := DigitToPage(digit)
	if page >= count {
		return 0, false
	}
	return page, true
}

// BuilderPageCount is the builder definition's build-menu page-count byte, the
// guard every page producer below is bounded by: valid pages are
// `0 .. count-1`, page 0 is the orders state, and the authored build pages are
// `1 .. count-1` with page N holding entries `(N-1)*perPage .. N*perPage-1`
// [07 R-HUD-03 §6].
//
// The byte is compiled from the authored `guis/<unitname>N.GUI` windows
// [02 R-CAT-01 §5 step 5], never from the length of the `CANBUILD` list.
// "Generated `<unit>N.GUI` pages are authoritative for page existence and
// placement, so a replacement engine must not infer an eight-slot grid or
// synthesize missing pages" [07 §9].
//
// This replaces PageCountFromButtons, which derived the count as
// `ceil(len(CANBUILD)/6) + 1`. That agrees with the authored pages for 39 of
// the reference install's 45 builders and overshoots by one for the other six:
// `ARMCA`/`ARMCK`/`ARMCV` author nineteen products and `CORCA`/`CORCK`/`CORCV`
// twenty, all across three authored pages, so the arithmetic claimed a fourth.
// Paging onto it asked the command switch for a window that does not exist,
// and the panel went blank — no build page and no orders page — until the
// selection changed.
//
// A definition with no authored page window has a count of 0, which is the
// established value and not an error: its products are unreachable in retail
// too, because there is no window to reach them through.
func BuilderPageCount(def *content.UnitDef) int {
	if def == nil {
		return 0
	}
	return int(def.BuildPageCount)
}

// ProductsForPage slices the authored button list for the given page. Page 0 is
// the orders page and carries no products; page N carries the authored entries
// (N-1)*perPage..N*perPage-1 [07 R-HUD-03 §6]. A page past the last authored
// one carries nothing rather than repeating the last page's products.
func ProductsForPage(all []string, page, perPage int) []string {
	if len(all) == 0 || page <= 0 {
		return nil
	}
	if perPage <= 0 {
		perPage = RetailBuildButtonsPerPage
	}
	start := (page - 1) * perPage
	if start >= len(all) {
		return nil
	}
	end := start + perPage
	if end > len(all) {
		end = len(all)
	}
	return all[start:end]
}

// QueueCountLabel returns the retail product-button count text: **one**
// running total summed over both the primary and secondary order lists for
// the matching product, formatted "+%d" [07 R-P0-11 §2]. A zero total clears
// the label; there is no clamp and no display cap.
//
// Correction (WU-19-135): this previously formatted the two lists as
// separate numbers — "%d" alone, "+%d" alone, or "%d +%d" together — on the
// theory that the secondary list was a distinct "+N" queued half. The
// refinement folded into [07 R-P0-11 §2] settles this from the executable:
// the bit-0x04 branch every build-product button authors resolves the toy's
// name to a product id and calls a **single** count query that walks the
// builder's primary list and then its secondary list into one running total,
// which is what gets formatted "+%d". A queue of five in the primary list and
// two in the secondary shows "+7", never "5 +2" — the two-number form is not
// a retail shape. `cmd/nanolathe/battle_hud.go`'s `productQueueCountLabel`
// already implements this corrected shape for the live battle HUD; this
// export now agrees with it instead of diverging.
func QueueCountLabel(queues []frame.OrderQueueView, product string) string {
	key := content.CanonicalKey(product)
	if key == "" {
		return ""
	}
	var total uint32
	for _, q := range queues {
		for _, o := range q.Primary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
		for _, o := range q.Secondary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("+%d", total)
}
