package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// These authored fixtures lock the requested modern layout policy. They do
// not assert a retail expansion rule or infer products from their menu order.
func expandedSidebarFixture(t *testing.T) (*battleSession, *client.Client, []*gui.Window) {
	t.Helper()
	b, cl, _ := paletteCallbackFactory(t, nil, nil, 1, 3)
	button := func(name string, x, y, w, height int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindButton, Name: name, Active: 1, Rect: gui.Rect{X: x, Y: y, W: w, H: height}}
	}
	// Order buttons are toggles, as in both stock orders windows: the
	// dispatcher arms from the fired button's down-state [07 §9].
	order := func(name string, x, y, w, height int32) gui.Gadget {
		g := button(name, x, y, w, height)
		g.Attribs, g.Assoc = 0x40, 1
		return g
	}
	newWindow := func() *gui.Window {
		return &gui.Window{Rect: gui.Rect{Y: 128, W: 128, H: 352}, OriginY: 128, Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel}, {Kind: gui.KindFont, FileName: "source-font"},
			button("PREV", 8, 222, 44, 16), button("NEXT", 72, 222, 44, 16),
		}}
	}
	var sources []*gui.Window
	menu := &content.BuildMenuPage{}
	for page := 0; page < 2; page++ {
		w := newWindow()
		for slot := 0; slot < 6; slot++ {
			name := fmt.Sprintf("product%d", page*6+slot)
			product := *b.cat.Units["armsolar"]
			product.CanonicalKey, product.UnitName, product.BMCode = name, name, 1
			b.cat.Units[name] = &product
			menu.Buttons = append(menu.Buttons, name)
			g := button(name, int32(slot%2)*64, 27+int32(slot/2)*64, 64, 64)
			g.CommonAttribs, g.Assoc = 4, 7
			w.Gadgets = append(w.Gadgets, g)
		}
		w.Gadgets = append(w.Gadgets, button("ORDERS", 3, 4, 59, 19), button("BUILD", 65, 4, 59, 19), order("MOVE", 5, 247, 55, 31), order("ATTACK", 5, 317, 55, 31))
		w.Gadgets[len(w.Gadgets)-2].QuickKey = 'm'
		sources = append(sources, w)
	}
	orders := newWindow()
	orders.Gadgets[2].Kind, orders.Gadgets[3].Kind = gui.KindFont, gui.KindFont
	orders.Gadgets[2].Name, orders.Gadgets[3].Name = "FONT2", "FONT3"
	orders.Gadgets = append(orders.Gadgets, button("ORDERS", 3, 4, 59, 19), button("BUILD", 65, 4, 59, 19), order("MOVE", 5, 247, 55, 31), order("ATTACK", 5, 317, 55, 31), order("REPAIR", 5, 35, 55, 31), order("CAPTURE", 64, 207, 55, 31))
	orders.Gadgets[len(orders.Gadgets)-2].QuickKey = 'r'
	sources = append(sources, orders)
	b.cat.BuildMenus["armfav"] = menu
	b.hud.windows = map[string]*gui.Window{"armfav1": sources[0], "armfav2": sources[1], "gen": orders}
	paletteCallbackFrame(t, b, func(f *frame.Frame) {
		f.Units[0].Flags = hud.EncodePageBits(f.Units[0].Flags, 1)
		f.CommandPage.CanMove, f.CommandPage.CanAttack, f.CommandPage.CanRepair, f.CommandPage.CanCapture = true, true, true, true
	})
	cl.Resize(1280, 1080)
	cl.SetEnhanced(true)
	b.viewerStep(0, cl)
	b.hud.retireExpandedSidebar()
	b.hud.sidebarProducts = nil
	b.hud.sidebarPaging = sidebarRowPaging{}
	return b, cl, sources
}

func expandedWindow(t *testing.T, b *battleSession) *gui.Window {
	t.Helper()
	f, _ := b.currentSnapshot()
	w, _, err := b.hud.windowForRequired(b, f)
	if err != nil || w == nil {
		t.Fatalf("resolve expanded window: %v", err)
	}
	return w
}

func expandedIndex(t *testing.T, w *gui.Window, name string) int {
	t.Helper()
	for i, g := range w.Gadgets {
		if g.Name == name {
			return i
		}
	}
	t.Fatalf("missing expanded gadget %q", name)
	return -1
}

