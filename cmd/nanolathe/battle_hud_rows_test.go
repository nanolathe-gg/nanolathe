package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func sidebarRowsFixture(t *testing.T, height int) (*battleSession, *client.Client, []*gui.Window) {
	t.Helper()
	b, cl, sources := expandedSidebarFixture(t)
	pages := append([]*gui.Window(nil), sources[:2]...)
	for page := 2; page < 4; page++ {
		w := cloneGUIWindow(sources[0])
		for slot := 0; slot < 6; slot++ {
			name := fmt.Sprintf("product%d", page*6+slot)
			g := &w.Gadgets[4+slot]
			g.Name = name
			product := *b.cat.Units["product0"]
			product.UnitName, product.CanonicalKey = name, name
			b.cat.Units[name] = &product
			b.cat.BuildMenus["armfav"].Buttons = append(b.cat.BuildMenus["armfav"].Buttons, name)
		}
		b.hud.windows[fmt.Sprintf("armfav%d", page+1)] = w
		pages = append(pages, w)
	}
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage.PageCount = 5 })
	cl.Resize(1280, height)
	return b, cl, append(pages, sources[2])
}

func sidebarVisibleProducts(w *gui.Window) []string {
	var products []string
	for _, g := range w.Gadgets {
		if g.CommonAttribs&4 != 0 {
			products = append(products, g.Name)
		}
	}
	return products
}

func TestExpandedSidebarRowsPartitionWithoutRepeatsOrCap(t *testing.T) {
	for _, capacity := range []int{4, 5, 6, 7, 12} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			b, _, _ := sidebarRowsFixture(t, 496+capacity*64)
			f, _ := b.currentSnapshot()
			state, ok := b.hud.expandedSidebarPaging(b, f)
			if !ok || state.Count != 1+(12+capacity-1)/capacity {
				t.Fatalf("paging state %+v active=%v", state, ok)
			}
			var seen []string
			controls := make(map[string]gui.Rect)
			for page := 1; page < state.Count; page++ {
				_, handled := b.hud.selectExpandedSidebarPage(b, f, page)
				if !handled {
					t.Fatal("adaptive navigation fell through")
				}
				w := expandedWindow(t, b)
				for _, name := range []string{"PREV", "REPAIR", "MOVE", "ATTACK"} {
					r := w.PlacedRect(expandedIndex(t, w, name))
					if page == 1 {
						controls[name] = r
					} else if r != controls[name] {
						t.Fatalf("page %d moved %s: %+v want %+v", page, name, r, controls[name])
					}
				}
				products := sidebarVisibleProducts(w)
				first := (page - 1) * capacity * 2
				last := min(first+capacity*2, 24)
				var want []string
				for i := first; i < last; i++ {
					want = append(want, fmt.Sprintf("product%d", i))
				}
				if !slices.Equal(products, want) {
					t.Fatalf("page %d products=%v want=%v", page, products, want)
				}
				seen = append(seen, products...)
				if expandedWindow(t, b) != w {
					t.Fatal("unchanged resolution replaced the panel")
				}
			}
			if len(seen) != 24 || len(b.sess.PendingHumanCommands()) != 0 {
				t.Fatal("local pagination lost rows or sent a simulation command")
			}
			for _, page := range []int{-1, state.Count, state.Count - 1} {
				if changed, handled := b.hud.selectExpandedSidebarPage(b, f, page); changed || !handled {
					t.Fatalf("no-op target %d changed=%v handled=%v", page, changed, handled)
				}
			}
		})
	}
}

