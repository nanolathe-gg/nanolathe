package main

import (
	"fmt"
	"reflect"
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
		w.Gadgets = append(w.Gadgets, button("ORDERS", 3, 4, 59, 19), button("BUILD", 65, 4, 59, 19), button("MOVE", 5, 247, 55, 31), button("ATTACK", 5, 317, 55, 31))
		w.Gadgets[len(w.Gadgets)-2].QuickKey = 'm'
		sources = append(sources, w)
	}
	orders := newWindow()
	orders.Gadgets[2].Kind, orders.Gadgets[3].Kind = gui.KindFont, gui.KindFont
	orders.Gadgets[2].Name, orders.Gadgets[3].Name = "FONT2", "FONT3"
	orders.Gadgets = append(orders.Gadgets, button("ORDERS", 3, 4, 59, 19), button("BUILD", 65, 4, 59, 19), button("MOVE", 5, 247, 55, 31), button("ATTACK", 5, 317, 55, 31), button("REPAIR", 5, 35, 55, 31), button("CAPTURE", 64, 207, 55, 31))
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

func TestExpandedSidebarUsesAuthoredBlocksAndSharedInput(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	original := cloneGUIWindow(sources[1])
	w := expandedWindow(t, b)
	if w == sources[0] || expandedWindow(t, b) != w {
		t.Fatal("expanded window is missing or not cached")
	}
	for i, g := range sources[0].Gadgets {
		fixed := g.CommonAttribs&4 != 0 || g.Name == "BUILD" || g.Name == "ORDERS"
		if i != 0 && (fixed && w.PlacedRect(i) != sources[0].PlacedRect(i) || w.Gadgets[i].Name != g.Name) {
			t.Fatalf("base gadget %d changed", i)
		}
	}
	product := expandedIndex(t, w, "product6")
	repair := expandedIndex(t, w, "REPAIR")
	for _, i := range []int{product, repair} {
		r := w.PlacedRect(i)
		if !b.hud.hitTestFor(b, r.X+1, r.Y+1) || b.hud.buttonAt(b, r.X+1, r.Y+1) != i {
			t.Fatalf("relocated gadget %d has a different pointer layout", i)
		}
		f, _ := b.currentSnapshot()
		b.hud.updateHoveredGadget(b, f, r.X+1, r.Y+1)
		if index, name := b.hud.hoveredGadgetSource(); index != i || name != w.Gadgets[i].Name {
			t.Fatalf("hover resolved %d %q, want %d %q", index, name, i, w.Gadgets[i].Name)
		}
	}
	if got := w.PlacedRect(product+1).X - w.PlacedRect(product).X; got != 64 {
		t.Fatalf("authored product spacing became %d", got)
	}
	if w.Gadgets[4].Assoc == w.Gadgets[product].Assoc || w.Gadgets[product].Assoc != w.Gadgets[product+1].Assoc {
		t.Fatal("source association groups were mixed or split")
	}
	paletteCallbackClick(t, b, cl, w, product, false, true)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].Kind != session.HumanFactoryBuild || pending[len(pending)-1].FactoryBuild.Product != "product6" || pending[len(pending)-1].FactoryBuild.Count != 5 {
		t.Fatalf("relocated product dispatch: %+v", pending)
	}
	paletteCallbackToken(t, b, cl, 'r')
	if b.battleState().Input.Latch != input.LatchRepair {
		t.Fatal("appended order accelerator did not use shared service")
	}
	if !reflect.DeepEqual(original, sources[1]) {
		t.Fatal("composing or clicking changed the authored source")
	}
	// Even a catalog-resolved authored product is not admitted if it is absent
	// from this builder's membership union.
	b.cat.BuildMenus["armfav"].Buttons = b.cat.BuildMenus["armfav"].Buttons[:6]
	r := w.PlacedRect(product)
	if b.hud.hitTestFor(b, r.X+1, r.Y+1) {
		t.Fatal("non-member product remained enabled")
	}
}

