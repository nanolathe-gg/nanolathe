package gpurender

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The FNT text family for the modern executor (docs/DESIGN_GPU_RENDERER.md §2.3,
// C-G4). Text is keyed, not destination-reading: a glyph's set bits write the
// run's single colour index and its clear bits leave the destination untouched,
// exactly as internal/client drawTextClipped writes `frame[index] = color` over the set
// bits [03 §7.1]. The layout — the truncate-to-width, the descender baseline, the
// offset-0 absent-glyph skip, the newline/NUL terminator, the per-glyph advance
// and first-character table bias — is reproduced from drawTextClipped/MeasureText/
// TruncateToWidth so the covered pixel set matches the byte writer [fmt fnt][07 §7].
//
// One glyph strip per *formats.FNT packs every present glyph horizontally into
// the scene atlas, the set bit flagged in green (the same opacity convention the
// GAF frame regions use); the run colour rides the vertex red channel and the
// scene shader's glyph op keys on the green flag. The strip is packed on first
// use and cached by pointer identity for the font's lifetime, so a text run
// merges into the same device draw as the sprites and fills around it (§11.2).

// fntAtlas is one font's packed glyph strip and its per-code layout. entry is the
// strip's scene atlas placement; xOffset[code] is a present glyph's left edge in
// scene atlas coordinates (-1 when the code is absent) and width[code] is its
// advance, both indexed by the character code.
type fntAtlas struct {
	entry   sceneEntry
	height  int
	xOffset [256]int32
	width   [256]int32
}

// baselineDescender returns the signed FNT baseline adjustment. It mirrors the
// software rasterizer so the modern layout matches the byte writer [03 R-FONT-01 §1].
func baselineDescender(fnt *formats.FNT) int {
	if fnt == nil {
		return 0
	}
	return int(fnt.Baseline)
}

// resolveGlyph applies the offset-0 absent-glyph rule, returning the character
// code and whether the code is present. LoadFNT has already applied the
// first-character table bias when it stored Glyphs [fmt fnt].
func resolveGlyph(fnt *formats.FNT, code int) (int, bool) {
	if fnt == nil || code < 0 || code > 255 {
		return 0, false
	}
	if fnt.Glyphs[code] == nil {
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

// truncateToWidth removes trailing bytes until the advance fits maxWidth,
// through the retail bounded copy, mirroring internal/client TruncateToWidth
// [07 §7][03 R-FONT-01 §3][GAP T22]. maxWidth <= 0 returns the input unchanged.
func truncateToWidth(fnt *formats.FNT, text string, maxWidth int) string {
	if maxWidth <= 0 || fnt == nil || len(text) == 0 {
		return text
	}
	// The 300-byte stack buffer is filled by a bounded copy of at most 299
	// bytes, so a longer string is cut to 299 before the width loop begins
	// [03 R-FONT-01 §3]. This mirrors internal/client.TextBufferBytes; the
	// modern executor does not import the software renderer, and text_test.go
	// cross-checks the two truncators against each other.
	const textBufferBytes = 299
	src := text
	if len(src) > textBufferBytes {
		src = src[:textBufferBytes]
	}
	if measureText(fnt, src) <= maxWidth {
		return src
	}
	for len(src) > 0 && measureText(fnt, src) > maxWidth {
		src = src[:len(src)-1]
	}
	return src
}

// buildFNTAtlas packs every present glyph of fnt into a horizontal strip in the
// scene atlas with the set-bit flag in green (255) and alpha opaque so
// premultiplied sampling recovers the flag; clear bits stay zero
// (docs/DESIGN_GPU_RENDERER.md §2.3, §11.2, C-G4). The per-code left edge — in
// scene atlas coordinates — and width are recorded for the draw walk. A font with
// no present glyph yields nil.
func (r *Renderer) buildFNTAtlas(fnt *formats.FNT) *fntAtlas {
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
	a.entry = r.scene.allocate(total, h)
	if !a.entry.ok {
		return nil
	}
	buf := r.scene.scratch(total * h * 4)
	for i := range buf {
		buf[i] = 0
	}
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
	r.scene.upload(a.entry, buf)
	// Rebase the per-code left edges onto the packed page, so a glyph quad
	// samples the strip in scene atlas coordinates.
	for i := 0; i < 256; i++ {
		if a.xOffset[i] >= 0 {
			a.xOffset[i] += a.entry.x
		}
	}
	return a
}

// fntAtlasFor returns the cached glyph strip for fnt, packing it on first use and
// caching it (including a nil for a font with no present glyph) by pointer
// identity (docs/DESIGN_GPU_RENDERER.md §2.3).
func (r *Renderer) fntAtlasFor(fnt *formats.FNT) *fntAtlas {
	if fnt == nil {
		return nil
	}
	if a, ok := r.scene.fonts[fnt]; ok {
		return a
	}
	a := r.buildFNTAtlas(fnt)
	if r.scene.fonts == nil {
		r.scene.fonts = make(map[*formats.FNT]*fntAtlas)
	}
	r.scene.fonts[fnt] = a
	return a
}

// glyphClipBounds returns the pixel boundary used by the text executor. A
// Glyphs record owns this boundary so deferred replay cannot borrow a later UI
// surface's clip.
func glyphClipBounds(g drawlist.Glyphs, width, height int) (x0, y0, x1, y1 int) {
	x0, y0, x1, y1 = 0, 0, width, height
	if !g.HasClip {
		return
	}
	x0, y0 = max(int(g.Clip.X), 0), max(int(g.Clip.Y), 0)
	x1, y1 = min(int(g.Clip.X+g.Clip.W), width), min(int(g.Clip.Y+g.Clip.H), height)
	return
}

// Glyphs replays one FNT text run, reproducing drawTextClipped's layout exactly: the
// truncate-to-width before clipping, the descender baseline, the offset-0 skip,
// the per-glyph advance and the newline/NUL terminator (docs/DESIGN_GPU_RENDERER.md
// §2.3)[02 §7][03 §7.1][07 §7]. Each glyph is a keyed quad from the font atlas to
// its screen rectangle, clipped to the framebuffer; the run colour rides the
// vertex red channel and the glyph shader writes it where the atlas marks a set
// bit, leaving the destination untouched elsewhere (C-G4).
func (r *Renderer) Glyphs(g drawlist.Glyphs) {
	if r == nil || r.surfaces[0] == nil || r.scene2D == nil {
		return
	}
	fnt := g.Font
	if fnt == nil || fnt.Height == 0 || len(g.Text) == 0 {
		return
	}
	text := g.Text
	// Truncate-to-width happens before clipping, exactly as drawTextClipped does [07 §7].
	if int(g.MaxWidth) > 0 {
		text = truncateToWidth(fnt, text, int(g.MaxWidth))
		if len(text) == 0 {
			return
		}
	}
	// Whole-string admission uses the baseline origin, not the adjusted glyph
	// top, and tests one-past text edges against inclusive clip bounds
	// [03 R-FONT-01 §3].
	clipX0, clipY0, clipX1, clipY1 := glyphClipBounds(g, r.clipW(), r.clipH())
	if int(g.X) < clipX0 || int(g.Y) < clipY0 || int(g.X)+measureText(fnt, text) >= clipX1 || int(g.Y)+int(fnt.Height) >= clipY1 {
		return
	}
	atlas := r.fntAtlasFor(fnt)
	if atlas == nil || !atlas.entry.ok {
		return
	}
	// Once admitted, the signed baseline may overrun the private surface.
	// Only framebuffer bounds remain as host storage protection.
	clipX0, clipY0, clipX1, clipY1 = 0, 0, r.clipW(), r.clipH()
	top := int(g.Y) - baselineDescender(fnt)
	curX := int(g.X)
	gh := atlas.height
	// The scheduler bounds cover the baseline-adjusted pixels.
	bx0, by0 := max(curX, clipX0), max(top, clipY0)
	bx1 := min(curX+measureText(fnt, text), clipX1)
	by1 := min(top+gh, clipY1)
	if !r.sched.begin(schedOpaque, bx0, by0, bx1, by1, r.sceneImages(atlas.entry)) {
		return
	}
	col := float32(g.Color)
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
			yoff := int(atlas.entry.y)
			// The glyph is a rectangle, so drawTextClipped's per-pixel framebuffer clip is
			// the rectangular intersection of [curX,curX+gw)×[top,top+gh) with the
			// framebuffer; the source sub-rect shifts to match.
			c0, c1 := max(clipX0-curX, 0), min(gw, clipX1-curX)
			r0, r1 := max(clipY0-top, 0), min(gh, clipY1-top)
			if c0 < c1 && r0 < r1 {
				r.sched.quad(schedOpaque,
					float32(curX+c0), float32(top+r0), float32(curX+c1), float32(top+r1),
					float32(xoff+c0), float32(yoff+r0), float32(xoff+c1), float32(yoff+r1),
					[4]float32{col, 0, 0, 0}, [4]float32{0, 0, 0, sceneOpGlyph})
			}
		}
		curX += gw // advance; space advances via its glyph width [03 §7.1].
	}
}
