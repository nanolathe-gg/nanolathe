package chrome

import (
	"image"
	"image/color"
	"math"

	xdraw "golang.org/x/image/draw"
)

// Layer is a straight-alpha RGBA image in float channels, 0..1.
type Layer struct {
	W, H int
	Pix  []float64 // r, g, b, a per pixel
}

type RGBA struct{ R, G, B, A float64 }

func NewLayer(w, h int) *Layer { return &Layer{W: w, H: h, Pix: make([]float64, w*h*4)} }

func (l *Layer) At(x, y int) RGBA {
	i := (y*l.W + x) * 4
	return RGBA{l.Pix[i], l.Pix[i+1], l.Pix[i+2], l.Pix[i+3]}
}

func (l *Layer) Set(x, y int, c RGBA) {
	i := (y*l.W + x) * 4
	l.Pix[i], l.Pix[i+1], l.Pix[i+2], l.Pix[i+3] = c.R, c.G, c.B, c.A
}

// Blend composites c, at its own alpha times k, over pixel (x, y).
func (l *Layer) Blend(x, y int, c RGBA, k float64) {
	if x < 0 || y < 0 || x >= l.W || y >= l.H {
		return
	}
	a := c.A * k
	if a <= 0 {
		return
	}
	d := l.At(x, y)
	oa := a + d.A*(1-a)
	if oa <= 0 {
		return
	}
	mix := func(s, t float64) float64 { return (s*a + t*d.A*(1-a)) / oa }
	l.Set(x, y, RGBA{mix(c.R, d.R), mix(c.G, d.G), mix(c.B, d.B), oa})
}

// Rect blends c over the inclusive rectangle (x0,y0)-(x1,y1).
func (l *Layer) Rect(x0, y0, x1, y1 int, c RGBA) {
	for y := max(y0, 0); y <= min(y1, l.H-1); y++ {
		for x := max(x0, 0); x <= min(x1, l.W-1); x++ {
			l.Blend(x, y, c, 1)
		}
	}
}

// Over composites src over l with its top-left at (ox, oy).
func (l *Layer) Over(src *Layer, ox, oy int) {
	for y := 0; y < src.H; y++ {
		for x := 0; x < src.W; x++ {
			l.Blend(ox+x, oy+y, src.At(x, y), 1)
		}
	}
}

// Mask is a coverage map, 0..1.
type Mask struct {
	W, H int
	V    []float64
}

func NewMask(w, h int) *Mask { return &Mask{W: w, H: h, V: make([]float64, w*h)} }

func (m *Mask) Get(x, y int) float64 {
	if x < 0 || y < 0 || x >= m.W || y >= m.H {
		return 0
	}
	return m.V[y*m.W+x]
}

// Paint blends colour c through mask m onto l at (ox, oy).
func (l *Layer) Paint(m *Mask, ox, oy int, c RGBA) {
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			if v := m.V[y*m.W+x]; v > 0 {
				l.Blend(ox+x, oy+y, c, v)
			}
		}
	}
}

func (m *Mask) Threshold(t float64) *Mask {
	o := NewMask(m.W, m.H)
	for i, v := range m.V {
		if v >= t {
			o.V[i] = 1
		}
	}
	return o
}

// Dilate takes the maximum over a disk of radius r.
func (m *Mask) Dilate(r float64) *Mask {
	o := NewMask(m.W, m.H)
	ri := int(math.Ceil(r))
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			best := 0.0
			for dy := -ri; dy <= ri; dy++ {
				for dx := -ri; dx <= ri; dx++ {
					if float64(dx*dx+dy*dy) <= r*r+0.01 {
						best = math.Max(best, m.Get(x+dx, y+dy))
					}
				}
			}
			o.V[y*m.W+x] = best
		}
	}
	return o
}

// Blur is a separable Gaussian blur of standard deviation sigma.
func (m *Mask) Blur(sigma float64) *Mask {
	if sigma <= 0 {
		return m
	}
	r := int(math.Ceil(sigma * 3))
	k := make([]float64, 2*r+1)
	sum := 0.0
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	tmp, out := NewMask(m.W, m.H), NewMask(m.W, m.H)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			s := 0.0
			for i, w := range k {
				s += w * m.Get(x+i-r, y)
			}
			tmp.V[y*m.W+x] = s
		}
	}
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			s := 0.0
			for i, w := range k {
				s += w * tmp.Get(x, y+i-r)
			}
			out.V[y*m.W+x] = s
		}
	}
	return out
}

// Pad returns m with p empty pixels on every side.
func (m *Mask) Pad(p int) *Mask {
	o := NewMask(m.W+2*p, m.H+2*p)
	for y := 0; y < m.H; y++ {
		copy(o.V[(y+p)*o.W+p:], m.V[y*m.W:(y+1)*m.W])
	}
	return o
}

