package main

// Oversized authored command pages use the existing presentation pager on
// either renderer. This host layout policy is documented in HUD design §3.3.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Numbered IGPATCH artwork families are authored empty slots as well. Keep
// this a slot-name convention, independent of any particular content profile.
func sidebarEmptySlot(name string) bool {
	name = strings.ToUpper(name)
	if !strings.HasPrefix(name, "IGPATCH") {
		return false
	}
	for _, c := range name[len("IGPATCH"):] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func sidebarProductSlot(g gui.Gadget) bool {
	return g.Kind == gui.KindButton && (g.CommonAttribs&4 != 0 || sidebarEmptySlot(g.Name))
}

// The general GUI loader retains the retail logical-surface clamp. Battle
// command pages can author a taller canvas: retain its explicit origin before
// fitting complete product groups below the minimap. Do not reinterpret the
// negative position sentinels or change ordinary retail windows.
func preserveSidebarOrigin(w *gui.Window) {
	if w == nil || len(w.Gadgets) == 0 {
		return
	}
	r := w.Gadgets[0].Rect
	if r.RawY >= 128 && r.RawY+r.H > retailScreenH && r.W <= hud.ChromeRailX {
		w.OriginY, w.Rect.Y, w.Gadgets[0].Rect.Y = r.RawY, r.RawY, r.RawY
	}
}

type sidebarProductGroup struct {
	top, end int32
	indices  []int
}
type sidebarOverflowPage struct {
	window       *gui.Window
	art          *formats.GAF
	page         int
	groups       []sidebarProductGroup
	omitted      []bool
	upper, lower sidebarSpan
	gridTop      int32
}
type sidebarOverflowPart struct {
	source             *sidebarOverflowPage
	start, end, anchor int
}

func (h *retailBattleHUD) overflowPage(w *gui.Window, art *formats.GAF, page int) (*sidebarOverflowPage, bool) {
	if w == nil {
		return nil, false
	}
	p := &sidebarOverflowPage{window: w, art: art, page: page, gridTop: 1 << 30, omitted: make([]bool, len(w.Gadgets))}
	var indices []int
	for i, g := range w.Gadgets {
		if i == 0 || g.Kind == gui.KindFont {
			continue
		}
		if h.coveredSidebarPlaceholder(w, art, i) {
			p.omitted[i] = true
			continue
		}
		if g.Kind != gui.KindButton || g.Link != "" || g.Attribs&0x1800 != 0 {
			return nil, false
		}
		r := w.PlacedRect(i)
		aw, ah := h.sidebarArtExtent(g, art)
		if r.W <= 0 || r.H <= 0 || r.X < 0 || r.X+aw > hud.ChromeRailX {
			return nil, false
		}
		if sidebarProductSlot(g) {
			indices = append(indices, i)
			p.gridTop = min(p.gridTop, r.Y)
			p.groups = append(p.groups, sidebarProductGroup{top: r.Y, end: r.Y + max(r.H, ah), indices: []int{i}})
		}
	}
	if len(indices) == 0 {
		return nil, false
	}
	slices.SortStableFunc(p.groups, func(a, b sidebarProductGroup) int { return cmp.Compare(a.top, b.top) })
	// Vertically intersecting records form one indivisible group. This preserves
	// the small shipyard controls beside a full-height product without overlap.
	groups := p.groups[:0]
	for _, g := range p.groups {
		if len(groups) > 0 && g.top < groups[len(groups)-1].end {
			last := &groups[len(groups)-1]
			last.end = max(last.end, g.end)
			last.indices = append(last.indices, g.indices...)
		} else {
			groups = append(groups, g)
		}
	}
	p.groups = groups
	gridEnd := groups[len(groups)-1].end
	for i, g := range w.Gadgets {
		if i == 0 || g.Kind == gui.KindFont || p.omitted[i] || sidebarProductSlot(g) {
			continue
		}
		r := w.PlacedRect(i)
		_, ah := h.sidebarArtExtent(g, art)
		if r.Y+ah <= p.gridTop {
			p.upper.include(r.Y, r.Y+ah)
		} else if r.Y >= gridEnd {
			p.lower.include(r.Y, r.Y+ah)
		} else {
			return nil, false
		}
	}
	// Template-only padding after the last product must not create empty pages.
	last := -1
	for n, g := range p.groups {
		for _, i := range g.indices {
			if !sidebarEmptySlot(w.Gadgets[i].Name) {
				last = n
			}
		}
	}
	p.groups = p.groups[:last+1]
	return p, len(p.groups) > 0
}

func (h *retailBattleHUD) overflowSidebarWindow(b *battleSession, f *frame.Frame, base *gui.Window) (*gui.Window, bool) {
	if base == nil || b == nil || b.cl == nil || b.cat == nil || f == nil || f.CommandPage.PageCount < 2 {
		return nil, false
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok || view.Owner != h.owner {
		return nil, false
	}
	def, ok := b.cat.Unit(view.DefName)
	if !ok || !def.Builder {
		return nil, false
	}
	// Only oversized authored canvases need this mandatory fit. Ordinary stock
	// pages continue through the optional modern expansion unchanged.
	first, firstArt := h.sidebarBuildPage(b.cat, def, 1)
	oversized := first != nil && first.Rect.Y+first.Rect.H > retailScreenH
	// All authored pages share this pager. A fitting first page says nothing
	// about later page canvases, including generated pages using another template.
	for page := 2; !oversized && page < int(f.CommandPage.PageCount); page++ {
		window, _ := h.sidebarBuildPage(b.cat, def, page)
		oversized = window != nil && window.Rect.Y+window.Rect.H > retailScreenH
	}
	if !oversized {
		return nil, false
	}
	width, height := b.cl.Size()
	p := &h.sidebarPaging
	reseed := p.definition != def || p.builder != f.CommandPage.Builder || !slices.Equal(p.selection, f.Selection.Handles) || p.authoredPage != int(f.CommandPage.Page) || p.authoredRemembered != buildButtonPage(f) || p.authoredCount != int(f.CommandPage.PageCount)
	key := expandedSidebarKey{base: base, definition: def, width: int32(width), height: int32(height), page: int(f.CommandPage.Page), count: int(f.CommandPage.PageCount), remembered: buildButtonPage(f), localPage: p.state.Page, transport: f.CommandPage.IsTransport, builder: f.CommandPage.Builder}
	if !reseed && key == h.expandedSidebar.key && h.expandedSidebar.window != nil {
		return h.expandedSidebar.window, true
	}
	var parts []sidebarOverflowPart
	anchor, seedAnchor := 0, 0
	seed := key.page
	if seed == 0 {
		seed = key.remembered
	}
	for page := 1; page < key.count; page++ {
		w, a := first, firstArt
		if page != 1 {
			w, a = h.sidebarBuildPage(b.cat, def, page)
		}
		source, safe := h.overflowPage(w, a, page)
		if !safe {
			return nil, false
		}
		upperHeight := source.upper.height()
		// Keep the authored separation from tabs to products. Reserve the complete
		// navigation/command block; no command gadgets become product slots.
		if source.upper.set {
			upperHeight = source.gridTop - source.upper.top
		}
		available := int32(height) - 128 - upperHeight - source.lower.height()
		if source.lower.set {
			available -= source.lower.top - (w.OriginY + maxProductBottom(w))
		}
		if page == seed {
			seedAnchor = anchor
		}
		for start := 0; start < len(source.groups); {
			end := start
			for end < len(source.groups) && source.groups[end].end-source.groups[start].top <= available {
				end++
			}
			if end == start {
				return nil, false
			}
			parts = append(parts, sidebarOverflowPart{source: source, start: start, end: end, anchor: anchor + start})
			start = end
		}
		anchor += len(source.groups)
	}
	if len(parts) == 0 {
		return nil, false
	}
	if reseed {
		*p = sidebarRowPaging{flat: p.flat, definition: def, builder: f.CommandPage.Builder, selection: append([]pool.Handle(nil), f.Selection.Handles...), authoredPage: key.page, authoredRemembered: key.remembered, authoredCount: key.count, anchor: seedAnchor, state: sidebarPagingState{Page: key.page}}
	}
	// A resize can move between flat products and fitted authored groups.
	// Their numeric anchors are unrelated; carry the visible source record.
	if !reseed && len(p.starts) == 0 {
		p.anchor = 0
		for _, part := range parts {
			if part.source.window != p.anchorSource.window {
				continue
			}
			for _, group := range part.source.groups[part.start:part.end] {
				if slices.Contains(group.indices, p.anchorSource.index) {
					p.anchor = part.anchor
				}
			}
		}
	}
	p.starts = p.starts[:0]
	selected := 0
	for i, part := range parts {
		p.starts = append(p.starts, part.anchor)
		if part.anchor <= p.anchor {
			selected = i
		}
	}
	p.state.Count = len(parts) + 1
	p.state.Remembered = selected + 1
	if p.state.Page != 0 {
		p.state.Page = p.state.Remembered
	}
	p.anchor = parts[selected].anchor
	p.anchorSource = sidebarGadgetSource{}
	remembered := parts[selected]
	for _, group := range remembered.source.groups[remembered.start:remembered.end] {
		for _, i := range group.indices {
			g := remembered.source.window.Gadgets[i]
			if g.Active == 0 || g.CommonAttribs&4 == 0 || sidebarEmptySlot(g.Name) {
				continue
			}
			r := remembered.source.window.PlacedRect(i)
			first := gui.Rect{Y: 1<<31 - 1}
			if p.anchorSource.window != nil {
				first = p.anchorSource.window.PlacedRect(p.anchorSource.index)
			}
			if r.Y < first.Y || r.Y == first.Y && r.X < first.X {
				p.anchorSource = sidebarGadgetSource{remembered.source.window, remembered.source.art, i}
			}
		}
		if p.anchorSource.window != nil {
			break
		}
	}
	h.retireExpandedSidebar()
	l := &h.expandedSidebar
	part := parts[selected]
	source := part.source
	l.window = cloneGUIWindow(source.window)
	l.window.Gadgets = nil
	for i, g := range source.window.Gadgets {
		if source.omitted[i] {
			continue
		}
		r := source.window.PlacedRect(i)
		if sidebarProductSlot(g) {
			included := false
			for _, group := range source.groups[part.start:part.end] {
				if slices.Contains(group.indices, i) {
					included = true
					break
				}
			}
			if !included {
				continue
			}
			top := int32(128)
			if source.upper.set {
				top += source.gridTop - source.upper.top
			}
			r.Y += top - source.groups[part.start].top
		} else if i != 0 && g.Kind != gui.KindFont {
			if source.upper.set && r.Y < source.gridTop {
				r.Y += 128 - source.upper.top
			} else {
				r.Y += int32(height) - source.lower.end
			}
		}
		if i != 0 {
			r.X -= l.window.OriginX
			r.Y -= l.window.OriginY
		}
		g.Rect = r
		l.window.Gadgets = append(l.window.Gadgets, g)
		l.sources = append(l.sources, sidebarGadgetSource{source.window, source.art, i})
	}
	// Orders keeps its own semantic controls. The build layout remains remembered
	// and returning via BUILD uses the same local page, as with stock expansion.
	if p.state.Page == 0 {
		ordersName := strings.ToLower(sideNamePrefix(h.side)) + "gen"
		if def.HasPageZeroGUI {
			ordersName = strings.ToLower(def.UnitName) + "0"
		}
		orders, ordersArt := h.loadWindow(ordersName)
		if orders == nil {
			h.retireExpandedSidebar()
			return nil, false
		}
		l.window = cloneGUIWindow(orders)
		l.window.Gadgets = nil
		l.sources = nil
		var span sidebarSpan
		// A custom orders canvas may carry unused product template records.
		// Empty slots have no orders action; retain every real product record.
		for i, g := range orders.Gadgets {
			if sidebarEmptySlot(g.Name) || h.coveredSidebarPlaceholder(orders, ordersArt, i) {
				continue
			}
			if i > 0 && g.Kind == gui.KindButton && g.Active != 0 {
				r := orders.PlacedRect(i)
				_, ah := h.sidebarArtExtent(g, ordersArt)
				span.include(r.Y, r.Y+ah)
			}
			l.window.Gadgets = append(l.window.Gadgets, g)
			l.sources = append(l.sources, sidebarGadgetSource{orders, ordersArt, i})
		}
		if span.height() > int32(height)-128 {
			h.retireExpandedSidebar()
			return nil, false
		}
		for i, g := range l.window.Gadgets {
			if i > 0 && g.Kind == gui.KindButton && g.Active != 0 {
				l.window.Gadgets[i].Rect.Y += 128 - span.top
			}
		}
	}
	l.window.Rect.Y = 128
	l.window.Rect.H = int32(height) - 128
	l.window.Gadgets[0].Rect = l.window.Rect
	l.window.Header.TotalGadgets = int16(len(l.window.Gadgets) - 1)
	key.localPage = p.state.Page
	l.key = key
	l.selection = append([]pool.Handle(nil), f.Selection.Handles...)
	return l.window, true
}

func maxProductBottom(w *gui.Window) int32 {
	var bottom int32
	for _, g := range w.Gadgets {
		if sidebarProductSlot(g) {
			bottom = max(bottom, g.Rect.Y+g.Rect.H)
		}
	}
	return bottom
}

// Unbound, textless buttons completely beneath a later authored slot add no
// reachable control. Omit only these covered template decorations when fitting
// a page; retain commands and every partially exposed record (HUD design §3.3).
func (h *retailBattleHUD) coveredSidebarPlaceholder(w *gui.Window, art *formats.GAF, index int) bool {
	g := w.Gadgets[index]
	if index == 0 || g.Kind != gui.KindButton || sidebarProductSlot(g) || commandButtonName(g.Name) != "" || sidebarNavigation(g) || g.Text != "" || len(g.Labels) != 0 || g.QuickKey != 0 || g.Link != "" {
		return false
	}
	r := w.PlacedRect(index)
	r.W, r.H = h.sidebarArtExtent(g, art)
	for i := index + 1; i < len(w.Gadgets); i++ {
		product := w.Gadgets[i]
		if product.Active == 0 || !sidebarProductSlot(product) {
			continue
		}
		cover := w.PlacedRect(i)
		if r.X >= cover.X && r.Y >= cover.Y && r.X+r.W <= cover.X+cover.W && r.Y+r.H <= cover.Y+cover.H {
			return true
		}
	}
	return false
}