func TestExpandedSidebarOrdersBandsAroundBuildPages(t *testing.T) {
	for _, ordersFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(ordersFirst), func(t *testing.T) {
			b, cl, sources := expandedSidebarFixture(t)
			if ordersFirst {
				// A mod's different content-to-footer gap survives composition.
				sources[2].Gadgets[expandedIndex(t, sources[2], "CAPTURE")].Rect.Y -= 6
				// Competing shortcuts still follow the primary source's record
				// order even when its controls are moved below appended builds.
				sources[0].Gadgets[4].QuickKey = 'r'
				paletteCallbackFrame(t, b, func(f *frame.Frame) {
					f.CommandPage.Page = 0
					f.Units[0].Flags = hud.EncodePageBits(f.Units[0].Flags, 0)
				})
			}
			w := expandedWindow(t, b)
			wantGap := int32(9)
			if ordersFirst {
				wantGap += 6
			}
			assertSidebarBandOrder(t, w, wantGap)
			if ordersFirst {
				for i, g := range sources[2].Gadgets {
					if w.Gadgets[i].Name != g.Name {
						t.Fatalf("primary record %d was reordered", i)
					}
				}
				paletteCallbackToken(t, b, cl, 'r')
				if b.battleState().Input.Latch != input.LatchRepair {
					t.Fatal("appended product shortcut displaced the primary order shortcut")
				}
			}
		})
	}
}

func assertSidebarBandOrder(t *testing.T, w *gui.Window, wantGap int32) {
	t.Helper()
	firstBottom, secondTop, secondBottom := int32(0), int32(1<<31-1), int32(0)
	for n := 0; n < 12; n++ {
		r := w.PlacedRect(expandedIndex(t, w, fmt.Sprintf("product%d", n)))
		if n < 6 {
			firstBottom = max(firstBottom, r.Y+r.H)
		} else {
			secondTop, secondBottom = min(secondTop, r.Y), max(secondBottom, r.Y+r.H)
		}
	}
	prev, next := w.PlacedRect(expandedIndex(t, w, "PREV")), w.PlacedRect(expandedIndex(t, w, "NEXT"))
	repair, capture := w.PlacedRect(expandedIndex(t, w, "REPAIR")), w.PlacedRect(expandedIndex(t, w, "CAPTURE"))
	move, attack := w.PlacedRect(expandedIndex(t, w, "MOVE")), w.PlacedRect(expandedIndex(t, w, "ATTACK"))
	if firstBottom != secondTop || secondBottom > min(prev.Y, next.Y) || max(prev.Y+prev.H, next.Y+next.H) > repair.Y || capture.Y+capture.H > move.Y {
		t.Fatalf("bands are not builds/arrows/orders/footer: first=%d second=%d..%d arrows=%+v/%+v orders=%+v/%+v footer=%+v", firstBottom, secondTop, secondBottom, prev, next, repair, capture, move)
	}
	if attack.Y-move.Y != 70 {
		t.Fatal("footer internal spacing changed")
	}
	if got := move.Y - (capture.Y + capture.H); got != wantGap {
		t.Fatalf("orders-to-footer gap = %d, want authored gap %d", got, wantGap)
	}
}

func TestExpandedSidebarRejectsInterleavedSourceBands(t *testing.T) {
	for _, source := range []string{"primary footer", "orders content", "second grid", "overlapping footer widget"} {
		t.Run(source, func(t *testing.T) {
			b, cl, sources := expandedSidebarFixture(t)
			switch source {
			case "primary footer":
				sources[0].Gadgets[expandedIndex(t, sources[0], "MOVE")].Rect.Y = 100
			case "orders content":
				sources[2].Gadgets[expandedIndex(t, sources[2], "REPAIR")].Rect.H = 250
			case "second grid":
				sources[1].Gadgets[4].Rect.Y = 300
			case "overlapping footer widget":
				sources[2].Gadgets = append(sources[2].Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "MODCONTROL", Active: 1, Rect: gui.Rect{X: 5, Y: 250, W: 10, H: 10}})
			}
			cl.Resize(1280, 1081)
			w := expandedWindow(t, b)
			if source != "second grid" && w != sources[0] {
				t.Fatal("unsafe primary/required source layout was composed")
			}
			if source == "second grid" {
				for _, g := range w.Gadgets {
					if g.Name == "product6" {
						t.Fatal("interleaved optional build page was composed")
					}
				}
			}
		})
	}
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