func TestExpandedSidebarSettingRetiresCaptureAndCache(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	b.shell = &gameShell{presentation: settings.DefaultPresentation()}
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product6")
	r := w.PlacedRect(i)
	cl.Input().Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	b.hud.servicePaletteFrame(b, cl.Input(), false)
	p := b.hud.palettePanels[w]
	if p == nil || p.CaptureIndex() != i {
		t.Fatal("product was not captured before the settings change")
	}
	b.shell.presentation.ExpandedSidebar = 0
	if expandedWindow(t, b) != sources[0] || p.CaptureIndex() != -1 || b.hud.expandedSidebar.window != nil {
		t.Fatal("disabling expansion did not restore the authored page and retire state")
	}
	b.shell.presentation.ExpandedSidebar = 1
	if expandedWindow(t, b) == w {
		t.Fatal("re-enabling expansion resurrected the old cached panel")
	}
	cl.Input().Mouse.ResetEdges()
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	b.hud.servicePaletteFrame(b, cl.Input(), false)
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("release after settings change revived a stale capture")
	}
}

func TestExpandedSidebarHeaderUnionAdmitsSupplementalButtons(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	sources[0].Rect.X, sources[0].Rect.W = 32, 64
	cl.Resize(1280, 1081)
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product6")
	r := w.PlacedRect(i)
	if !guiRectContains(w.Rect, r.X+1, r.Y+1) {
		t.Fatal("composed header excludes supplemental button")
	}
	paletteCallbackClick(t, b, cl, w, i, false, false)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].FactoryBuild.Product != "product6" {
		t.Fatalf("header union blocked pointer activation: %+v", pending)
	}
}

type sidebarDrawStage struct{ b *battleSession }

func (s sidebarDrawStage) DrawUI(c *client.Client, f client.UIFrame) {
	s.b.hud.drawSidePage(c, s.b, f.Committed)
}

type sidebarSpriteTrace struct {
	overlayTrace
	sprites []drawlist.Sprite
}

func (s *sidebarSpriteTrace) Sprite(sprite drawlist.Sprite) { s.sprites = append(s.sprites, sprite) }

func TestExpandedSidebarRecordsSourceArtAtHitRectangle(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	entry := &formats.GAFEntry{Name: "same-art-name", Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{Width: 64, Height: 64, Pixels: make([]byte, 64*64)}}}}
	sources[1].Gadgets[4].ButtonArtResolved = true
	sources[1].Gadgets[4].ButtonArt = entry
	cl.Resize(1280, 1081)
	w := expandedWindow(t, b)
	r := w.PlacedRect(expandedIndex(t, w, "product6"))
	cl.SetUIStage(sidebarDrawStage{b})
	trace := &sidebarSpriteTrace{}
	cl.RecordFrame().Replay(trace)
	for _, sprite := range trace.sprites {
		if sprite.Frame == entry.Frames[0].Frame {
			if sprite.Dst.X != r.X || sprite.Dst.Y != r.Y || sprite.Dst.W != 64 || sprite.Dst.H != 64 {
				t.Fatalf("art at %d,%d, hit rectangle %+v", sprite.Dst.X, sprite.Dst.Y, r)
			}
			return
		}
	}
	t.Fatal("appended source art was not recorded")
}

