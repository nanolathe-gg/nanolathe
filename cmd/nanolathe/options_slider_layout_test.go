package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// stockShapedSliderArt is an authored SLIDERS entry with the stock frame
// sizes: 16x16 horizontal track pieces (base 10..12), three 10x10 knob pieces
// (base+3..5) and 9x16 arrows (base+6..9).
func stockShapedSliderArt() *formats.GAF {
	frames := make([]formats.GAFFrameRef, 20)
	for i := range frames {
		w, h := 16, 16
		switch {
		case i >= 13 && i <= 15:
			w, h = 10, 10
		case i >= 16:
			w, h = 9, 16
		}
		frames[i].Frame = &formats.GAFFrame{Width: uint16(w), Height: uint16(h), Pixels: make([]byte, w*h), Transparent: make([]bool, w*h)}
	}
	return &formats.GAF{Entries: []formats.GAFEntry{{Name: "SLIDERS", Frames: frames}}}
}

// assertKnobClearOfArrows checks the painted knob at both ends of travel lies
// inside the bar and never reaches either appended arrow, and that it is one
// knob-frame wide [07 R-WGT-01 §5 "Synthesis at open", "painting"].
func assertKnobClearOfArrows(t *testing.T, name string, bar gui.Rect, travel, knobSize, knobW int, arrows []gui.Rect) {
	t.Helper()
	for _, knob := range []int{0, travel - 1} {
		x, length := retailOptionsKnobSpan(bar, knob, knobSize, knobW)
		if length != knobW {
			t.Fatalf("%s knob %d painted %d wide, want one %d-pixel knob frame", name, knob, length, knobW)
		}
		if x < int(bar.X) || x+length > int(bar.X+bar.W) {
			t.Fatalf("%s knob %d spans [%d,%d) outside bar [%d,%d)", name, knob, x, x+length, bar.X, bar.X+bar.W)
		}
		for _, a := range arrows {
			if x < int(a.X+a.W) && x+length > int(a.X) {
				t.Fatalf("%s knob %d spans [%d,%d), overlapping arrow [%d,%d)", name, knob, x, x+length, a.X, a.X+a.W)
			}
		}
	}
}

// The options sliders take the builder's kind-4 synthesis: arrows appended at
// both ends, the bar shrunk between them, knobsize from frame base+5 and travel
// `w' - knobsize - 4`. Painted over that, the knob stays clear of the arrows at
// both ends of travel [07 R-WGT-01 §5].
func TestOptionsSliderSynthesisKeepsKnobOffArrows(t *testing.T) {
	g := &gameShell{assets: &menuAssets{common: stockShapedSliderArt()}}
	window := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindScrollBar, Name: "GAMMA", SourceName: retailOptionsPageSource + "GAMMA", Active: 1, Attribs: 1, Rect: gui.Rect{X: 278, Y: 99, W: 122, H: 16}, Range: 114, KnobSize: 10},
	}}
	g.buildRetailOptionsSliders(window)
	if len(window.Gadgets) != 4 {
		t.Fatalf("synthesis left %d gadgets, want the bar plus two appended arrows", len(window.Gadgets))
	}
	bar := window.Gadgets[1]
	if bar.Rect.X != 287 || bar.Rect.W != 104 || bar.Range != 90 || bar.KnobSize != 10 {
		t.Fatalf("built bar x=%d w=%d travel=%d knob=%d, want 287/104/90/10", bar.Rect.X, bar.Rect.W, bar.Range, bar.KnobSize)
	}
	dec, inc := window.Gadgets[2], window.Gadgets[3]
	if dec.Rect.X != 278 || inc.Rect.X != 391 || dec.Rect.W != 9 || inc.Rect.W != 9 {
		t.Fatalf("arrows at x=%d/%d w=%d/%d, want 278/391 w=9", dec.Rect.X, inc.Rect.X, dec.Rect.W, inc.Rect.W)
	}
	if !retailOptionsPageGadget(dec) || !retailOptionsPageGadget(inc) {
		t.Fatal("appended arrows are not page gadgets, so a page switch would keep them")
	}
	travel, knobSize, _, ok := g.retailSliderMetrics(bar)
	if !ok || travel != 90 || knobSize != 10 {
		t.Fatalf("slider metrics travel=%d knob=%d ok=%v, want the built record's 90/10", travel, knobSize, ok)
	}
	assertKnobClearOfArrows(t, "GAMMA", bar.Rect, travel, knobSize, 10, []gui.Rect{dec.Rect, inc.Rect})
}

