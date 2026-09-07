package client

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Unit presentation primitives drawn straight into the indexed framebuffer.
// The palette is sampled once at SetPalette time so team/health colors resolve
// no matter how the retail PALETTE lays out its entries.

// UnitStyle holds resolved palette indices for unit presentation.
type UnitStyle struct {
	// Body shades run dark→light so an oriented footprint reads as a solid
	// object with a lit edge regardless of palette layout.
	BodyDark, BodyMid, BodyLit uint8
	HealthGreen                uint8
	HealthRed                  uint8
	Outline                    uint8 // black
	SelectWhite                uint8
}

var unitStyle = UnitStyle{
	BodyDark: 8, BodyMid: 12, BodyLit: 16,
	HealthGreen: 250, HealthRed: 200, Outline: 0, SelectWhite: 250,
}

func (c *Client) nearestIndex(r, g, b byte) uint8 {
	if c.pal == nil {
		return 0
	}
	best, bestD := uint8(0), 1<<30
	for i := 0; i < 256; i++ {
		rr, gg, bb, _ := c.pal.RGBA(uint8(i))
		dr, dg, db := int(rr)-int(r), int(gg)-int(g), int(bb)-int(b)
		d := dr*dr + dg*dg + db*db
		if d < bestD {
			bestD, best = d, uint8(i)
		}
	}
	return best
}

// ResolveUnitStyle samples the palette for the fixed presentation colors.
func (c *Client) ResolveUnitStyle() {
	unitStyle.BodyDark = c.nearestIndex(48, 48, 56)
	unitStyle.BodyMid = c.nearestIndex(96, 96, 108)
	unitStyle.BodyLit = c.nearestIndex(168, 168, 184)
	unitStyle.HealthGreen = c.paletteIndex(10)
	unitStyle.HealthRed = c.paletteIndex(12)
	unitStyle.Outline = c.nearestIndex(0, 0, 0)
	unitStyle.SelectWhite = c.nearestIndex(255, 255, 255)
}

// paletteIndex resolves a retail logical palette entry through the active
// PALETTE.PAL mapping. HUD health colors are dcb[10]/[14]/[12], not guessed
// RGB colors and not GUIPAL semantic fields [07 §6].
func (c *Client) paletteIndex(logical byte) uint8 {
	if c.pal == nil {
		return logical
	}
	return c.pal.Logical[logical]
}

// fillIndexedRect fills an axis-aligned rectangle, clipped.
func (c *Client) fillIndexedRect(x, y, w, h int, idx uint8) {
	W := c.width
	H := c.height
	if W <= 0 || H <= 0 {
		return
	}
	x0, y0 := x, y
	x1, y1 := x+w, y+h
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > W {
		x1 = W
	}
	if y1 > H {
		y1 = H
	}
	for yy := y0; yy < y1; yy++ {
		row := yy*W + x0
		for xx := x0; xx < x1; xx++ {
			c.indexed[row] = idx
			row++
		}
	}
}

// frameIndexedRect draws a one-pixel outline, clipped.
func (c *Client) frameIndexedRect(x, y, w, h int, idx uint8) {
	if c == nil {
		return
	}
	drawIndexedFrameInclusive(c.indexed, c.width, c.height,
		Rect{MinX: int32(x), MinY: int32(y), MaxX: int32(x + w - 1), MaxY: int32(y + h - 1)},
		idx, Rect{MinX: 0, MinY: 0, MaxX: int32(c.width) - 1, MaxY: int32(c.height) - 1})
}

// The oriented-footprint fallback renderer that stood here was deleted with
// WU-17-6.  It had no callers anywhere in the tree, and every part of it was
// invented rather than traced: a footprint rectangle standing in for the model,
// a dashed nanoframe outline, and a health bar derived from the footprint box
// and drawn whenever a unit was "damaged or selected".  Retail's per-unit bar is
// the 35x5 raster of [03 R-FX-01 §6], gated on the damagebars interface bit and
// on the unit belonging to the viewing player, and it lives in healthbar.go.
// Dead code that reads like a contract is how an invention outlives the session
// that wrote it (AGENTS.md rule 1), so it is removed rather than left.

// UIFillRect fills a clipped rectangle in the indexed framebuffer. idx is an
// active PALETTE.PAL index, matching retail's indexed primitive writers. It
// records the fill and the classic sink runs fillIndexedRect inline, so the HUD
// and menu draws land in exact per-frame order under the committed-frame list
// (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIFillRect(x, y, w, h int, idx uint8) {
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Index: idx,
		Style: drawlist.FillSolid,
	})
}