func TestExpandedSidebarFlatCoverageAndResize(t *testing.T) {
	b, cl, sources := sidebarRowsFixture(t, 480)
	// Read order differs from source record order; products may have arbitrary
	// dimensions and gaps. Neither holes nor inactive records occupy cells.
	sources[0].Gadgets[4].Rect.X = 64
	sources[0].Gadgets[5].Rect.X = 0
	sources[0].Gadgets[4].Rect.W = 32
	sources[0].Gadgets[4].Rect.H = 16
	sources[1].Gadgets[4].Name = "IGPATCH7"
	sources[1].Gadgets[5].Active = 0
	duplicate := sources[1].Gadgets[6]
	duplicate.Name = "product9"
	sources[1].Gadgets = append(sources[1].Gadgets, duplicate)
	b.hud.sidebarProducts = nil
	f, _ := b.currentSnapshot()
	want := []string{"product1", "product0", "product2", "product3", "product4", "product5", "product8", "product9", "product9", "product10", "product11"}
	for n := 12; n < 24; n++ {
		want = append(want, fmt.Sprintf("product%d", n))
	}
	for _, height := range []int{480, 688, 1080} {
		cl.Resize(1280, height)
		state, ok := b.hud.expandedSidebarPaging(b, f)
		if !ok {
			t.Fatalf("no flat pager at %d", height)
		}
		var got []string
		controls := map[string]gui.Rect{}
		for page := 1; page < state.Count; page++ {
			b.hud.selectExpandedSidebarPage(b, f, page)
			w := expandedWindow(t, b)
			if w.PlacedRect(expandedIndex(t, w, "CAPTURE")).Y >= w.PlacedRect(expandedIndex(t, w, "MOVE")).Y {
				t.Fatal("supplementary Orders are not above normal commands")
			}
			for i, g := range w.Gadgets {
				if g.CommonAttribs&4 != 0 {
					r := w.PlacedRect(i)
					if r.Y+r.H > w.PlacedRect(expandedIndex(t, w, "PREV")).Y {
						t.Fatal("build cells overlap the reserved command panel")
					}
				}
			}
			for _, name := range []string{"PREV", "NEXT", "MOVE", "ATTACK", "REPAIR", "CAPTURE"} {
				r := w.PlacedRect(expandedIndex(t, w, name))
				if page == 1 {
					controls[name] = r
				} else if controls[name] != r {
					t.Fatalf("partial page moved %s", name)
				}
			}
			for i, g := range w.Gadgets {
				if g.CommonAttribs&4 == 0 {
					continue
				}
				r := w.PlacedRect(i)
				if r.W != 64 || r.H != 64 || r.Y < 128 || r.Y+r.H > int32(height) || w.HitTest(r.X+32, r.Y+32) != i {
					t.Fatalf("unusable cell %+v", r)
				}
				got = append(got, g.Name)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("coverage at %d=%v want %v", height, got, want)
		}
	}
	// Resizing locates the page containing the old first visible product.
	cl.Resize(640, 480)
	b.hud.expandedSidebarPaging(b, f)
	b.hud.selectExpandedSidebarPage(b, f, 3)
	old := sidebarVisibleProducts(expandedWindow(t, b))[0]
	catalog := b.hud.sidebarProducts[sidebarProductCatalogKey{b.cat, b.cat.Units["armfav"], 5}]
	cl.Resize(1024, 688)
	w := expandedWindow(t, b)
	if !slices.Contains(sidebarVisibleProducts(w), old) {
		t.Fatalf("resize lost %s", old)
	}
	if b.hud.sidebarProducts[sidebarProductCatalogKey{b.cat, b.cat.Units["armfav"], 5}] != catalog {
		t.Fatal("resize re-extracted product list")
	}
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("local paging submitted commands")
	}
}

func TestExpandedSidebarCombinedOrdersAndSourceInput(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	w := expandedWindow(t, b)
	product := expandedIndex(t, w, "product6")
	r := w.PlacedRect(product)
	f, _ := b.currentSnapshot()
	b.hud.updateHoveredGadget(b, f, r.X+1, r.Y+1)
	if index, name := b.hud.hoveredGadgetSource(); index != product || name != "product6" {
		t.Fatal("hover lost source identity")
	}
	source, _ := b.hud.sidebarSource(w, product, nil)
	if source != sources[1] {
		t.Fatal("product lost source font/art owner")
	}
	paletteCallbackClick(t, b, cl, w, product, false, true)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].Kind != session.HumanFactoryBuild || pending[len(pending)-1].FactoryBuild.Product != "product6" || pending[len(pending)-1].FactoryBuild.Count != 5 {
		t.Fatalf("factory dispatch=%+v", pending)
	}
	// Physical factory identities remain independent of CANBUILD.
	b.cat.BuildMenus["armfav"].Buttons = b.cat.BuildMenus["armfav"].Buttons[:6]
	if !b.hud.hitTestFor(b, r.X+1, r.Y+1) {
		t.Fatal("factory identity filtered through membership")
	}
	// A structure outside CANBUILD is equally live: a product slot greys only
	// when its name resolves to no definition [07 R-HUD-03 §6] (stock CORCS
	// installs CORSY and CORLLT without listing them).
	b.cat.Units["product6"].BMCode = 0
	if !b.hud.hitTestFor(b, r.X+1, r.Y+1) {
		t.Fatal("structure identity filtered through membership")
	}
	b.hud.selectExpandedSidebarPage(b, f, 0)
	w = expandedWindow(t, b)
	if len(sidebarVisibleProducts(w)) != 12 {
		t.Fatal("Orders hid remembered build cells")
	}
	paletteCallbackToken(t, b, cl, 'r')
	if b.battleState().Input.Latch != input.LatchRepair {
		t.Fatal("Orders shortcut bypassed shared service")
	}
	state, _ := b.hud.expandedSidebarPaging(b, f)
	if state.Page != 0 || state.Remembered != 1 {
		t.Fatalf("Orders memory=%+v", state)
	}
	b.hud.selectExpandedSidebarPage(b, f, state.Remembered)
	if len(sidebarVisibleProducts(expandedWindow(t, b))) != 12 {
		t.Fatal("BUILD did not restore products")
	}
}