func TestExpandedSidebarRowsResizeAnchorAndLocalSelection(t *testing.T) {
	b, cl, _ := sidebarRowsFixture(t, 752)
	f, _ := b.currentSnapshot()
	b.hud.selectExpandedSidebarPage(b, f, 3)
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product16")
	r := w.PlacedRect(i)
	cl.Input().Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	b.hud.servicePaletteFrame(b, cl.Input(), false)
	p := b.hud.palettePanels[w]
	if p == nil || p.CaptureIndex() != i {
		t.Fatal("first visible row was not captured")
	}
	cl.Resize(1280, 816)
	state, ok := b.hud.expandedSidebarPaging(b, f)
	if !ok || state.Page != 2 || state.Remembered != 2 || p.CaptureIndex() != -1 {
		t.Fatalf("resize state %+v active=%v capture=%d", state, ok, p.CaptureIndex())
	}
	w = expandedWindow(t, b)
	if got := sidebarVisibleProducts(w); len(got) != 10 || got[0] != "product10" || !slices.Contains(got, "product16") {
		t.Fatalf("resize lost previous first row: %v", got)
	}
	cl.Input().Mouse.ResetEdges()
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	b.hud.servicePaletteFrame(b, cl.Input(), false)
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("resize release activated stale capture")
	}
	b.hud.selectExpandedSidebarPage(b, f, 0)
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.Tick++ })
	f, _ = b.currentSnapshot()
	state, ok = b.hud.expandedSidebarPaging(b, f)
	if !ok || state.Page != 0 || state.Remembered != 2 {
		t.Fatalf("published frame lost local Orders memory: %+v", state)
	}
	b.hud.selectExpandedSidebarPage(b, f, state.Remembered)
	state, _ = b.hud.expandedSidebarPaging(b, f)
	if state.Page != 2 {
		t.Fatal("Build did not restore remembered visible page")
	}
	// A new selection reseeds from the committed authored page even when its
	// builder and immutable definition happen to be the same.
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.Selection.Handles = append(f.Selection.Handles, f.CommandPage.Builder) })
	f, _ = b.currentSnapshot()
	state, _ = b.hud.expandedSidebarPaging(b, f)
	if state.Page != 1 {
		t.Fatalf("selection did not reseed: %+v", state)
	}
	paletteCallbackFrame(t, b, func(f *frame.Frame) {
		f.CommandPage.Page = 4
		f.Units[0].Flags = hud.EncodePageBits(f.Units[0].Flags, 4)
	})
	f, _ = b.currentSnapshot()
	state, _ = b.hud.expandedSidebarPaging(b, f)
	if state.Page != 2 {
		t.Fatalf("external source page did not locate its first row: %+v", state)
	}
	cl.SetEnhanced(false)
	if _, active := b.hud.expandedSidebarPaging(b, f); active || b.hud.sidebarPaging.state.Count != 0 {
		t.Fatal("classic mode retained local paging")
	}
}

func TestExpandedSidebarRowsKeepPartialAndDuplicateRecords(t *testing.T) {
	b, _, sources := sidebarRowsFixture(t, 816)
	// Delete just one last-row record. Its left neighbour remains a complete
	// authored row with an absent right cell, rather than pulling another row up.
	sources[3].Gadgets = append(sources[3].Gadgets[:9], sources[3].Gadgets[10:]...)
	duplicate := sources[1].Gadgets[4]
	duplicate.Name = "product7"
	sources[1].Gadgets = append(sources[1].Gadgets, duplicate)
	f, _ := b.currentSnapshot()
	w := expandedWindow(t, b)
	products := sidebarVisibleProducts(w)
	if len(products) != 11 || products[6] != "product6" || products[len(products)-1] != "product7" {
		t.Fatalf("source record order changed: %v", products)
	}
	i := expandedIndex(t, w, "product6")
	r := w.PlacedRect(i)
	if got := b.hud.buttonAt(b, r.X+1, r.Y+1); got != i {
		t.Fatalf("duplicate record replaced first-source precedence: %d want %d", got, i)
	}
	b.hud.selectExpandedSidebarPage(b, f, 3)
	w = expandedWindow(t, b)
	if got := sidebarVisibleProducts(w); !slices.Equal(got, []string{"product20", "product21", "product22"}) {
		t.Fatalf("partial final page wrapped or collapsed: %v", got)
	}
	left := w.PlacedRect(expandedIndex(t, w, "product20"))
	last := w.PlacedRect(expandedIndex(t, w, "product22"))
	if last.X != left.X || last.Y-left.Y != 64 {
		t.Fatalf("partial row moved cells: first=%+v last=%+v", left, last)
	}
}

func TestExpandedSidebarRowsRejectAndCacheUnsafeSources(t *testing.T) {
	for _, kind := range []string{"column", "pitch", "footer art", "widget"} {
		t.Run(kind, func(t *testing.T) {
			b, cl, sources := sidebarRowsFixture(t, 816)
			bad := sources[3]
			switch kind {
			case "column":
				bad.Gadgets[4].Rect.X++
			case "pitch":
				bad.Gadgets[6].Rect.Y++
				bad.Gadgets[7].Rect.Y++
			case "footer art":
				bad.Gadgets[len(bad.Gadgets)-1].Rect.H++
			case "widget":
				bad.Gadgets[4].CommonAttribs = 0
				bad.Gadgets[4].Name = "CUSTOM"
			}
			f, _ := b.currentSnapshot()
			if _, ok := b.hud.expandedSidebarPaging(b, f); ok {
				t.Fatal("unsafe layout was composed")
			}
			// Source definitions are immutable in production. Repairing this test
			// source without changing the cache key must leave the cached failure.
			sources[3] = cloneGUIWindow(sources[2])
			b.hud.windows["armfav4"] = sources[3]
			if _, ok := b.hud.expandedSidebarPaging(b, f); ok {
				t.Fatal("failed compatibility was rescanned")
			}
			cl.Resize(1280, 817)
			if _, ok := b.hud.expandedSidebarPaging(b, f); !ok {
				t.Fatal("surface change failed to retry compatibility")
			}
		})
	}
}

