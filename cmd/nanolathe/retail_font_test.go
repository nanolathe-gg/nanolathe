package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func syntheticRetailGAFFont() *formats.GAFEntry {
	font := &formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	font.Frames[' '] = formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 7, Height: 12}}
	font.Frames['A'] = formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 8, Height: 12}}
	font.Frames['B'] = formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 5, Height: 12}}
	font.Frames['I'] = formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 5, Height: 12}}
	return font
}

func TestRetailGAFTextMetrics(t *testing.T) {
	font := syntheticRetailGAFFont()
	if got := retailGAFTextWidth(font, "A B\x00B"); got != 20 {
		t.Fatalf("width %d, want 20", got)
	}
	// The GAF path skips control bytes instead of applying the FNT newline
	// terminator [07 §4].
	if got := retailGAFTextWidth(font, "A\nB"); got != 13 {
		t.Fatalf("control-byte width %d, want 13", got)
	}
	if got := retailGAFTextHeight(font); got != 14 {
		t.Fatalf("height %d, want I-frame height + 2 = 14", got)
	}
	if got := retailGAFBaselineHeight(font); got != 12 {
		t.Fatalf("baseline normalization %d, want I-frame height 12", got)
	}
	r := gui.Rect{Y: 393, H: 20}
	if got := retailTextPenY(gui.Gadget{Stages: 1}, r, 14); got != 396 {
		t.Fatalf("staged pen y %d, want 396", got)
	}
	if got := retailTextPenY(gui.Gadget{}, r, 14); got != 395 {
		t.Fatalf("unstaged pen y %d, want 395", got)
	}
}

// [07 R-FE-01 §3] halves the primary font's width with integer division,
// and each menu open starts from the authored (initially hidden) label.
func TestMainMenuVersionPlacementOnReopen(t *testing.T) {
	font := syntheticRetailGAFFont()
	for _, code := range []byte("v3.1") {
		font.Frames[code] = formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 3}}
	}
	font.Frames['v'].Frame.Width = 4 // Total width 13: the shift must be 6.
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindLabel, Name: "DebugString", Text: "authored", Rect: gui.Rect{X: 320, Y: 460, W: 100, H: 20}},
	}}
	shell := &gameShell{
		assets: &menuAssets{
			panel:   map[shellMode]*retailPanelAssets{modeMenuMain: {window: window}},
			gafFont: &formats.GAF{Entries: []formats.GAFEntry{*font}},
		},
		font: fixedWidthFont(10), // Different fallback metrics must not win.
	}
	for open := 0; open < 2; open++ {
		shell.openMenu(modeMenuMain)
		panel := shell.activePanel()
		if !panel.ActiveOf("DebugString") || panel.TextOf("DebugString") != "v3.1" {
			t.Fatalf("open %d: version label active=%t text=%q", open, panel.ActiveOf("DebugString"), panel.TextOf("DebugString"))
		}
		want := window.Gadgets[1].Rect
		want.X = 314
		if got := panel.Window.Gadgets[1].Rect; got != want {
			t.Fatalf("open %d: version rectangle = %+v, want %+v", open, got, want)
		}
	}
	if got := window.Gadgets[1]; got.Rect.X != 320 || got.Active != 0 || got.Text != "authored" {
		t.Fatalf("cached authored label changed: %+v", got)
	}
}

func TestRetailMainMenuUsesPrimaryGAFGlyphPixels(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	assets := loadMenuAssets(cs)
	shell := &gameShell{cs: cs, assets: assets, frontend: ui.NewFrontend(modeMenuMain), font: assets.font}
	shell.openMenu(modeMenuMain)
	if shell.retailGAFTextFont() == nil {
		t.Fatal("primary anims/hattfont12.gaf slot was not loaded")
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(assets.pal)
	cl.SetUIStage(gameShellUIStage{shell: shell})
	image := cl.ComposeFrame()

	var single gui.Gadget
	found := false
	for _, gadget := range shell.frontend.Panels.Top().Window.Gadgets {
		if gadget.Name == "SINGLE" {
			single, found = gadget, true
			break
		}
	}
	if !found {
		t.Fatal("SINGLE gadget not found")
	}
	r := single.Rect
	textWidth := shell.retailTextWidth(single.Text)
	x := int(r.X) + (int(r.W)-1-textWidth)/2 + 1
	penY := retailTextPenY(single, r, shell.retailTextHeight())
	frame := retailGAFGlyph(shell.retailGAFTextFont(), 'S')
	if frame == nil {
		t.Fatal("hattfont12 S frame missing")
	}
	// Font loading subtracts I.Height from each frame YOffset, then glyph
	// placement subtracts that normalized offset from the pen [07 §4].
	x -= int(frame.XOffset)
	y := penY - (int(frame.YOffset) - retailGAFBaselineHeight(shell.retailGAFTextFont()))
	if y != penY+1 {
		t.Fatalf("stock hattfont12 draw y %d, want pen y %d + 1", y, penY)
	}
	for py := 0; py < int(frame.Height); py++ {
		for px := 0; px < int(frame.Width); px++ {
			index := py*int(frame.Width) + px
			if frame.Transparent[index] {
				continue
			}
			rr, gg, bb, aa := assets.pal.RGBA(frame.Pixels[index])
			got := image.RGBAAt(x+px, y+py)
			if got.R != rr || got.G != gg || got.B != bb || got.A != aa {
				t.Fatalf("S pixel (%d,%d) = %v, want direct GAF palette pixel (%d,%d,%d,%d)", px, py, got, rr, gg, bb, aa)
			}
		}
	}
}
