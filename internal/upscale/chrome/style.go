package chrome

import (
	"hash/fnv"
	"image"
	"math"
	"math/rand"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Light is the direction light arrives from, as screen-space signs: X -1 from
// the left, +1 from the right; Y -1 from the top, +1 from the bottom. The
// originals are lit from the bottom-left.
type Light struct{ X, Y int }

// Style holds every tunable of the look; sizes are in 1x pixels and scale
// with the output.
type Style struct {
	Scale    float64
	Light    Light
	Cool     float64 // shifts the material towards blue
	Grime    float64 // darkness of soot, oxide and edge grime, 0 for none
	Material *Layer  // seamless gunmetal texture

	CaptionFont, LabelFont *opentype.Font

	tile  *Layer // Material resized for Scale, built on first use
	masks map[textKey]*Mask
}

type textKey struct {
	font    *opentype.Font
	text    string
	spacing float64
}

// textMask memoizes TextMask: captionWidth measures RECLAIM for every caption.
func (s *Style) textMask(f *opentype.Font, text string, spacing float64) *Mask {
	k := textKey{f, text, spacing}
	if m, ok := s.masks[k]; ok {
		return m
	}
	if s.masks == nil {
		s.masks = map[textKey]*Mask{}
	}
	m := TextMask(f, text, spacing)
	s.masks[k] = m
	return m
}

func (s *Style) px(v float64) int { return int(math.Round(v * s.Scale)) }

// atLeast1 is a scaled thickness that never vanishes.
func (s *Style) atLeast1(v float64) int { return max(1, s.px(v)) }

// faceContrast is how far Face stretches the plate's light variation.
const faceContrast = 2.2

// Face fills a w x h surface with the material, tiled at a density that keeps
// its detail constant across scales, then grades it:
// brightness 48%, saturation 60%, a 95% white point, a brightness multiplier
// and the cool shift.
func (s *Style) Face(w, h int, mult float64) *Layer {
	// The material's native 1024 px spans 512 output px at 2x.
	if s.tile == nil {
		s.tile = s.Material.Resize(int(math.Round(float64(s.Material.W)*s.Scale/4)), int(math.Round(float64(s.Material.H)*s.Scale/4)))
	}
	tile := s.tile
	tw, th := tile.W, tile.H
	l := NewLayer(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := tile.At(x%tw, y%th)
			hh, ss, ll := rgbToHSL(c.R, c.G, c.B)
			// Stretch the plate's light variation about its mean, so the
			// mottling and scratches survive the darkening and the palette.
			ll = materialMean + (ll-materialMean)*faceContrast
			r, g, b := hslToRGB(hh, ss*0.60, ll*0.48)
			k := mult / 0.95
			l.Set(x, y, RGBA{r * k * (1 - s.Cool), g * k, b * k * (1 + s.Cool), 1})
		}
	}
	return l
}

// Bevel draws a graded rim of n steps around the layer, fading inwards, with
// the edges facing the light bright and the far edges dark; sunken swaps
// them. A gentle gradient across the face runs towards the light.
func (s *Style) Bevel(l *Layer, steps int, lightA, darkA, curve float64, sunken bool) {
	lit, shade := white, black
	if sunken {
		lit, shade = black, white
	}
	// Which edges face the light.
	leftLit, bottomLit := s.Light.X < 0, s.Light.Y > 0
	// Face curve.
	for y := 0; y < l.H; y++ {
		t := float64(y) / float64(l.H-1)
		if !bottomLit {
			t = 1 - t
		}
		top, bot := shade.WithA(curve), lit.WithA(curve)
		c := lerp(top, bot, t)
		for x := 0; x < l.W; x++ {
			l.Blend(x, y, c, 1)
		}
	}
	for i := 0; i < steps; i++ {
		f := 1 - float64(i)/float64(steps)
		edge := func(isLit bool) RGBA {
			if isLit {
				return lit.WithA(lightA * f)
			}
			return shade.WithA(darkA * f)
		}
		l.Rect(i, i, l.W-1-i, i, edge(!bottomLit))            // top
		l.Rect(i, l.H-1-i, l.W-1-i, l.H-1-i, edge(bottomLit)) // bottom
		l.Rect(i, i, i, l.H-1-i, edge(leftLit))               // left
		l.Rect(l.W-1-i, i, l.W-1-i, l.H-1-i, edge(!leftLit))  // right
	}
	// A bright line just inside the lit edges, as on the retail art.
	if !sunken {
		hl := white.WithA(0.45)
		if bottomLit {
			l.Rect(1, l.H-2, l.W-2, l.H-2, hl)
		} else {
			l.Rect(1, 1, l.W-2, 1, hl)
		}
		if leftLit {
			l.Rect(1, 1, 1, l.H-2, hl)
		} else {
			l.Rect(l.W-2, 1, l.W-2, l.H-2, hl)
		}
	}
	// One-pixel dark outline.
	o := black.WithA(0.9)
	l.Rect(0, 0, l.W-1, 0, o)
	l.Rect(0, l.H-1, l.W-1, l.H-1, o)
	l.Rect(0, 0, 0, l.H-1, o)
	l.Rect(l.W-1, 0, l.W-1, l.H-1, o)
}

