package main

import (
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// This immutable extraction is independent of surface size and local paging.
// GUI records, including download replacements, own identity [07 R-HUD-03 §6].
type sidebarProductCatalogKey struct {
	catalog    *content.Catalog
	definition *content.UnitDef
	count      int
}
type sidebarProductCatalog struct {
	cells                    []sidebarBuildCell
	pages                    []*sidebarCommandScaffold
	orders                   *sidebarCommandScaffold
	upperHeight, lowerHeight int32
	commands, tabs           []sidebarProduct
	groups                   map[sidebarAssociation]int32
	spacing                  []sidebarCommandGap
	safe                     bool
}
type sidebarAssociation struct {
	window *gui.Window
	group  int32
}

type sidebarBuildCell struct {
	page     int
	bounds   gui.Rect // source-space ordering; children carry cell-relative geometry
	products []sidebarProduct
}
type sidebarProduct struct {
	source sidebarGadgetSource
	rect   gui.Rect
}
type sidebarCommandScaffold struct {
	window  *gui.Window
	art     *formats.GAF
	indices []int
}

// Products have no geometric constraints here. Only command widgets can make
// the host extension unsafe; an opaque linked widget retains its authored GUI.
func (h *retailBattleHUD) sidebarScaffold(w *gui.Window, art *formats.GAF) (*sidebarCommandScaffold, bool) {
	if w == nil || len(w.Gadgets) == 0 {
		return nil, false
	}
	s := &sidebarCommandScaffold{window: w, art: art}
	for i, g := range w.Gadgets {
		if i == 0 || g.Kind == gui.KindFont || g.Active == 0 || sidebarProductSlot(g) || h.coveredSidebarPlaceholder(w, art, i) {
			continue
		}
		if g.Kind != gui.KindButton || g.Link != "" || g.Attribs&0x1800 != 0 {
			return nil, false
		}
		r := w.PlacedRect(i)
		aw, _ := h.sidebarArtExtent(g, art)
		if r.W <= 0 || r.H <= 0 || r.X < 0 || r.X+aw > 128 {
			return nil, false
		}
		s.indices = append(s.indices, i)

	}
	return s, true
}

// Widget shortcuts fold ASCII letters only [07 R-WGT-01 §3]. Preserve the
// source bytes; their spelling does not make otherwise identical pages differ.
func sidebarQuickKey(key byte) byte {
	if key >= 'a' && key <= 'z' {
		return key - ('a' - 'A')
	}
	return key
}

// Flattening may combine products from several authored pages. Retained controls
// must agree in behavior; shared controls use the canonical Orders shortcut.
// Other differences still reject composition so no later-page control is lost.
func sidebarCommandsMatch(a, b, orders *sidebarCommandScaffold) bool {
	if len(a.indices) != len(b.indices) {
		return false
	}
	groups := make(map[int32]int32)
	reverse := make(map[int32]int32)
	for n, i := range a.indices {
		j := b.indices[n]
		x, y := a.window.Gadgets[i], b.window.Gadgets[j]
		sameShortcut := sidebarQuickKey(x.QuickKey) == sidebarQuickKey(y.QuickKey)
		// Shared controls are replaced by the Orders source in the combined
		// panel, so its shortcut is authoritative on every local page (HUD §3.3).
		for _, j := range orders.indices {
			if strings.EqualFold(x.Name, orders.window.Gadgets[j].Name) {
				sameShortcut = true
				break
			}
		}
		if !strings.EqualFold(x.Name, y.Name) || x.Attribs != y.Attribs || x.CommonAttribs != y.CommonAttribs || !sameShortcut || x.Stages != y.Stages || x.Status != y.Status || x.GrayedOut != y.GrayedOut || x.Text != y.Text || !slices.Equal(x.Labels, y.Labels) {
			return false
		}
		if mapped, ok := groups[x.Assoc]; ok && mapped != y.Assoc {
			return false
		}
		if mapped, ok := reverse[y.Assoc]; ok && mapped != x.Assoc {
			return false
		}
		groups[x.Assoc], reverse[y.Assoc] = y.Assoc, x.Assoc
	}
	return true
}

func (h *retailBattleHUD) sidebarProductCatalog(cat *content.Catalog, def *content.UnitDef, count int) *sidebarProductCatalog {
	key := sidebarProductCatalogKey{cat, def, count}
	if cached, ok := h.sidebarProducts[key]; ok {
		return cached
	}
	if h.sidebarProducts == nil {
		h.sidebarProducts = make(map[sidebarProductCatalogKey]*sidebarProductCatalog)
	}
	c := &sidebarProductCatalog{safe: true, pages: make([]*sidebarCommandScaffold, count)}
	h.sidebarProducts[key] = c
	start := 1
	if def.HasPageZeroGUI {
		start = 0
	}
	for page := start; page < count; page++ {
		w, art := h.sidebarBuildPage(cat, def, page)
		scaffold, safe := h.sidebarScaffold(w, art)
		if !safe {
			c.safe = false
			return c
		}
		c.pages[page] = scaffold
		var indices []int
		for i, g := range w.Gadgets {
			if g.Kind != gui.KindButton || g.Active == 0 || g.CommonAttribs&4 == 0 || sidebarEmptySlot(g.Name) {
				continue
			}
			if product, ok := cat.Unit(g.Name); !ok || product == nil {
				continue
			}
			indices = append(indices, i)
		}
		c.cells = append(c.cells, h.sidebarBuildCells(w, art, page, indices)...)

	}
	if def.HasPageZeroGUI {
		c.orders = c.pages[0]
	} else {
		w, art := h.loadWindow(strings.ToLower(sideNamePrefix(h.side)) + "gen")
		c.orders, c.safe = h.sidebarScaffold(w, art)
	}
	if c.safe {
		for page := 2; page < count; page++ {
			if !sidebarCommandsMatch(c.pages[1], c.pages[page], c.orders) {
				c.safe = false
				return c
			}
		}
	}
	if c.safe {
		c.safe = h.sidebarCombinedCommands(c)
	}
	return c
}

// The Orders source owns the full command panel, including the common footer.
// Numbered-page-only controls (usually page arrows) precede that panel. Compact
// empty vertical bands without changing horizontal geometry or overlapping rows.
// This is the Modern host layout policy in HUD §3.3, not a retail layout rule.
func (h *retailBattleHUD) sidebarCombinedCommands(c *sidebarProductCatalog) bool {
	build := c.pages[0]
	if len(c.pages) > 1 {
		build = c.pages[1]
	}
	if build == nil || c.orders == nil {
		return false
	}
	seen := make(map[string]bool)
	var orders, extra []sidebarProduct
	collect := func(s *sidebarCommandScaffold, dst *[]sidebarProduct) {
		for _, i := range s.indices {
			g := s.window.Gadgets[i]
			name := strings.ToUpper(g.Name)
			if seen[name] {
				continue
			}
			p := sidebarProduct{source: sidebarGadgetSource{s.window, s.art, i}, rect: s.window.PlacedRect(i)}
			if command := commandButtonName(g.Name); command == "BUILD" || command == "ORDERS" {
				c.tabs = append(c.tabs, p)
			} else {
				*dst = append(*dst, p)
			}
		}
	}
	collect(c.orders, &orders)
	for _, i := range c.orders.indices {
		seen[strings.ToUpper(c.orders.window.Gadgets[i].Name)] = true
	}
	collect(build, &extra)
	pack := func(items []sidebarProduct, offset int32, gaps bool) int32 {
		indices := make([]int, len(items))
		for i := range indices {
			indices[i] = i
		}
		slices.SortStableFunc(indices, func(i, j int) int { return int(items[i].rect.Y - items[j].rect.Y) })
		var height, previousEnd int32
		for start := 0; start < len(indices); {
			top := items[indices[start]].rect.Y
			if gaps && start > 0 {
				c.spacing = append(c.spacing, sidebarCommandGap{at: offset + height, pixels: top - previousEnd})
			}
			end, last := top, start
			for last < len(indices) && (last == start || items[indices[last]].rect.Y < end) {
				p := items[indices[last]]
				_, ah := h.sidebarArtExtent(p.source.window.Gadgets[p.source.index], p.source.art)
				end = max(end, p.rect.Y+ah)
				last++
			}
			for _, i := range indices[start:last] {
				items[i].rect.Y += height - top
			}
			height += end - top
			previousEnd = end
			start = last
		}
		return height
	}
	margins := h.sidebarCommandMargins(build, c.orders, extra)
	c.upperHeight = pack(c.tabs, 0, false)
	extraHeight := pack(extra, 0, true)
	c.lowerHeight = extraHeight + pack(orders, extraHeight, true)
	c.spacing = append(c.spacing,
		sidebarCommandGap{at: -1, pixels: margins.grid},
		sidebarCommandGap{at: 0, pixels: margins.above},
		sidebarCommandGap{at: extraHeight, pixels: margins.below},
		sidebarCommandGap{at: c.lowerHeight, pixels: margins.bottom})
	for i := range orders {
		orders[i].rect.Y += extraHeight
	}
	c.commands = append(extra, orders...)
	// Match duplicate command associations before remapping source-local groups.
	// This keeps supplementary orders in the same radio group as shared STOP.
	groups := make(map[sidebarAssociation]int32)
	var next int32
	for _, i := range c.orders.indices {
		g := c.orders.window.Gadgets[i]
		k := sidebarAssociation{c.orders.window, g.Assoc}
		if _, ok := groups[k]; !ok {
			next++
			groups[k] = next
		}
	}
	for _, source := range c.pages {
		if source == nil || source == c.orders {
			continue
		}
		reverse := make(map[int32]int32)
		for _, i := range source.indices {
			g := source.window.Gadgets[i]
			for _, j := range c.orders.indices {
				other := c.orders.window.Gadgets[j]
				if !strings.EqualFold(g.Name, other.Name) {
					continue
				}
				k := sidebarAssociation{source.window, g.Assoc}
				group := groups[sidebarAssociation{c.orders.window, other.Assoc}]
				if old, ok := groups[k]; ok && old != group {
					return false
				}
				if old, ok := reverse[group]; ok && old != g.Assoc {
					return false
				}
				reverse[group] = g.Assoc
				groups[k] = group
			}
		}
	}
	c.groups = groups
	return true
}

func (h *retailBattleHUD) sidebarProductsWindow(b *battleSession, f *frame.Frame, base *gui.Window) *gui.Window {
	if base == nil || b == nil || b.cl == nil || b.cat == nil || f == nil || f.CommandPage.PageCount < 1 {
		return nil
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok || view.Owner != h.owner {
		return nil
	}
	def, ok := b.cat.Unit(view.DefName)
	if !ok || def == nil || !def.Builder {
		return nil
	}
	c := h.sidebarProductCatalog(b.cat, def, int(f.CommandPage.PageCount))
	if !c.safe || len(c.cells) == 0 {
		return nil
	}
	width, height := b.cl.Size()
	// Reserve the complete command panel before allocating any build cells.
	spacing, fits := c.sidebarSpacing(int32(height))
	if !fits {
		return nil
	}
	gridGap, lowerHeight := int32(0), c.lowerHeight
	for _, gap := range spacing {
		if gap.at < 0 {
			gridGap += gap.pixels
		} else {
			lowerHeight += gap.pixels
		}
	}
	capacity := int((int32(height)-128-c.upperHeight-gridGap-lowerHeight)/64) * 2
	if capacity < 2 {
		return nil
	}
	p := &h.sidebarPaging
	reseed := !p.flat || p.definition != def || p.builder != f.CommandPage.Builder || !slices.Equal(p.selection, f.Selection.Handles) || p.authoredPage != int(f.CommandPage.Page) || p.authoredRemembered != buildButtonPage(f) || p.authoredCount != int(f.CommandPage.PageCount)
	key := expandedSidebarKey{base: base, definition: def, width: int32(width), height: int32(height), page: int(f.CommandPage.Page), count: int(f.CommandPage.PageCount), remembered: buildButtonPage(f), localPage: p.state.Page, flat: true, transport: f.CommandPage.IsTransport, builder: f.CommandPage.Builder}
	if !reseed && key == h.expandedSidebar.key && h.expandedSidebar.window != nil {
		return h.expandedSidebar.window
	}
	if reseed {
		*p = sidebarRowPaging{flat: true, definition: def, builder: f.CommandPage.Builder, selection: append([]pool.Handle(nil), f.Selection.Handles...), authoredPage: key.page, authoredRemembered: key.remembered, authoredCount: key.count, state: sidebarPagingState{Page: key.page}}
		seed := key.page
		if seed == 0 {
			seed = key.remembered
		}
		for i, cell := range c.cells {
			if cell.page == seed {
				p.anchor = i
				break
			}
		}
	}
	// Fitted pages count authored row groups; flat pages count logical cells.
	// Translate through record identity when a resize changes layout kind.
	if !reseed && len(p.starts) != 0 {
		p.anchor = 0
		for i, cell := range c.cells {
			for _, product := range cell.products {
				if product.source == p.anchorSource {
					p.anchor = i
				}
			}
		}
	}
	p.capacity = capacity
	p.starts = nil
	p.state.Count = 1 + (len(c.cells)+capacity-1)/capacity
	p.state.Remembered = 1 + p.anchor/capacity
	if p.state.Page != 0 {
		p.state.Page = p.state.Remembered
	}
	start := (p.state.Remembered - 1) * capacity
	end := min(start+capacity, len(c.cells))
	p.anchor = start
	p.anchorSource = c.cells[start].products[0].source
	// The same command panel remains visible on every local page.
	scaffold := c.orders
	h.retireExpandedSidebar()
	l := &h.expandedSidebar
	l.window = cloneGUIWindow(scaffold.window)
	l.window.OriginX, l.window.OriginY = 0, 128
	l.window.Rect = gui.Rect{Y: 128, W: 128, H: int32(height) - 128}
	l.window.Gadgets = nil
	appendGadget := func(source sidebarGadgetSource, r gui.Rect) {
		g := source.window.Gadgets[source.index]
		g.Rect = r
		g.Rect.Y -= 128
		g.Labels = append([]string(nil), g.Labels...)
		l.window.Gadgets = append(l.window.Gadgets, g)
		l.sources = append(l.sources, source)
	}
	appendGadget(sidebarGadgetSource{scaffold.window, scaffold.art, 0}, l.window.Rect)
	l.window.Gadgets[0].Rect = l.window.Rect
	for _, item := range c.tabs {
		r := item.rect
		r.Y += 128
		appendGadget(item.source, r)
	}
	for _, item := range c.commands {
		r := item.rect
		for _, gap := range spacing {
			if gap.at >= 0 && gap.at <= item.rect.Y {
				r.Y += gap.pixels
			}
		}
		r.Y += int32(height) - lowerHeight
		appendGadget(item.source, r)
	}
	for n, cell := range c.cells[start:end] {
		x, y := int32(n%2)*64, 128+c.upperHeight+gridGap+int32(n/2)*64
		for _, product := range cell.products {
			r := product.rect
			r.X, r.Y = r.X+x, r.Y+y
			appendGadget(product.source, r)
		}
	}
	groups := make(map[sidebarAssociation]int32, len(c.groups))
	for k, v := range c.groups {
		groups[k] = v
	}
	next := int32(len(groups))
	for i, g := range l.window.Gadgets {
		if g.Kind != gui.KindButton {
			continue
		}
		k := sidebarAssociation{l.sources[i].window, g.Assoc}
		if _, ok := groups[k]; !ok {
			next++
			groups[k] = next
		}
		l.window.Gadgets[i].Assoc = groups[k]
	}
	l.window.Header.TotalGadgets = int16(len(l.window.Gadgets) - 1)
	key.localPage = p.state.Page
	l.key = key
	l.selection = append([]pool.Handle(nil), f.Selection.Handles...)
	return l.window
}

// Normalize art using its pixel aspect, retaining the source gadget separately
// for BUTTONS0 family selection. Hit, queue label and shade all use the cell.
func sidebarFittedArtRect(cell gui.Rect, art *formats.GAFFrame) gui.Rect {
	if art == nil || art.Width == 0 || art.Height == 0 {
		return cell
	}
	w, h := cell.W, cell.H
	if int64(art.Width)*int64(h) > int64(art.Height)*int64(w) {
		h = max(1, int32(int64(w)*int64(art.Height)/int64(art.Width)))
	} else {
		w = max(1, int32(int64(h)*int64(art.Width)/int64(art.Height)))
	}
	return gui.Rect{X: cell.X + (cell.W-w)/2, Y: cell.Y + (cell.H-h)/2, W: w, H: h}
}