func TestExpandedSidebarCustomOrdersAndNormalizedArt(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	def := b.cat.Units["armfav"]
	def.HasPageZeroGUI = true
	custom := cloneGUIWindow(sources[2])
	g := sources[0].Gadgets[4]
	g.Name = "product3"
	g.Rect = gui.Rect{X: 0, Y: 40, W: 128, H: 32}
	art := &formats.GAFFrame{Width: 128, Height: 32, Pixels: make([]byte, 128*32)}
	g.ButtonArtResolved = true
	g.ButtonArt = &formats.GAFEntry{Name: "product3", Frames: []formats.GAFFrameRef{{Frame: art}}}
	custom.Gadgets = append(custom.Gadgets, g)
	b.hud.windows["armfav0"] = custom
	b.hud.sidebarProducts = nil
	b.hud.retireExpandedSidebar()
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product3")
	r := w.PlacedRect(i)
	if r.X != 0 || r.W != 64 || r.H != 64 {
		t.Fatalf("custom page-zero product missing from head: %+v", r)
	}
	cl.SetUIStage(sidebarDrawStage{b})
	trace := &sidebarSpriteTrace{}
	cl.RecordFrame().Replay(trace)
	found := false
	for _, sprite := range trace.sprites {
		if sprite.Frame == art {
			found = true
			if sprite.Kind != drawlist.BlitScaled || sprite.Dst.X != r.X || sprite.Dst.Y != r.Y+24 || sprite.Dst.W != 64 || sprite.Dst.H != 16 || sprite.Clip.W != 64 || sprite.Clip.H != 64 {
				t.Fatalf("aspect fit=%+v", sprite)
			}
		}
	}
	if !found {
		t.Fatal("custom source artwork absent")
	}
	f, _ := b.currentSnapshot()
	b.hud.selectExpandedSidebarPage(b, f, 0)
	w = expandedWindow(t, b)
	i = expandedIndex(t, w, "REPAIR")
	if source, _ := b.hud.sidebarSource(w, i, nil); source != custom {
		t.Fatal("custom Orders replaced by generic")
	}
	if len(sidebarVisibleProducts(w)) != 13 {
		t.Fatal("custom Orders changed the normalized product list")
	}
}

func TestExpandedSidebarUnsafeCommandsAndModeReset(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	sources[1].Gadgets[expandedIndex(t, sources[1], "MOVE")].Link = "other"
	b.hud.sidebarProducts = nil
	b.hud.retireExpandedSidebar()
	if expandedWindow(t, b) != sources[0] {
		t.Fatal("linked command did not use authored fallback")
	}
	sources[1].Gadgets[expandedIndex(t, sources[1], "MOVE")].Link = ""
	b.hud.sidebarProducts = nil
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product6")
	r := w.PlacedRect(i)
	cl.Input().Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	b.hud.servicePaletteFrame(b, cl.Input(), false)
	panel := b.hud.palettePanels[w]
	if panel == nil || panel.CaptureIndex() != i {
		t.Fatal("product capture absent")
	}
	cl.Resize(640, 480)
	expandedWindow(t, b)
	if panel.CaptureIndex() != -1 {
		t.Fatal("resize retained capture")
	}
	cl.SetEnhanced(false)
	if expandedWindow(t, b) != sources[0] || b.hud.sidebarPaging.state.Count != 0 {
		t.Fatal("Classic retained flat layout")
	}
	cl.SetEnhanced(true)
	f, _ := b.currentSnapshot()
	state, ok := b.hud.expandedSidebarPaging(b, f)
	if !ok || state.Page != 1 {
		t.Fatalf("mode switch failed to reseed: %+v", state)
	}
}

