package main

// The expanded sidebar is a modern presentation extension. Authored windows
// still own every control and product slot; only authored content rows and
// command blocks move.

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
	transport               bool
	builder                 pool.Handle
}

type sidebarGadgetSource struct {
	window *gui.Window
	art    *formats.GAF
}

type expandedSidebarLayout struct {
	key       expandedSidebarKey
	window    *gui.Window
	sources   []sidebarGadgetSource
	selection []pool.Handle
}

func (h *retailBattleHUD) expandedSidebarWindow(b *battleSession, f *frame.Frame, base *gui.Window, art *formats.GAF) *gui.Window {
	return h.sidebarRowsWindow(b, f, base, art)
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

func sidebarExistingCommands(w *gui.Window, f *frame.Frame) map[string]bool {
	commands := map[string]bool{"LOAD": !f.CommandPage.IsTransport, "BLAST": f.CommandPage.IsTransport}
	for _, g := range w.Gadgets {
		if g.Kind == gui.KindButton && g.CommonAttribs&4 == 0 {
			if command := commandButtonName(g.Name); command != "" && g.Active != 0 {
				commands[command] = true
			}
		}
	}
	return commands
}

func sidebarOmitted(g gui.Gadget, omitted map[string]bool) bool {
	return g.CommonAttribs&4 == 0 && omitted != nil && (sidebarNavigation(g) || omitted[commandButtonName(g.Name)])
}

func sidebarNavigation(g gui.Gadget) bool {
	command := commandButtonName(g.Name)
	name := strings.ToUpper(g.Name)
	return command == "BUILD" || command == "ORDERS" || strings.HasSuffix(name, "NEXT") || strings.HasSuffix(name, "PREV") || strings.Contains(name, "NEXTPAGE") || strings.Contains(name, "PREVPAGE") || strings.Contains(name, "PAGEUP") || strings.Contains(name, "PAGEDOWN")
}

type sidebarBand uint8

const (
	sidebarUnused sidebarBand = iota
	sidebarTabs
	sidebarGrid
	sidebarArrows
	sidebarOrders
	sidebarFooter
	sidebarBandCount
)

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

type sidebarPageBands struct {
	window *gui.Window
	art    *formats.GAF
	kinds  []sidebarBand
	spans  [sidebarBandCount]sidebarSpan
}

// Build pages supply a separable tabs/products/arrows/footer skeleton. The
// orders page locates its footer through the common semantic command buttons;
// controls above that band remain one extra-orders block. Interleaved layouts
// have no unambiguous insertion boundary and retain the original page.
func (h *retailBattleHUD) sidebarBands(w *gui.Window, art *formats.GAF, build *sidebarPageBands) (*sidebarPageBands, bool) {
	if w == nil || len(w.Gadgets) == 0 {
		return nil, false
	}
	p := &sidebarPageBands{window: w, art: art, kinds: make([]sidebarBand, len(w.Gadgets))}
	footerTop := int32(1<<31 - 1)
	if build != nil {
		for i, g := range w.Gadgets {
			if g.Kind != gui.KindButton || g.CommonAttribs&4 != 0 {
				continue
			}
			command := commandButtonName(g.Name)
			for j, shared := range build.window.Gadgets {
				if command != "" && build.kinds[j] == sidebarFooter && commandButtonName(shared.Name) == command {
					footerTop = min(footerTop, w.PlacedRect(i).Y)
				}
			}
		}
		if footerTop == int32(1<<31-1) {
			return nil, false
		}
	}
	for i, g := range w.Gadgets {
		if i == 0 || g.Kind == gui.KindFont {
			continue
		}
		if g.Kind != gui.KindButton || g.Link != "" || g.Attribs&0x1800 != 0 {
			return nil, false
		}
		r := w.PlacedRect(i)
		artW, artH := h.sidebarArtExtent(g, art)
		if r.W <= 0 || r.H <= 0 || r.X < 0 || r.X+artW > hud.ChromeRailX || r.Y < 128 {
			return nil, false
		}
		band := sidebarGrid
		command := commandButtonName(g.Name)
		if g.CommonAttribs&4 == 0 && sidebarNavigation(g) {
			band = sidebarArrows
			if command == "BUILD" || command == "ORDERS" {
				band = sidebarTabs
			}
		} else if build != nil {
			band = sidebarOrders
			if r.Y >= footerTop {
				band = sidebarFooter
			}
		} else if g.CommonAttribs&4 == 0 && command != "" {
			band = sidebarFooter
		}
		p.kinds[i] = band
		p.spans[band].include(r.Y, r.Y+artH)
	}
	if !p.spans[sidebarTabs].set || !p.spans[sidebarFooter].set {
		return nil, false
	}
	content := sidebarGrid
	if build != nil {
		content = sidebarOrders
	}
	if build == nil && !p.spans[sidebarGrid].set {
		return nil, false
	}
	end := p.spans[sidebarTabs].end
	for _, band := range []sidebarBand{content, sidebarArrows, sidebarFooter} {
		span := p.spans[band]
		if span.set {
			if span.top < end {
				return nil, false
			}
			end = span.end
		}
	}
	// An orders page's own arrows can take the common arrow band only when
	// they fit the first build page's authored navigation space.
	if build != nil && p.spans[sidebarArrows].set && (!build.spans[sidebarArrows].set || p.spans[sidebarArrows].height() > build.spans[sidebarArrows].height()) {
		return nil, false
	}
	return p, true
}

// Only cross-source overlaps are new: within-source overlap remains authored
// behavior (notably LOAD/BLAST). Compare visible art at the shared footer origin
// after duplicate/hidden controls are omitted, before publishing any layout.
func (h *retailBattleHUD) sidebarFootersCompatible(primary, other *sidebarPageBands, f *frame.Frame) bool {
	omitted := sidebarExistingCommands(primary.window, f)
	for i, g := range other.window.Gadgets {
		if other.kinds[i] != sidebarFooter || g.Active == 0 || sidebarOmitted(g, omitted) {
			continue
		}
		verdict, _ := commandGadgetVerdict(g, f, commandPageIsPaged(f))
		if verdict.hidden {
			continue
		}
		r := other.window.PlacedRect(i)
		r.Y -= other.spans[sidebarFooter].top
		r.W, r.H = h.sidebarArtExtent(g, other.art)
		for j, p := range primary.window.Gadgets {
			if primary.kinds[j] != sidebarFooter || p.Active == 0 {
				continue
			}
			verdict, _ := commandGadgetVerdict(p, f, commandPageIsPaged(f))
			if verdict.hidden {
				continue
			}
			s := primary.window.PlacedRect(j)
			s.Y -= primary.spans[sidebarFooter].top
			s.W, s.H = h.sidebarArtExtent(p, primary.art)
			if r.X < s.X+s.W && s.X < r.X+r.W && r.Y < s.Y+s.H && s.Y < r.Y+r.H {
				return false
			}
		}
	}
	return true
}

// Corresponding command groups are identified before common buttons are
// omitted. In particular, source STOP often disappears from an appended block
// but remains the authored anchor of REPAIR/CAPTURE's association. A source
// without STOP can use a common order control already in the primary STOP
// group. Unanchored radio orders cannot preserve the idle-reset contract.
func sidebarSharedOrderGroups(base, source *gui.Window, omitted map[string]bool) (map[int32]int32, bool) {
	if source == nil {
		return nil, false
	}
	var stopGroup int32
	hasStop := false
	for _, g := range base.Gadgets {
		if sidebarLatchCommand(g) == "STOP" {
			stopGroup, hasStop = g.Assoc, true
			break
		}
	}
	for _, g := range base.Gadgets {
		if sidebarLatchCommand(g) != "" && (g.Attribs&(0x10|0x40|8) != 0 || g.Status != 0) && (!hasStop || g.Assoc != stopGroup) {
			return nil, false
		}
	}
	groups := make(map[int32]int32)
	if hasStop {
		for _, g := range source.Gadgets {
			command := sidebarLatchCommand(g)
			if command == "" {
				continue
			}
			for _, existing := range base.Gadgets {
				if existing.Assoc == stopGroup && sidebarLatchCommand(existing) == command {
					groups[g.Assoc] = stopGroup
					break
				}
			}
		}
	}
	for _, g := range source.Gadgets {
		if sidebarLatchCommand(g) != "" && (g.Attribs&(0x10|0x40|8) != 0 || g.Status != 0) && !sidebarOmitted(g, omitted) {
			if _, anchored := groups[g.Assoc]; !anchored {
				return nil, false
			}
		}
	}
	return groups, true
}

func sidebarLatchCommand(g gui.Gadget) string {
	if g.Kind != gui.KindButton || g.CommonAttribs&4 != 0 {
		return ""
	}
	switch command := commandButtonName(g.Name); command {
	case "MOVE", "STOP", "ATTACK", "DEFEND", "PATROL", "REPAIR", "RECLAIM", "CAPTURE", "LOAD", "UNLOAD", "BLAST":
		return command
	}
	return ""
}

func sidebarRemapGroups(gadgets []gui.Gadget, next *int32, groups map[int32]int32) {
	if groups == nil {
		groups = make(map[int32]int32)
	}
	for i := range gadgets {
		g := &gadgets[i]
		if g.Kind != gui.KindButton {
			continue
		}
		assoc, ok := groups[g.Assoc]
		if !ok {
			*next = *next + 1
			assoc = *next
			groups[g.Assoc] = assoc
		}
		g.Assoc = assoc
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
	if cat == nil || f == nil {
		return false
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok {
		return false
	}
	menu := cat.BuildMenus[content.CanonicalKey(view.DefName)]
	if menu == nil {
		return false
	}
	for _, product := range menu.Buttons {
		if content.CanonicalKey(product) == content.CanonicalKey(name) {
			return true
		}
	}
	return false
}

func (h *retailBattleHUD) sidebarGadgetVerdict(window *gui.Window, gad gui.Gadget, f *frame.Frame, paged bool, cat *content.Catalog) (commandButtonVerdict, bool) {
	if window == h.expandedSidebar.window {
		paged = h.sidebarPaging.state.Page != 0
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
	if entry := h.gadgetArtEntry(g, art); entry != nil {
		for _, ref := range entry.Frames {
			include(ref.Frame)
		}
	} else {
		for down := 0; down <= 1; down++ {
			for _, grey := range []bool{false, true} {
				include(h.gadgetButtonFrame(g, art, down, 0, grey))
			}
		}
	}
	return
}