// UIFrameRect outlines a clipped rectangle in the indexed framebuffer. idx is
// an active PALETTE.PAL index. It emits; the classic sink runs frameIndexedRect
// (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIFrameRect(x, y, w, h int, idx uint8) {
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Index: idx,
		Style: drawlist.FillOutline,
	})
}

// UIText draws FNT text into the indexed framebuffer when a font is loaded.
func (c *Client) UIText(fnt *formats.FNT, text string, x, y int, color byte) {
	c.UITextWidth(fnt, text, x, y, c.width-x, color)
}

// UITextWidth draws FNT text with an explicit retail control width. The
// frontend uses this so authored labels truncate before clipping to their
// gadget rectangle rather than running into neighboring controls. It records
// the run and the classic sink runs the FNT rasterizer inline with the same
// pen, control width and no per-glyph callback, so the text lands in per-frame
// order (docs/DESIGN_GPU_RENDERER.md §2.2)[07 §7][03 §7.1].
func (c *Client) UITextWidth(fnt *formats.FNT, text string, x, y, maxWidth int, color byte) {
	if c.fnt == nil || fnt == nil {
		return
	}
	c.emitGlyphs(drawlist.Glyphs{
		Font:     fnt,
		Text:     text,
		X:        int32(x),
		Y:        int32(y),
		Color:    color,
		MaxWidth: int32(maxWidth),
	})
}

// WorldToScreenPx exposes the camera projection for overlay geometry.
func (c *Client) WorldToScreenPx(x, y, z numeric.Fixed) (int32, int32) {
	return c.cam.WorldToScreen(x, y, z)
}

// UIBlit stamps a decoded GAF frame into the indexed framebuffer at (x, y),
// honoring GAF transparency and clipping to the framebuffer [fmt gaf]. This is
// the plain keyed placement — the rectangle is the contract, no authored offset
// is subtracted, unlike UIBlitAnchor [07 §4]. It records a non-anchored keyed
// Sprite clipped to the framebuffer and the classic sink runs the byte writer
// inline, so the blit lands in per-frame order (docs/DESIGN_GPU_RENDERER.md
// §2.2). Presentation only [I6].
func (c *Client) UIBlit(f *formats.GAFFrame, x, y int) {
	c.UIBlitClipped(f, x, y, 0, 0, c.width, c.height)
}

// UIBlitClipped stamps a decoded GAF frame while confining every write to a
// destination rectangle. Retail GUI windows draw into a private WxH surface
// before that surface is copied to the framebuffer, so tiled backgrounds,
// oversized picture gadgets, and glyph overhang cannot escape the window
// rectangle [07 §4]. Presentation only [I6].
//
// It records a non-anchored keyed Sprite carrying the clip and the classic sink
// runs uiBlitClippedRaw inline, so the chrome lands in per-frame order; a nil
// frame records nothing exactly as the direct call drew nothing
// (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIBlitClipped(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil {
		return
	}
	c.emitSprite(drawlist.Sprite{
		Frame:   f,
		X:       int32(x),
		Y:       int32(y),
		Kind:    drawlist.BlitKeyed,
		HasClip: true,
		Clip:    drawlist.Rect{X: int32(clipX), Y: int32(clipY), W: int32(clipW), H: int32(clipH)},
	})
}