func TestExpandedSidebarOrdersRadioGroups(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	orders := sources[2]
	for i := range orders.Gadgets {
		g := &orders.Gadgets[i]
		if g.Name == "REPAIR" || g.Name == "ATTACK" {
			g.Assoc = 23
			g.Attribs = 0x10
		}
	}
	orders.Gadgets = append(orders.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "STOP", Active: 1, Assoc: 23, Attribs: 0x10, Rect: gui.Rect{X: 64, Y: 247, W: 54, H: 30}})
	for _, source := range sources[:2] {
		for i := range source.Gadgets {
			if source.Gadgets[i].Name == "ATTACK" {
				source.Gadgets[i].Assoc, source.Gadgets[i].Attribs = 7, 0x10
			}
		}
		stop := orders.Gadgets[len(orders.Gadgets)-1]
		stop.Assoc = 7
		source.Gadgets = append(source.Gadgets, stop)
	}
	b.hud.sidebarProducts = nil
	f, _ := b.currentSnapshot()
	b.hud.selectExpandedSidebarPage(b, f, 1)
	w := expandedWindow(t, b)
	repair, attack, stop := expandedIndex(t, w, "REPAIR"), expandedIndex(t, w, "ATTACK"), expandedIndex(t, w, "STOP")
	paletteCallbackClick(t, b, cl, w, repair, false, false)
	p := b.hud.palettePanels[w]
	if p.DownAt(repair) != 1 {
		t.Fatal("Orders radio not armed")
	}
	paletteCallbackClick(t, b, cl, w, attack, false, false)
	if p.DownAt(repair) != 0 || p.DownAt(attack) != 1 {
		t.Fatal("source group did not release prior order")
	}
	b.resetOrderLatch()
	if p.DownAt(attack) != 0 || p.DownAt(stop) != 0 || b.battleState().Input.Latch != input.LatchNormal {
		t.Fatal("idle reset left radio down")
	}
}

func TestExpandedSidebarGeneratedProductsKeepAuthoredIdentity(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	delete(b.hud.windows, "armfav2")
	template := cloneGUIWindow(sources[1])
	for i := 4; i < 10; i++ {
		template.Gadgets[i].Name = "IGPATCH"
		template.Gadgets[i].GrayedOut = 1
	}
	template.Gadgets[4].Rect.X, template.Gadgets[5].Rect.X = 64, 0
	b.hud.windows["dl"] = template
	for _, placement := range []struct {
		slot    uint8
		product string
	}{{5, "product11"}, {0, "product7"}, {0, "product6"}} {
		b.cat.DownloadPlacements = append(b.cat.DownloadPlacements, content.DownloadMenuPlacement{Builder: "armfav", Product: placement.product, Menu: 3, Button: placement.slot, BuilderResolved: true, ProductResolved: true})
	}
	w := expandedWindow(t, b)
	if got := sidebarVisibleProducts(w); !slices.Equal(got, []string{"product0", "product1", "product2", "product3", "product4", "product5", "product6", "product11"}) {
		t.Fatalf("generated identity=%v", got)
	}
	i, j := expandedIndex(t, w, "product6"), expandedIndex(t, w, "product11")
	if w.PlacedRect(i).X != 0 || w.PlacedRect(j).X != 64 || w.PlacedRect(i).Y != w.PlacedRect(j).Y {
		t.Fatal("generated holes did not collapse")
	}
	source, _ := b.hud.sidebarSource(w, i, nil)
	if source == template || source.Gadgets[4].Name != "product6" || template.Gadgets[4].Name != "IGPATCH" {
		t.Fatal("download resolution changed identity or template")
	}
	paletteCallbackClick(t, b, cl, w, j, true, true)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].FactoryBuild.Product != "product11" || pending[len(pending)-1].FactoryBuild.Count != -5 {
		t.Fatalf("generated cancel=%+v", pending)
	}
}

func TestExpandedSidebarOverflowFallbackRetainsLocalPage(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	sources[0].Rect.H = 640
	sources[2].Gadgets[4].Kind = gui.KindLabel
	cl.Resize(640, 480)
	f, _ := b.currentSnapshot()
	state, active := b.hud.expandedSidebarPaging(b, f)
	if !active || state.Count < 3 || b.hud.expandedSidebar.key.flat {
		t.Fatalf("unsafe flat layout did not use fitted fallback: %+v", state)
	}
	if changed, handled := b.hud.selectExpandedSidebarPage(b, f, 2); !changed || !handled {
		t.Fatal("fallback cannot page")
	}
	w := expandedWindow(t, b)
	if next := expandedWindow(t, b); next != w || b.hud.sidebarPaging.state.Page != 2 {
		t.Fatal("flat preference reset overflow fallback")
	}
	cl.SetEnhanced(false)
	if next := expandedWindow(t, b); next == w || b.hud.sidebarPaging.state.Page != 1 {
		t.Fatal("renderer switch retained fallback paging")
	}
}

