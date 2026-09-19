package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Adaptive pages are presentation state. Authored page numbers still identify
// the source windows [07 R-HUD-03 §6]; they never become adaptive page commands.
type sidebarPagingState struct{ Page, Count, Remembered int }

type sidebarRowPaging struct {
	state                                           sidebarPagingState
	flat                                            bool
	definition                                      *content.UnitDef
	builder                                         pool.Handle
	selection                                       []pool.Handle
	authoredPage, authoredRemembered, authoredCount int
	anchor, capacity                                int
	anchorSource                                    sidebarGadgetSource
	starts                                          []int
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
		if len(p.starts) >= page {
			p.anchor = p.starts[page-1]
		}
	}
	h.retireExpandedSidebar()
	return true, true
}