// On the stock visuals page both sliders are built, keep their knob clear of
// the arrows at both ends, and the appended increment arrow steps its own bar
// by one [07 R-WGT-01 §3][07 R-WGT-01 §5].
func TestRetailVisualsSlidersKnobClearOfArrows(t *testing.T) {
	shell, _, cl := retailAssetShell(t)
	shell.openMenu(modeMenuSingle)
	shell.activateGadget("Options")
	shell.activateGadget("VISUALS")
	for _, name := range []string{"GAMMA", "VIDSLDR"} {
		index := optionsPanel.Index(name)
		slider := retailOptionsSliderNamed(shell, name)
		if index < 0 || slider == nil {
			t.Fatalf("visuals page has no tracked %s", name)
		}
		gad := optionsPanel.Window.Gadgets[index]
		var arrows []gui.Rect
		inc := -1
		for i, other := range optionsPanel.Window.Gadgets {
			if other.Kind == gui.KindButton && other.Attribs&0x1800 != 0 && other.Assoc == gad.Assoc && retailOptionsPageGadget(other) {
				arrows = append(arrows, optionsPanel.Window.PlacedRect(i))
				if other.Attribs&gui.AttribSliderIncrement == gui.AttribSliderIncrement {
					inc = i
				}
			}
		}
		if len(arrows) != 2 || inc < 0 {
			t.Fatalf("%s has %d synthesised arrows, want 2", name, len(arrows))
		}
		assertKnobClearOfArrows(t, name, optionsPanel.Window.PlacedRect(index), slider.travel, slider.knobSize, slider.knobSize, arrows)

		moveRetailSliderNamed(shell, name, slider, 0)
		before := slider.knob
		r := optionsPanel.Window.PlacedRect(inc)
		in := cl.Input()
		in.Mouse.ResetEdges()
		widgetLeftDown(in, float32(r.X+r.W/2), float32(r.Y+r.H/2))
		shell.serviceMenuWidgets(optionsPanel, in)
		in.Mouse.ResetEdges()
		in.Mouse.SetButton(input.MouseButtonLeft, false)
		shell.serviceMenuWidgets(optionsPanel, in)
		if slider.knob != before+1 {
			t.Fatalf("%s increment arrow moved knob %d -> %d, want one step", name, before, slider.knob)
		}
	}
}

// Under desktop scaling the monitor's device-independent size is smaller than
// its panel: a 3440x1440 monitor at 125% reports 2752x1152. The list offers
// the physical size, keeps the device-independent one, and derives the
// widescreen heights from the physical proportions; the retail gates still
// read the device-independent desktop [07 R-FE-02 §9]
// (DESIGN_PRESENTATION_CLIENT §2.1).
func TestMonitorDisplayModesOfferNativePixelsUnderScaling(t *testing.T) {
	logical, native := retailDisplayMode{2752, 1152}, retailDisplayMode{3440, 1440}
	modes := retailMonitorDisplayModes(logical, native, retailDisplayMode{640, 480})
	for _, want := range []retailDisplayMode{native, logical, {1920, 804}, {1280, 1024}} {
		if i := retailDisplayModeIndex(modes, want.W, want.H); modes[i] != want {
			t.Fatalf("scaled 43:18 desktop omitted %v: %v", want, modes)
		}
	}
	if i := retailDisplayModeIndex(modes, 1600, 1200); modes[i] == (retailDisplayMode{1600, 1200}) {
		t.Fatalf("1600x1200 passed the desktop gate on a 1152-high logical desktop: %v", modes)
	}
	if last := modes[len(modes)-1]; last != native {
		t.Fatalf("largest offered size is %v, want the native %v", last, native)
	}
}
