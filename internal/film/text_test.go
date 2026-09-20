package film

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func flat(w, h int, shade uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = shade, shade, shade, 0xff
	}
	return img
}

// inkBounds reports the rectangle the drawn text actually changed.
func inkBounds(img *image.RGBA, shade uint8) image.Rectangle {
	out := image.Rectangle{Min: image.Point{X: math.MaxInt32, Y: math.MaxInt32}}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			o := img.PixOffset(x, y)
			if img.Pix[o] == shade && img.Pix[o+1] == shade && img.Pix[o+2] == shade {
				continue
			}
			out.Min.X, out.Min.Y = min(out.Min.X, x), min(out.Min.Y, y)
			out.Max.X, out.Max.Y = max(out.Max.X, x+1), max(out.Max.Y, y+1)
		}
	}
	return out
}

// The measured advance is what a capture lays out against — centring, the
// wipe column and the rule all derive from it — so it has to agree with the
// ink the rasterizer actually lays down.
func TestMeasuredWidthMatchesDrawnInk(t *testing.T) {
	style := TextStyle{Size: 60, Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}
	const text = "NANOLATHE"
	img := flat(1200, 200, 0x20)
	width := DrawText(img, text, 100, 140, style, 1, math.Inf(1))
	if measured := MeasureText(text, style); measured != width {
		t.Fatalf("DrawText returned %v but MeasureText says %v", width, measured)
	}
	ink := inkBounds(img, 0x20)
	// The stroke has width, so the ink overhangs the advance by about half of
	// it at each end. Anything further apart is a measurement bug.
	slack := style.Size * style.weight()
	if float64(ink.Min.X) < 100-slack || float64(ink.Min.X) > 100+slack {
		t.Fatalf("ink starts at %d, want the pen at 100 within %v", ink.Min.X, slack)
	}
	if right := 100 + width; math.Abs(float64(ink.Max.X)-right) > slack+1 {
		t.Fatalf("ink ends at %d, want %v within %v", ink.Max.X, right, slack+1)
	}
	// Cap height is the size, measured from the baseline upward.
	if top := 140 - style.Size; math.Abs(float64(ink.Min.Y)-top) > slack+1 {
		t.Fatalf("cap top at %d, want %v", ink.Min.Y, top)
	}
}

// Every character the face claims must actually draw something, or a title
// silently loses a letter and nobody notices until the render is watched.
func TestEveryMappedGlyphDrawsInk(t *testing.T) {
	style := TextStyle{Size: 40, Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}
	for r := range face {
		if r == ' ' {
			continue
		}
		img := flat(120, 120, 0x00)
		DrawText(img, string(r), 20, 80, style, 1, math.Inf(1))
		if inkBounds(img, 0x00).Empty() {
			t.Fatalf("glyph %q drew nothing", string(r))
		}
	}
}

// Unmapped runes must still advance, so an unsupported character costs one
// blank rather than collapsing the rest of the line.
func TestUnmappedRuneAdvancesLikeASpace(t *testing.T) {
	style := TextStyle{Size: 40}
	if MeasureText("AéA", style) != MeasureText("A A", style) {
		t.Fatal("an unmapped rune does not advance like a space")
	}
	if _, ok := lookupGlyph('é'); ok {
		t.Fatal("an unmapped rune reported a glyph")
	}
	if _, ok := lookupGlyph('a'); !ok {
		t.Fatal("lower case did not fold onto the face")
	}
}

// The wipe reveals left to right and nothing beyond its column.
func TestWipeRevealsOnlyUpToItsColumn(t *testing.T) {
	style := TextStyle{Size: 50, Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}
	img := flat(900, 160, 0x10)
	width := MeasureText("NANOLATHE", style)
	DrawText(img, "NANOLATHE", 50, 120, style, 1, 50+width/2)
	ink := inkBounds(img, 0x10)
	if float64(ink.Max.X) > 50+width/2+2 {
		t.Fatalf("ink reaches %d, past the reveal column %v", ink.Max.X, 50+width/2)
	}
	if float64(ink.Max.X) < 50+width/4 {
		t.Fatalf("ink stops at %d, well short of the reveal column", ink.Max.X)
	}
}

func TestCueIsInvisibleOutsideItsWindowAndFadesInsideIt(t *testing.T) {
	cue := Cue{At: 10, Ticks: 30, In: 10, Out: 10, Style: "title", Lines: []string{"X"}}
	for _, at := range []float64{0, 9.9, 40, 100} {
		if _, _, on := cue.envelope(at); on {
			t.Fatalf("cue is on screen at %v, outside its window", at)
		}
	}
	start, _, _ := cue.envelope(10)
	mid, _, _ := cue.envelope(25)
	end, _, _ := cue.envelope(39.9)
	if start != 0 {
		t.Fatalf("cue opens at alpha %v, want 0", start)
	}
	if mid != 1 {
		t.Fatalf("cue holds at alpha %v, want 1", mid)
	}
	if end > 0.05 {
		t.Fatalf("cue closes at alpha %v, want it faded out", end)
	}
}

// A cue that draws nothing is a wasted render, so an unknown style or
// animation is a script error rather than a default.
func TestCueValidationRejectsUnknownStyleAndAnimation(t *testing.T) {
	if err := (Cue{Ticks: 10, Lines: []string{"X"}, Style: "banner"}).Validate(); err == nil {
		t.Fatal("an unknown style validated")
	}
	if err := (Cue{Ticks: 10, Lines: []string{"X"}, Anim: "spin"}).Validate(); err == nil {
		t.Fatal("an unknown animation validated")
	}
	if err := (Cue{Ticks: 10, Lines: []string{"X"}}).Validate(); err != nil {
		t.Fatalf("the default style and animation did not validate: %v", err)
	}
}

func TestLetterboxCoversTopAndBottomOnly(t *testing.T) {
	img := flat(100, 200, 0x80)
	Letterbox(img, 0.1)
	if got := img.Pix[img.PixOffset(50, 5)]; got != 0 {
		t.Fatalf("top bar is %d, want black", got)
	}
	if got := img.Pix[img.PixOffset(50, 195)]; got != 0 {
		t.Fatalf("bottom bar is %d, want black", got)
	}
	if got := img.Pix[img.PixOffset(50, 100)]; got != 0x80 {
		t.Fatalf("centre is %d, want the picture", got)
	}
	// The clamp keeps a script from letterboxing the picture away.
	wide := flat(100, 200, 0x80)
	Letterbox(wide, 0.9)
	if got := wide.Pix[wide.PixOffset(50, 100)]; got != 0x80 {
		t.Fatalf("an over-large letterbox covered the centre (%d)", got)
	}
}
