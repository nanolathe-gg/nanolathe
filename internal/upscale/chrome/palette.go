package chrome

import (
	"image"
	"image/color"
	"math"
)

// Dither reduces the layer to the palette with Floyd-Steinberg error
// diffusion. Pixels below half alpha become the colour key, which opaque
// pixels never take.
func Dither(l *Layer, pal color.Palette, colourKey uint8) *image.Paletted {
	out := image.NewPaletted(image.Rect(0, 0, l.W, l.H), pal)
	type rgb struct{ r, g, b float64 }
	cols := make([]rgb, len(pal))
	for i, c := range pal {
		r, g, b, _ := c.RGBA()
		cols[i] = rgb{float64(r) / 65535, float64(g) / 65535, float64(b) / 65535}
	}
	work := make([]rgb, l.W*l.H)
	for i := range work {
		work[i] = rgb{l.Pix[i*4], l.Pix[i*4+1], l.Pix[i*4+2]}
	}
	spread := func(x, y int, e rgb, k float64) {
		if x < 0 || y < 0 || x >= l.W || y >= l.H {
			return
		}
		w := &work[y*l.W+x]
		w.r += e.r * k
		w.g += e.g * k
		w.b += e.b * k
	}
	for y := 0; y < l.H; y++ {
		for x := 0; x < l.W; x++ {
			if l.At(x, y).A < 0.5 {
				out.SetColorIndex(x, y, colourKey)
				continue
			}
			c := work[y*l.W+x]
			best, bestD := 0, math.MaxFloat64
			for i, p := range cols {
				if i == int(colourKey) {
					continue
				}
				d := (c.r-p.r)*(c.r-p.r) + (c.g-p.g)*(c.g-p.g) + (c.b-p.b)*(c.b-p.b)
				if d < bestD {
					best, bestD = i, d
				}
			}
			out.SetColorIndex(x, y, uint8(best))
			p := cols[best]
			e := rgb{c.r - p.r, c.g - p.g, c.b - p.b}
			spread(x+1, y, e, 7.0/16)
			spread(x-1, y+1, e, 3.0/16)
			spread(x, y+1, e, 5.0/16)
			spread(x+1, y+1, e, 1.0/16)
		}
	}
	return out
}
