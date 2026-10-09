package chrome

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// Opaque pixels never take the colour key, which retail GAF art treats as
// transparent; a fully transparent pixel always does.
func TestDitherAvoidsColourKey(t *testing.T) {
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(i), uint8(i), uint8(i), 255}
	}
	l := NewLayer(16, 4)
	for y := 0; y < 3; y++ {
		for x := 0; x < 16; x++ {
			l.Set(x, y, RGBA{9.0 / 255, 9.0 / 255, 9.0 / 255, 1})
		}
	}
	img := Dither(l, pal, 9)
	for y := 0; y < 3; y++ {
		for x := 0; x < 16; x++ {
			if img.ColorIndexAt(x, y) == 9 {
				t.Fatalf("opaque pixel (%d,%d) took the colour key", x, y)
			}
		}
	}
	if img.ColorIndexAt(0, 3) != 9 {
		t.Fatalf("transparent pixel took index %d, want the colour key", img.ColorIndexAt(0, 3))
	}
}

// Wear is seeded by the label: the same label wears identically, different
// labels differently.
func TestWearIsSeededByLabel(t *testing.T) {
	s := &Style{Scale: 2, Light: Light{-1, 1}, Grime: 0.6}
	draw := func(seed string) *Layer {
		l := NewLayer(108, 60)
		l.Rect(0, 0, 107, 59, RGBA{0.5, 0.5, 0.5, 1})
		s.Wear(l, seed, l.W)
		return l
	}
	same := func(a, b *Layer) bool {
		for i := range a.Pix {
			if a.Pix[i] != b.Pix[i] {
				return false
			}
		}
		return true
	}
	if !same(draw("MOVE"), draw("MOVE")) {
		t.Fatal("the same label wore differently")
	}
	if same(draw("MOVE"), draw("STOP")) {
		t.Fatal("different labels wore identically")
	}
}

// A covered entry whose art is not stock — a mod's repaint, a new side, a
// localized caption — is enlarged from its own pixels and never redrawn with
// stock captions; an entry the remaster does not cover is left to the client.
func TestModdedArtTakesTheScale2xFallback(t *testing.T) {
	// A diagonal: Scale2x rounds its steps where nearest doubling stairs them.
	f := &formats.GAFFrame{Width: 3, Height: 3, ColorKey: 9, XOffset: 1, YOffset: -2,
		Pixels: []byte{1, 2, 2, 1, 1, 2, 1, 1, 1}, Transparent: make([]bool, 9)}
	f.PlainPixels, f.PlainTransparent = f.Pixels, f.Transparent
	bank := &formats.GAF{Entries: []formats.GAFEntry{
		{Name: "GOKMOVE", Frames: []formats.GAFFrameRef{{Frame: f}}},
		{Name: "PANELSIDE", Frames: []formats.GAFFrameRef{{Frame: f}}},
	}}
	out, err := Bank2x(bank, [256][3]uint8{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Entries[0].Frames[0].Frame
	if got == nil || got.Width != 6 || got.Height != 6 || got.XOffset != 2 || got.YOffset != -4 {
		t.Fatalf("modded MOVE frame: got %+v, want a 6x6 variant with doubled offsets", got)
	}
	// Row 2 of the source steps from 1 to 2 at (1,0)-(1,1); Scale2x fills the
	// inner corner of that step, nearest doubling would not.
	want := []byte{1, 1, 2, 2, 2, 2, 1, 1, 1, 2, 2, 2}
	if !bytes.Equal(got.Pixels[:12], want) {
		t.Fatalf("Scale2x rows 0-1 = %v, want %v", got.Pixels[:12], want)
	}
	if out.Entries[1].Frames[0].Frame != nil {
		t.Fatal("an uncovered entry must be left to the client's nearest doubling")
	}
}
