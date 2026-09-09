package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// TestGlyphsClipCompilesMatchingDestinationAndSourceQuads exercises the real
// Renderer.Glyphs scheduler path. The source span must shrink with the
// destination span: clipping only destination coordinates stretches the glyph
// back across the clipped rectangle instead of preserving its bitmap pixels.
func TestGlyphsClipCompilesMatchingDestinationAndSourceQuads(t *testing.T) {
	r, _ := schedulerFixture(t)
	font := &formats.FNT{Height: 4}
	font.Glyphs['A'] = &formats.FNTGlyph{Width: 4, Height: 4, Bits: []byte{0xf0, 0xf0, 0xf0, 0xf0}}

	r.sched.resetFrame(16, 16)
	r.Glyphs(drawlist.Glyphs{
		Font: font, Text: "A", X: 2, Y: 4, Color: 9,
		HasClip: true, Clip: drawlist.Rect{X: 4, Y: 5, W: 2, H: 2},
	})
	verts := r.sched.classVerts(schedOpaque)
	if len(verts) != 4 {
		t.Fatalf("clipped glyph vertices=%d, want one quad", len(verts))
	}
	if verts[0].DstX != 4 || verts[0].DstY != 5 || verts[3].DstX != 6 || verts[3].DstY != 7 {
		t.Fatalf("clipped destination=%+v..%+v, want (4,5)..(6,7)", verts[0], verts[3])
	}
	if got := verts[1].SrcX - verts[0].SrcX; got != 2 {
		t.Fatalf("clipped source width=%v, want 2 to match destination crop", got)
	}
	if got := verts[2].SrcY - verts[0].SrcY; got != 2 {
		t.Fatalf("clipped source height=%v, want 2 to match destination crop", got)
	}

	clippedOrigin := verts[0]
	r.sched.resetFrame(16, 16)
	r.Glyphs(drawlist.Glyphs{
		Font: font, Text: "A", X: 2, Y: 4, Color: 9,
		HasClip: true, Clip: drawlist.Rect{X: 12, Y: 12, W: 2, H: 2},
	})
	if got := len(r.sched.classVerts(schedOpaque)); got != 0 {
		t.Fatalf("clipped-away glyph vertices=%d, want none", got)
	}

	r.sched.resetFrame(16, 16)
	r.Glyphs(drawlist.Glyphs{Font: font, Text: "A", X: 2, Y: 4, Color: 9})
	verts = r.sched.classVerts(schedOpaque)
	if len(verts) != 4 {
		t.Fatalf("unclipped glyph vertices=%d, want one full quad", len(verts))
	}
	if verts[0].DstX != 2 || verts[0].DstY != 4 || verts[3].DstX != 6 || verts[3].DstY != 8 {
		t.Fatalf("unclipped destination=%+v..%+v, want (2,4)..(6,8)", verts[0], verts[3])
	}
	if clippedOrigin.SrcX-verts[0].SrcX != 2 || clippedOrigin.SrcY-verts[0].SrcY != 1 {
		t.Fatalf("source origin did not advance by the destination crop: clipped=%+v full=%+v", clippedOrigin, verts[0])
	}
	if got := verts[1].SrcX - verts[0].SrcX; got != 4 {
		t.Fatalf("unclipped source width=%v, want 4", got)
	}
}
