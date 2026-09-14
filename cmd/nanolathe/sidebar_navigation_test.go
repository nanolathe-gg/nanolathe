package main

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func sidebarNavigationFixture(t *testing.T) (*battleSession, *client.Client) {
	t.Helper()
	b, cl, sources := expandedSidebarFixture(t)
	third := cloneGUIWindow(sources[1])
	n := 12
	for i := range third.Gadgets {
		g := &third.Gadgets[i]
		if g.CommonAttribs&4 == 0 {
			continue
		}
		product := *b.cat.Units[g.Name]
		g.Name = fmt.Sprintf("product%d", n)
		product.CanonicalKey, product.UnitName = g.Name, g.Name
		b.cat.Units[g.Name] = &product
		b.cat.BuildMenus["armfav"].Buttons = append(b.cat.BuildMenus["armfav"].Buttons, g.Name)
		n++
	}
	b.hud.windows["armfav3"] = third
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage.PageCount = 4 })
	cl.Resize(1280, 780)
	expandedWindow(t, b)
	return b, cl
}

func visibleSidebarProducts(w *gui.Window) []string {
	type product struct {
		name string
		x, y int32
	}
	var products []product
	for i, g := range w.Gadgets {
		if g.CommonAttribs&4 != 0 {
			r := w.PlacedRect(i)
			products = append(products, product{g.Name, r.X, r.Y})
		}
	}
	slices.SortFunc(products, func(a, b product) int {
		if a.y != b.y {
			return int(a.y - b.y)
		}
		return int(a.x - b.x)
	})
	names := make([]string, len(products))
	for i, p := range products {
		names[i] = p.name
	}
	return names
}

func assertSidebarProducts(t *testing.T, b *battleSession, start, end int) {
	t.Helper()
	var want []string
	for i := start; i < end; i++ {
		want = append(want, fmt.Sprintf("product%d", i))
	}
	if got := visibleSidebarProducts(expandedWindow(t, b)); !reflect.DeepEqual(got, want) {
		t.Fatalf("visible products %v, want %v", got, want)
	}
}

// These are the actual retained button and keyboard dispatch seams. The new
// view must not silently return to authored six-slot navigation between inputs.
func TestAdaptiveSidebarNavigationUsesVisiblePages(t *testing.T) {
	b, cl := sidebarNavigationFixture(t)
	initial, _ := b.currentSnapshot()
	page, count := b.buildPageNavigationState(initial)
	if page != 1 || count != 4 {
		t.Fatalf("initial visible range = %d/%d", page, count)
	}
	click := func(name string) {
		t.Helper()
		w := expandedWindow(t, b)
		paletteCallbackClick(t, b, cl, w, expandedIndex(t, w, name), false, false)
	}
	assertSidebarProducts(t, b, 0, 8)
	click("NEXT")
	assertSidebarProducts(t, b, 8, 16)
	click("NEXT")
	assertSidebarProducts(t, b, 16, 18)
	click("NEXT")
	assertSidebarProducts(t, b, 0, 8)
	click("PREV")
	assertSidebarProducts(t, b, 16, 18)

	// The period/comma producers include Orders; the buttons above do not.
	b.nextBuildPage()
	f, _ := b.currentSnapshot()
	if state, ok := b.hud.expandedSidebarPaging(b, f); !ok || state.Page != 0 || state.Remembered != 3 {
		t.Fatalf("Orders did not remember the visible build page: %+v (%t)", state, ok)
	}
	w := expandedWindow(t, b)
	orders, _ := b.hud.sidebarGadgetVerdict(w, w.Gadgets[expandedIndex(t, w, "ORDERS")], f, true, b.cat)
	build, _ := b.hud.sidebarGadgetVerdict(w, w.Gadgets[expandedIndex(t, w, "BUILD")], f, true, b.cat)
	if orders.stage != 1 || build.stage != 0 {
		t.Fatal("tab stages followed committed authored bits instead of local page selection")
	}
	assertSidebarProducts(t, b, 16, 18)
	click("BUILD")
	assertSidebarProducts(t, b, 16, 18)
	b.prevBuildPage()
	assertSidebarProducts(t, b, 8, 16)
	click("ORDERS")
	click("BUILD")
	assertSidebarProducts(t, b, 8, 16)

	paletteCallbackToken(t, b, cl, '2')
	assertSidebarProducts(t, b, 0, 8)
	paletteCallbackToken(t, b, cl, '3')
	assertSidebarProducts(t, b, 8, 16)
	paletteCallbackToken(t, b, cl, '9')
	assertSidebarProducts(t, b, 8, 16)
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("presentation paging submitted a simulation command")
	}
	current, _ := b.currentSnapshot()
	if current.CommandPage.Page != initial.CommandPage.Page || current.Units[0].Flags != initial.Units[0].Flags {
		t.Fatal("presentation paging changed committed authored page state")
	}

	// SwitchAlt still routes the other digit arm to squad recall.
	b.switchAlt = true
	b.routeDigit(2, true, false, cl)
	assertSidebarProducts(t, b, 0, 8)
	b.routeDigit(2, false, false, cl)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanGroupRecall {
		t.Fatalf("SwitchAlt group arm dispatched %+v", pending)
	}
}

func TestAdaptiveSidebarNavigationFallsBackToAuthoredPages(t *testing.T) {
	b, cl := sidebarNavigationFixture(t)
	cl.SetEnhanced(false)
	b.nextBuildPage()
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanBuildPage || pending[0].BuildPage.Page != 2 {
		t.Fatalf("Classic navigation lost its authored page command: %+v", pending)
	}
}

func TestAdaptiveSidebarPaintsLocalTabSelection(t *testing.T) {
	b, cl := sidebarNavigationFixture(t)
	entries := map[string]*formats.GAFEntry{}
	for _, name := range []string{"BUILD", "ORDERS"} {
		entry := &formats.GAFEntry{Name: name}
		for range 2 {
			entry.Frames = append(entry.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{1}}})
		}
		entries[name] = entry
	}
	for _, w := range b.hud.windows {
		for i := range w.Gadgets {
			g := &w.Gadgets[i]
			if entry := entries[commandButtonName(g.Name)]; entry != nil {
				g.Attribs, g.Stages = 0x100, 2
				g.ButtonArtResolved, g.ButtonArt = true, entry
			}
		}
	}
	cl.Resize(1280, 781)
	if err := b.dispatchBuildPageCued(0); err != nil {
		t.Fatal(err)
	}
	w := expandedWindow(t, b)
	cl.SetUIStage(sidebarDrawStage{b})
	trace := &sidebarSpriteTrace{}
	cl.RecordFrame().Replay(trace)
	for _, name := range []string{"BUILD", "ORDERS"} {
		stage := 0
		if name == "ORDERS" {
			stage = 1
		}
		r := w.PlacedRect(expandedIndex(t, w, name))
		found := false
		for _, sprite := range trace.sprites {
			found = found || sprite.X == r.X && sprite.Y == r.Y && sprite.Frame == entries[name].Frames[stage].Frame
		}
		if !found {
			t.Fatalf("%s did not draw its local selection stage %d", name, stage)
		}
	}
}