// uiBlitClippedRaw is the byte writer of UIBlitClipped. It is reached only
// through the classic sink for the converted keyed/cursor paths, so it is never
// executed twice.
//
// Every piece of chrome the battle shell draws comes through here, so the
// per-pixel work is what the HUD stage costs. The clip is therefore resolved
// into a row and column range once per call rather than re-tested per pixel,
// and the source and destination rows are taken as slices so the transparency
// test and the store are the only per-pixel work left. GAFFrame.At is not
// called: its two guards are exactly the ranges computed here, so inlining
// them keeps the same pixels — including a frame whose pixel or transparency
// arrays are shorter than its declared size, where At skips the missing tail
// and the per-row limit below skips the same tail.
func (c *Client) uiBlitClippedRaw(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	if len(c.indexed) < c.width*c.height {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
	// Source row and column ranges: destination pixel (x+col, y+row) is written
	// when it is inside the clip, so col runs over [minX-x, maxX-x) intersected
	// with the frame's own [0, fw), and likewise for row.
	col0, col1 := max(0, minX-x), min(fw, maxX-x)
	row0, row1 := max(0, minY-y), min(fh, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	// A short Pixels or Transparent array truncates the frame; At returned
	// "absent" past either end, so the walk stops at the same pixel.
	avail := len(f.Pixels)
	if len(f.Transparent) < avail {
		avail = len(f.Transparent)
	}
	for row := row0; row < row1; row++ {
		base := row * fw
		hi := col1
		if base+hi > avail {
			hi = avail - base
		}
		if hi <= col0 {
			continue
		}
		src := f.Pixels[base+col0 : base+hi]
		blank := f.Transparent[base+col0 : base+hi]
		dstBase := (y+row)*c.width + x + col0
		dst := c.indexed[dstBase : dstBase+len(src)]
		for i, b := range src {
			if blank[i] {
				continue
			}
			dst[i] = b
		}
	}
}

// UIBlitLit stamps a GAF frame with every opaque pixel remapped through one
// row of the PALETTE.LHT brightening table. This is retail's shaded glyph
// blitter, which indexes the same PALETTE.LHT table as the light-level
// remapper for a non-negative level; the loading screen draws a stage's label
// through it so the label flashes as the stage completes. Level 0 is UIBlit
// [03 §4.3.1]. Presentation only [I6].
func (c *Client) UIBlitLit(f *formats.GAFFrame, x, y int, pal *palette.Tables, level int) {
	if f == nil {
		return
	}
	if pal == nil || level <= 0 {
		// Level 0 (and the no-palette case) is exactly UIBlit, so it emits the
		// plain keyed record [03 §4.3.1].
		c.UIBlit(f, x, y)
		return
	}
	// The palette rides the client scratch field to the sink; emitSprite runs the
	// sink inline, so it is consumed before any later lit emit overwrites it, as
	// UILightRect does (docs/DESIGN_GPU_RENDERER.md §2.2). The LHT level is an
	// LHT-table row (LightLookup clamps to 0..31); it rides LightRow.
	c.uiRectPal = pal
	c.emitSprite(drawlist.Sprite{
		Frame:    f,
		X:        int32(x),
		Y:        int32(y),
		Kind:     drawlist.BlitLit,
		LightRow: uint8(level),
	})
}

// uiBlitLitRaw is the byte writer of UIBlitLit's lit path. It is reached only
// through the classic sink for the converted lit-blit path, so it is never
// executed twice; the loop, clip and per-pixel LHT lookup are unchanged from the
// direct call [03 §4.3.1]. The caller (the sink) guarantees pal is non-nil and
// level > 0, exactly the branch UIBlitLit routes here.
func (c *Client) uiBlitLitRaw(f *formats.GAFFrame, x, y int, pal *palette.Tables, level int) {
	if f == nil || pal == nil {
		return
	}
	for row := 0; row < int(f.Height); row++ {
		py := y + row
		if py < 0 || py >= c.height {
			continue
		}
		for col := 0; col < int(f.Width); col++ {
			px := x + col
			if px < 0 || px >= c.width {
				continue
			}
			b, ok := f.At(col, row)
			if !ok {
				continue
			}
			c.indexed[py*c.width+px] = pal.LightLookup(level, b)
		}
	}
}

// UIBlitAnchor applies the GAF frame-anchor placement: x and y are caller
// coordinates and the frame's authored offsets are subtracted before
// pixels are written. Battle-shell callers pass the desired pixel origin plus
// XOffset/YOffset, so those two operations cancel [fmt gaf][07 §6]. Frontend
// .GUI controls deliberately use UIBlit instead: their rectangles are the
// placement contract [07 §4].
func (c *Client) UIBlitAnchor(f *formats.GAFFrame, x, y int) {
	if f == nil {
		return
	}
	// The offset subtraction is deferred to the sink so the record carries the
	// caller coordinates: it emits an ANCHORED keyed Sprite and the classic sink
	// runs uiBlitAnchorRaw inline, distinct from UIBlit's non-anchored record
	// (docs/DESIGN_GPU_RENDERER.md §2.2)[fmt gaf][07 §6].
	c.emitSprite(drawlist.Sprite{
		Frame:    f,
		X:        int32(x),
		Y:        int32(y),
		Kind:     drawlist.BlitKeyed,
		Anchored: true,
	})
}

// uiBlitAnchorRaw is the byte writer of UIBlitAnchor: it subtracts the frame's
// authored offsets and defers to uiBlitClippedRaw over the full framebuffer,
// exactly as the direct UIBlitAnchor→UIBlit call did [fmt gaf][07 §6]. It is
// reached only through the classic sink's anchored keyed branch, so it is never
// executed twice.
func (c *Client) uiBlitAnchorRaw(f *formats.GAFFrame, x, y int) {
	if f == nil {
		return
	}
	c.uiBlitClippedRaw(f, x-int(f.XOffset), y-int(f.YOffset), 0, 0, c.width, c.height)
}

// UIBlitFrameScaled stretches a decoded GAF frame across a destination
// rectangle, honoring GAF transparency and clipping to the framebuffer.
//
// This is the retail surface-gadget path. It builds a four-corner quad whose
// destination spans (x, y)..(x+w-1, y+h-1) and whose source spans
// (0, 0)..(frameW-1, frameH-1), then hands both to the texture-mapped blitter,
// so the frame is resampled onto the authored gadget rectangle rather than
// stamped at its own size [07 §4]. Presentation only [I6].
func (c *Client) UIBlitFrameScaled(f *formats.GAFFrame, x, y, w, h int) {
	c.UIBlitFrameScaledClipped(f, x, y, w, h, 0, 0, c.width, c.height)
}

// UIBlitFrameScaledClipped is the private-window-surface form of
// UIBlitFrameScaled. Sampling still spans the complete destination rectangle;
// the clip only rejects writes outside the owning GUI surface [07 §4].
func (c *Client) UIBlitFrameScaledClipped(f *formats.GAFFrame, x, y, w, h, clipX, clipY, clipW, clipH int) {
	if f == nil {
		return
	}
	c.UIBlitFrameSourceRectScaledClipped(f, 0, 0, int(f.Width), int(f.Height), x, y, w, h, clipX, clipY, clipW, clipH)
}

// UIBlitFrameSourceRectScaledClipped stretches an inclusive source sub-rect
// across a destination rectangle. ENDMSN's PlayerColor surface uses the
// interior source `(1,1)..(frameWidth-1,frameHeight-1)` rather than sampling
// the logo frame's outer border [07 R-HUD-03 §11].
//
// It records a scaled Sprite carrying the source sub-rect in Src, the
// destination in Dst and the clip in Clip, and the classic sink runs the byte
// writer inline; the guard is kept here so a degenerate call records nothing,
// exactly as the direct call drew nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIBlitFrameSourceRectScaledClipped(f *formats.GAFFrame, srcX, srcY, srcW, srcH, x, y, w, h, clipX, clipY, clipW, clipH int) {
	if f == nil || w <= 0 || h <= 0 || srcW <= 0 || srcH <= 0 || f.Width == 0 || f.Height == 0 {
		return
	}
	c.emitSprite(drawlist.Sprite{
		Frame:   f,
		Kind:    drawlist.BlitScaled,
		Src:     drawlist.Rect{X: int32(srcX), Y: int32(srcY), W: int32(srcW), H: int32(srcH)},
		Dst:     drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		HasClip: true,
		Clip:    drawlist.Rect{X: int32(clipX), Y: int32(clipY), W: int32(clipW), H: int32(clipH)},
	})
}

// uiBlitFrameSourceRectScaledClippedRaw is the byte writer of the scaled blit,
// reached only through the classic sink's BlitScaled branch, so it is never
// executed twice; the sampling, clip and per-pixel store are unchanged from the
// direct call [07 R-HUD-03 §11].
func (c *Client) uiBlitFrameSourceRectScaledClippedRaw(f *formats.GAFFrame, srcX, srcY, srcW, srcH, x, y, w, h, clipX, clipY, clipW, clipH int) {
	if f == nil || w <= 0 || h <= 0 || srcW <= 0 || srcH <= 0 || f.Width == 0 || f.Height == 0 {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	// Inclusive corner spans: the last destination column samples the last
	// source column of the selected sub-rect, which is what the quad's corner
	// pairs describe.
	spanX, spanY := w-1, h-1
	srcSpanX, srcSpanY := srcW-1, srcH-1
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < minY || py >= maxY {
			continue
		}
		sy := 0
		if spanY > 0 {
			sy = dy * srcSpanY / spanY
		}
		sy += srcY
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < minX || px >= maxX {
				continue
			}
			sx := 0
			if spanX > 0 {
				sx = dx * srcSpanX / spanX
			}
			sx += srcX
			b, ok := f.At(sx, sy)
			if !ok {
				continue
			}
			c.indexed[py*c.width+px] = b
		}
	}
}