func TestExpandedSidebarOrderRadiosShareStopReset(t *testing.T) {
	for _, anchor := range []string{"source STOP", "common order", "no anchor"} {
		t.Run(anchor, func(t *testing.T) {
			b, cl, sources := expandedSidebarFixture(t)
			primary, extra := sources[0], sources[2]
			primary.Gadgets = append(primary.Gadgets,
				gui.Gadget{Kind: gui.KindButton, Name: "STOP", Active: 1, Assoc: 11, Attribs: 0x10, Rect: gui.Rect{X: 64, Y: 247, W: 54, H: 30}},
				gui.Gadget{Kind: gui.KindButton, Name: "ONOFF", Active: 1, Assoc: 99, Status: 2, Rect: gui.Rect{X: 80, Y: 317, W: 20, H: 20}},
			)
			extra.Gadgets = append(extra.Gadgets,
				gui.Gadget{Kind: gui.KindButton, Name: "EXTRAOTHER", Active: 1, Assoc: 99, Status: 2, Rect: gui.Rect{X: 80, Y: 35, W: 20, H: 20}},
			)
			if anchor == "source STOP" {
				extra.Gadgets = append(extra.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "STOP", Active: 1, Assoc: 23, Rect: gui.Rect{X: 64, Y: 247, W: 54, H: 30}})
			}
			for _, source := range []*gui.Window{primary, extra} {
				for i := range source.Gadgets {
					g := &source.Gadgets[i]
					if sidebarLatchCommand(*g) == "" {
						continue
					}
					g.Attribs = 0x10
					g.Assoc = 11
					if source == extra {
						g.Assoc = 23
					}
					if source == extra && anchor == "no anchor" && (g.Name == "MOVE" || g.Name == "ATTACK") {
						g.Name = "UNANCHORED"
					}
				}
			}
			// Both build pages share the same safe footer, with independent source
			// associations. The unrelated staged control is outside the product rows.
			for i := 10; i < len(primary.Gadgets); i++ {
				if i < len(sources[1].Gadgets) {
					sources[1].Gadgets[i] = primary.Gadgets[i]
				} else {
					sources[1].Gadgets = append(sources[1].Gadgets, primary.Gadgets[i])
				}
			}
			cl.Resize(1280, 1081)
			w := expandedWindow(t, b)
			if anchor == "no anchor" {
				if w != primary {
					t.Fatal("unanchored radio source did not retain the authored page")
				}
				return
			}
			repair, attack := expandedIndex(t, w, "REPAIR"), expandedIndex(t, w, "ATTACK")
			stop := expandedIndex(t, w, "STOP")
			firstOther, extraOther := expandedIndex(t, w, "ONOFF"), expandedIndex(t, w, "EXTRAOTHER")
			if w.Gadgets[repair].Assoc != w.Gadgets[stop].Assoc || w.Gadgets[attack].Assoc != w.Gadgets[stop].Assoc {
				t.Fatal("order associations do not share the STOP reset group")
			}
			if w.Gadgets[firstOther].Assoc == w.Gadgets[extraOther].Assoc {
				t.Fatal("unrelated source groups were merged")
			}
			paletteCallbackClick(t, b, cl, w, repair, false, false)
			p := b.hud.palettePanels[w]
			if p.DownAt(repair) != 1 || b.battleState().Input.Latch != input.LatchRepair {
				t.Fatal("supplemental Repair did not arm its radio and latch")
			}
			paletteCallbackClick(t, b, cl, w, attack, false, false)
			if p.DownAt(repair) != 0 || p.DownAt(attack) != 1 || b.battleState().Input.Latch != input.LatchAttack {
				t.Fatal("primary Attack did not release supplemental Repair")
			}
			paletteCallbackClick(t, b, cl, w, repair, false, false)
			b.resetOrderLatch()
			if p.DownAt(repair) != 0 || p.DownAt(attack) != 0 || p.DownAt(stop) != 0 || b.battleState().Input.Latch != input.LatchNormal {
				t.Fatal("idle reset left an order radio down")
			}
			if p.DownAt(firstOther) != 2 || p.DownAt(extraOther) != 2 {
				t.Fatal("order switch/reset changed unrelated source groups")
			}
		})
	}
}