// ArrowMask is the coverage of a w x h plate whose left (or right, when next)
// end comes to a 45-degree point over its full height, supersampled 8x8.
func ArrowMask(w, h int, next bool) *Mask {
	m := NewMask(w, h)
	half := float64(h) / 2
	const ss = 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px, py := float64(x)+(float64(sx)+0.5)/ss, float64(y)+(float64(sy)+0.5)/ss
					from := px // distance from the pointed end
					if next {
						from = float64(w) - px
					}
					if from >= math.Abs(py-half) {
						n++
					}
				}
			}
			m.V[y*w+x] = float64(n) / ss / ss
		}
	}
	return m
}

// BevelShape bevels a plate of any outline: each pixel within edge of the
// outline takes the rim shade of the direction it faces, lit on the sides
// facing the light, dark on the far sides (swapped when sunken), fading
// inwards. The face is cut to the mask and gets a dark one-pixel outline.
func (s *Style) BevelShape(l *Layer, mask *Mask, edge, lightA, darkA, curve float64, sunken bool) {
	lit, shade := white, black
	if sunken {
		lit, shade = black, white
	}
	// Face curve towards the light.
	for y := 0; y < l.H; y++ {
		t := float64(y) / float64(l.H-1)
		if s.Light.Y < 0 {
			t = 1 - t
		}
		c := lerp(shade.WithA(curve), lit.WithA(curve), t)
		for x := 0; x < l.W; x++ {
			l.Blend(x, y, c, 1)
		}
	}
	// Distance inside the outline and the outward normal, sampled locally.
	in := mask.Threshold(0.5)
	r := int(math.Ceil(edge)) + 1
	lx, ly := float64(s.Light.X), float64(s.Light.Y)
	ln := math.Hypot(lx, ly)
	for y := 0; y < l.H; y++ {
		for x := 0; x < l.W; x++ {
			if in.Get(x, y) == 0 {
				continue
			}
			best, nx, ny := math.Inf(1), 0.0, 0.0
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					qx, qy := x+dx, y+dy
					outside := qx < 0 || qy < 0 || qx >= l.W || qy >= l.H || in.Get(qx, qy) == 0
					if !outside {
						continue
					}
					if d := math.Hypot(float64(dx), float64(dy)); d < best {
						best, nx, ny = d, float64(dx)/d, float64(dy)/d
					}
				}
			}
			if best > edge {
				continue
			}
			f := 1 - (best-1)/edge
			// Facing the light when the outward normal points towards it.
			d := (nx*lx + ny*ly) / ln
			if d > 0 {
				l.Blend(x, y, lit.WithA(lightA*f*d), 1)
			} else {
				l.Blend(x, y, shade.WithA(darkA*f*-d), 1)
			}
			if best <= 1 {
				l.Blend(x, y, black.WithA(0.9), 1)
			}
		}
	}
	for i := range mask.V {
		l.Pix[i*4+3] *= mask.V[i]
	}
}

// Recess darkens a rectangle and shades its rim as a groove: the edges nearest
// the light are in shadow, the far edges catch it.
func (s *Style) Recess(l *Layer, x0, y0, x1, y1 int, floor, rimDark, rimLight float64) {
	l.Rect(x0, y0, x1, y1, black.WithA(floor))
	dark, light := black.WithA(rimDark), white.WithA(rimLight)
	pick := func(nearLight bool) RGBA {
		if nearLight {
			return dark
		}
		return light
	}
	leftLit, bottomLit := s.Light.X < 0, s.Light.Y > 0
	l.Rect(x0, y0, x1, y0, pick(!bottomLit))
	l.Rect(x0, y1, x1, y1, pick(bottomLit))
	l.Rect(x0, y0, x0, y1, pick(leftLit))
	l.Rect(x1, y0, x1, y1, pick(!leftLit))
}