// UIBlitPCX stamps a decoded PCX image into the indexed framebuffer at
// (x, y), clipped. Retail frontend backgrounds carry pixels already addressed
// by the active PALETTE.PAL display table; their PCX trailer palette is not
// installed as a second display palette [fmt pcx][07 "Retail palette
// contract"]. Presentation only [I6].
func (c *Client) UIBlitPCX(p *formats.PCX, x, y int) {
	c.UIBlitPCXClipped(p, x, y, 0, 0, c.width, c.height)
}

// UIBlitPCXClipped draws an opaque indexed bitmap confined to a destination
// rectangle. This is the retail window fill: every window gets its own WxH
// drawing surface positioned at the window origin, and the background bitmap
// is copied into that surface at (0,0), so a bitmap larger
// than the window shows only the part the window rectangle admits. The
// full-screen frontend screens are authored at (0,0,640,480) and are
// unaffected; SELMAP.GUI is the case that needs the clip, because
// bitmaps/dselectmap2.pcx is a 640x480 file whose panel art occupies just the
// top-left 494x420 [07 §4]. Presentation only [I6].
//
// A PCX cannot ride Sprite.Frame (which holds a GAF frame), so it records a
// Sprite whose additive PCX field carries the source; the classic sink routes
// any Sprite with a non-nil PCX to uiBlitPCXClippedRaw, honoring the clip in
// Clip. A nil image records nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIBlitPCXClipped(p *formats.PCX, x, y, clipX, clipY, clipW, clipH int) {
	if p == nil {
		return
	}
	c.emitSprite(drawlist.Sprite{
		PCX:     p,
		X:       int32(x),
		Y:       int32(y),
		HasClip: true,
		Clip:    drawlist.Rect{X: int32(clipX), Y: int32(clipY), W: int32(clipW), H: int32(clipH)},
	})
}

