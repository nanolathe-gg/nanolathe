package film

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
)

// TestFontSpecimen writes a visual proof when FILM_SPECIMEN names a path.
// It exercises both faces, headline/caption scale, kerning, punctuation and
// contrast over a busy authored background. It writes nothing by default.
func TestFontSpecimen(t *testing.T) {
	out := os.Getenv("FILM_SPECIMEN")
	if out == "" {
		t.Skip("set FILM_SPECIMEN to a path to write the specimen sheet")
	}
	img := flat(1600, 1000, 0x18)
	for y := 0; y < 540; y++ {
		for x := 0; x < 1600; x++ {
			texture := 0.5 + 0.18*math.Sin(float64(x)*0.037+float64(y)*0.021) + 0.12*math.Sin(float64(x-y)*0.13) + 0.12*math.Cos(float64(x+y)*0.083)
			img.SetRGBA(x, y, color.RGBA{R: uint8(65 + 90*texture), G: uint8(72 + 92*texture), B: uint8(48 + 66*texture), A: 255})
		}
	}
	Cue{Ticks: 100, Lines: []string{"NANOLATHE"}, Size: 0.13, Y: 0.26, Scrim: 0.25}.Draw(img, 30)
	Cue{Ticks: 100, Style: "subtitle", Lines: []string{"TOTAL ANNIHILATION. REBUILT."}, Size: 0.027, Y: 0.335, Color: []uint8{255, 190, 90}}.Draw(img, 30)
	Cue{Ticks: 100, Style: "caption", Lines: []string{"A new engine for a war without end."}, Size: 0.026, Y: 0.46}.Draw(img, 30)
	white := color.RGBA{R: 242, G: 244, B: 247, A: 255}
	amber := color.RGBA{R: 255, G: 190, B: 90, A: 255}
	DrawText(img, "DISPLAY / BARLOW CONDENSED EXTRABOLD", 70, 597, TextStyle{Font: "body", Size: 16, Tracking: 0.10, Color: amber}, 1, math.Inf(1))
	DrawText(img, "ABCDEFGHIJKLMNOPQRSTUVWXYZ", 70, 672, TextStyle{Size: 56, Tracking: 0.025, Color: white}, 1, math.Inf(1))
	DrawText(img, "0123456789 &+= 100%!? — “OPEN SOURCE”", 70, 738, TextStyle{Size: 38, Color: white}, 1, math.Inf(1))
	DrawText(img, "BODY / BARLOW MEDIUM", 70, 800, TextStyle{Font: "body", Size: 16, Tracking: 0.10, Color: amber}, 1, math.Inf(1))
	DrawText(img, "Build an army. Command the battlefield.", 70, 853, TextStyle{Font: "body", Size: 30, Color: white}, 1, math.Inf(1))
	DrawText(img, "abcdefghijklmnopqrstuvwxyz 0123456789 / (MIT) & more…", 70, 904, TextStyle{Font: "body", Size: 23, Color: white}, 1, math.Inf(1))
	DrawText(img, "Small captions remain legible: AVATAR / Factory production / 60 FPS", 70, 951, TextStyle{Font: "body", Size: 17, Color: white}, 1, math.Inf(1))
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
