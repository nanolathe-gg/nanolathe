package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Adaptive pages are presentation state. Authored page numbers still identify
// the source windows [07 R-HUD-03 §6]; they never become adaptive page commands.
type sidebarPagingState struct{ Page, Count, Remembered int }

type sidebarRowPaging struct {
	state                                           sidebarPagingState
	definition                                      *content.UnitDef
	builder                                         pool.Handle
	selection                                       []pool.Handle
	authoredPage, authoredRemembered, authoredCount int
	anchor, capacity                                int
}

type sidebarRow struct {
	source  *sidebarPageBands
	page    int
	top     int32
	indices []int
}

func (h *retailBattleHUD) expandedSidebarPaging(b *battleSession, f *frame.Frame) (sidebarPagingState, bool) {
	if _, _, err := h.windowForRequired(b, f); err != nil {
		return sidebarPagingState{}, false
	}
	return h.sidebarPaging.state, h.expandedSidebar.window != nil
}

func (h *retailBattleHUD) selectExpandedSidebarPage(b *battleSession, f *frame.Frame, page int) (changed, handled bool) {
	state, active := h.expandedSidebarPaging(b, f)
	if !active {
		return false, false
	}
	if page < 0 || page >= state.Count || page == state.Page {
		return false, true
	}
	p := &h.sidebarPaging
	p.state.Page = page
	if page > 0 {
		p.state.Remembered = page
		p.anchor = (page - 1) * p.capacity
	}
	h.retireExpandedSidebar()
	return true, true
}

// A row uses authored hit rectangles; artwork may extend into the rail's edge,
// but must fit vertically in that row. Equal slot geometry makes a row boundary
// unambiguous even when records are sparse, repeated, or in a different order.
func (h *retailBattleHUD) sidebarRows(p *sidebarPageBands, page int) ([]sidebarRow, bool) {
	var rows []sidebarRow
	for i, g := range p.window.Gadgets {
		if p.kinds[i] != sidebarGrid {
			continue
		}
		if g.CommonAttribs&4 == 0 && !strings.EqualFold(g.Name, "IGPATCH") {
			return nil, false
		}
		r := p.window.PlacedRect(i)
		_, ah := h.sidebarArtExtent(g, p.art)
		if ah > r.H {
			return nil, false
		}
		j := 0
		for j < len(rows) && rows[j].top != r.Y {
			j++
		}
		if j == len(rows) {
			rows = append(rows, sidebarRow{source: p, page: page, top: r.Y})
		}
		rows[j].indices = append(rows[j].indices, i)
	}
	slices.SortStableFunc(rows, func(a, b sidebarRow) int { return cmp.Compare(a.top, b.top) })
	return rows, len(rows) > 0
}

func sidebarRowSlots(row sidebarRow) []gui.Rect {
	var slots []gui.Rect
	for _, i := range row.indices {
		r := row.source.window.PlacedRect(i)
		r = gui.Rect{X: r.X, W: r.W, H: r.H}
		if !slices.Contains(slots, r) {
			slots = append(slots, r)
		}
	}
	slices.SortStableFunc(slots, func(a, b gui.Rect) int { return cmp.Compare(a.X, b.X) })
	return slots
}

// Every build source must supply the same non-product controls and spacing.
// Their artwork/fonts may differ: the first visible row chooses the source
// scaffold. Unknown widgets or differing footer shapes retain authored pages.
func sidebarScaffoldsMatch(a, b *sidebarPageBands) bool {
	for _, band := range []sidebarBand{sidebarTabs, sidebarArrows, sidebarFooter} {
		if a.spans[band] != b.spans[band] {
			return false
		}
	}
	var ai, bi []int
	for i, g := range a.window.Gadgets {
		if g.Kind == gui.KindButton && a.kinds[i] != sidebarGrid {
			ai = append(ai, i)
		}
	}
	for i, g := range b.window.Gadgets {
		if g.Kind == gui.KindButton && b.kinds[i] != sidebarGrid {
			bi = append(bi, i)
		}
	}
	if len(ai) != len(bi) {
		return false
	}
	for n, i := range ai {
		j := bi[n]
		x, y := a.window.Gadgets[i], b.window.Gadgets[j]
		if a.kinds[i] != b.kinds[j] || !strings.EqualFold(x.Name, y.Name) || a.window.PlacedRect(i) != b.window.PlacedRect(j) || x.Attribs != y.Attribs || x.Active != y.Active || x.CommonAttribs != y.CommonAttribs {
			return false
		}
	}
	return true
}

