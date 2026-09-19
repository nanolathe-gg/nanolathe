package main

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func TestCompositeBuildCellPreservesChildrenAndPaging(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	source := sources[1]
	// Five ordinary cells precede this composite, placing it in the final
	// available slot of page one: its four children must never be split.
	sources[0].Gadgets[9].Active = 0
	// Deliberately author the south button first, preserving widget precedence
	// independently of the containing cell's spatial reading order.
	rects := []gui.Rect{{X: 16, Y: 32, W: 32, H: 32}, {X: 16, W: 32, H: 32}, {W: 16, H: 64}, {X: 48, W: 16, H: 64}}
	keys := []byte{'s', 'n', 'w', 'e'}
	frames := make([]*formats.GAFFrame, 4)
	for n, r := range rects {
		g := &source.Gadgets[4+n]
		r.Y += 27
		g.Rect, g.QuickKey = r, keys[n]
		frames[n] = &formats.GAFFrame{Width: uint16(r.W), Height: uint16(r.H), Pixels: make([]byte, r.W*r.H)}
		g.ButtonArtResolved = true
		g.ButtonArt = &formats.GAFEntry{Name: g.Name, Frames: []formats.GAFFrameRef{{Frame: frames[n]}}}
	}
	source.Gadgets[8].Rect = gui.Rect{X: 64, Y: 27, W: 64, H: 64}
	source.Gadgets[9].Rect = gui.Rect{Y: 91, W: 64, H: 64}
	cl.Resize(640, 704)
	f, _ := b.currentSnapshot()
	w := expandedWindow(t, b)
	catalog := b.hud.sidebarProductCatalog(b.cat, b.cat.Units["armfav"], 3)
	if len(catalog.cells) != 8 || len(catalog.cells[5].products) != 4 {
		t.Fatalf("composite not counted as one cell: %+v", catalog.cells)
	}
	if b.hud.sidebarPaging.state.Count != 3 {
		t.Fatalf("pages counted child buttons: %+v", b.hud.sidebarPaging.state)
	}
	first := w.PlacedRect(expandedIndex(t, w, "product8"))
	for n, r := range rects {
		name := source.Gadgets[4+n].Name
		i := expandedIndex(t, w, name)
		got := w.PlacedRect(i)
		r.X, r.Y = r.X+first.X, r.Y+first.Y
		if got != r {
			t.Fatalf("child %s geometry=%+v want %+v", name, got, r)
		}
		b.hud.updateHoveredGadget(b, f, got.X+got.W/2, got.Y+got.H/2)
		if index, hover := b.hud.hoveredGadgetSource(); index != i || hover != name {
			t.Fatalf("child %s hover=%s/%d", name, hover, index)
		}
		paletteCallbackClick(t, b, cl, w, i, false, false)
		pending := b.sess.PendingHumanCommands()
		if len(pending) == 0 || pending[len(pending)-1].Kind != session.HumanFactoryBuild || pending[len(pending)-1].FactoryBuild.Product != name {
			t.Fatalf("child click %s dispatched %+v", name, pending)
		}
		before := len(pending)
		paletteCallbackToken(t, b, cl, rune(keys[n]))
		pending = b.sess.PendingHumanCommands()
		if len(pending) != before+1 || pending[len(pending)-1].FactoryBuild.Product != name {
			t.Fatalf("child shortcut %s dispatched %+v", name, pending)
		}
	}
	cl.SetUIStage(sidebarDrawStage{b})
	trace := &sidebarSpriteTrace{}
	cl.RecordFrame().Replay(trace)
	for n, art := range frames {
		found := false
		want := w.PlacedRect(expandedIndex(t, w, source.Gadgets[4+n].Name))
		for _, sprite := range trace.sprites {
			if sprite.Frame == art {
				found = true
				if sprite.Kind != drawlist.BlitScaled || sprite.Dst != (drawlist.Rect{X: want.X, Y: want.Y, W: want.W, H: want.H}) {
					t.Fatalf("child art stretched into parent: %+v", sprite)
				}
			}
		}
		if !found {
			t.Fatalf("child art %d not drawn", n)
		}
	}
	// A composite is indivisible even at a page boundary; resize anchors count
	// cells while all of their original products remain independently reachable.
	b.hud.selectExpandedSidebarPage(b, f, 2)
	old := sidebarVisibleProducts(expandedWindow(t, b))[0]
	cl.Resize(1024, 768)
	if !slices.Contains(sidebarVisibleProducts(expandedWindow(t, b)), old) {
		t.Fatalf("resize lost %s", old)
	}
}

func TestCompositeBuildCellRejectsAmbiguousGeometry(t *testing.T) {
	cases := []struct {
		name  string
		rects []gui.Rect
	}{
		{"gap", []gui.Rect{{W: 16, H: 64}, {X: 32, W: 32, H: 64}}},
		{"overlap", []gui.Rect{{W: 48, H: 64}, {X: 32, W: 32, H: 64}}},
		{"duplicate", []gui.Rect{{W: 32, H: 64}, {W: 32, H: 64}, {X: 32, W: 32, H: 64}}},
		{"crossing product", []gui.Rect{{W: 32, H: 64}, {X: 32, W: 32, H: 64}, {X: 48, Y: 32, W: 32, H: 64}}},
		{"multiple tilings", []gui.Rect{{W: 16, H: 64}, {X: 16, W: 16, H: 64}, {X: 32, W: 16, H: 64}, {X: 48, W: 16, H: 64}, {X: 64, W: 16, H: 64}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}}
			var indices []int
			for _, r := range tc.rects {
				indices = append(indices, len(w.Gadgets))
				w.Gadgets = append(w.Gadgets, gui.Gadget{Kind: gui.KindButton, Active: 1, CommonAttribs: 4, Name: "product", Rect: r})
			}
			cells := (&retailBattleHUD{}).sidebarBuildCells(w, nil, 1, indices)
			if len(cells) != len(tc.rects) {
				t.Fatalf("ambiguous layout collapsed %d buttons into %d cells", len(tc.rects), len(cells))
			}
		})
	}
}
