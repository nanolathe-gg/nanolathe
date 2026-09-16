package gpurender

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"testing"
)

// FNT admission uses the whole unadjusted string rectangle, including its
// one-past right/bottom edges [03 R-FONT-01 §3].
func TestGlyphsWholeStringAdmissionAndBaseline(t *testing.T) {
	r, _ := schedulerFixture(t)
	font := &formats.FNT{Height: 2, Baseline: 1}
	font.Glyphs['A'] = &formats.FNTGlyph{Width: 3, Height: 2, Bits: []byte{0xfc}}
	for _, tc := range []struct {
		name string
		x, y int32
		want bool
	}{
		{"inside", 2, 2, true}, {"inclusive one-past bounds", 3, 3, true}, {"left", 1, 2, false}, {"top", 2, 1, false},
		{"right last pixel", 4, 2, false}, {"bottom last pixel", 2, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.sched.resetFrame(16, 16)
			r.Glyphs(drawlist.Glyphs{Font: font, Text: "A", X: tc.x, Y: tc.y, Color: 9, HasClip: true, Clip: drawlist.Rect{X: 2, Y: 2, W: 5, H: 4}})
			verts := r.sched.classVerts(schedOpaque)
			if (len(verts) != 0) != tc.want {
				t.Fatalf("vertices=%d, admitted=%v", len(verts), tc.want)
			}
			if tc.want && (verts[0].DstY != float32(tc.y-1) || verts[3].DstY != float32(tc.y+1)) {
				t.Fatalf("baseline overrun was clipped: %+v", verts)
			}
		})
	}
	// Host-storage clipping retains source coordinates for an admitted glyph
	// whose baseline places its first row above the framebuffer.
	r.sched.resetFrame(16, 16)
	r.Glyphs(drawlist.Glyphs{Font: font, Text: "A", X: 2, Y: 0, Color: 9})
	verts := r.sched.classVerts(schedOpaque)
	if len(verts) != 4 || verts[0].DstY != 0 || verts[3].DstY != 1 || verts[2].SrcY-verts[0].SrcY != 1 {
		t.Fatalf("host-storage crop=%+v", verts)
	}
}
