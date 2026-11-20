package hud

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The committed frame's presentation-only sections (frame.SelectionView).
//
// The simulation publishes each unit's status word as it holds it — no
// selected bit, and a page field holding only the creation seed or a loaded
// value — and leaves the selection and the command page at their zero value.
// The host composes them from its LocalInterface onto the frames it presents,
// so every existing reader of the selection, the selected bit, the page field
// and the command page reads the local state without reaching it directly.

// ComposeSelection writes the local selection onto f: Selection's handles,
// primary and count; the selected bit (0x10) of each unit's status-word copy;
// the local page field of every builder whose page local input has set; the
// selected, status and range-status words of each unit contact; and any local
// logo override. It reports false, writing nothing, when f was already
// composed at the current epoch. Only units of f.Selection.LocalPlayer are
// ever selected, matched by slot and allocation serial, in the frame's own
// ascending slot order [07 §9][I1].
func (l *LocalInterface) ComposeSelection(f *frame.Frame) bool {
	if l == nil || f == nil || f.Selection.Composed == l.epoch {
		return false
	}
	sel := &f.Selection
	sel.Handles = sel.Handles[:0]
	sel.Primary, sel.Count = 0, 0
	for i := range f.Units {
		v := &f.Units[i]
		ref := pool.UnitRef{Handle: v.Slot, Serial: v.AllocationSerial}
		v.Flags &^= SelectionFlag
		if v.Owner == sel.LocalPlayer && l.Selected(ref) {
			v.Flags |= SelectionFlag
			sel.Handles = append(sel.Handles, v.Slot)
		}
		if l.HasPage(ref) {
			v.Flags = v.Flags&^PageFieldMask | l.PageFlags(ref, v.Flags)
		}
		if logo, ok := l.LogoOverride(int(v.Owner)); ok && v.OwnerColorKnown {
			v.OwnerColor = logo
		}
	}
	if len(sel.Handles) != 0 {
		sel.Primary = sel.Handles[0]
	}
	sel.Count = uint16(len(sel.Handles))
	for i := range f.Radar.Contacts {
		c := &f.Radar.Contacts[i]
		if logo, ok := l.LogoOverride(int(c.Owner)); ok && c.PaletteKnown {
			c.Palette = logo
		}
		if c.Kind != frame.RadarContactUnit {
			continue
		}
		c.Selected = c.Owner == sel.LocalPlayer && sel.Contains(c.Handle)
		c.Status &^= SelectionFlag
		if c.Selected {
			c.Status |= SelectionFlag
		}
		c.RangeStatus = c.Selected && c.RangeEligible
	}
	for i := range f.Players {
		if logo, ok := l.LogoOverride(i); ok && f.Players[i].Present {
			f.Players[i].Logo = logo
		}
	}
	sel.Composed = l.epoch
	return true
}

