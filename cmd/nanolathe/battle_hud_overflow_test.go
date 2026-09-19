package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestGeneratedPageUsesAuthoredExtendedSlotsWithoutReplacingCommands(t *testing.T) {
	source := generatedPageFixture("guis/armdl.gui")
	for len(source.Gadgets) < 16 {
		source.Gadgets = append(source.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "IGPATCH3"})
	}
	source.Gadgets = append(source.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "ARMNEXT"})
	h := &retailBattleHUD{fs: vfs.New(), side: &content.SideDef{NamePrefix: "ARM"}, windows: map[string]*gui.Window{"armdl": source}}
	w, _, err := h.numberedPage("builder2", []frame.GeneratedProductPlacement{{ProductKey: "factory-eight", Button: 8}, {ProductKey: "factory-eleven", Button: 11}, {ProductKey: "must-not-replace-next", Button: 12}})
	if err != nil {
		t.Fatal(err)
	}
	if w.Gadgets[12].Name != "factory-eight" || w.Gadgets[15].Name != "factory-eleven" || w.Gadgets[16].Name != "ARMNEXT" {
		t.Fatalf("generated placements lost slot boundaries: %+v", w.Gadgets)
	}
	if source.Gadgets[12].Name != "IGPATCH3" {
		t.Fatal("template mutated")
	}
}

func TestOversizedSidebarPreservesOriginAndMixedProductGroups(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	for page, w := range sources[:2] {
		w.Rect = gui.Rect{Y: 0, W: 128, H: 640, RawY: 128}
		w.OriginY = 0
		w.Gadgets = w.Gadgets[:2]
		w.Gadgets[0].Rect = w.Rect
		preserveSidebarOrigin(w)
		if w.OriginY != 128 || w.PlacedRect(0).Y != 128 {
			t.Fatal("oversized header lost authored origin")
		}
		for n := 0; n < 6; n++ {
			w.Gadgets = append(w.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: fmt.Sprintf("product%d", page*6+n), Active: 1, CommonAttribs: 4, Rect: gui.Rect{X: int32(n%2) * 64, Y: int32(n/2) * 64, W: 64, H: 64}})
		}
		// Three products share a vertical group with unequal heights, widths and
		// starting positions; they must move together without changing hit shapes.
		w.Gadgets[3].Rect = gui.Rect{X: 64, W: 32, H: 32}
		w.Gadgets[5].Rect = gui.Rect{X: 64, Y: 32, W: 32, H: 32}
		w.Gadgets[7].Rect = gui.Rect{X: 96, W: 32, H: 64}
		w.Gadgets = append(w.Gadgets, []gui.Gadget{
			{Kind: gui.KindButton, Name: "PREV", Active: 1, Rect: gui.Rect{Y: 388, W: 44, H: 16}},
			{Kind: gui.KindButton, Name: "NEXT", Active: 1, Rect: gui.Rect{X: 64, Y: 388, W: 44, H: 16}},
			{Kind: gui.KindButton, Name: "MOVE", Active: 1, Rect: gui.Rect{Y: 607, W: 55, H: 31}},
		}...)
		// Padding accounts for the authored gap, without creating product pages.
		w.Gadgets = append(w.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "IGPATCH", Active: 1, Rect: gui.Rect{Y: 192, W: 64, H: 192}})
	}
	original := cloneGUIWindow(sources[0])
	for _, modern := range []bool{false, true} {
		cl.SetEnhanced(modern)
		cl.Resize(640, 480)
		f, _ := b.currentSnapshot()
		state, ok := b.hud.expandedSidebarPaging(b, f)
		if !ok {
			t.Fatal("oversized mixed layout has no pager")
		}
		seen := map[string]bool{}
		for page := 1; page < state.Count; page++ {
			b.hud.selectExpandedSidebarPage(b, f, page)
			w := expandedWindow(t, b)
			for i, g := range w.Gadgets {
				if g.CommonAttribs&4 == 0 {
					continue
				}
				r := w.PlacedRect(i)
				if r.Y < 128 || r.Y+r.H > 480 || w.HitTest(r.X+r.W/2, r.Y+r.H/2) != i {
					t.Fatalf("inaccessible product %s: %+v", g.Name, r)
				}
				if seen[g.Name] {
					t.Fatalf("repeated %s", g.Name)
				}
				seen[g.Name] = true
			}
		}
		if len(seen) != 12 {
			t.Fatalf("lost products: %v", seen)
		}
		// A resize keeps the first visible authored group in the new partition.
		anchor := b.hud.sidebarPaging.anchor
		cl.Resize(1024, 768)
		expandedWindow(t, b)
		if b.hud.sidebarPaging.anchor > anchor {
			t.Fatal("resize skipped remembered group")
		}
	}
	if !reflect.DeepEqual(original, sources[0]) {
		t.Fatal("source geometry mutated")
	}
}

