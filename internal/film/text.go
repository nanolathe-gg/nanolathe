package film

import (
	"image"
	"image/color"
	"math"
)

// TextStyle is one drawn run of the stroke face. Size is the cap height in
// pixels; every other measurement is a fraction of it, so a script authored at
// 1080p composes the same picture at any capture size.
type TextStyle struct {
	Size     float64    // cap height, pixels
	Weight   float64    // stroke width as a fraction of Size
	Tracking float64    // extra advance between glyphs, in cap heights
	Color    color.RGBA // opaque colour; the draw alpha scales it
	Shadow   float64    // drop-shadow offset as a fraction of Size; 0 is none
	Halo     float64    // dark outline radius as a fraction of Size; 0 is none
}

func (s TextStyle) weight() float64 {
	if s.Weight <= 0 {
		return 0.095
	}
	return s.Weight
}

// MeasureText reports the advance width of a run in pixels.
func MeasureText(text string, style TextStyle) float64 {
	w := 0.0
	for _, r := range text {
		g, _ := lookupGlyph(r)
		w += g.advance + style.Tracking
	}
	if w > 0 {
		w -= style.Tracking
	}
	return w * style.Size
}

// mask is a coverage buffer for one drawn run: the stroke rasterizer fills it
// once and the compositor reads it for the shadow and the face, so overlapping
// strokes composite as one shape instead of darkening at every join.
type mask struct {
	x0, y0 int // top-left in destination pixels
	w, h   int
	cov    []float32
	hasInk bool
}

func newMask(x0, y0, w, h int) *mask {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &mask{x0: x0, y0: y0, w: w, h: h, cov: make([]float32, w*h)}
}

// stroke rasterizes one segment as a round-capped bar of the given half width.
// Coverage is the signed distance narrowed to one pixel of edge, taken as a
// maximum so joins do not accumulate.
func (m *mask) stroke(ax, ay, bx, by, half float64) {
	if m == nil || m.w == 0 || m.h == 0 {
		return
	}
	minX := int(math.Floor(math.Min(ax, bx)-half-1)) - m.x0
	maxX := int(math.Ceil(math.Max(ax, bx)+half+1)) - m.x0
	minY := int(math.Floor(math.Min(ay, by)-half-1)) - m.y0
	maxY := int(math.Ceil(math.Max(ay, by)+half+1)) - m.y0
	minX, minY = max(minX, 0), max(minY, 0)
	maxX, maxY = min(maxX, m.w-1), min(maxY, m.h-1)
	dx, dy := bx-ax, by-ay
	length2 := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		py := float64(y+m.y0) + 0.5
		row := y * m.w
		for x := minX; x <= maxX; x++ {
			px := float64(x+m.x0) + 0.5
			t := 0.0
			if length2 > 0 {
				t = ((px-ax)*dx + (py-ay)*dy) / length2
				t = math.Max(0, math.Min(1, t))
			}
			ox, oy := px-(ax+t*dx), py-(ay+t*dy)
			d := math.Sqrt(ox*ox + oy*oy)
			c := half + 0.5 - d
			if c <= 0 {
				continue
			}
			if c > 1 {
				c = 1
			}
			if float32(c) > m.cov[row+x] {
				m.cov[row+x] = float32(c)
			}
			if c > 0.02 {
				m.hasInk = true
			}
		}
	}
}

// buildRun rasterizes one line of text with its pen at (x, baseline).
func buildRun(text string, x, baseline float64, style TextStyle) *mask {
	size := style.Size
	half := size * style.weight() / 2
	width := MeasureText(text, style)
	pad := half + 2
	x0 := int(math.Floor(x - pad))
	y0 := int(math.Floor(baseline - size - pad))
	m := newMask(x0, y0, int(math.Ceil(width+2*pad)), int(math.Ceil(size*1.35+2*pad)))
	pen := x
	for _, r := range text {
		g, _ := lookupGlyph(r)
		for _, stroke := range g.strokes {
			for i := 0; i+1 < len(stroke); i++ {
				a, b := stroke[i], stroke[i+1]
				m.stroke(pen+a.X*size, baseline-size+a.Y*size,
					pen+b.X*size, baseline-size+b.Y*size, half)
			}
			if len(stroke) == 1 {
				a := stroke[0]
				m.stroke(pen+a.X*size, baseline-size+a.Y*size, pen+a.X*size, baseline-size+a.Y*size, half)
			}
		}
		pen += (g.advance + style.Tracking) * size
	}
	return m
}

