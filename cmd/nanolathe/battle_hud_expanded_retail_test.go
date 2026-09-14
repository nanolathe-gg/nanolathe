//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

func TestRetailExpandedSidebarShowsTwoAuthoredCommanderPages(t *testing.T) {
	cs, err := openContent(Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, cat, err := newBattleSession(Options{Map: "ashap plateau", Seed: 7}, cs)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Owner == sess.LocalOwner && u.Def != nil && u.Def.Builder {
			u.Flags |= hud.SelectionFlag
			break
		}
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	// This capacity leaves a partial final page and previously exposed a third
	// page containing only the generated template's unused slots.
	cl, err := client.New(client.Options{Width: 1280, Height: 844})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetEnhanced(true)
	b := &battleSession{sess: sess, cat: cat, cl: cl}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	f := sess.Snapshot.Current()
	w, _, err := b.hud.windowForRequired(b, f)
	if err != nil || w == nil {
		t.Fatalf("commander window: %v", err)
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok {
		t.Fatal("no selected commander")
	}
	def, _ := cat.Unit(view.DefName)
	second, _ := b.hud.sidebarBuildPage(cat, def, hud.NextPageButton(int(f.CommandPage.Page), int(f.CommandPage.PageCount)))
	if second == nil {
		t.Fatal("second authored page failed to load")
	}
	first, _ := b.hud.sidebarBuildPage(cat, def, int(f.CommandPage.Page))
	var firstBottom, secondBottom int32
	secondTop := int32(1<<31 - 1)
	var arrows, repair, capture, move gui.Rect
	var authoredGap int32
	for i, source := range b.hud.expandedSidebar.sources {
		g := w.Gadgets[i]
		r := w.PlacedRect(i)
		if g.CommonAttribs&4 != 0 {
			if source.window == first {
				firstBottom = max(firstBottom, r.Y+r.H)
			}
			if source.window == second {
				secondTop, secondBottom = min(secondTop, r.Y), max(secondBottom, r.Y+r.H)
			}
		}
		if sidebarNavigation(g) && commandButtonName(g.Name) == "" {
			arrows = r
		}
		switch commandButtonName(g.Name) {
		case "REPAIR":
			repair = r
			original := source.window.PlacedRect(source.window.GadgetIndex(g.Name))
			for j, gad := range source.window.Gadgets {
				if commandButtonName(gad.Name) == "MOVE" {
					authoredGap = source.window.PlacedRect(j).Y - (original.Y + original.H)
				}
			}
		case "CAPTURE":
			capture = r
		case "MOVE":
			move = r
		}
	}
	if firstBottom != secondTop || secondBottom > arrows.Y || arrows.Y+arrows.H > repair.Y || capture.Y+capture.H > move.Y || repair.H == 0 || move.H == 0 {
		t.Fatalf("retail bands are not builds/arrows/orders/footer: first=%d second=%d..%d arrows=%+v repair=%+v capture=%+v move=%+v", firstBottom, secondTop, secondBottom, arrows, repair, capture, move)
	}
	if got := move.Y - (repair.Y + repair.H); got != authoredGap || authoredGap <= 0 {
		t.Fatalf("stock orders-to-footer gap = %d, want authored %d pixels", got, authoredGap)
	}
	// Walk every adaptive page against the actual source records. A taller
	// surface must neither repeat the early authored pages nor omit later ones.
	type productSource struct {
		window *gui.Window
		name   string
	}
	want, got := make(map[productSource]int), make(map[productSource]int)
	for page := 1; page < int(f.CommandPage.PageCount); page++ {
		source, _ := b.hud.sidebarBuildPage(cat, def, page)
		for _, g := range source.Gadgets {
			if g.CommonAttribs&4 != 0 && !strings.EqualFold(g.Name, "IGPATCH") {
				want[productSource{source, g.Name}]++
			}
		}
	}
	state, active := b.hud.expandedSidebarPaging(b, f)
	if !active {
		t.Fatal("retail adaptive paging unavailable")
	}
	controlRects := make(map[string]gui.Rect)
	for page := 1; page < state.Count; page++ {
		b.hud.selectExpandedSidebarPage(b, f, page)
		composed, _, err := b.hud.windowForRequired(b, f)
		if err != nil {
			t.Fatal(err)
		}
		products := 0
		for i, g := range composed.Gadgets {
			if g.CommonAttribs&4 != 0 && !strings.EqualFold(g.Name, "IGPATCH") {
				products++
				got[productSource{b.hud.expandedSidebar.sources[i].window, g.Name}]++
			}
			if commandButtonName(g.Name) != "" || sidebarNavigation(g) {
				r := composed.PlacedRect(i)
				if page == 1 {
					controlRects[g.Name] = r
				} else if r != controlRects[g.Name] {
					t.Fatalf("retail page %d moved %s: %+v want %+v", page, g.Name, r, controlRects[g.Name])
				}
			}
		}
		if products == 0 {
			t.Fatalf("retail adaptive page %d contains only placeholders", page)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("retail adaptive source coverage=%d want=%d", len(got), len(want))
	}
	for source, count := range want {
		if got[source] != count {
			t.Fatalf("retail product %s appeared %d times, want authored %d", source.name, got[source], count)
		}
	}

}