func TestExpandedSidebarOrdersBaseAndResizeRetireCapture(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	paletteCallbackFrame(t, b, func(f *frame.Frame) {
		f.CommandPage.Page = 0
		f.Units[0].Flags = hud.EncodePageBits(f.Units[0].Flags, 0)
	})
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product0")
	if source, _ := b.hud.sidebarSource(w, i, nil); source != sources[0] {
		t.Fatal("orders base lost the remembered build page")
	}
	r := w.PlacedRect(i)
	cl.Input().Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	b.viewerStep(0, cl)
	p := b.hud.palettePanels[w]
	if p == nil || p.CaptureIndex() != i {
		t.Fatal("relocated product was not captured")
	}
	cl.Resize(640, 480)
	if got := expandedWindow(t, b); got != sources[2] || p.CaptureIndex() != -1 {
		t.Fatal("short resize did not restore authored page and retire capture")
	}
	cl.Resize(1280, 1080)
	expandedWindow(t, b)
	cl.Input().Mouse.ResetEdges()
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	b.viewerStep(0, cl)
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("release after resize revived a previous capture")
	}
	cl.SetEnhanced(false)
	if got := expandedWindow(t, b); got != sources[2] {
		t.Fatal("classic renderer did not retain the authored page")
	}
}

func TestExpandedSidebarCapacityAndUnsupportedLayouts(t *testing.T) {
	for _, tc := range []struct {
		height             int
		orders, secondPage bool
	}{{480, false, false}, {687, false, false}, {688, true, false}, {751, true, false}, {752, true, true}, {879, true, true}, {880, true, true}} {
		t.Run(fmt.Sprint(tc.height), func(t *testing.T) {
			b, cl, sources := expandedSidebarFixture(t)
			cl.Resize(1280, tc.height)
			w := expandedWindow(t, b)
			if (w != sources[0]) != tc.orders {
				t.Fatalf("orders capacity at height %d", tc.height)
			}
			hasSecond := false
			for _, g := range w.Gadgets {
				hasSecond = hasSecond || g.Name == "product6"
			}
			if hasSecond != tc.secondPage {
				t.Fatalf("second page at height %d = %v", tc.height, hasSecond)
			}
		})
	}
	for _, kind := range []string{"linked control", "custom page zero", "art spill"} {
		t.Run(kind, func(t *testing.T) {
			b, cl, sources := expandedSidebarFixture(t)
			switch kind {
			case "linked control":
				sources[2].Gadgets[4].Kind = gui.KindLabel
			case "custom page zero":
				b.cat.Units["armfav"].HasPageZeroGUI = true
			case "art spill":
				sources[2].Gadgets[8].ButtonArtResolved = true
				sources[2].Gadgets[8].ButtonArt = &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{Width: 200, Height: 19}}}}
			}
			cl.Resize(1280, 1081)
			if expandedWindow(t, b) != sources[0] {
				t.Fatal("unsupported layout did not preserve original page")
			}
		})
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

func TestExpandedSidebarGeneratedSlotsKeepSourceAndHoles(t *testing.T) {
	b, cl, sources := expandedSidebarFixture(t)
	// Replace page two by a generated template with an intentionally sparse,
	// reordered sequence and a conflicting slot. Its authored rectangles win.
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
	// New dimensions invalidate the previous composition; immutable production
	// catalogs do not change while a battle is open.
	cl.Resize(1280, 1081)
	w := expandedWindow(t, b)
	i := expandedIndex(t, w, "product6")
	j := expandedIndex(t, w, "product11")
	if w.PlacedRect(i).X != 64 || w.PlacedRect(j).Y-w.PlacedRect(i).Y != 128 {
		t.Fatal("generated slots were packed or reconstructed from membership")
	}
	if template.Gadgets[4].Name != "IGPATCH" || template.Gadgets[9].Name != "IGPATCH" {
		t.Fatal("generated composition mutated its template")
	}
	source, _ := b.hud.sidebarSource(w, i, nil)
	if source == template || source.Gadgets[4].Name != "product6" || source.Gadgets[5].GrayedOut&1 == 0 {
		t.Fatal("generated source lost last-slot-writer or empty-slot identity")
	}
	paletteCallbackClick(t, b, cl, w, j, true, true)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].FactoryBuild.Product != "product11" || pending[len(pending)-1].FactoryBuild.Count != -5 {
		t.Fatalf("generated cancellation: %+v", pending)
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
			if sprite.X != r.X || sprite.Y != r.Y {
				t.Fatalf("art at %d,%d, hit rectangle %+v", sprite.X, sprite.Y, r)
			}
			return
		}
	}
	t.Fatal("appended source art was not recorded")
}