// ComposeCommandPage writes the command page of f's composed selection into
// dst, reusing dst's storage [07 §9][07 R-HUD-03 §6]. Every aggregate folds the
// selected units in ascending pool order; the single selected unit, whatever
// it is, owns the page. products answers the bound construction rule's
// complete product membership for a builder definition
// (session.CommandPageProducts); cat is the battle's compiled catalog, the
// immutable definitions the publisher used to read.
func ComposeCommandPage(dst *frame.CommandPageView, f *frame.Frame, cat *content.Catalog, products func(builder string) []string) {
	if dst == nil {
		return
	}
	*dst = frame.CommandPageView{
		ProductKeys:       dst.ProductKeys[:0],
		GeneratedProducts: dst.GeneratedProducts[:0],
		AllowedProducts:   nil,
	}
	foldSelectionAggregate(dst, f, cat)
	if f == nil || cat == nil || f.Selection.Count != 1 || f.Selection.Primary == 0 {
		return
	}
	var page *frame.UnitView
	for i := range f.Units {
		if f.Units[i].Slot == f.Selection.Primary {
			page = &f.Units[i]
			break
		}
	}
	if page == nil || page.Owner != f.Selection.LocalPlayer {
		return
	}
	def, ok := cat.Unit(page.DefName)
	if !ok || def == nil {
		return
	}
	// The single selected unit owns the command page whatever it is. The
	// window the switch then opens is chosen by that unit's own page-shown bit
	// and page field, and which windows exist is the definition's page-count
	// byte — the catalog compiler's probe of guis/<internal name>N.GUI
	// [07 R-HUD-03 §6][02 R-CAT-01 §5 step 5]. A count of 0 is a valid state,
	// not an absent page: the switch opens the side's "%sGEN.GUI" and the
	// stage/grey table greys BUILD and ORDERS on its own count-0 arm.
	//
	// Neither the FBI `Builder` word nor CANBUILD membership is part of that
	// test, and gating on both is what hid the stockpile launchers' pages.
	// Exactly eight reference-install definitions author a page window
	// without the builder word — ARMSILO/CORSILO, ARMAMD/CORFMD,
	// ARMSCAB/CORMABM and ARMEMP/CORTRON, the eight that carry a `stockpile`
	// weapon — and each one's page holds a single MAKENUKE/MAKEANTI toy
	// [06 §11.1].
	pageCount := BuilderPageCount(def)
	dst.Builder = page.Slot
	// A nonnil empty slice is an authoritative empty list. Nil is reserved
	// for older hand-built presentation fixtures.
	dst.AllowedProducts = make([]string, 0)
	if products != nil {
		dst.AllowedProducts = append(dst.AllowedProducts, products(def.CanonicalKey)...)
	}
	// Page 0 is the orders state, not a build page: the count is the
	// definition's page-count byte, page N carries the authored entries
	// (N-1)*6..N*6-1, and page 0 carries none [07 R-HUD-03 §6]. The page
	// number is the unit's own page-shown bit and page field [07 §9], which
	// composition has already overlaid with the local page.
	dst.PageCount = uint16(pageCount)
	pageNumber := ClampPage(DecodePage(page.Flags), pageCount)
	dst.Page = uint16(pageNumber)
	// The held-round byte the MAKENUKE/MAKEANTI toy prints; the pending half
	// of the label comes from the committed order queues [07 R-P0-11 §2].
	dst.Stockpile = page.StockpileRounds
	// A page window with no CANBUILD list behind it publishes no products;
	// its authored toys are its own. Only a unit that authors a build menu has
	// product membership to place.
	menu := cat.BuildMenus[content.CanonicalKey(def.CanonicalKey)]
	if menu == nil {
		return
	}
	// Base CANBUILD membership keeps its canonical page order. The
	// download-menu tail on BuildMenuPage.Buttons is an extension of
	// authoritative membership, not a flat continuation whose slice position
	// determines a page: download records carry PAGE and BUTTON explicitly
	// [02 R-CAT-01 §8][07 §9].
	baseButtons := menu.BaseButtons()
	// Hand-built catalogs predating BaseButtonCount have no download records
	// and therefore consist wholly of CANBUILD membership. Compiled catalogs
	// always set the count, including the legitimate zero-base/download-only
	// case.
	if menu.BaseButtonCount == 0 && len(cat.DownloadPlacements) == 0 {
		baseButtons = menu.Buttons
	}
	dst.ProductKeys = append(dst.ProductKeys, ProductsForPage(baseButtons, pageNumber, RetailBuildButtonsPerPage)...)
	for _, placement := range cat.DownloadPlacementsForPage(def.CanonicalKey, pageNumber) {
		dst.GeneratedProducts = append(dst.GeneratedProducts, frame.GeneratedProductPlacement{
			ProductKey: placement.Product,
			Button:     placement.Button,
		})
		if containsCanonicalProduct(dst.ProductKeys, placement.Product) {
			continue
		}
		dst.ProductKeys = append(dst.ProductKeys, placement.Product)
	}
}

