package main

// Command spacing belongs to the approved Modern host layout (HUD §3.3).
// Keep source row gaps and outer margins. Only a surface too short for one
// build row compresses them, retaining at least one pixel per positive gap.
type sidebarCommandGap struct {
	at     int32 // compact command Y; -1 is the gap between tabs and build grid
	pixels int32
}

type sidebarCommandMargins struct{ grid, above, below, bottom int32 }

func (h *retailBattleHUD) sidebarCommandMargins(build, orders *sidebarCommandScaffold, extra []sidebarProduct) sidebarCommandMargins {
	var tabs, products, arrows, footer sidebarSpan
	for _, item := range extra {
		_, height := h.sidebarArtExtent(item.source.window.Gadgets[item.source.index], item.source.art)
		arrows.include(item.rect.Y, item.rect.Y+height)
	}
	for i, g := range build.window.Gadgets {
		if i == 0 || g.Active == 0 {
			continue
		}
		r := build.window.PlacedRect(i)
		_, height := h.sidebarArtExtent(g, build.art)
		if sidebarProductSlot(g) {
			products.include(r.Y, r.Y+height)
		}
	}
	for _, i := range build.indices {
		g := build.window.Gadgets[i]
		r := build.window.PlacedRect(i)
		_, height := h.sidebarArtExtent(g, build.art)
		if name := commandButtonName(g.Name); name == "BUILD" || name == "ORDERS" {
			tabs.include(r.Y, r.Y+height)
		} else if !sidebarNavigation(g) {
			footer.include(r.Y, r.Y+height)
		}
	}
	var m sidebarCommandMargins
	if tabs.set && products.set {
		m.grid = max(0, products.top-tabs.end)
	}
	if arrows.set && products.set {
		m.above = max(0, arrows.top-products.end)
	}
	if arrows.set && footer.set {
		m.below = max(0, footer.top-arrows.end)
	}
	var bottom int32
	for _, i := range orders.indices {
		r := orders.window.PlacedRect(i)
		_, height := h.sidebarArtExtent(orders.window.Gadgets[i], orders.art)
		bottom = max(bottom, r.Y+height)
	}
	m.bottom = max(0, orders.window.Rect.Y+orders.window.Rect.H-bottom)
	return m
}

func (c *sidebarProductCatalog) sidebarSpacing(height int32) ([]sidebarCommandGap, bool) {
	budget := height - 128 - c.upperHeight - c.lowerHeight - 64
	var total, minimum int32
	for _, gap := range c.spacing {
		total += gap.pixels
		if gap.pixels > 0 {
			minimum++
		}
	}
	if budget < minimum {
		return nil, false
	}
	if total <= budget {
		return c.spacing, true
	}
	gaps := append([]sidebarCommandGap(nil), c.spacing...)
	// Cumulative integer apportionment preserves the exact gap budget without
	// starving later gaps or introducing floating-point rounding drift.
	var weight, assigned int64
	for i, gap := range gaps {
		if gap.pixels == 0 {
			continue
		}
		weight += int64(gap.pixels - 1)
		next := weight * int64(budget-minimum) / int64(total-minimum)
		gaps[i].pixels = 1 + int32(next-assigned)
		assigned = next
	}
	return gaps, true
}
