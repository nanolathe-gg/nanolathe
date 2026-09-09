package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestFNTLayoutMatchesSoftwareAfterFirstCodeTableBias(t *testing.T) {
	f := &formats.FNT{Height: 1, Baseline: -2, FirstCode: 32, Glyphs: [256]*formats.FNTGlyph{
		'A': {Width: 2, Height: 1, Bits: []byte{0xc0}},
	}}
	if got := baselineDescender(f); got != -2 {
		t.Fatalf("baseline = %d, want -2", got)
	}
	if code, ok := resolveGlyph(f, 'A'); !ok || code != 'A' {
		t.Fatalf("A resolves as %d, %v", code, ok)
	}
	if _, ok := resolveGlyph(f, 31); ok {
		t.Fatal("code below the first table entry resolved")
	}
	if got, want := measureText(f, " A"), client.MeasureText(f, " A"); got != want {
		t.Fatalf("GPU measure = %d, software = %d", got, want)
	}
	if got, want := truncateToWidth(f, "AAA", 4), client.TruncateToWidth(f, "AAA", 4); got != want {
		t.Fatalf("GPU truncate = %q, software = %q", got, want)
	}
}

func TestGlyphClipBoundsCarriesPrivateSurface(t *testing.T) {
	clipped := drawlist.Glyphs{HasClip: true, Clip: drawlist.Rect{X: 3, Y: -2, W: 8, H: 12}}
	if x0, y0, x1, y1 := glyphClipBounds(clipped, 8, 6); x0 != 3 || y0 != 0 || x1 != 8 || y1 != 6 {
		t.Fatalf("clipped bounds=%d,%d..%d,%d, want 3,0..8,6", x0, y0, x1, y1)
	}
	if x0, y0, x1, y1 := glyphClipBounds(drawlist.Glyphs{}, 8, 6); x0 != 0 || y0 != 0 || x1 != 8 || y1 != 6 {
		t.Fatalf("unclipped bounds=%d,%d..%d,%d, want framebuffer", x0, y0, x1, y1)
	}
}
