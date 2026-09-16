package client

// FNT text rendering for authored UI surfaces.
//
// Retail contracts implemented here [02 §7][03 §7.1][07 §7][GAP T22] C8:
//
//   - FNT file: four one-byte header fields, then an offset table from its
//     first character code through 255. Offset 0 means absent glyph, skipped
//     in both measurement and drawing.
//     Space (code 32) has non-zero offset, width 7, all-zero bitmap — advances
//     without drawing.
//   - Baseline descender: glyph rows are drawn at y - the signed baseline byte.
//     LoadFNT applies the first-character table bias once when it stores each
//     glyph under its character code [fmt fnt][03 R-FONT-01 §1].
//   - 0x0A (newline) terminates advance: both measurement and drawing stop at
//     the first 0x0A or NUL [02 §7]. Bytes after the terminator are ignored for
//     width and are not drawn.
//   - Text truncation to max width happens before clipping: when a maximum width
//     is supplied and the total glyph advance exceeds it, the string is copied
//     through a bounded 300-byte buffer and trailing bytes are removed until the
//     measured advance fits [07 §7][GAP T22]. Clipping to the indexed framebuffer
//     happens only after truncation.
//   - Glyph bits are 1-bit, most-significant-bit first, packed continuously
//     across rows (not byte-aligned) [fmt fnt]. Set bits write the current text
//     color; clear bits are transparent.
//
// This file writes physical palette indices into the indexed framebuffer that
// client.Frame composes at logical size. GUI semantic colors are resolved
// through the logical map before recording the command; presentation later
// expands these physical indices to RGBA (C7).
//
// No simulation state is read or written here (I6). RNG draw counts shown in
// the overlay are passed in by the caller.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
)

// baselineDescender returns the signed FNT baseline adjustment. Glyph rows are
// drawn at y - descender [03 R-FONT-01 §1].
func baselineDescender(fnt *formats.FNT) int { // [02 §7]
	if fnt == nil {
		return 0
	}
	return int(fnt.Baseline)
}

// glyphFor returns the glyph that retail would use for byte code. LoadFNT
// applies the first-code table bias while constructing Glyphs, so this lookup
// uses the text byte directly [fmt fnt]. Offset 0 (nil entry) means absent.
func glyphFor(fnt *formats.FNT, code int) *formats.FNTGlyph { // [02 §7]
	if fnt == nil || code < 0 || code > 255 {
		return nil
	}
	return fnt.Glyphs[code]
}

// MeasureText returns the pixel advance of one line of FNT text [02 §7][03 §7.1] C8.
//
// It sums glyph Width for each present glyph until the first 0x0A (newline) or
// NUL, skipping absent glyphs (offset 0). Space advances 7 via its glyph
// width; no extra kerning is added.
func MeasureText(fnt *formats.FNT, text string) int { // [02 §7][03 §7.1]
	if fnt == nil || len(text) == 0 {
		return 0
	}
	width := 0
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == 0x0A || b == 0x00 { // 0x0A terminates advance; NUL also terminates [02 §7]
			break
		}
		g := glyphFor(fnt, int(b))
		if g == nil { // offset 0 means absent glyph [02 §7][03 §7.1]
			continue
		}
		width += int(g.Width)
	}
	return width
}

// TextBufferBytes is the most bytes the retail truncate-to-width step keeps:
// the 300-byte stack buffer is filled by a bounded copy of at most 299 bytes,
// and a longer string is cut to 299 before the width loop begins [03
// R-FONT-01 §3]. gpurender mirrors the value; the packages do not import one
// another, and text_test.go there cross-checks the two truncators agree.
const TextBufferBytes = 299

// TruncateToWidth implements the retail truncate-to-width contract [07 §7][03
// R-FONT-01 §3][GAP T22] C8.
//
// When maxWidth <= 0 the input is returned unchanged (no limit). Otherwise the
// string is copied through a bounded copy of at most TextBufferBytes bytes and
// trailing bytes are removed until MeasureText fits within maxWidth. This
// happens before any clipping test [07 §7][03 R-FONT-01 §3].
//
// The input is treated as bytes (Windows-1252), not runes; byte 0x0A or NUL
// terminates the measured prefix [02 §7].
func TruncateToWidth(fnt *formats.FNT, text string, maxWidth int) string { // [07 §7][GAP T22]
	if maxWidth <= 0 || fnt == nil || len(text) == 0 {
		return text
	}
	// Bounded copy into the 300-byte stack buffer, which is filled by a copy of
	// at most 299 bytes: a longer string is cut to 299 before the width loop
	// begins [03 R-FONT-01 §3]. The measured prefix ends at the first
	// newline/NUL, but the bytes after that terminator remain harmless data in
	// the bounded text buffer when no width truncation is needed; the draw and
	// measure loops stop at the terminator [02 §7].
	src := text
	if len(src) > TextBufferBytes {
		src = src[:TextBufferBytes]
	}
	// If it already fits, return the bounded copy unchanged; drawing and
	// measurement still stop at the first terminator.
	if MeasureText(fnt, src) <= maxWidth {
		return src
	}
	// Remove trailing bytes one at a time until it fits. Each iteration
	// re-measures exactly as retail does [07 §7].
	for len(src) > 0 && MeasureText(fnt, src) > maxWidth {
		src = src[:len(src)-1]
	}
	return src
}

