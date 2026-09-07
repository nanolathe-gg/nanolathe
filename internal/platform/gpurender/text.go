package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The FNT text family for the modern executor (docs/DESIGN_GPU_RENDERER.md §2.3,
// C-G4). Text is keyed, not destination-reading: a glyph's set bits write the
// run's single colour index and its clear bits leave the destination untouched,
// exactly as internal/client drawText writes `frame[index] = color` over the set
// bits [03 §7.1]. The layout — the truncate-to-width, the descender baseline, the
// offset-0 absent-glyph skip, the newline/NUL terminator, the per-glyph advance
// and the high-byte base bias — is reproduced from drawText/MeasureText/
// TruncateToWidth so the covered pixel set matches the byte writer [02 §7][07 §7].
//
// One glyph atlas per *formats.FNT packs every present glyph in a horizontal
// strip, the set bit flagged in green (the same opacity convention the GAF frame
// images use); the run colour rides the vertex red channel and the glyph shader
// keys on the green flag. The atlas is built on first use and cached by pointer
// identity for the font's lifetime.

// fntAtlas is one font's packed glyph strip and its per-code layout. img is a
// total-width × height image with the set-bit flag in green; xOffset[code] is a
// present glyph's left edge in img (-1 when the code is absent) and width[code] is
// its advance, both indexed by the RESOLVED glyph code (after the base bias).
type fntAtlas struct {
	img     *ebiten.Image
	height  int
	xOffset [256]int32
	width   [256]int32
}

// baselineDescender returns the signed baseline adjustment stored in the low byte
// of the FNT control word: glyph rows are drawn at y - descender [02 §7][03 §7.1].
// It mirrors internal/client baselineDescender so the modern layout matches the
// byte writer's.
func baselineDescender(fnt *formats.FNT) int {
	if fnt == nil {
		return 0
	}
	return int(int8(fnt.Unknown & 0xFF))
}

// resolveGlyph applies the high-byte base-char bias and the offset-0 absent-glyph
// rule, returning the resolved glyph code and whether the code is present,
// mirroring internal/client glyphFor [02 §7]. Retail fonts store bias 0 so the
// subtraction never fires; it is reproduced for completeness.
func resolveGlyph(fnt *formats.FNT, code int) (int, bool) {
	if fnt == nil || code < 0 || code > 255 {
		return 0, false
	}
	bias := int((fnt.Unknown >> 8) & 0xFF)
	if bias != 0 {
		if code < bias {
			return 0, false
		}
		code -= bias
	}
	if code < 0 || code > 255 || fnt.Glyphs[code] == nil {
		return 0, false
	}
	return code, true
}

// measureText sums the glyph advance of one line up to the first newline or NUL,
// skipping absent glyphs, mirroring internal/client MeasureText [02 §7][03 §7.1].
func measureText(fnt *formats.FNT, text string) int {
	if fnt == nil || len(text) == 0 {
		return 0
	}
	width := 0
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == 0x0A || b == 0x00 {
			break
		}
		code, ok := resolveGlyph(fnt, int(b))
		if !ok {
			continue
		}
		width += int(fnt.Glyphs[code].Width)
	}
	return width
}

// truncateToWidth removes trailing bytes until the advance fits maxWidth, through
// the retail bounded 300-byte buffer, mirroring internal/client TruncateToWidth
// [07 §7][GAP T22]. maxWidth <= 0 returns the input unchanged.
func truncateToWidth(fnt *formats.FNT, text string, maxWidth int) string {
	if maxWidth <= 0 || fnt == nil || len(text) == 0 {
		return text
	}
	const limit = 300
	src := text
	if len(src) > limit {
		src = src[:limit]
	}
	if measureText(fnt, src) <= maxWidth {
		return src
	}
	for len(src) > 0 && measureText(fnt, src) > maxWidth {
		src = src[:len(src)-1]
	}
	return src
}

// buildFNTAtlas packs every present glyph of fnt into a horizontal strip with the
// set-bit flag in green (255) and alpha opaque so premultiplied sampling recovers
// the flag; clear bits stay zero (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). The
// per-code left edge and width are recorded for the draw walk. A font with no
// present glyph yields nil.
func buildFNTAtlas(fnt *formats.FNT) *fntAtlas {
	if fnt == nil || fnt.Height == 0 {
		return nil
	}
	h := int(fnt.Height)
	a := &fntAtlas{height: h}
	total := 0
	for i := 0; i < 256; i++ {
		g := fnt.Glyphs[i]
		if g == nil {
			a.xOffset[i] = -1
			continue
		}
		a.xOffset[i] = int32(total)
		a.width[i] = int32(g.Width)
		total += int(g.Width)
	}
	if total <= 0 {
		return nil
	}
	buf := make([]byte, total*h*4)
	for i := 0; i < 256; i++ {
		g := fnt.Glyphs[i]
		if g == nil {
			continue
		}
		xoff := int(a.xOffset[i])
		gw := int(g.Width)
		for gy := 0; gy < h; gy++ {
			for gx := 0; gx < gw; gx++ {
				if !g.On(gx, gy) {
					continue
				}
				p := (gy*total + xoff + gx) * 4
				buf[p+1] = 255 // green: set-bit flag
				buf[p+3] = 255 // alpha opaque so premultiplied green survives
			}
		}
	}
	img := ebiten.NewImage(total, h)
	img.WritePixels(buf)
	a.img = img
	return a
}