// uiBlitPCXClippedRaw is the byte writer of UIBlitPCXClipped, reached only
// through the classic sink's PCX branch, so it is never executed twice; the
// clip and per-pixel copy are unchanged from the direct call
// [fmt pcx][07 "Retail palette contract"].
func (c *Client) uiBlitPCXClippedRaw(p *formats.PCX, x, y, clipX, clipY, clipW, clipH int) {
	if p == nil {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	for row := 0; row < int(p.Height); row++ {
		py := y + row
		if py < minY || py >= maxY {
			continue
		}
		for col := 0; col < int(p.Width); col++ {
			px := x + col
			if px < minX || px >= maxX {
				continue
			}
			index := py*c.width + px
			c.indexed[index] = p.Pixels[row*int(p.Width)+col]
		}
	}
}

// UILightRect remaps every pixel in a rectangle through one row of the
// PALETTE.LHT brightening table. For a non-negative level, the level selects
// an LHT row directly and the operator
// rewrites the destination in place, so it lifts whatever is already there
// instead of painting a color. The GUI uses it for the selected list row
// [03 §4.3.1]. Presentation only [I6].
//
// It records the op and the classic sink runs uiLightRectRaw inline against the
// passed palette (carried to the sink on the client scratch field), so the light
// rect lands in per-frame order; the guard on the passed pal is preserved here
// so a nil palette records nothing exactly as the direct call drew nothing
// (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UILightRect(pal *palette.Tables, x, y, w, h, level int) {
	if pal == nil || w <= 0 || h <= 0 {
		return
	}
	// The palette rides the client scratch field to the sink; emitFill runs the
	// sink inline, so it is consumed before any later lit/shade emit overwrites it.
	c.uiRectPal = pal
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Style: drawlist.FillLitRect,
		Level: int32(level),
	})
}

// uiLightRectRaw is the byte writer of UILightRect. It is reached only through
// the classic sink for the converted light-rect path, so it is never executed
// twice; the loop, clip and in-place LHT lookup are unchanged from the direct
// call [03 §4.3.1].
func (c *Client) uiLightRectRaw(pal *palette.Tables, x, y, w, h, level int) {
	if pal == nil || w <= 0 || h <= 0 {
		return
	}
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < 0 || py >= c.height {
			continue
		}
		row := py * c.width
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < 0 || px >= c.width {
				continue
			}
			c.indexed[row+px] = pal.LightLookup(level, c.indexed[row+px])
		}
	}
}