func (h *retailBattleHUD) sidebarRowsWindow(b *battleSession, f *frame.Frame, base *gui.Window, art *formats.GAF) *gui.Window {
	fallback := func() *gui.Window { h.retireExpandedSidebar(); h.sidebarPaging = sidebarRowPaging{}; return base }
	if base == nil || b == nil || b.cl == nil || !b.cl.Enhanced() || !b.expandedSidebarEnabled() || f == nil || f.CommandPage.PageCount < 2 || b.cat == nil {
		return fallback()
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok || view.Owner != h.owner {
		return fallback()
	}
	def, ok := b.cat.Unit(view.DefName)
	if !ok || def == nil || !def.Builder || def.HasPageZeroGUI {
		return fallback()
	}
	width, height := b.cl.Size()
	if height <= retailScreenH {
		return fallback()
	}
	p := &h.sidebarPaging
	reseed := p.definition != def || p.builder != f.CommandPage.Builder || !slices.Equal(p.selection, f.Selection.Handles) || p.authoredPage != int(f.CommandPage.Page) || p.authoredRemembered != buildButtonPage(f) || p.authoredCount != int(f.CommandPage.PageCount)
	key := expandedSidebarKey{base: base, definition: def, width: int32(width), height: int32(height), page: int(f.CommandPage.Page), count: int(f.CommandPage.PageCount), remembered: buildButtonPage(f), localPage: p.state.Page, transport: f.CommandPage.IsTransport, builder: f.CommandPage.Builder}
	if key == h.expandedSidebar.key && slices.Equal(h.expandedSidebar.selection, f.Selection.Handles) {
		if h.expandedSidebar.window == nil {
			return base
		}
		if !reseed {
			return h.expandedSidebar.window
		}
	}
	fallback = func() *gui.Window {
		h.retireExpandedSidebar()
		h.sidebarPaging = sidebarRowPaging{}
		key.localPage = 0
		h.expandedSidebar.key = key
		h.expandedSidebar.selection = append([]pool.Handle(nil), f.Selection.Handles...)
		return base
	}
	h.retireExpandedSidebar()
	var pages []*sidebarPageBands
	var rows []sidebarRow
	var slots []gui.Rect
	var pitch, rowHeight int32
	minimumRows := 0
	for page := 1; page < key.count; page++ {
		w, a := h.sidebarBuildPage(b.cat, def, page)
		bands, safe := h.sidebarBands(w, a, nil)
		if !safe || len(pages) > 0 && !sidebarScaffoldsMatch(pages[0], bands) {
			return fallback()
		}
		pageRows, safe := h.sidebarRows(bands, page)
		if !safe {
			return fallback()
		}
		minimumRows = max(minimumRows, len(pageRows))
		lastProductRow := -1
		for i, row := range pageRows {
			for _, index := range row.indices {
				g := bands.window.Gadgets[index]
				if g.CommonAttribs&4 != 0 && !strings.EqualFold(g.Name, "IGPATCH") {
					lastProductRow = i
				}
			}
			shape := sidebarRowSlots(row)
			if len(shape) > len(slots) {
				slots = shape
			}
			for _, r := range shape {
				if rowHeight != 0 && rowHeight != r.H {
					return fallback()
				}
				rowHeight = r.H
			}
			if i > 0 {
				delta := row.top - pageRows[i-1].top
				if delta < rowHeight || pitch != 0 && pitch != delta {
					return fallback()
				}
				pitch = delta
			}
		}
		pages = append(pages, bands)
		// Keep holes before or beside products, but template padding after the
		// last product must not create an empty adaptive page (HUD design §3.3).
		rows = append(rows, pageRows[:lastProductRow+1]...)
	}
	if len(rows) == 0 {
		return fallback()
	}
	for j, r := range slots {
		if j > 0 && slots[j-1].X+slots[j-1].W > r.X {
			return fallback()
		}
	}
	for _, row := range rows {
		for _, r := range sidebarRowSlots(row) {
			if !slices.Contains(slots, r) {
				return fallback()
			}
		}
	}
	if pitch == 0 {
		pitch = rowHeight
	}
	build := pages[0]
	ordersWindow, ordersArt := h.loadWindow(strings.ToLower(sideNamePrefix(h.side)) + "gen")
	orders, safe := h.sidebarBands(ordersWindow, ordersArt, build)
	if !safe || orders.spans[sidebarTabs].end > build.spans[sidebarGrid].top {
		return fallback()
	}
	for _, page := range pages {
		if !h.sidebarFootersCompatible(page, orders, f) || !h.sidebarFootersCompatible(orders, page, f) {
			return fallback()
		}
	}
	orderHeight := orders.spans[sidebarOrders].height()
	var orderGap int32
	if orders.spans[sidebarOrders].set {
		orderGap = orders.spans[sidebarFooter].top - orders.spans[sidebarOrders].end
	}
	footerHeight := max(build.spans[sidebarFooter].height(), orders.spans[sidebarFooter].height())
	// Reserve the authored product-to-arrow and arrow-to-footer spacing, the
	// complete extra-orders block, its footer gap, and the common command block.
	fixed := build.spans[sidebarFooter].top - build.spans[sidebarGrid].height() + orderHeight + orderGap + footerHeight
	capacity := int((int32(height) - fixed + pitch - rowHeight) / pitch)
	if capacity < minimumRows {
		return fallback()
	}
	if reseed {
		*p = sidebarRowPaging{definition: def, builder: f.CommandPage.Builder, selection: append([]pool.Handle(nil), f.Selection.Handles...), authoredPage: key.page, authoredRemembered: key.remembered, authoredCount: key.count}
		seed := key.page
		if seed == 0 {
			seed = key.remembered
		}
		for i, row := range rows {
			if row.page == seed {
				p.anchor = i
				break
			}
		}
		p.state.Page = key.page
	}
	p.capacity = capacity
	p.state.Count = 1 + (len(rows)+capacity-1)/capacity
	p.state.Remembered = 1 + p.anchor/capacity
	if p.state.Page != 0 {
		p.state.Page = p.state.Remembered
	}
	start := (p.state.Remembered - 1) * capacity
	end := min(start+capacity, len(rows))
	p.anchor = start
	visible := rows[start:end]
	skeleton := build
	build = visible[0].source
	primary, other := build, orders
	if p.state.Page == 0 {
		primary, other = orders, build
	}
	// Navigation and orders stay put on a partial final page. A builder with
	// fewer rows than one page needs no more space than its full row sequence.
	gridHeight := int32(min(capacity, len(rows))-1)*pitch + rowHeight
	extra := gridHeight - skeleton.spans[sidebarGrid].height()
	targets := [sidebarBandCount]int32{sidebarGrid: skeleton.spans[sidebarGrid].top, sidebarArrows: skeleton.spans[sidebarArrows].top + extra, sidebarOrders: skeleton.spans[sidebarFooter].top + extra, sidebarFooter: skeleton.spans[sidebarFooter].top + extra + orderHeight + orderGap}
	l := &h.expandedSidebar
	l.window = cloneGUIWindow(primary.window)
	l.window.Gadgets = nil
	// Retain source record order, including duplicate slots and accelerator
	// precedence. Rows only determine inclusion and the translation applied.
	appendSource := func(source *sidebarPageBands, primarySource bool, omitted map[string]bool, groups map[int32]int32, next *int32) {
		begin := len(l.window.Gadgets)
		for i, g := range source.window.Gadgets {
			band := source.kinds[i]
			var delta int32
			if band == sidebarGrid {
				found := false
				for n, row := range visible {
					if row.source == source && slices.Contains(row.indices, i) {
						delta = targets[sidebarGrid] + int32(n)*pitch - row.top
						found = true
						break
					}
				}
				if !found {
					continue
				}
			} else if !primarySource {
				if source != other || band == sidebarUnused || band == sidebarTabs || band == sidebarArrows && primary.spans[sidebarArrows].set || band != sidebarArrows && sidebarOmitted(g, omitted) {
					continue
				}
				delta = targets[band] - source.spans[band].top
			} else if band != sidebarUnused && band != sidebarTabs {
				delta = targets[band] - source.spans[band].top
			}
			r := source.window.PlacedRect(i)
			r.X -= l.window.OriginX
			r.Y += delta - l.window.OriginY
			g.Rect = r
			g.Labels = append([]string(nil), g.Labels...)
			l.window.Gadgets = append(l.window.Gadgets, g)
			l.sources = append(l.sources, sidebarGadgetSource{source.window, source.art})
		}
		sidebarRemapGroups(l.window.Gadgets[begin:], next, groups)
	}
	var next int32
	appendSource(primary, true, nil, nil, &next)
	omitted := sidebarExistingCommands(primary.window, f)
	groups, safe := sidebarSharedOrderGroups(l.window, other.window, omitted)
	if !safe {
		return fallback()
	}
	appendSource(other, false, omitted, groups, &next)
	for _, source := range pages {
		if source != primary && source != other {
			appendSource(source, false, nil, nil, &next)
		}
	}
	for i, g := range l.window.Gadgets {
		if g.Kind != gui.KindButton {
			continue
		}
		r := l.window.PlacedRect(i)
		right, bottom := max(l.window.Rect.X+l.window.Rect.W, r.X+r.W), max(l.window.Rect.Y+l.window.Rect.H, r.Y+r.H)
		l.window.Rect.X, l.window.Rect.Y = min(l.window.Rect.X, r.X), min(l.window.Rect.Y, r.Y)
		l.window.Rect.W, l.window.Rect.H = right-l.window.Rect.X, bottom-l.window.Rect.Y
	}
	l.window.Gadgets[0].Rect = l.window.Rect
	l.window.Header.TotalGadgets = int16(len(l.window.Gadgets) - 1)
	key.localPage = p.state.Page
	l.key = key
	l.selection = append([]pool.Handle(nil), f.Selection.Handles...)
	return l.window
}
