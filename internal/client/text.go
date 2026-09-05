package client

// FNT text rendering for authored UI surfaces.
//
// Retail contracts implemented here [02 §7][03 §7.1][07 §7][GAP T22] C8:
//
//   - FNT file: 16-bit height, 16-bit control word, 256-entry offset table.
//     Offset 0 means absent glyph, skipped in both measurement and drawing.
//     Space (code 32) has non-zero offset, width 7, all-zero bitmap — advances
//     without drawing.
//   - Baseline descender: glyph rows are drawn at y - *(char*)(fnt+2) [02 §7].
//     The field at offset 2 is the low byte of the 16-bit control word
//     (fnt.Unknown). It is interpreted as signed 8-bit.
//   - High byte of the control word is a base-character bias applied during
//     width measurement: measured index becomes code - bias, gated to apply only
//     when bias <= code [02 §7]. Retail fonts store 0 (values 1–3 occupy only
//     the low byte) so the subtraction never fires; it is implemented for
//     completeness.
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
// This file writes directly into the indexed framebuffer (active palette
// indices) that client.Frame composes at logical size. Palette conversion
// happens at present time in convertIndexedToRGBA (C7). GUI semantic color
// fields are resolved by the GUI caller before invoking this rasterizer.
//
// No simulation state is read or written here (I6). RNG draw counts shown in
// the overlay are passed in by the caller.

import (
	"github.com/nanolathe/nanolathe/formats"
)

// baselineDescender returns the signed baseline adjustment stored in the low
// byte of the FNT control word. Glyph rows are drawn at y - descender
// [02 §7][03 §7.1] C8.
//
// The control word is fnt.Unknown; the low byte at file offset 2 is
// interpreted as signed char: *(char*)(fnt+2).
func baselineDescender(fnt *formats.FNT) int { // [02 §7]
	if fnt == nil {
		return 0
	}
	return int(int8(fnt.Unknown & 0xFF))
}

// baseCharBias returns the high byte of the control word, used only during
// width measurement [02 §7]. Retail fonts store 0 so it never activates.
func baseCharBias(fnt *formats.FNT) int { // [02 §7]
	if fnt == nil {
		return 0
	}
	return int((fnt.Unknown >> 8) & 0xFF)
}

// glyphFor returns the glyph that retail would use for byte code, applying the
// high-byte base-char bias for measurement/draw when active [02 §7]. Offset 0
// (nil entry) means absent glyph and is skipped.
func glyphFor(fnt *formats.FNT, code int) *formats.FNTGlyph { // [02 §7]
	if fnt == nil || code < 0 || code > 255 {
		return nil
	}
	bias := baseCharBias(fnt)
	if bias != 0 {
		if code < bias {
			return nil
		}
		code -= bias
	}
	if code < 0 || code > 255 {
		return nil
	}
	return fnt.Glyphs[code]
}

// MeasureText returns the pixel advance of one line of FNT text [02 §7][03 §7.1] C8.
//
// It sums glyph Width for each present glyph until the first 0x0A (newline) or
// NUL, skipping absent glyphs (offset 0). The high-byte base-char bias is
// applied as retail does [02 §7]. Space advances 7 via its glyph width; no
// extra kerning is added.
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

// TruncateToWidth implements the retail truncate-to-width contract [07 §7][GAP T22] C8.
//
// When maxWidth <= 0 the input is returned unchanged (no limit). Otherwise the
// string is copied through a bounded 300-byte buffer and trailing bytes are
// removed until MeasureText fits within maxWidth. This happens before any
// clipping test [07 §7][GAP T22].
//
// The input is treated as bytes (Windows-1252), not runes; byte 0x0A or NUL
// terminates the measured prefix [02 §7].
func TruncateToWidth(fnt *formats.FNT, text string, maxWidth int) string { // [07 §7][GAP T22]
	if maxWidth <= 0 || fnt == nil || len(text) == 0 {
		return text
	}
	const limit = 300 // retail bounded buffer [07 §7]
	// Bounded copy into the 300-byte buffer. The measured prefix ends at the
	// first newline/NUL, but the bytes after that terminator remain harmless
	// data in the bounded text buffer when no width truncation is needed; the
	// draw and measure loops stop at the terminator [02 §7].
	src := text
	if len(src) > limit {
		src = src[:limit]
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
//   - frame is row-major width×height indexed pixels (logical palette indices).
//   - fnt is the bitmap font (*formats.FNT); nil is a no-op.
//   - text is treated as bytes; 0x0A or NUL terminates the advance [02 §7].
//   - x,y is the baseline origin: glyph rows are drawn at y - *(char*)(fnt+2)
//     [02 §7][03 §7.1] (signed low byte of fnt.Unknown). curX advances by glyph
//     Width per present glyph; absent glyphs (offset 0) are skipped [03 §7.1].
//   - maxWidth > 0 enables truncate-to-width before clipping: the string is
//     truncated via TruncateToWidth until its advance fits [07 §7][GAP T22].
//   - color is the indexed color written where glyph bits are set (1-bit,
//     MSB first, packed continuously [fmt fnt]); clear bits are transparent.
//   - Pixels outside the framebuffer are clipped.
//
// No palette conversion is performed here (C7); that happens at present time in
// convertIndexedToRGBA. No simulation state is touched (I6).
func DrawText(frame []uint8, width, height int, fnt *formats.FNT, text string, x, y, maxWidth int, color byte) { // [02 §7][03 §7.1][07 §7]
	drawText(frame, width, height, fnt, text, x, y, maxWidth, color, nil)
}

// drawText is the common FNT rasterizer. onWrite is retained for the generic
// helper shape used by the text tests; retail GUI callers pass nil because
// glyph colors are already active palette indices.
func drawText(frame []uint8, width, height int, fnt *formats.FNT, text string, x, y, maxWidth int, color byte, onWrite func(int)) { // [02 §7][03 §7.1][07 §7]
	if len(frame) < width*height || fnt == nil || width <= 0 || height <= 0 || len(text) == 0 {
		return
	}
	if maxWidth > 0 {
		text = TruncateToWidth(fnt, text, maxWidth)
		if len(text) == 0 {
			return
		}
	}
	desc := baselineDescender(fnt) // y - *(char*)(fnt+2) [02 §7]
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
		// Clip per pixel against the indexed framebuffer.
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