// composite blends the mask into dst at an offset, scaled by alpha and clipped
// by a soft reveal edge. revealX is a destination column: coverage fully right
// of it is dropped and the pixel either side of it is feathered, which is the
// wipe animation's only mechanism.
func (m *mask) composite(dst *image.RGBA, dx, dy int, col color.RGBA, alpha, revealX float64, feather float64) {
	if m == nil || alpha <= 0 || !m.hasInk {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	bounds := dst.Bounds()
	for y := 0; y < m.h; y++ {
		py := m.y0 + dy + y
		if py < bounds.Min.Y || py >= bounds.Max.Y {
			continue
		}
		row := y * m.w
		for x := 0; x < m.w; x++ {
			cov := float64(m.cov[row+x])
			if cov <= 0 {
				continue
			}
			px := m.x0 + dx + x
			if px < bounds.Min.X || px >= bounds.Max.X {
				continue
			}
			if !math.IsInf(revealX, 1) {
				edge := (revealX - float64(px)) / math.Max(feather, 0.5)
				if edge <= 0 {
					continue
				}
				if edge < 1 {
					cov *= edge
				}
			}
			a := cov * alpha
			if a <= 0 {
				continue
			}
			o := dst.PixOffset(px, py)
			dst.Pix[o+0] = blend8(dst.Pix[o+0], col.R, a)
			dst.Pix[o+1] = blend8(dst.Pix[o+1], col.G, a)
			dst.Pix[o+2] = blend8(dst.Pix[o+2], col.B, a)
			dst.Pix[o+3] = 0xff
		}
	}
}

func blend8(dst, src uint8, a float64) uint8 {
	v := float64(dst)*(1-a) + float64(src)*a
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// DrawText draws one line with its pen at (x, baseline) and returns its width.
// alpha scales the whole run, revealX is the wipe column (+Inf draws it all).
func DrawText(dst *image.RGBA, text string, x, baseline float64, style TextStyle, alpha, revealX float64) float64 {
	m := buildRun(text, x, baseline, style)
	// A title crosses grass, smoke and unit art in the same line, so the face
	// carries its own contrast: a dark outline tight against the stroke, then
	// the drop shadow, then the face itself.
	if style.Halo > 0 {
		r := max(1, int(math.Round(style.Halo*style.Size)))
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx*dx+dy*dy > r*r || (dx == 0 && dy == 0) {
					continue
				}
				m.composite(dst, dx, dy, color.RGBA{A: 0xff}, alpha*0.42, revealX, style.Size*0.08)
			}
		}
	}
	if style.Shadow > 0 {
		off := int(math.Round(style.Shadow * style.Size))
		m.composite(dst, off, off, color.RGBA{A: 0xff}, alpha*0.55, revealX, style.Size*0.08)
	}
	m.composite(dst, 0, 0, style.Color, alpha, revealX, style.Size*0.08)
	return MeasureText(text, style)
}

// Letterbox draws the top and bottom bars. The fraction is of the frame
// height, each bar, and is clamped to a quarter so a script cannot letterbox
// the picture away entirely.
func Letterbox(dst *image.RGBA, fraction float64) {
	if fraction <= 0 {
		return
	}
	fraction = math.Min(fraction, 0.25)
	b := dst.Bounds()
	bar := int(math.Round(float64(b.Dy()) * fraction))
	fill := func(y0, y1 int) {
		for y := max(y0, b.Min.Y); y < min(y1, b.Max.Y); y++ {
			o := dst.PixOffset(b.Min.X, y)
			end := o + 4*b.Dx()
			for i := o; i < end; i += 4 {
				dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = 0, 0, 0, 0xff
			}
		}
	}
	fill(b.Min.Y, b.Min.Y+bar)
	fill(b.Max.Y-bar, b.Max.Y)
}