// Pill is the coverage of a capsule w x h with flat sides and fully rounded
// ends, inset by inset pixels, supersampled 8x8.
func Pill(w, h int, inset float64) *Mask {
	m := NewMask(w, h)
	r := float64(w)/2 - inset
	cx := float64(w) / 2
	top, bot := inset+r, float64(h)-inset-r
	in := func(x, y float64) bool {
		if x < inset || x > float64(w)-inset {
			return false
		}
		cy := math.Max(top, math.Min(bot, y))
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
	}
	const ss = 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					if in(float64(x)+(float64(sx)+0.5)/ss, float64(y)+(float64(sy)+0.5)/ss) {
						n++
					}
				}
			}
			m.V[y*w+x] = float64(n) / ss / ss
		}
	}
	return m
}

// Well is a pill-shaped recess w x h whose walls are shaded by facing: walls
// facing the light are lit, those facing away are in shadow. The result is
// blurred by soft so it blends into the face; the layer is padded by the
// returned margin.
func (s *Style) Well(w, h int, wall, soft float64) (*Layer, int) {
	outer, inner := Pill(w, h, 0), Pill(w, h, wall)
	l := NewLayer(w, h)
	l.Paint(outer, 0, 0, black.WithA(0.62))
	shadow, lit := hex("#1a1a1a"), hex("#d4d4d4")
	// Each wall slopes down towards the pill's centre line, so its facing is
	// the direction from the pixel to the nearest point on that line.
	lx, ly := float64(s.Light.X), float64(s.Light.Y)
	ln := math.Hypot(lx, ly)
	cx, r := float64(w)/2, float64(w)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ring := outer.Get(x, y) - inner.Get(x, y)
			if ring <= 0 {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			qy := math.Max(r, math.Min(float64(h)-r, py))
			nx, ny := cx-px, qy-py
			if n := math.Hypot(nx, ny); n > 0 {
				nx, ny = nx/n, ny/n
			}
			d := (nx*lx + ny*ly) / ln // -1 facing away .. +1 facing the light
			l.Blend(x, y, lerp(shadow, lit, (d+1)/2).WithA(0.55+0.2*math.Abs(d)), ring)
		}
	}
	m := int(math.Ceil(soft * 3))
	return l.Pad(m).Blur(soft), m
}

// LED is a convex glass pill: brighter down its centre like a tube, with a
// soft highlight on the side facing the light.
func (s *Style) LED(w, h int, dark, bright, hl RGBA, hlA float64) *Layer {
	mask := Pill(w, h, 0)
	l := NewLayer(w, h)
	hx := 0.5 + 0.14*float64(s.Light.X)
	hy := 0.5 + 0.26*float64(s.Light.Y)
	rx, ry := 0.16*float64(w), 0.13*float64(h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k := math.Cos((float64(x)/float64(w-1)-0.5)*2.4) * (0.72 + 0.28*math.Sin(float64(y)/float64(h-1)*math.Pi))
			c := lerp(dark, bright, math.Max(0, k))
			dx, dy := (float64(x)+0.5-hx*float64(w))/rx, (float64(y)+0.5-hy*float64(h))/ry
			c = lerp(c, hl, hlA*math.Exp(-(dx*dx+dy*dy)/2))
			c.A = mask.Get(x, y)
			l.Set(x, y, c)
		}
	}
	return l
}

// TextMask renders text at a large size with letter spacing, trimmed to the ink.
func TextMask(f *opentype.Font, text string, spacing float64) *Mask {
	const size = 140
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err)
	}
	defer face.Close()
	w := int(float64(len(text))*size*1.2+spacing*float64(len(text))) + 40
	h := size * 2
	img := image.NewAlpha(image.Rect(0, 0, w, h))
	d := font.Drawer{Dst: img, Src: image.Opaque, Face: face, Dot: fixed.P(20, size*3/2)}
	for i, r := range text {
		if i > 0 {
			d.Dot.X += fixed.Int26_6(spacing * 64)
		}
		d.DrawString(string(r))
	}
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.AlphaAt(x, y).A > 0 {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		// A font without these glyphs leaves no ink: no caption, not a panic.
		return NewMask(1, 1)
	}
	m := NewMask(maxX-minX+1, maxY-minY+1)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			m.V[(y-minY)*m.W+x-minX] = float64(img.AlphaAt(x, y).A) / 255
		}
	}
	return m
}

