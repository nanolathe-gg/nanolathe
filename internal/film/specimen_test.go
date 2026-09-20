package film

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
)

// TestFontSpecimen writes a sheet of the whole face when FILM_SPECIMEN names
// a path. A stroke face regresses invisibly — a glyph can lose a segment and
// still measure and draw ink — so the reviewer's check is to look at the
// sheet (docs/FILM_CAPTURE.md "Titles"). It writes nothing by default.
func TestFontSpecimen(t *testing.T) {
	out := os.Getenv("FILM_SPECIMEN")
	if out == "" {
		t.Skip("set FILM_SPECIMEN to a path to write the specimen sheet")
	}
	img := flat(1100, 420, 0x18)
	st := TextStyle{Size: 64, Color: color.RGBA{R: 0xf2, G: 0xf4, B: 0xf7, A: 0xff}, Tracking: 0.12, Halo: 0.05, Shadow: 0.07}
	DrawText(img, "ABCDEFGHIJKLM", 40, 100, st, 1, math.Inf(1))
	DrawText(img, "NOPQRSTUVWXYZ", 40, 200, st, 1, math.Inf(1))
	DrawText(img, "0123456789 &+=", 40, 300, st, 1, math.Inf(1))
	DrawText(img, "NANOLATHE: MIT'S (2026) -- 100%!?", 40, 390, TextStyle{Size: 34, Color: st.Color, Tracking: 0.1, Halo: 0.05}, 1, math.Inf(1))
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
