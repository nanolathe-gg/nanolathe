package main

import (
	"cmp"
	"slices"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// Composite cells are a Modern host policy (DESIGN_INTERFACE_HUD_INPUT §3.3).
// Recognize a complete, unambiguous tiling of one 64px square in the resolved
// source GUI, never a relationship inferred from product names or unit stats.
func (h *retailBattleHUD) sidebarBuildCells(w *gui.Window, art *formats.GAF, page int, indices []int) []sidebarBuildCell {
	type candidate struct {
		bounds  gui.Rect
		indices []int
	}
	var candidates []candidate
	eligible := make([]bool, len(w.Gadgets))
	for _, i := range indices {
		eligible[i] = true
	}
	for _, i := range indices {
		r := w.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 || r.W > 64 || r.H > 64 || r.W == 64 && r.H == 64 {
			continue
		}
		bounds := gui.Rect{X: r.X, Y: r.Y, W: 64, H: 64}
		var members []int
		area, valid := int64(0), true
		for j, g := range w.Gadgets {
			if g.Active == 0 || g.Kind != gui.KindButton || g.CommonAttribs&4 == 0 || sidebarEmptySlot(g.Name) {
				continue
			}
			child := w.PlacedRect(j)
			if !sidebarRectsOverlap(bounds, child) {
				continue
			}
			aw, ah := h.sidebarArtExtent(g, art)
			if !eligible[j] || child.W <= 0 || child.H <= 0 || child.X < bounds.X || child.Y < bounds.Y || child.X+child.W > bounds.X+64 || child.Y+child.H > bounds.Y+64 || aw > child.W || ah > child.H {
				valid = false
				break
			}
			for _, previous := range members {
				if sidebarRectsOverlap(child, w.PlacedRect(previous)) {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
			members = append(members, j)
			area += int64(child.W) * int64(child.H)
		}
		if valid && len(members) > 1 && area == 64*64 {
			candidates = append(candidates, candidate{bounds, members})
		}
	}
	// A run of adjacent narrow buttons can admit several different tilings.
	// Do not choose one arbitrarily; all intersecting candidates stay individual.
	memberships := make([]int, len(w.Gadgets))
	for _, c := range candidates {
		for _, i := range c.indices {
			memberships[i]++
		}
	}
	grouped := make([]bool, len(w.Gadgets))
	var cells []sidebarBuildCell
	for _, c := range candidates {
		unique := true
		for _, i := range c.indices {
			if memberships[i] != 1 {
				unique = false
				break
			}
		}
		if !unique {
			continue
		}
		cell := sidebarBuildCell{page: page, bounds: c.bounds}
		// Keep authored child record precedence for shortcuts and the widget tree.
		for _, i := range c.indices {
			r := w.PlacedRect(i)
			r.X, r.Y = r.X-c.bounds.X, r.Y-c.bounds.Y
			cell.products = append(cell.products, sidebarProduct{source: sidebarGadgetSource{w, art, i}, rect: r})
			grouped[i] = true
		}
		cells = append(cells, cell)
	}
	for _, i := range indices {
		if !grouped[i] {
			cells = append(cells, sidebarBuildCell{page: page, bounds: w.PlacedRect(i), products: []sidebarProduct{{source: sidebarGadgetSource{w, art, i}, rect: gui.Rect{W: 64, H: 64}}}})
		}
	}
	slices.SortStableFunc(cells, func(a, b sidebarBuildCell) int {
		if y := cmp.Compare(a.bounds.Y, b.bounds.Y); y != 0 {
			return y
		}
		if x := cmp.Compare(a.bounds.X, b.bounds.X); x != 0 {
			return x
		}
		return cmp.Compare(a.products[0].source.index, b.products[0].source.index)
	})
	return cells
}

func sidebarRectsOverlap(a, b gui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
