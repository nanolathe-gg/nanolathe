package main

// The expanded sidebar is a modern presentation extension. Authored windows
// still own every control and product identity; the host normalizes product
// cells independently of the authored command scaffold.

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

type expandedSidebarKey struct {
	base                    *gui.Window
	definition              *content.UnitDef
	width, height           int32
	page, count, remembered int
	localPage               int
	flat                    bool
	transport               bool
	builder                 pool.Handle
}

type sidebarGadgetSource struct {
	window *gui.Window
	art    *formats.GAF
	index  int
}

type expandedSidebarLayout struct {
	key       expandedSidebarKey
	window    *gui.Window
	sources   []sidebarGadgetSource
	selection []pool.Handle
}

func (h *retailBattleHUD) expandedSidebarWindow(b *battleSession, f *frame.Frame, base *gui.Window, art *formats.GAF) *gui.Window {
	flat := b != nil && b.cl != nil && b.cl.Enhanced() && b.expandedSidebarEnabled()
	if h.sidebarPaging.flat != flat {
		h.retireExpandedSidebar()
		h.sidebarPaging = sidebarRowPaging{flat: flat}
	}
	if flat {
		if window := h.sidebarProductsWindow(b, f, base); window != nil {
			return window
		}
	}
	if window, ok := h.overflowSidebarWindow(b, f, base); ok {
		return window
	}
	h.retireExpandedSidebar()
	h.sidebarPaging = sidebarRowPaging{flat: flat}
	return base
}

func (h *retailBattleHUD) retireExpandedSidebar() {
	if old := h.expandedSidebar.window; old != nil {
		if panel := h.palettePanels[old]; panel != nil {
			panel.ResetPress()
		}
		delete(h.palettePanels, old)
	}
	h.expandedSidebar = expandedSidebarLayout{}
}

// Load exactly the authored/generated numbered page. Download records retain
// their explicit BUTTON slots; CANBUILD membership is never a geometry source.
func (h *retailBattleHUD) sidebarBuildPage(cat *content.Catalog, def *content.UnitDef, page int) (*gui.Window, *formats.GAF) {
	name := commandWindowName(sideNamePrefix(h.side), def.UnitName, true, page)
	if page == 0 {
		name = strings.ToLower(def.UnitName) + "0"
	}
	if window := h.generatedWindows[name]; window != nil {
		return window, h.generatedPageArt[name]
	}
	var placements []frame.GeneratedProductPlacement
	for _, placement := range cat.DownloadPlacementsForPage(def.CanonicalKey, page) {
		placements = append(placements, frame.GeneratedProductPlacement{ProductKey: placement.Product, Button: placement.Button})
	}
	window, art, err := h.numberedPage(name, placements)
	if err != nil {
		// Optional extension failure keeps the selected page usable. Selecting
		// the failing page normally still reports its original diagnostic.
		return nil, nil
	}
	return window, art
}

func sidebarNavigation(g gui.Gadget) bool {
	command := commandButtonName(g.Name)
	name := strings.ToUpper(g.Name)
	return command == "BUILD" || command == "ORDERS" || strings.HasSuffix(name, "NEXT") || strings.HasSuffix(name, "PREV") || strings.Contains(name, "NEXTPAGE") || strings.Contains(name, "PREVPAGE") || strings.Contains(name, "PAGEUP") || strings.Contains(name, "PAGEDOWN")
}

type sidebarSpan struct {
	top, end int32
	set      bool
}

func (s sidebarSpan) height() int32 { return s.end - s.top }
func (s *sidebarSpan) include(top, end int32) {
	if !s.set {
		s.top, s.end, s.set = top, end, true
	} else {
		s.top, s.end = min(s.top, top), max(s.end, end)
	}
}

func (h *retailBattleHUD) sidebarSource(window *gui.Window, index int, art *formats.GAF) (*gui.Window, *formats.GAF) {
	if window == h.expandedSidebar.window && index >= 0 && index < len(h.expandedSidebar.sources) {
		source := h.expandedSidebar.sources[index]
		return source.window, source.art
	}
	return window, art
}

func sidebarProductAllowed(f *frame.Frame, cat *content.Catalog, name string) bool {
	// A factory queue resolves the installed widget name, not CANBUILD.
	// Stock ARMPLAT authors ARMCSA where its list names ARMCA [07 §9].
	if cat != nil {
		if product, ok := cat.Unit(name); ok && product != nil && product.BMCode != 0 {
			return true
		}
	}
	return hud.BuildProductAllowed(cat, f, name)
}

func (h *retailBattleHUD) sidebarGadgetVerdict(window *gui.Window, gad gui.Gadget, f *frame.Frame, paged bool, cat *content.Catalog) (commandButtonVerdict, bool) {
	if window == h.expandedSidebar.window {
		paged = h.sidebarPaging.state.Page != 0
		name := strings.ToUpper(gad.Name)
		if strings.HasSuffix(name, "NEXT") || strings.HasSuffix(name, "PREV") {
			return commandButtonVerdict{hidden: h.sidebarPaging.state.Count < 2}, true
		}
	}
	if window == h.expandedSidebar.window && gad.CommonAttribs&4 != 0 {
		product, ok := cat.Unit(gad.Name)
		return commandButtonVerdict{grey: !ok || product == nil || !sidebarProductAllowed(f, cat, gad.Name)}, true
	}
	return paletteGadgetVerdict(gad, f, paged, cat)
}

// Include every runtime art state's extent without changing authored hit
// rectangles. Stock ARMRAD has a 65-pixel disabled frame for a 64-pixel toy;
// its extra column still fits the rail. Reject only art spilling into the world.
func (h *retailBattleHUD) sidebarArtExtent(g gui.Gadget, art *formats.GAF) (width, height int32) {
	width, height = g.Rect.W, g.Rect.H
	include := func(f *formats.GAFFrame) {
		if f != nil {
			width, height = max(width, int32(f.Width)), max(height, int32(f.Height))
		}
	}
	if entry := h.gadgetArtEntry(g, art); entry != nil && !strings.EqualFold(entry.Name, "BUTTONS0") {
		for _, ref := range entry.Frames {
			include(ref.Frame)
		}
	} else {
		// BUTTONS0 contains multiple size families. Only the selected family
		// can paint this gadget; unrelated frames do not constrain its layout.
		for down := 0; down <= 1; down++ {
			for _, grey := range []bool{false, true} {
				include(h.gadgetButtonFrame(g, art, down, 0, grey))
			}
		}
	}
	return
}