// EdgeToward marks pixels inside m whose neighbour (dx, dy) away is outside.
func (m *Mask) EdgeToward(dx, dy int) *Mask {
	t := m.Threshold(0.5)
	o := NewMask(m.W, m.H)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			if t.Get(x, y) > 0 && t.Get(x+dx, y+dy) == 0 {
				o.V[y*m.W+x] = 1
			}
		}
	}
	return o
}

// Resize scales the mask with Catmull-Rom filtering.
func (m *Mask) Resize(w, h int) *Mask {
	src := image.NewGray16(image.Rect(0, 0, m.W, m.H))
	for i, v := range m.V {
		src.Pix[i*2], src.Pix[i*2+1] = uint8(uint16(v*65535)>>8), uint8(uint16(v*65535))
	}
	dst := image.NewGray16(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	o := NewMask(w, h)
	for i := range o.V {
		o.V[i] = float64(uint16(dst.Pix[i*2])<<8|uint16(dst.Pix[i*2+1])) / 65535
	}
	return o
}

// Resize scales the layer with Catmull-Rom filtering.
func (l *Layer) Resize(w, h int) *Layer {
	dst := image.NewNRGBA64(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), l.NRGBA64(), image.Rect(0, 0, l.W, l.H), xdraw.Src, nil)
	return fromNRGBA64(dst)
}

func (l *Layer) NRGBA64() *image.NRGBA64 {
	img := image.NewNRGBA64(image.Rect(0, 0, l.W, l.H))
	q := func(v float64) uint16 { return uint16(math.Round(math.Max(0, math.Min(1, v)) * 65535)) }
	for y := 0; y < l.H; y++ {
		for x := 0; x < l.W; x++ {
			c := l.At(x, y)
			img.SetNRGBA64(x, y, color.NRGBA64{q(c.R), q(c.G), q(c.B), q(c.A)})
		}
	}
	return img
}

func fromNRGBA64(img *image.NRGBA64) *Layer {
	b := img.Bounds()
	l := NewLayer(b.Dx(), b.Dy())
	for y := 0; y < l.H; y++ {
		for x := 0; x < l.W; x++ {
			c := img.NRGBA64At(x, y)
			l.Set(x, y, RGBA{float64(c.R) / 65535, float64(c.G) / 65535, float64(c.B) / 65535, float64(c.A) / 65535})
		}
	}
	return l
}

func hex(s string) RGBA {
	var r, g, b uint8
	if len(s) == 7 {
		for i, p := range []*uint8{&r, &g, &b} {
			v := 0
			for _, ch := range s[1+2*i : 3+2*i] {
				v *= 16
				switch {
				case ch >= '0' && ch <= '9':
					v += int(ch - '0')
				case ch >= 'a' && ch <= 'f':
					v += int(ch-'a') + 10
				case ch >= 'A' && ch <= 'F':
					v += int(ch-'A') + 10
				}
			}
			*p = uint8(v)
		}
	}
	return RGBA{float64(r) / 255, float64(g) / 255, float64(b) / 255, 1}
}

func (c RGBA) WithA(a float64) RGBA { c.A = a; return c }

func lerp(a, b RGBA, t float64) RGBA {
	return RGBA{a.R + (b.R-a.R)*t, a.G + (b.G-a.G)*t, a.B + (b.B-a.B)*t, a.A + (b.A-a.A)*t}
}

var (
	white = RGBA{1, 1, 1, 1}
	black = RGBA{0, 0, 0, 1}
)

// Pad returns l with p transparent pixels on every side.
func (l *Layer) Pad(p int) *Layer {
	o := NewLayer(l.W+2*p, l.H+2*p)
	for y := 0; y < l.H; y++ {
		copy(o.Pix[((y+p)*o.W+p)*4:], l.Pix[y*l.W*4:(y+1)*l.W*4])
	}
	return o
}

// Blur is a Gaussian blur of the premultiplied colour and alpha.
func (l *Layer) Blur(sigma float64) *Layer {
	if sigma <= 0 {
		return l
	}
	var ch [4]*Mask
	for c := range 4 {
		ch[c] = NewMask(l.W, l.H)
	}
	for i := 0; i < l.W*l.H; i++ {
		a := l.Pix[i*4+3]
		for c := range 3 {
			ch[c].V[i] = l.Pix[i*4+c] * a
		}
		ch[3].V[i] = a
	}
	for c := range 4 {
		ch[c] = ch[c].Blur(sigma)
	}
	o := NewLayer(l.W, l.H)
	for i := 0; i < l.W*l.H; i++ {
		a := ch[3].V[i]
		if a > 0 {
			o.Pix[i*4], o.Pix[i*4+1], o.Pix[i*4+2] = ch[0].V[i]/a, ch[1].V[i]/a, ch[2].V[i]/a
		}
		o.Pix[i*4+3] = a
	}
	return o
}