func TestOversizedSidebarFindsLaterAuthoredCanvas(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	// Page one fits the retail surface. A later authored canvas positions its
	// entire control scaffold farther down, with the same separable spacing.
	sources[1].Rect.H = 640
	sources[1].Gadgets[0].Rect = sources[1].Rect
	for i := range sources[1].Gadgets {
		if sources[1].Gadgets[i].Kind == gui.KindButton {
			sources[1].Gadgets[i].Rect.Y += 300
		}
	}
	cl.Resize(640, 480)
	f, _ := b.currentSnapshot()
	for _, modern := range []bool{false, true} {
		cl.SetEnhanced(modern)
		state, active := b.hud.expandedSidebarPaging(b, f)
		if !active {
			t.Fatal("later oversized authored page does not activate mandatory pager")
		}
		seen := map[string]bool{}
		for page := 1; page < state.Count; page++ {
			b.hud.selectExpandedSidebarPage(b, f, page)
			w := expandedWindow(t, b)
			for i, g := range w.Gadgets {
				if g.CommonAttribs&4 == 0 {
					continue
				}
				r := w.PlacedRect(i)
				if r.Y < 128 || r.Y+r.H > 480 || w.HitTest(r.X+r.W/2, r.Y+r.H/2) != i {
					t.Fatalf("inaccessible product %s: %+v", g.Name, r)
				}
				seen[g.Name] = true
			}
		}
		if len(seen) != 12 {
			t.Fatalf("lost authored products: %v", seen)
		}
	}
}

func TestOverflowCoveredPlaceholdersAndCustomOrders(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	cl.Resize(640, 480)
	cl.SetEnhanced(false)
	source := sources[0]
	source.Rect.H = 640
	source.Gadgets[0].Rect = source.Rect
	fallback := &formats.GAFEntry{Name: "BUTTONS0"}
	for i := 0; i < 8; i++ {
		size := uint16(16)
		if i >= 4 {
			size = 321
		}
		fallback.Frames = append(fallback.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: size, Height: 16}})
	}
	placeholder := gui.Gadget{Kind: gui.KindButton, Name: "UNBOUND", Active: 1, Rect: gui.Rect{Y: 27, W: 16, H: 16}, ButtonArtResolved: true, ButtonArt: fallback}
	source.Gadgets = append(source.Gadgets[:4:4], append([]gui.Gadget{placeholder}, source.Gadgets[4:]...)...)
	if width, height := b.hud.sidebarArtExtent(placeholder, nil); width != 16 || height != 16 {
		t.Fatalf("unrelated fallback family changed extent: %d,%d", width, height)
	}
	if !b.hud.coveredSidebarPlaceholder(source, nil, 4) {
		t.Fatal("covered unbound placeholder retained")
	}
	source.Gadgets[4].QuickKey = 'q'
	if b.hud.coveredSidebarPlaceholder(source, nil, 4) {
		t.Fatal("bound control omitted")
	}
	source.Gadgets[4].QuickKey = 0
	source.Gadgets[4].Rect.X = 120
	if b.hud.coveredSidebarPlaceholder(source, nil, 4) {
		t.Fatal("partly exposed control omitted")
	}
	source.Gadgets[4].Rect.X = 0
	w := expandedWindow(t, b)
	for _, g := range w.Gadgets {
		if g.Name == "UNBOUND" {
			t.Fatal("composed page kept covered placeholder")
		}
	}
	// Custom Orders has unused template padding and one genuine product. Only
	// padding is omitted; the real authored product and source identity survive.
	orders := cloneGUIWindow(sources[2])
	orders.Name = "guis/armfav0.gui"
	orders.Gadgets = append(orders.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "IGPATCH3", Active: 1, Rect: gui.Rect{W: 64, H: 640}}, gui.Gadget{Kind: gui.KindButton, Name: "product0", Active: 1, CommonAttribs: 4, Rect: gui.Rect{X: 64, Y: 80, W: 32, H: 32}})
	b.cat.Units["armfav"].HasPageZeroGUI = true
	b.hud.windows["armfav0"] = orders
	f, _ := b.currentSnapshot()
	b.hud.selectExpandedSidebarPage(b, f, 0)
	w = expandedWindow(t, b)
	index := expandedIndex(t, w, "product0")
	r := w.PlacedRect(index)
	origin, _ := b.hud.sidebarSource(w, index, nil)
	verdict, _ := b.hud.sidebarGadgetVerdict(w, w.Gadgets[index], f, false, b.cat)
	if origin != orders || r.Y < 128 || r.Y+r.H > 480 || w.HitTest(r.X+r.W/2, r.Y+r.H/2) != index || verdict.grey {
		t.Fatalf("custom Orders lost usable product: source%p rect%+v verdict%+v", origin, r, verdict)
	}
	for _, g := range w.Gadgets {
		if sidebarEmptySlot(g.Name) {
			t.Fatal("custom Orders kept empty padding")
		}
	}
	b.cat.Units["product0"].BMCode = 0
	if !hudConsumeClick(b.hud, b, r.X+r.W/2, r.Y+r.H/2) || b.battleState().Input.BuildDef != "product0" {
		t.Fatal("custom Orders product did not arm placement")
	}
}
