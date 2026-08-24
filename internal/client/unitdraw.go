package client

import (
	"math"

	"github.com/nanolathe/nanolathe/formats"
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
	unitStyle.HealthGreen = c.nearestIndex(64, 220, 64)
	unitStyle.HealthRed = c.nearestIndex(230, 48, 32)
	unitStyle.Outline = c.nearestIndex(0, 0, 0)
	unitStyle.SelectWhite = c.nearestIndex(255, 255, 255)
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
	cos, sin := headingCosSin(v.Heading)
	// Corners of the unrotated rect relative to center; long axis = Z (down).
	type pt struct{ x, y int }
	pts := [4]pt{}
	corners := [4][2]int{
		{-halfW, -halfH}, {halfW, -halfH}, {halfW, halfH}, {-halfW, halfH},
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
	const W, HMax = 640, 480
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
		c.fillIndexedRect(bx-1, by-1, bw+2, 4, unitStyle.Outline)
		frac := float64(v.Health) / float64(v.MaxHealth)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		fill := unitStyle.HealthGreen
		if frac < 0.34 {
			fill = unitStyle.HealthRed
		}
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

// UIFillRect fills a clipped rectangle in the indexed framebuffer.
func (c *Client) UIFillRect(x, y, w, h int, idx uint8) { c.fillIndexedRect(x, y, w, h, idx) }

// UIFrameRect outlines a clipped rectangle in the indexed framebuffer.
func (c *Client) UIFrameRect(x, y, w, h int, idx uint8) { c.frameIndexedRect(x, y, w, h, idx) }

// UIText draws FNT text into the indexed framebuffer when a font is loaded.
func (c *Client) UIText(fnt *formats.FNT, text string, x, y int, color byte) {
	if c.fnt == nil || fnt == nil {
		return
	}
	DrawText(c.indexed, c.width, c.height, c.fnt, text, x, y, c.width-x, color)
}

// WorldToScreenPx exposes the camera projection for overlay geometry.
func (c *Client) WorldToScreenPx(x, y, z numeric.Fixed) (int32, int32) {
	return c.cam.WorldToScreen(x, y, z)
}

// UIBlit stamps a decoded GAF frame into the indexed framebuffer at (x, y),
// honoring GAF transparency and clipping to the framebuffer [fmt gaf].
// Presentation only [I6].
func (c *Client) UIBlit(f *formats.GAFFrame, x, y int) {
	if f == nil {
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
			c.indexed[py*c.width+px] = b
		}
	}
}