// fntAtlasFor returns the cached glyph atlas for fnt, building it on first use and
// caching it (including a nil for a font with no present glyph) by pointer
// identity (docs/DESIGN_GPU_RENDERER.md §2.3).
func (r *Renderer) fntAtlasFor(fnt *formats.FNT) *fntAtlas {
	if fnt == nil {
		return nil
	}
	if a, ok := r.fntAtlases[fnt]; ok {
		return a
	}
	a := buildFNTAtlas(fnt)
	r.fntAtlases[fnt] = a
	return a
}

// Glyphs replays one FNT text run, reproducing drawText's layout exactly: the
// truncate-to-width before clipping, the descender baseline, the offset-0 skip,
// the per-glyph advance and the newline/NUL terminator (docs/DESIGN_GPU_RENDERER.md
// §2.3)[02 §7][03 §7.1][07 §7]. Each glyph is a keyed quad from the font atlas to
// its screen rectangle, clipped to the framebuffer; the run colour rides the
// vertex red channel and the glyph shader writes it where the atlas marks a set
// bit, leaving the destination untouched elsewhere (C-G4).
func (r *Renderer) Glyphs(g drawlist.Glyphs) {
	if r == nil || r.offscreen == nil || r.glyph == nil {
		return
	}
	fnt := g.Font
	if fnt == nil || fnt.Height == 0 || len(g.Text) == 0 {
		return
	}
	atlas := r.fntAtlasFor(fnt)
	if atlas == nil || atlas.img == nil {
		return
	}
	text := g.Text
	// Truncate-to-width happens before clipping, exactly as drawText does [07 §7].
	if int(g.MaxWidth) > 0 {
		text = truncateToWidth(fnt, text, int(g.MaxWidth))
		if len(text) == 0 {
			return
		}
	}
	top := int(g.Y) - baselineDescender(fnt)
	curX := int(g.X)
	gh := atlas.height
	col := float32(g.Color) / 255.0
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == 0x0A || b == 0x00 { // newline / NUL terminates the advance [02 §7].
			break
		}
		code, ok := resolveGlyph(fnt, int(b))
		if !ok { // offset 0 means absent glyph, skipped [02 §7].
			continue
		}
		gw := int(atlas.width[code])
		if gw > 0 {
			xoff := int(atlas.xOffset[code])
			// The glyph is a rectangle, so drawText's per-pixel framebuffer clip is
			// the rectangular intersection of [curX,curX+gw)×[top,top+gh) with the
			// framebuffer; the source sub-rect shifts to match.
			c0, c1 := maxInt(0, -curX), minInt(gw, r.w-curX)
			r0, r1 := maxInt(0, -top), minInt(gh, r.h-top)
			if c0 < c1 && r0 < r1 {
				r.appendGlyphQuad(
					float32(curX+c0), float32(top+r0), float32(curX+c1), float32(top+r1),
					float32(xoff+c0), float32(r0), float32(xoff+c1), float32(r1),
					col)
			}
		}
		curX += gw // advance; space advances via its glyph width [03 §7.1].
	}
	if len(r.verts) == 0 {
		return
	}
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.glyph, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendSourceOver,
		Images: [4]*ebiten.Image{atlas.img, nil, nil, nil},
	})
}

// appendGlyphQuad appends one axis-aligned glyph quad sampling the atlas
// [sx0,sx1)×[sy0,sy1) across the screen [dx0,dx1)×[dy0,dy1) rectangle, every
// vertex carrying the run colour in the red channel (C-G4).
func (r *Renderer) appendGlyphQuad(dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1, col float32) {
	base := uint16(len(r.verts))
	r.verts = append(r.verts,
		ebiten.Vertex{DstX: dx0, DstY: dy0, SrcX: sx0, SrcY: sy0, ColorR: col, ColorA: 1},
		ebiten.Vertex{DstX: dx1, DstY: dy0, SrcX: sx1, SrcY: sy0, ColorR: col, ColorA: 1},
		ebiten.Vertex{DstX: dx0, DstY: dy1, SrcX: sx0, SrcY: sy1, ColorR: col, ColorA: 1},
		ebiten.Vertex{DstX: dx1, DstY: dy1, SrcX: sx1, SrcY: sy1, ColorR: col, ColorA: 1},
	)
	r.idx = append(r.idx, base, base+1, base+2, base+1, base+2, base+3)
}