func TestExpandedSidebarRowsCrossShortSourceWithCanonicalSpacing(t *testing.T) {
	b, _, sources := sidebarRowsFixture(t, 688)
	// The second source has only its first row, but retains its authored
	// navigation/footer. A visible page begins here and continues in source three.
	sources[1].Gadgets = append(sources[1].Gadgets[:6], sources[1].Gadgets[10:]...)
	f, _ := b.currentSnapshot()
	state, ok := b.hud.expandedSidebarPaging(b, f)
	if !ok || state.Count != 5 {
		t.Fatalf("short source paging: %+v active=%v", state, ok)
	}
	for _, page := range []int{2, 4} {
		b.hud.selectExpandedSidebarPage(b, f, page)
		w := expandedWindow(t, b)
		var gridBottom int32
		for i, g := range w.Gadgets {
			if g.CommonAttribs&4 != 0 {
				r := w.PlacedRect(i)
				gridBottom = max(gridBottom, r.Y+r.H)
			}
		}
		arrows := w.PlacedRect(expandedIndex(t, w, "PREV"))
		if page == 2 && arrows.Y-gridBottom != 3 {
			t.Fatalf("page %d grid-to-arrow gap=%d, want authored3", page, arrows.Y-gridBottom)
		}
		footer := w.PlacedRect(expandedIndex(t, w, "ATTACK"))
		if footer.Y+footer.H > 688 {
			t.Fatalf("page %d footer exceeded reserved height: %+v", page, footer)
		}
		if page == 2 {
			want := []string{"product6", "product7", "product12", "product13", "product14", "product15"}
			if got := sidebarVisibleProducts(w); !slices.Equal(got, want) {
				t.Fatalf("short source crossing=%v want=%v", got, want)
			}
		}
	}
}

func TestExpandedSidebarRowsExcludeTrailingTemplatePadding(t *testing.T) {
	for _, allBlank := range []bool{false, true} {
		t.Run(fmt.Sprint(allBlank), func(t *testing.T) {
			b, _, sources := sidebarRowsFixture(t, 816)
			// Empty sources can occur between populated sources as well as at
			// the end. Retain a hole inside a populated source, not its padding.
			for page, source := range sources[:4] {
				for i := 4; i < 10; i++ {
					if allBlank || page == 1 || page == 3 || page == 2 && i != 6 {
						g := &source.Gadgets[i]
						g.Name, g.CommonAttribs, g.GrayedOut = "IGPATCH", 0, 1
					}
				}
			}
			f, _ := b.currentSnapshot()
			state, active := b.hud.expandedSidebarPaging(b, f)
			if allBlank {
				if active {
					t.Fatal("all-empty sources created adaptive pages")
				}
				return
			}
			// Three rows from source one and two from source three exactly fill
			// this five-row view. Blank sources must not add another page.
			if !active || state.Count != 2 {
				t.Fatalf("placeholder paging=%+v active=%v", state, active)
			}
			w := expandedWindow(t, b)
			last := w.PlacedRect(expandedIndex(t, w, "product14"))
			first := w.PlacedRect(expandedIndex(t, w, "product0"))
			if last.Y-first.Y != 4*64 {
				t.Fatal("empty row before the last product was packed away")
			}
			for _, g := range sources[2].Gadgets[4:10] {
				if g.Name != "IGPATCH" && g.Name != "product14" {
					t.Fatal("composition changed source gadgets")
				}
			}
		})
	}
}

func TestExpandedSidebarRowsPreserveAuthoredPitchAcrossSources(t *testing.T) {
	b, _, sources := sidebarRowsFixture(t, 764)
	for _, source := range sources[:4] {
		for i := range source.Gadgets {
			g := &source.Gadgets[i]
			if g.CommonAttribs&4 != 0 {
				g.Rect.Y += int32((i-4)/2) * 4
			} else if g.Kind == gui.KindButton && g.Name != "BUILD" && g.Name != "ORDERS" {
				g.Rect.Y += 8
			}
		}
	}
	w := expandedWindow(t, b)
	products := sidebarVisibleProducts(w)
	if len(products) != 8 {
		t.Fatalf("authored pitch capacity=%v", products)
	}
	for product := 2; product < 8; product += 2 {
		previous := w.PlacedRect(expandedIndex(t, w, fmt.Sprintf("product%d", product-2)))
		current := w.PlacedRect(expandedIndex(t, w, fmt.Sprintf("product%d", product)))
		if current.Y-previous.Y != 68 {
			t.Fatalf("row %d lost its authored gap: %d", product/2, current.Y-previous.Y)
		}
	}
}
