package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func mapScrollShell(t *testing.T) (*gameShell, *ui.Panel, *client.Client) {
	t.Helper()
	w := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindListBox, Name: "MAPNAMES", Active: 1, Assoc: 1, Rect: gui.Rect{X: 10, Y: 20, W: 60, H: 146}, ItemHeight: 12},
		{Kind: gui.KindScrollBar, Name: "SLIDER", Active: 1, Assoc: 1, Rect: gui.Rect{X: 80, Y: 20, W: 16, H: 160}},
	}}
	p := ui.NewPanel(w)
	g := &gameShell{frontend: ui.NewFrontend(modeMenuMap)}
	g.frontend.Panels.Replace(p)
	for i := 0; i < 100; i++ {
		g.maps = append(g.maps, fmt.Sprintf("Map %d", i))
	}
	g.refreshMapPanel()
	cl := bindingClient(t)
	cl.Resize(640, 480)
	return g, p, cl
}

// Host wheel policy retains sub-row trackpad deltas and does not move the
// selected map. Clicking a visible row then keeps that viewport [07 R-WGT-01 §4].
func TestMapWheelThenSelectKeepsViewport(t *testing.T) {
	g, p, cl := mapScrollShell(t)
	p.SetListTopAt(1, 40, p.ListMaxTopAt(1))
	in := cl.Input()
	in.Mouse.SetPosition(20, 25)
	for i := 0; i < 4; i++ {
		in.Mouse.SetWheel(0, -0.25)
		g.serviceMenuWidgets(p, in)
		in.Mouse.ResetEdges()
	}
	if p.ListAt(1).Top() != 41 || p.ListAt(1).Selected() != 0 || g.mapIdx != 0 {
		t.Fatalf("wheel top/selection/map=%d/%d/%d", p.ListAt(1).Top(), p.ListAt(1).Selected(), g.mapIdx)
	}
	in.Mouse.SetPosition(20, 35)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	g.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	g.serviceMenuWidgets(p, in)
	if p.ListAt(1).Top() != 41 || p.ListAt(1).Selected() != 42 || g.mapIdx != 42 {
		t.Fatalf("click top/selection/map=%d/%d/%d", p.ListAt(1).Top(), p.ListAt(1).Selected(), g.mapIdx)
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(85, 35)
	in.Mouse.SetWheel(0, -1)
	g.serviceMenuWidgets(p, in)
	if p.ListAt(1).Top() != 42 {
		t.Fatalf("scrollbar wheel top=%d", p.ListAt(1).Top())
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(300, 300)
	in.Mouse.SetWheel(0, -1)
	g.serviceMenuWidgets(p, in)
	if p.ListAt(1).Top() != 42 {
		t.Fatal("wheel outside list changed viewport")
	}
}

// Exercise the runtime-built stock SELMAP rather than a second drawing model.
// The middle of the painted thumb must start capture without jumping, and a
// drag must advance the exact retained knob used by the painter [07 R-WGT-01 §5].
func TestRetailMapScrollbarCaptureAndShot(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	g.openMenu(modeMenuMap)
	p := g.activePanel()
	index := p.Index("MAPNAMES")
	bar := -1
	for i, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindScrollBar && gad.Assoc == p.Window.Gadgets[index].Assoc {
			bar = i
			break
		}
	}
	if bar < 0 || p.ListMaxTopAt(index) < 4 {
		t.Fatal("map list has no overflowing scrollbar")
	}
	p.SetListTopAt(index, p.ListMaxTopAt(index)/2, p.ListMaxTopAt(index))
	knob := p.SliderKnobAt(bar)
	size, travel := p.SliderMetricsAt(bar, g.retailTextHeight())
	r := p.Window.PlacedRect(bar)
	in := cl.Input()
	in.Mouse.SetPosition(float32(r.X+r.W/2), float32(int(r.Y)+3+knob+size/2))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	g.serviceMenuWidgets(p, in)
	if p.SliderKnobAt(bar) != knob {
		t.Fatalf("press on drawn thumb moved knob from %d to %d", knob, p.SliderKnobAt(bar))
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(in.Mouse.X, in.Mouse.Y+10)
	g.serviceMenuWidgets(p, in)
	if p.SliderKnobAt(bar) != min(knob+10, travel-1) {
		t.Fatalf("drag knob=%d, started at %d", p.SliderKnobAt(bar), knob)
	}
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	g.serviceMenuWidgets(p, in)
	// Scroll-to uses travel while capture clamps to travel-1. At the bottom
	// their distinct retail formulas must still preserve the list's top.
	in.Mouse.ResetEdges()
	p.ScrollTextListAt(index, float32(p.ListMaxTopAt(index)))
	bottom := p.ListAt(index).Top()
	in.Mouse.SetPosition(float32(r.X+r.W/2), float32(int(r.Y)+3+p.SliderKnobAt(bar)))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	g.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	g.serviceMenuWidgets(p, in)
	if p.ListAt(index).Top() != bottom {
		t.Fatalf("bottom thumb click moved top from %d to %d", bottom, p.ListAt(index).Top())
	}
	writeShellShot(t, cl, os.Getenv("NANOLATHE_MAP_SCROLL_SHOT"))
}

type mapScrollbarStage struct {
	g *gameShell
	p *ui.Panel
}

func (s mapScrollbarStage) DrawUI(cl *client.Client, _ client.UIFrame) {
	s.g.drawRetailScrollbar(cl, s.p, 2, s.p.Window.Gadgets[2], s.p.Window.PlacedRect(2))
}

// The painter must use the runtime track and the service-owned knob, including
// fractional-size truncation; reconstructing from list top loses drag precision
// and paints a hit target at a different position [07 R-WGT-01 §5].
func TestMapScrollbarPaintsRetainedKnob(t *testing.T) {
	g, p, cl := mapScrollShell(t)
	g.assets = &menuAssets{common: callbackSliderArt()}
	p.SetSliderKnobAt(2, 50)
	cl.SetUIStage(mapScrollbarStage{g, p})
	snapshot := cl.ComposeFrameSnapshot()
	r := p.Window.PlacedRect(2)
	// 12 visible rows / 100 maps * (160 - 3) truncates to 18 pixels.
	x, start := int(r.X), int(r.Y)+3+50
	for _, tc := range []struct {
		y     int
		color byte
	}{{int(r.Y), 1}, {start, 4}, {start + 16, 5}, {start + 17, 6}, {start + 18, 2}} {
		if got := snapshot.Indexed[tc.y*snapshot.Width+x]; got != tc.color {
			t.Fatalf("scrollbar pixel y=%d is %d, want %d", tc.y, got, tc.color)
		}
	}
}
