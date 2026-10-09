package chrome

import (
	"math"
	"math/rand"
)

// materialSize is the procedural plate's edge; Face tiles it at Scale/4, so a
// 2x element spans 512 output pixels before it repeats.
const materialSize = 1024

// materialSeed fixes the plate, so every load and the cache agree on it.
const materialSeed = 0x6e616e6f

// materialMean is the plate's mean lightness before wear.
const materialMean = 0.267

// Material draws the seamless worn-gunmetal plate every element is cut from:
// a mid grey with soft mottling, horizontal brushed grain, long faint
// scratches and dark pits. Every feature wraps, so the plate tiles.
func Material() *Layer {
	const n = materialSize
	r := rand.New(rand.NewSource(materialSeed))
	v := make([]float64, n*n)
	for i := range v {
		v[i] = materialMean
	}
	add := func(field []float64, amp float64) {
		for i := range v {
			v[i] += field[i] * amp
		}
	}
	add(periodicNoise(r, n, 256, 256), 0.048)
	add(periodicNoise(r, n, 64, 64), 0.030)
	add(periodicNoise(r, n, 16, 16), 0.018)
	// Brushed grain: noise stretched along x, so streaks run horizontally.
	add(periodicNoise(r, n, 128, 1), 0.036)
	add(periodicNoise(r, n, 32, 2), 0.018)
	// Tint: broad patches drift between blue-grey and a warmer grey.
	tint := periodicNoise(r, n, 128, 128)

	wrap := func(x, y int) int { return ((y%n+n)%n)*n + (x%n+n)%n }
	// Scratches: long thin curves, mostly near vertical as on the reference
	// plate, each a dark groove with a faint lit edge beside it.
	for range 70 {
		x, y := r.Float64()*n, r.Float64()*n
		ang := math.Pi/2 + (r.Float64()-0.5)*0.9
		bend := (r.Float64() - 0.5) * 0.004
		length := 60 + r.Float64()*260
		depth := 0.05 + r.Float64()*0.08
		for t := 0.0; t < length; t += 0.5 {
			fade := math.Sin(math.Pi * t / length)
			i := wrap(int(x), int(y))
			v[i] -= depth * fade
			v[wrap(int(x)+1, int(y))] += depth * 0.35 * fade
			x += math.Cos(ang) * 0.5
			y += math.Sin(ang) * 0.5
			ang += bend
		}
	}
	// Pits: small dark specks.
	for range 90 {
		cx, cy := r.Float64()*n, r.Float64()*n
		rad := 0.8 + r.Float64()*1.6
		depth := 0.12 + r.Float64()*0.15
		for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
			for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
				if d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy); d <= rad {
					v[wrap(x, y)] -= depth * (1 - d/(rad+0.5))
				}
			}
		}
	}
	l := NewLayer(n, n)
	for i, g := range v {
		g = math.Max(0, math.Min(1, g))
		// Retail's steel is blue-grey; the tint keeps it from reading as flat paint.
		t := tint[i] * 0.12
		l.Pix[i*4], l.Pix[i*4+1], l.Pix[i*4+2], l.Pix[i*4+3] = g*(0.94-t), g*0.98, g*(1.08+t), 1
	}
	return l
}

// periodicNoise is smooth value noise in -1..1 over an n×n plane whose lattice
// has cw×ch cells and wraps, so the plane tiles.
func periodicNoise(r *rand.Rand, n, cw, ch int) []float64 {
	gx, gy := n/cw, n/ch
	lattice := make([]float64, gx*gy)
	for i := range lattice {
		lattice[i] = r.Float64()*2 - 1
	}
	at := func(x, y int) float64 { return lattice[(y%gy)*gx+x%gx] }
	smooth := func(t float64) float64 { return t * t * (3 - 2*t) }
	out := make([]float64, n*n)
	for y := range n {
		cy, fy := y/ch, smooth(float64(y%ch)/float64(ch))
		for x := range n {
			cx, fx := x/cw, smooth(float64(x%cw)/float64(cw))
			top := at(cx, cy)*(1-fx) + at(cx+1, cy)*fx
			bottom := at(cx, cy+1)*(1-fx) + at(cx+1, cy+1)*fx
			out[y*n+x] = top*(1-fy) + bottom*fy
		}
	}
	return out
}