func containsCanonicalProduct(products []string, candidate string) bool {
	want := content.CanonicalKey(candidate)
	for _, product := range products {
		if content.CanonicalKey(product) == want {
			return true
		}
	}
	return false
}

// foldSelectionAggregate folds the composed selection into the
// selection-aggregate command state the side panel stages and greys its
// command buttons from [07 §9][07 R-HUD-03 §6], walking the frame's units in
// ascending pool order, the order every selection walk uses [07 §9]. Values
// and folds are documented on frame.CommandPageView.
func foldSelectionAggregate(page *frame.CommandPageView, f *frame.Frame, cat *content.Catalog) {
	// Each field starts at its not-applicable sentinel: 4 for the three-bit
	// stance fields [04 R-STANCE-01 §1], 3 for the two-bit pairs, which is
	// also the value that greys CLOAK and ONOFF [07 R-HUD-03 §6].
	page.MoveStance = 4
	page.FireStance = 4
	page.CloakState = 3
	page.OnOffState = 3
	if f == nil || cat == nil || len(f.Selection.Handles) == 0 {
		return
	}
	for i := range f.Units {
		v := &f.Units[i]
		if !f.Selection.Contains(v.Slot) {
			continue
		}
		def, ok := cat.Unit(v.DefName)
		if !ok || def == nil {
			continue
		}
		// A unit joins a stance fold only when its definition authors the
		// matching accept key [04 R-STANCE-01 §5]; the unit's own two-bit
		// fields are bits 18-19 (move) and 20-21 (fire) of its status word
		// [04 R-STANCE-01 §2].
		if def.MobileStandOrders {
			s := uint8((v.Flags >> 18) & 3)
			if page.MoveStance == 4 {
				page.MoveStance = s
			} else if page.MoveStance != s {
				page.MoveStance = 3
			}
		}
		if def.FireStandOrders {
			s := uint8((v.Flags >> 20) & 3)
			if page.FireStance == 4 {
				page.FireStance = s
			} else if page.FireStance != s {
				page.FireStance = 3
			}
		}
		// onoffable gates the on/off pair; the folded state is the unit's
		// committed activation [04 R-SPEC-01 §11][02 "Unit record"].
		if def.OnOffable {
			s := uint8(0)
			if v.Activated {
				s = 1
			}
			if page.OnOffState == 3 {
				page.OnOffState = s
			} else if page.OnOffState != s {
				page.OnOffState = 2
			}
		}
		// The can-cloak capability is derived at definition load as
		// cloakcost > 0 [05 "which units can request cloak at all"]; the folded
		// state is the unit's cloak-requested bit. Unlike the on/off fold this
		// one takes the disagreement value for any second cloak-capable unit,
		// agreeing or not. It stays on the REQUEST: the gadget shows what the
		// player asked for and is what `Cloak_On` / `Cloak_Off` toggle, not
		// whether the unit is paid up and hidden this pass [04 R-ORD-01 §2].
		if def.CloakCost > 0 {
			if page.CloakState == 3 {
				s := uint8(0)
				if v.CloakRequested {
					s = 1
				}
				page.CloakState = s
			} else {
				page.CloakState = 2
			}
		}
		// The capability aggregates of the stage/grey table [07 R-HUD-03 §6],
		// each from its authored key [02 "Unit record"]. REPAIR reads the
		// parser's derived copy of canreclamate [02 R-KEYS-01 §1].
		page.CanMove = page.CanMove || def.CanMove
		page.CanStop = page.CanStop || def.CanStop
		page.CanAttack = page.CanAttack || def.CanAttack
		page.CanDefend = page.CanDefend || def.CanGuard
		page.CanPatrol = page.CanPatrol || def.CanPatrol
		page.CanReclaim = page.CanReclaim || def.CanReclamate
		page.CanCapture = page.CanCapture || def.CanCapture
		page.CanRepair = page.CanRepair || def.CanReclamate
		page.IsTransport = page.IsTransport || def.CanLoad
		page.CanBlast = page.CanBlast || def.CanDGun
	}
}