// DrawText draws text into the indexed framebuffer using FNT glyphs [02 §7][03 §7.1] C8.
//
//   - frame is row-major width×height indexed pixels (physical palette indices).
//   - fnt is the bitmap font (*formats.FNT); nil is a no-op.
//   - text is treated as bytes; 0x0A or NUL terminates the advance [02 §7].
//   - x,y is the baseline origin: glyph rows are drawn at y - the signed FNT
//     baseline byte [03 R-FONT-01 §1]. curX advances by glyph Width per present
//     glyph; absent glyphs (offset 0) are skipped [03 §7.1].
//   - maxWidth > 0 enables truncate-to-width before clipping: the string is
//     truncated via TruncateToWidth until its advance fits [07 §7][GAP T22].
//   - color is the indexed color written where glyph bits are set (1-bit,
//     MSB first, packed continuously [fmt fnt]); clear bits are transparent.
//   - The unadjusted whole-string rectangle must fit the inclusive clip bounds.
//     Baseline overrun is bounded only by framebuffer storage [03 R-FONT-01 §3].
//
// The caller has already resolved semantic colors through the logical map
// (C7). No simulation state is touched (I6).
func DrawText(frame []uint8, width, height int, fnt *formats.FNT, text string, x, y, maxWidth int, color byte) { // [02 §7][03 §7.1][07 §7]
	drawText(frame, width, height, fnt, text, x, y, maxWidth, color, nil)
}

// drawText is the common FNT rasterizer. onWrite is retained for the generic
// helper shape used by the text tests; retail GUI callers pass nil because
// glyph colors are already active palette indices.
func drawText(frame []uint8, width, height int, fnt *formats.FNT, text string, x, y, maxWidth int, color byte, onWrite func(int)) { // [02 §7][03 §7.1][07 §7]
	drawTextClipped(frame, width, height, fnt, text, x, y, maxWidth, color, 0, 0, width, height, onWrite)
}

// drawTextClipped admits the entire unadjusted string rectangle against the
// private surface before applying the baseline [03 R-FONT-01 §3].
func drawTextClipped(frame []uint8, width, height int, fnt *formats.FNT, text string, x, y, maxWidth int, color byte, clipX, clipY, clipW, clipH int, onWrite func(int)) {
	if len(frame) < width*height || fnt == nil || width <= 0 || height <= 0 || len(text) == 0 {
		return
	}
	if clipW <= 0 || clipH <= 0 {
		return
	}
	clipRight, clipBottom := clipX+clipW, clipY+clipH
	if maxWidth > 0 {
		text = TruncateToWidth(fnt, text, maxWidth)
		if len(text) == 0 {
			return
		}
	}
	// The text rectangle's one-past edge is tested against the clip's last
	// pixel: one column/row of slack is required [03 R-FONT-01 §3]. The
	// baseline is deliberately absent from this test.
	if x < clipX || y < clipY || x+MeasureText(fnt, text) >= clipRight || y+int(fnt.Height) >= clipBottom {
		return
	}
	desc := baselineDescender(fnt) // signed baseline [03 R-FONT-01 §1]
	top := y - desc
	curX := x
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == 0x0A || b == 0x00 { // 0x0A terminates advance [02 §7][03 §7.1]
			break
		}
		g := glyphFor(fnt, int(b))
		if g == nil { // offset 0 means absent glyph [02 §7][03 §7.1]
			continue
		}
		// Blit 1bpp glyph, MSB first, packed continuously [fmt fnt].
		// Retail allows baseline rows outside the private clip. Bound only
		// host storage here, preserving the documented overrun when in range.
		for gy := 0; gy < int(g.Height); gy++ {
			dy := top + gy
			if dy < 0 || dy >= height {
				continue
			}
			rowBase := dy * width
			for gx := 0; gx < int(g.Width); gx++ {
				if !g.On(gx, gy) {
					continue
				}
				dx := curX + gx
				if dx < 0 || dx >= width {
					continue
				}
				index := rowBase + dx
				frame[index] = color
				if onWrite != nil {
					onWrite(index)
				}
			}
		}
		curX += int(g.Width) // advance; space advances 7 via its glyph [03 §7.1]
	}
}