func TestExpandedSidebarArtFamilyUsesSourceGeometry(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	entry := formats.GAFEntry{Name: "BUTTONS0"}
	for _, size := range [][2]uint16{{32, 16}, {64, 64}} {
		for n := 0; n < 4; n++ {
			entry.Frames = append(entry.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: size[0], Height: size[1], Pixels: make([]byte, int(size[0])*int(size[1]))}})
		}
	}
	b.hud.common = &formats.GAF{Entries: []formats.GAFEntry{entry}}
	g := &sources[0].Gadgets[4]
	g.Rect.W, g.Rect.H = 32, 16
	g.ButtonArtResolved, g.ExternalArtResolved = false, false
	g.ButtonArt = nil
	w := expandedWindow(t, b)
	r := w.PlacedRect(expandedIndex(t, w, "product0"))
	cl.SetUIStage(sidebarDrawStage{b})
	trace := &sidebarSpriteTrace{}
	cl.RecordFrame().Replay(trace)
	for _, sprite := range trace.sprites {
		if sprite.HasClip && sprite.Clip.X == r.X && sprite.Clip.Y == r.Y && sprite.Clip.W == 64 && sprite.Clip.H == 64 {
			if sprite.Frame != entry.Frames[0].Frame || sprite.Dst.W != 64 || sprite.Dst.H != 32 || sprite.Dst.Y != r.Y+16 {
				t.Fatalf("normalized cell changed fallback family: %+v", sprite)
			}
			return
		}
	}
	t.Fatal("source button family not rendered in normalized cell")
}

func TestExpandedSidebarUnsafeOrdersRejectsPagerBeforeNavigation(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	sources[2].Gadgets[expandedIndex(t, sources[2], "CAPTURE")].Rect.H = 600
	cl.Resize(640, 480)
	f, _ := b.currentSnapshot()
	if _, active := b.hud.expandedSidebarPaging(b, f); active {
		t.Fatal("flat pager activated with unreachable Orders")
	}
	if _, handled := b.hud.selectExpandedSidebarPage(b, f, 0); handled {
		t.Fatal("unavailable local Orders consumed navigation")
	}
	if expandedWindow(t, b) != sources[0] {
		t.Fatal("unsafe Orders did not retain authored layout")
	}
}

func TestExpandedSidebarDifferentCommandScaffoldRetainsAuthoredPages(t *testing.T) {
	b, _, sources := expandedSidebarFixture(t)
	sources[1].Gadgets = append(sources[1].Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "CAPTURE", Active: 1, QuickKey: 'c', Rect: gui.Rect{X: 64, Y: 317, W: 55, H: 31}})
	f, _ := b.currentSnapshot()
	if _, active := b.hud.expandedSidebarPaging(b, f); active {
		t.Fatal("flat catalog hid a later-page-only command")
	}
	if expandedWindow(t, b) != sources[0] {
		t.Fatal("incompatible commands did not retain authored pages")
	}
}

func TestExpandedSidebarResizeAcrossFittedFallbackKeepsProduct(t *testing.T) {
	b, cl, sources := sidebarRowsFixture(t, 480)
	sources[0].Rect.H = 640
	orders := sources[len(sources)-1]
	orders.Gadgets[expandedIndex(t, orders, "CAPTURE")].Rect.H = 300
	f, _ := b.currentSnapshot()
	state, active := b.hud.expandedSidebarPaging(b, f)
	if !active || b.hud.expandedSidebar.key.flat {
		t.Fatal("short surface did not use fitted fallback")
	}
	b.hud.selectExpandedSidebarPage(b, f, state.Count-1)
	old := sidebarVisibleProducts(expandedWindow(t, b))[0]
	cl.Resize(1280, 768)
	w := expandedWindow(t, b)
	if !b.hud.expandedSidebar.key.flat || !slices.Contains(sidebarVisibleProducts(w), old) {
		t.Fatalf("switch to flat lost %s: %v", old, sidebarVisibleProducts(w))
	}
	old = sidebarVisibleProducts(w)[0]
	cl.Resize(640, 480)
	w = expandedWindow(t, b)
	if b.hud.expandedSidebar.key.flat || !slices.Contains(sidebarVisibleProducts(w), old) {
		t.Fatalf("switch to fitted lost %s: %v", old, sidebarVisibleProducts(w))
	}
}