// Caption is engraved button lettering: letters sx narrower and sy taller than
// the w x h box, spaced out to fill its width, cream with a concave engrave
// (shadowed on the side nearest the light) and a solid outline. The returned
// layer is padded; offset is the padding to subtract when placing it.
type CaptionStyle struct {
	Fill, NearLight, FarLight, Outline RGBA
	OutlineR, SX, SY                   float64
}

func (s *Style) Caption(text string, w, h int, cs CaptionStyle) (*Layer, int) {
	base := s.textMask(s.CaptionFont, text, 0)
	k := float64(base.W) * (1/cs.SX - 1) / float64(len(text)-1)
	spaced := s.textMask(s.CaptionFont, text, k)
	ch := int(math.Round(float64(h) * cs.SY))
	pad := 3 * int(math.Ceil(s.Scale/2))
	m := spaced.Resize(w, ch).Pad(pad)
	l := NewLayer(m.W, m.H)
	l.Paint(m.Threshold(0.4).Dilate(cs.OutlineR*s.Scale), 0, 0, cs.Outline)
	l.Paint(m, 0, 0, cs.Fill)
	// Concave: the stroke edge whose neighbour toward the light is open is in shadow.
	l.Paint(m.EdgeToward(s.Light.X, s.Light.Y), 0, 0, cs.NearLight)
	l.Paint(m.EdgeToward(-s.Light.X, -s.Light.Y), 0, 0, cs.FarLight)
	// Inner border: the rim of each stroke, half way from outline to fill.
	open := NewMask(m.W, m.H)
	for i, v := range m.V {
		open.V[i] = 1 - v
	}
	rim := open.Dilate(s.Scale / 2)
	for i, v := range m.V {
		rim.V[i] = math.Min(rim.V[i], v)
	}
	l.Paint(rim, 0, 0, lerp(cs.Outline, cs.Fill, 0.5))
	return l, pad
}

// GradientLabel is top-bar lettering: a vertical gradient fitted to the ink,
// squeezed into a w x h box.
func (s *Style) GradientLabel(text string, w, h int, top, bottom RGBA) *Layer {
	m := s.textMask(s.LabelFont, text, 0).Resize(w, h)
	l := NewLayer(w, h)
	for y := 0; y < h; y++ {
		c := lerp(top, bottom, float64(y)/float64(h-1))
		for x := 0; x < w; x++ {
			l.Blend(x, y, c, m.Get(x, y))
		}
	}
	return l
}

// Shadow is a soft drop shadow of the layer's alpha, to be placed offset away
// from the light.
func (s *Style) Shadow(src *Layer, opacity float64) (*Layer, int, int) {
	m := NewMask(src.W, src.H)
	for y := 0; y < src.H; y++ {
		for x := 0; x < src.W; x++ {
			m.V[y*src.W+x] = src.At(x, y).A
		}
	}
	p := s.atLeast1(1.5)
	m = m.Pad(p).Blur(s.Scale / 2)
	l := NewLayer(m.W, m.H)
	l.Paint(m, 0, 0, black.WithA(opacity))
	off := s.atLeast1(0.5)
	return l, -p - off*s.Light.X, -p - off*s.Light.Y
}

// Place composites a padded layer with its drop shadow at (x, y); a zero
// shadow opacity draws none.
func (s *Style) Place(dst, src *Layer, pad, x, y int, shadow float64) {
	if shadow > 0 {
		sh, sx, sy := s.Shadow(src, shadow)
		dst.Over(sh, x-pad+sx, y-pad+sy)
	}
	dst.Over(src, x-pad, y-pad)
}

