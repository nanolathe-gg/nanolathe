package client

import (
	"math"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func mathCos(t float64) float64 { return math.Cos(t) }
func mathSin(t float64) float64 { return math.Sin(t) }

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

// retailHealthColor returns the exact three-tier health color selection used
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// positive-health fraction toward zero [07 §6].
func (c *Client) retailHealthColor(health, max int32) uint8 {
	third := max / 3
	if health > third*2 {
		return c.paletteIndex(10)
	}
	if health > third {
		return c.paletteIndex(14)
	}
	return c.paletteIndex(12)
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
	c.fillIndexedRect(x, y, w, 1, idx)
	c.fillIndexedRect(x, y+h-1, w, 1, idx)
	c.fillIndexedRect(x, y, 1, h, idx)
	c.fillIndexedRect(x+w-1, y, 1, h, idx)
}

// drawUnitOriented renders one interpolated unit as an oriented footprint
// rectangle rotated by its heading, with health bar and selection brackets.
// Heading rotates about the projected center; the long axis follows heading
// (north at 0) matching TA's top-down presentation.
func (c *Client) drawUnitOriented(v snapshot.UnitView, sx, sy int32) {
	fx, fz := int(v.FootX), int(v.FootZ)
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	const pxPerCell = 16
	halfW := fx * pxPerCell / 2
	halfH := fz * pxPerCell / 2
	if halfW < 4 {
		halfW = 4
	}
	if halfH < 4 {
		halfH = 4
	}
	// Heading is clockwise north→east per [03 §2.4] and movement.TestHeadingUsesTAWorldConvention, but screen Y down makes north down (+Y).
	// Unrotated rect must have north (+Z world) at +Y screen (down) and east (+X) at +X screen (right) per camera.WorldToScreen [03 §2.5].
	// Apply -heading so positive heading (north→east clockwise) rotates north (+Y) → east (+X) via Y-down screen.
	cos, sin := headingCosSin(uint16(0 - v.Heading))
	// Corners of the unrotated rect relative to center; long axis = Z (down) with north at +Y [03 §2.5].
	type pt struct{ x, y int }
	pts := [4]pt{}
	corners := [4][2]int{
		{-halfW, halfH}, {halfW, halfH}, {halfW, -halfH}, {-halfW, -halfH},
	}
	for i, cnr := range corners {
		rx, ry := float64(cnr[0]), float64(cnr[1])
		pts[i] = pt{
			x: int(sx) + int(cos*rx-sin*ry),
			y: int(sy) + int(sin*rx+cos*ry),
		}
	}
	// Painter: fill via two-triangle scan over bounding box using sign tests.
	minX, minY, maxX, maxY := int(sx), int(sy), int(sx), int(sy)
	for _, p := range pts {
		if p.x < minX {
			minX = p.x
		}
		if p.x > maxX {
			maxX = p.x
		}
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}
	}
	W := c.width
	HMax := c.height
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	minX, minY = clamp(minX, 0, W-1), clamp(minY, 0, HMax-1)
	maxX, maxY = clamp(maxX, 0, W-1), clamp(maxY, 0, HMax-1)
	inside := func(px, py int) bool {
		sign := 0
		for i := 0; i < 4; i++ {
			a, b := pts[i], pts[(i+1)%4]
			ex, ey := b.x-a.x, b.y-a.y
			cross := ex*(py-a.y) - ey*(px-a.x)
			s := 0
			if cross > 0 {
				s = 1
			} else if cross < 0 {
				s = -1
			}
			if s == 0 {
				continue
			}
			if sign == 0 {
				sign = s
			} else if s != sign {
				return false
			}
		}
		return true
	}
	litEdge := func(px, py int) bool {
		// Light from screen top: upper half of the shape gets the lit shade.
		return py <= int(sy)
	}
	for py := minY; py <= maxY; py++ {
		rowBase := py * W
		for px := minX; px <= maxX; px++ {
			if inside(px, py) {
				idx := unitStyle.BodyMid
				if litEdge(px, py) {
					idx = unitStyle.BodyLit
				} else {
					idx = unitStyle.BodyDark
				}
				c.indexed[rowBase+px] = idx
			}
		}
	}
	// Black outline along the polygon edges.
	for i := 0; i < 4; i++ {
		a, b := pts[i], pts[(i+1)%4]
		steps := 2 * (abs(a.x-b.x) + abs(a.y-b.y))
		if steps == 0 {
			continue
		}
		for s := 0; s <= steps; s++ {
			px := a.x + (b.x-a.x)*s/steps
			py := a.y + (b.y-a.y)*s/steps
			if px < 0 || px >= W || py < 0 || py >= HMax {
				continue
			}
			c.indexed[py*W+px] = unitStyle.Outline
		}
	}
	// Nanoframe: dashed outline only grows more solid as remaining falls.
	if v.BuildRemaining > 0 {
		for i := 0; i < 4; i++ {
			a, b := pts[i], pts[(i+1)%4]
			steps := 2 * (abs(a.x-b.x) + abs(a.y-b.y))
			for s := 0; s <= steps; s += 3 {
				px := a.x + (b.x-a.x)*s/steps
				py := a.y + (b.y-a.y)*s/steps
				if px >= 0 && px < W && py >= 0 && py < HMax {
					c.indexed[py*W+px] = unitStyle.HealthGreen
				}
			}
		}
	}
	// Selection brackets.
	if v.Flags&SelectionFlag != 0 {
		for _, cornerSet := range []pt{pts[0], pts[1], pts[2], pts[3]} {
			for d := -3; d <= 3; d++ {
				for _, off := range [2]pt{{d, 0}, {0, d}} {
					px, py := cornerSet.x+off.x, cornerSet.y+off.y
					if px >= 0 && px < W && py >= 0 && py < HMax {
						c.indexed[py*W+px] = unitStyle.SelectWhite
					}
				}
			}
		}
	}
	// Health bar under the footprint when damaged or selected.
	if v.MaxHealth > 0 && (v.Health != v.MaxHealth || v.Flags&SelectionFlag != 0) {
		bw := halfW * 2
		if bw < 12 {
			bw = 12
		}
		bx := int(sx) - bw/2
		by := maxY + 3
		c.fillIndexedRect(bx-1, by-1, bw+2, 4, c.paletteIndex(0))
		frac := float64(v.Health) / float64(v.MaxHealth)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		fill := c.retailHealthColor(v.Health, v.MaxHealth)
		c.fillIndexedRect(bx, by, int(float64(bw)*frac), 2, fill)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// headingCosSin returns fixed-point-ish cos/sin for a 65536-per-circle angle.
func headingCosSin(h uint16) (float64, float64) {
	const twoPi = 6.283185307179586
	theta := float64(h) * twoPi / 65536.0
	return cos(theta), sin(theta)
}

func cos(t float64) float64 {
	// Local math.Cos alias to keep the file dependency-light.
	return mathCos(t)
}

func sin(t float64) float64 {
	return mathSin(t)
}

// UIFillRect fills a clipped rectangle in the indexed framebuffer. idx is an
// active PALETTE.PAL index, matching retail's indexed primitive writers.
func (c *Client) UIFillRect(x, y, w, h int, idx uint8) {
	c.fillIndexedRect(x, y, w, h, idx)
}

// UIFrameRect outlines a clipped rectangle in the indexed framebuffer. idx is
// an active PALETTE.PAL index.
func (c *Client) UIFrameRect(x, y, w, h int, idx uint8) {
	c.frameIndexedRect(x, y, w, h, idx)
}

// UIText draws FNT text into the indexed framebuffer when a font is loaded.
func (c *Client) UIText(fnt *formats.FNT, text string, x, y int, color byte) {
	c.UITextWidth(fnt, text, x, y, c.width-x, color)
}

// UITextWidth draws FNT text with an explicit retail control width. The
// frontend uses this so authored labels truncate before clipping to their
// gadget rectangle rather than running into neighboring controls.
func (c *Client) UITextWidth(fnt *formats.FNT, text string, x, y, maxWidth int, color byte) {
	if c.fnt == nil || fnt == nil {
		return
	}
	drawText(c.indexed, c.width, c.height, fnt, text, x, y, maxWidth, color, nil)
}

// WorldToScreenPx exposes the camera projection for overlay geometry.
func (c *Client) WorldToScreenPx(x, y, z numeric.Fixed) (int32, int32) {
	return c.cam.WorldToScreen(x, y, z)
}

// UIBlit stamps a decoded GAF frame into the indexed framebuffer at (x, y),
// honoring GAF transparency and clipping to the framebuffer [fmt gaf].
// Presentation only [I6].
func (c *Client) UIBlit(f *formats.GAFFrame, x, y int) {
	c.UIBlitClipped(f, x, y, 0, 0, c.width, c.height)
}

// UIBlitClipped stamps a decoded GAF frame while confining every write to a
// destination rectangle. Retail GUI windows draw into a private WxH surface
// before that surface is copied to the framebuffer, so tiled backgrounds,
// oversized picture gadgets, and glyph overhang cannot escape the window
// rectangle [07 §4]. Presentation only [I6].
func (c *Client) UIBlitClipped(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	for row := 0; row < int(f.Height); row++ {
		py := y + row
		if py < minY || py >= maxY {
			continue
		}
		for col := 0; col < int(f.Width); col++ {
			px := x + col
			if px < minX || px >= maxX {
				continue
			}
			b, ok := f.At(col, row)
			if !ok {
				continue
			}
			index := py*c.width + px
			c.indexed[index] = b
		}
	}
}

// UIBlitLit stamps a GAF frame with every opaque pixel remapped through one
// row of the PALETTE.LHT brightening table. This is retail's shaded glyph
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// for a non-negative level; the loading screen draws a stage's label through
// it so the label flashes as the stage completes. Level 0 is UIBlit
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) UIBlitLit(f *formats.GAFFrame, x, y int, pal *palette.Tables, level int) {
	if f == nil {
		return
	}
	if pal == nil || level <= 0 {
		c.UIBlit(f, x, y)
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

// UIBlitAnchor reproduces retail's raw GAF rasterizer: x and y are the raw
// call-site coordinates and the frame's authored offsets are subtracted before
// pixels are written. Battle-shell callers pass the desired pixel origin plus
// XOffset/YOffset, so those two operations cancel [fmt gaf][07 §6]. Frontend
// .GUI controls deliberately use UIBlit instead: their rectangles are the
// placement contract [07 §4].
func (c *Client) UIBlitAnchor(f *formats.GAFFrame, x, y int) {
	if f == nil {
		return
	}
	c.UIBlit(f, x-int(f.XOffset), y-int(f.YOffset))
}

// UIBlitFrameScaled stretches a decoded GAF frame across a destination
// rectangle, honoring GAF transparency and clipping to the framebuffer.
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// quad whose destination spans (x, y)..(x+w-1, y+h-1) and whose source spans
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
	if f == nil || w <= 0 || h <= 0 || f.Width == 0 || f.Height == 0 {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	// Inclusive corner spans: the last destination column samples the last
	// source column, which is what the quad's corner pairs describe.
	spanX, spanY := w-1, h-1
	srcX, srcY := int(f.Width)-1, int(f.Height)-1
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < minY || py >= maxY {
			continue
		}
		sy := 0
		if spanY > 0 {
			sy = dy * srcY / spanY
		}
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < minX || px >= maxX {
				continue
			}
			sx := 0
			if spanX > 0 {
				sx = dx * srcX / spanX
			}
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// its own WxH drawing surface positioned at the window origin and copies the
// window's background bitmap into that surface at (0,0), so a bitmap larger
// than the window shows only the part the window rectangle admits. The
// full-screen frontend screens are authored at (0,0,640,480) and are
// unaffected; SELMAP.GUI is the case that needs the clip, because
// bitmaps/dselectmap2.pcx is a 640x480 file whose panel art occupies just the
// top-left 494x420 [07 §4]. Presentation only [I6].
func (c *Client) UIBlitPCXClipped(p *formats.PCX, x, y, clipX, clipY, clipW, clipH int) {
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// non-negative level: the level selects an LHT row directly and the operator
// rewrites the destination in place, so it lifts whatever is already there
// instead of painting a color. The GUI uses it for the selected list row
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) UILightRect(pal *palette.Tables, x, y, w, h, level int) {
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

// UIBlitIndexed draws an opaque indexed image with nearest-neighbour scaling.
// It is used by retail surface gadgets such as SELMAP's MAPPIC, whose pixels
// are supplied by the selected TNT minimap. The source bytes already address
// PALETTE.PAL, like every other indexed image path.
func (c *Client) UIBlitIndexed(src []byte, srcW, srcH, x, y, w, h int) {
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