// UIShadeRect remaps a rectangle through the signed full-screen fade table.
// Negative levels address SHD row level+32 after clamping at -32; nonnegative
// levels use the brighten-only LHT row. This is the two-table signed shader
// contract, not a single-table approximation [03 R-COMP-02 §5].
//
// It records the op with the signed level and the classic sink runs
// uiShadeRectRaw inline against the passed palette (carried to the sink on the
// client scratch field), so the shade rect lands in per-frame order. The guard
// on the passed pal is preserved so a nil palette records nothing, as the direct
// call drew nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIShadeRect(pal *palette.Tables, x, y, w, h, level int) {
	if c == nil || pal == nil || w <= 0 || h <= 0 {
		return
	}
	// The palette rides the client scratch field to the sink; emitFill runs the
	// sink inline, so it is consumed before any later lit/shade emit overwrites it.
	c.uiRectPal = pal
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Style: drawlist.FillShadeRect,
		Level: int32(level),
	})
}

// uiShadeRectRaw is the byte writer of UIShadeRect. It is reached only through
// the classic sink for the converted shade-rect path, so it is never executed
// twice; the signed-level row selection, clamps, clip and in-place SHD/LHT
// lookup are unchanged from the direct call [03 R-COMP-02 §5].
func (c *Client) uiShadeRectRaw(pal *palette.Tables, x, y, w, h, level int) {
	if c == nil || pal == nil || w <= 0 || h <= 0 {
		return
	}
	useShade := level < 0
	row := level
	if useShade {
		if row < -32 {
			row = -32
		}
		row += 32
	}
	if row > 31 {
		row = 31
	}
	if row < 0 {
		row = 0
	}
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < 0 || py >= c.height {
			continue
		}
		base := py * c.width
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < 0 || px >= c.width {
				continue
			}
			if useShade {
				c.indexed[base+px] = pal.ShadeLookup(row, c.indexed[base+px])
			} else {
				c.indexed[base+px] = pal.LightLookup(row, c.indexed[base+px])
			}
		}
	}
}

// UIBlitIndexed draws an opaque indexed image with nearest-neighbour scaling.
// It is used by retail surface gadgets such as SELMAP's MAPPIC, whose pixels
// are supplied by the selected TNT minimap. The source bytes already address
// PALETTE.PAL, like every other indexed image path.
//
// It records a Surface carrying the source bytes and the destination rectangle
// in Dst, and the classic sink runs uiBlitIndexedRaw inline; the guard is kept
// here so a degenerate call records nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) UIBlitIndexed(src []byte, srcW, srcH, x, y, w, h int) {
	if len(src) == 0 || srcW <= 0 || srcH <= 0 || w <= 0 || h <= 0 {
		return
	}
	c.emitSurface(drawlist.Surface{
		Pixels: src,
		SrcW:   int32(srcW),
		SrcH:   int32(srcH),
		Dst:    drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
	})
}

// uiBlitIndexedRaw is the byte writer of UIBlitIndexed, reached only through the
// classic sink's Surface branch, so it is never executed twice; the sampling,
// clip and per-pixel store are unchanged from the direct call.
func (c *Client) uiBlitIndexedRaw(src []byte, srcW, srcH, x, y, w, h int) {
	if len(src) == 0 || srcW <= 0 || srcH <= 0 || w <= 0 || h <= 0 {
		return
	}
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < 0 || py >= c.height {
			continue
		}
		sy := dy * srcH / h
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < 0 || px >= c.width {
				continue
			}
			sx := dx * srcW / w
			idx := sy*srcW + sx
			if idx >= 0 && idx < len(src) {
				index := py*c.width + px
				c.indexed[index] = src[idx]
			}
		}
	}
}

// GUIColor resolves a GUI semantic color index to an active PALETTE.PAL index
// [03 §4.3][07 "Retail palette contract"].
//
// Retail's world overlays — the drag-selection box, the build ghost, the queued
// build markers, the nanolathe beam — do not name palette entries directly.
// They index a 256-byte map built at GUI bootstrap by matching every GUIPAL.PAL
// entry against the display palette, and GUIPAL's first sixteen entries are the
// familiar sixteen-color set. That is why those overlays are pure primaries:
// entry 10 is bright green, 4 is dark red, 15 is white, 0 is black.
//
// Use this for primitives the engine colors semantically. Never apply it to
// GAF/PCX/TNT pixels, which are already active palette indices.
func (c *Client) GUIColor(index uint8) uint8 {
	if c == nil || c.pal == nil {
		return index
	}
	return c.pal.GUIColor(index)
}
