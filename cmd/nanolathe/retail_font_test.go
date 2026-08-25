package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/snapshot"
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// the FNT newline terminator [07 §4].
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

func TestRetailMainMenuUsesPrimaryGAFGlyphPixels(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	assets := loadMenuAssets(cs)
	shell := &gameShell{cs: cs, assets: assets, mode: modeMenuMain, font: assets.font}
	shell.openMenu(modeMenuMain)
	if shell.retailGAFTextFont() == nil {
		t.Fatal("primary anims/hattfont12.gaf slot was not loaded")
	}
	cl, err := client.New(client.Options{Buffer: &snapshot.Buffer{}, Width: 640, Height: 480, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(assets.pal)
	cl.Overlay = func(c *client.Client) { shell.draw(c) }
	image := cl.ComposeFrame()

	var single gui.Gadget
	found := false
	for _, gadget := range shell.panel.window.Gadgets {
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