func TestExpandedSidebarContradictorySharedGroupsRetainAuthoredPages(t *testing.T) {
	for _, splitOrders := range []bool{false, true} {
		t.Run(fmt.Sprint(splitOrders), func(t *testing.T) {
			b, _, sources := expandedSidebarFixture(t)
			if splitOrders {
				sources[2].Gadgets[expandedIndex(t, sources[2], "MOVE")].Assoc = 11
			} else {
				for _, source := range sources[:2] {
					source.Gadgets[expandedIndex(t, source, "MOVE")].Assoc = 11
				}
			}
			if expandedWindow(t, b) != sources[0] {
				t.Fatal("combined panel changed contradictory source radio groups")
			}
		})
	}
}

func TestExpandedSidebarShortcutEquivalence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		a, b       byte
		compatible bool
	}{
		{"ASCII case", 'm', 'M', true},
		{"different shortcut", 'm', 'n', false},
		{"extended byte", 0xe9, 0xc9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, sources := expandedSidebarFixture(t)
			sources[0].Gadgets[expandedIndex(t, sources[0], "NEXT")].QuickKey = tc.a
			sources[1].Gadgets[expandedIndex(t, sources[1], "NEXT")].QuickKey = tc.b
			w := expandedWindow(t, b)
			if (w != sources[0]) != tc.compatible {
				t.Fatal("shortcut compatibility disagrees with ASCII widget matching")
			}
			if sources[0].Gadgets[expandedIndex(t, sources[0], "NEXT")].QuickKey != tc.a {
				t.Fatal("source shortcut was rewritten")
			}
		})
	}
}

func TestExpandedSidebarRetainsCommandSpacing(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	w := expandedWindow(t, b)
	for _, pair := range [][2]string{{"REPAIR", "CAPTURE"}, {"CAPTURE", "MOVE"}, {"MOVE", "ATTACK"}} {
		a, b := w.PlacedRect(expandedIndex(t, w, pair[0])), w.PlacedRect(expandedIndex(t, w, pair[1]))
		x, y := sources[2].PlacedRect(expandedIndex(t, sources[2], pair[0])), sources[2].PlacedRect(expandedIndex(t, sources[2], pair[1]))
		if b.Y-a.Y != y.Y-x.Y {
			t.Fatalf("lost source row spacing for %v", pair)
		}
	}
	cl.Resize(640, 480)
	w = expandedWindow(t, b)
	if !b.hud.expandedSidebar.key.flat || len(sidebarVisibleProducts(w)) < 2 {
		t.Fatal("short surface lost combined build row")
	}
	for _, pair := range [][2]string{{"PREV", "REPAIR"}, {"REPAIR", "CAPTURE"}, {"CAPTURE", "MOVE"}, {"MOVE", "ATTACK"}} {
		a, b := w.PlacedRect(expandedIndex(t, w, pair[0])), w.PlacedRect(expandedIndex(t, w, pair[1]))
		if b.Y <= a.Y+a.H {
			t.Fatalf("short surface erased gap for %v", pair)
		}
	}
}

func TestExpandedSidebarSharedCommandsUseOrdersShortcut(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	sources[0].Gadgets[expandedIndex(t, sources[0], "MOVE")].QuickKey = 'q'
	sources[1].Gadgets[expandedIndex(t, sources[1], "MOVE")].QuickKey = 'M'
	sources[2].Gadgets[expandedIndex(t, sources[2], "MOVE")].QuickKey = 'm'
	w := expandedWindow(t, b)
	if !b.hud.expandedSidebar.key.flat || w.Gadgets[expandedIndex(t, w, "MOVE")].QuickKey != 'm' {
		t.Fatal("shared command did not retain canonical Orders shortcut")
	}
	paletteCallbackToken(t, b, cl, 'm')
	if b.battleState().Input.Latch != input.LatchMove {
		t.Fatal("canonical command shortcut did not activate")
	}
}