// Wear adds damage seeded by seed (a button's label), so the same button always
// wears the same way and neighbours differ: up to two tapering gouges running
// in from seeded edges, a few round dents, faint scratches, and dark soot and
// oxide blemishes, all left of right (output pixels). It is drawn at 4x and reduced so edges are smooth.
// Gouges and dents are concave: dark, with a lit rim one output pixel towards
// the far side from the light.
func (s *Style) Wear(l *Layer, seed string, right int) {
	h := fnv.New64a()
	h.Write([]byte(seed))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	const ss = 4
	deep, faint := NewMask(l.W*ss, l.H*ss), NewMask(l.W*ss, l.H*ss)
	k := s.Scale / 2 // sizes below were tuned at 2x
	disc := func(m *Mask, cx, cy, rad, v float64) {
		cx, cy, rad = cx*ss, cy*ss, rad*ss
		for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
			for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
				if x < 0 || y < 0 || x >= m.W || y >= m.H {
					continue
				}
				if d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy); d <= rad {
					m.V[y*m.W+x] = math.Max(m.V[y*m.W+x], v)
				}
			}
		}
	}
	stroke := func(m *Mask, pts [][2]float64, r0, r1, v0, v1 float64) {
		total := 0.0
		for i := 1; i < len(pts); i++ {
			total += math.Hypot(pts[i][0]-pts[i-1][0], pts[i][1]-pts[i-1][1])
		}
		done := 0.0
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			seg := math.Hypot(b[0]-a[0], b[1]-a[1])
			for t := 0.0; t <= seg; t += 0.25 {
				f := (done + t) / total
				disc(m, a[0]+(b[0]-a[0])*t/seg, a[1]+(b[1]-a[1])*t/seg, r0+(r1-r0)*f, v0+(v1-v0)*f)
			}
			done += seg
		}
	}
	// Gouges: none, one or two per button, each starting at a seeded point on
	// a seeded edge and running inwards. Toggles keep them left of right.
	limit := float64(right)
	gouge := func(scale float64) {
		var x, y, ix, iy float64 // start, and the inward unit direction
		switch e := r.Intn(4); {
		case e == 0 || (e == 3 && right < l.W):
			x, y, ix, iy = (8+float64(r.Intn(max(1, int(limit/k)-16))))*k, 0, 0, 1
		case e == 1:
			x, y, ix, iy = (8+float64(r.Intn(max(1, int(limit/k)-16))))*k, float64(l.H), 0, -1
		case e == 2:
			x, y, ix, iy = 0, (4+float64(r.Intn(max(1, int(float64(l.H)/k)-8))))*k, 1, 0
		default:
			x, y, ix, iy = float64(l.W), (4+float64(r.Intn(max(1, int(float64(l.H)/k)-8))))*k, -1, 0
		}
		lean := float64(r.Intn(2)*2 - 1) // which way it drifts across the edge
		pts := [][2]float64{{x, y}}
		for range 4 + r.Intn(4) {
			along := (1.4 + 1.6*r.Float64()) * k * scale
			across := (0.6 + 1.6*r.Float64()) * k * scale * lean
			x += ix*along + iy*across
			y += iy*along + ix*across
			pts = append(pts, [2]float64{x, y})
		}
		stroke(deep, pts, 1.3*k*scale, 0.35*k, 0.85, 0.35)
		if r.Intn(2) == 0 {
			b := pts[1+r.Intn(len(pts)-2)]
			stroke(deep, [][2]float64{b, {b[0] + (ix*2+iy*lean*2)*k, b[1] + (iy*2+ix*lean*2)*k}}, 0.6*k, 0.25*k, 0.6, 0.25)
		}
	}
	switch n := r.Intn(6); {
	case n >= 4:
		gouge(1)
		gouge(0.7)
	default:
		gouge(1)
	}
	// Dents.
	for range 6 + r.Intn(5) {
		cx := (4 + float64(r.Intn(max(1, int(limit/k)-8)))) * k
		cy := (3 + float64(r.Intn(max(1, int(float64(l.H)/k)-6)))) * k
		disc(deep, cx, cy, (0.8+1.4*r.Float64())*k, 0.45+0.35*r.Float64())
	}
	// Faint scratches.
	for range 2 + r.Intn(2) {
		sx := (6 + float64(r.Intn(max(1, int(limit/k)-30)))) * k
		sy := (5 + float64(r.Intn(max(1, int(float64(l.H)/k)-10)))) * k
		stroke(faint, [][2]float64{{sx, sy}, {sx + (8+10*r.Float64())*k, sy + (r.Float64()*4-2)*k}}, 0.35*k, 0.25*k, 1, 0.6)
	}
	// Soot and black oxide: soft dark smudges of powder residue, denser
	// speckled patches of oxide, and grime gathered towards the edges.
	grime := NewMask(l.W, l.H)
	// Smudges: clusters of small offset blots, broken up by value noise, so
	// each has a ragged, irregular outline rather than an oval one.
	noise := valueNoise(r, l.W, l.H, 3*k)
	for range 2 + r.Intn(3) {
		cx, cy := r.Float64()*limit, r.Float64()*float64(l.H)
		spread := (4 + 6*r.Float64()) * k
		a := 0.22 + 0.2*r.Float64()
		for range 4 + r.Intn(5) {
			bx := cx + (r.Float64()*2-1)*spread
			by := cy + (r.Float64()*2-1)*spread*0.6
			br := (1.2 + 2.8*r.Float64()) * k
			for y := max(0, int(by-3*br)); y < min(l.H, int(by+3*br)+1); y++ {
				for x := max(0, int(bx-3*br)); x < min(l.W, int(bx+3*br)+1); x++ {
					dx, dy := (float64(x)-bx)/br, (float64(y)-by)/br
					grime.V[y*l.W+x] += a * math.Exp(-(dx*dx+dy*dy)/2)
				}
			}
		}
	}
	for i := range grime.V {
		// Noise eats into the smudges unevenly.
		grime.V[i] *= math.Max(0, noise[i]*1.6-0.3)
	}
	for range 1 + r.Intn(2) {
		cx, cy := r.Float64()*limit, r.Float64()*float64(l.H)
		rad := (2 + 3*r.Float64()) * k
		for range int(30 * k * k) {
			ang, d := r.Float64()*2*math.Pi, rad*math.Sqrt(r.Float64())
			x, y := int(cx+d*math.Cos(ang)), int(cy+d*math.Sin(ang))
			if x >= 0 && y >= 0 && x < l.W && y < l.H {
				grime.V[y*l.W+x] += 0.3 + 0.3*r.Float64()
			}
		}
	}
	edgeGrime := 0.16 + 0.12*r.Float64()
	for y := 0; y < l.H; y++ {
		for x := 0; x < l.W; x++ {
			e := math.Min(math.Min(float64(x), float64(l.W-1-x)), math.Min(float64(y), float64(l.H-1-y)))
			grime.V[y*l.W+x] += edgeGrime * math.Exp(-e/(3*k))
		}
	}
	grime = grime.Blur(0.4 * k)
	for i, v := range grime.V {
		grime.V[i] = math.Min(v, 0.7)
	}
	l.Paint(grime, 0, 0, RGBA{0.06, 0.055, 0.05, s.Grime})
	d, f := deep.Resize(l.W, l.H), faint.Resize(l.W, l.H)
	ax, ay := -s.Light.X, -s.Light.Y
	l.Paint(d, ax, ay, white.WithA(0.35))
	l.Paint(d, 0, 0, black.WithA(0.8))
	l.Paint(f, ax, ay, white.WithA(0.12))
	l.Paint(f, 0, 0, black.WithA(0.22))
}

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	if s == 0 {
		return l, l, l
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	f := func(t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return f(h + 1.0/3), f(h), f(h - 1.0/3)
}

// valueNoise is smooth random noise in 0..1 with features about cell pixels
// across, from a seeded grid of values interpolated bicubically.
func valueNoise(r *rand.Rand, w, h int, cell float64) []float64 {
	gw, gh := int(float64(w)/cell)+3, int(float64(h)/cell)+3
	grid := make([]float64, gw*gh)
	for i := range grid {
		grid[i] = r.Float64()
	}
	at := func(x, y int) float64 { return grid[min(max(y, 0), gh-1)*gw+min(max(x, 0), gw-1)] }
	fade := func(t float64) float64 { return t * t * (3 - 2*t) }
	out := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/cell, float64(y)/cell
			ix, iy := int(fx), int(fy)
			tx, ty := fade(fx-float64(ix)), fade(fy-float64(iy))
			top := at(ix, iy) + (at(ix+1, iy)-at(ix, iy))*tx
			bot := at(ix, iy+1) + (at(ix+1, iy+1)-at(ix, iy+1))*tx
			out[y*w+x] = top + (bot-top)*ty
		}
	}
	return out
}
