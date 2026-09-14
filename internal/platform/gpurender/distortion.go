package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// User-authored modern presentation tuning, not retail behavior (GPU design §25).
const blastLimit = 32
const blastTicks = 15
const blastMinSize = 64

type blastWave struct {
	x, y, radius, width, strength float32
	clip                          drawlist.Rect
	hasClip                       bool
}

type worldDistortion struct {
	candidates              []blastWave
	blastDisabled, resolved bool
	shader                  *ebiten.Shader
	waves                   [blastLimit]blastWave
	count                   int
	verts                   []ebiten.Vertex
	indices                 []uint32
	opts                    ebiten.DrawTrianglesShaderOptions
	// read is the union of the regions the refraction samples. Unlike the
	// ground pools a refraction reads away from its own fragment, so each quad
	// widens the region by the bound on its own displacement (readcopy.go).
	read readRect
}

// blastMaxOffset bounds the blast shader's displacement as a multiple of the
// wave's strength lane. The shader moves a fragment along the radial direction
// by band·(1−band²)²·3.5·strength, and |band·(1−band²)²| peaks at band = 1/√5
// with the value (1/√5)·(4/5)² = 0.28644, so the displacement can never exceed
// 3.5 × 0.28644 = 1.0026 strengths.
const blastMaxOffset = 1.0026

// treeHeatMaxOffsetX and treeHeatMaxOffsetY bound the plume shader's
// displacement as a multiple of the scale lane the plume's vertices carry. The
// lateral term is (sin ± 0.35·sin)·envelope·1.8, whose bracket is bounded by
// 1.35 and whose envelope is bounded by 1; the vertical term is the same
// product with an extra 0.24 factor (tree_heat.go).
const (
	treeHeatMaxOffsetX = 1.35 * 1.8
	treeHeatMaxOffsetY = 0.24 * 1.8
)

// bilinearPad is the extra texel the refraction's bilinear tap reaches past a
// displaced sample point.
const bilinearPad = 1

// setBlastDistortion is the executor gate the player's Distortion switch drives (§30).
func (r *Renderer) setBlastDistortion(on bool) { r.distortion.blastDisabled = !on }

// dynamicBlastShape is modern artistic tuning, not retail damage arithmetic.
// Keep existing artwork waves, admit substantial medium impacts, and saturate
// ordinary blast boosts before the special-effect sizes (GPU design §25.2).
func dynamicBlastShape(age, size, scale float32, area, damage int32, known bool) (radius, width, strength float32) {
	if !known || size >= 128 {
		return blastShape(age, size, scale)
	}
	if age < 0 || age >= blastTicks || scale <= 0 || size < 48 {
		return
	}
	if size < blastMinSize && (area < 32 || damage < 80) {
		return
	}
	t := age / blastTicks
	baseline := max(size*2.5, 160)
	breadth := float32(math.Sqrt(float64(min(max(float32(area)-48, 0)/208, 1))))
	force := float32(math.Sqrt(float64(min(max(float32(damage)-80, 0)/1120, 1))))
	extent := max(baseline, min(baseline*(1+0.5*breadth), 280))
	radius = (12 + (extent-12)*t) * scale
	width = (10 + size*0.12) * scale
	strength = min(age, 1) * (1 - t) * (1 - t) * min(size/16, 7) * (1 + 0.75*force) * scale
	return
}

func blastShape(age, size, scale float32) (radius, width, strength float32) {
	if age < 0 || age >= blastTicks || size < blastMinSize || scale <= 0 {
		return
	}
	t := age / blastTicks
	extent := min(max(size*2.5, 120), 320)
	radius = (12 + (extent-12)*t) * scale
	width = (10 + size*0.12) * scale
	// A fast attack and squared decay keep the outer wave from ending abruptly.
	strength = min(age, 1) * (1 - t) * (1 - t) * min(size/16, 7) * scale
	return
}

func (r *Renderer) prepareBlastDistortion(list *drawlist.List) {
	d := &r.distortion
	d.count, d.resolved = 0, false
	d.candidates = d.candidates[:0]
	if d.blastDisabled {
		return
	}
	list.VisitLightSources(func(sp drawlist.Sprite) {
		if sp.LightingKind != drawlist.SpriteLightingExplosion || sp.Frame == nil {
			return
		}
		radius, width, strength := dynamicBlastShape(sp.BlastAge, sp.BlastSize, sp.LightingScale, sp.BlastAreaOfEffect, sp.BlastDamage, sp.HasBlastProfile)
		if strength <= 0 {
			return
		}
		wave := blastWave{float32(sp.X), float32(sp.Y), radius, width, strength, sp.Clip, sp.HasClip}
		d.candidates = append(d.candidates, wave)
	})
}

// Both families sample one immutable world copy beneath fog/chrome. Heat quads
// follow every blast quad, so tree heat wins overlaps (GPU design §27.2).
func (r *Renderer) resolveDistortion() {
	d := &r.distortion
	if d.resolved {
		return
	}
	d.resolved = true
	if d.shader == nil || r.surfaces[0] == nil {
		return
	}
	d.verts, d.indices = d.verts[:0], d.indices[:0]
	d.read.reset()
	r.appendBlastWaves()
	heat := len(d.verts)
	r.appendTreeHeat()
	d.addHeatRead(heat)
	r.drawOverComposite(d.read, d.verts, d.indices, d.shader, &d.opts, ebiten.BlendCopy)
}

// addHeatRead widens the read region by the plume quads appended from position
// first onward. The plumes share this batch but belong to tree_heat.go, so the
// region is derived from the vertices they wrote rather than from that file:
// each plume is four corners in the order (x0,y0) (x1,y0) (x0,y1) (x1,y1),
// carrying its clip limits in the colour lanes and one plus its scale lane in
// the source-Y offset. The shader clamps every sample into those same clip
// limits, so the displaced quad intersected with them is a cover.
func (d *worldDistortion) addHeatRead(first int) {
	for i := first; i+3 < len(d.verts); i += 4 {
		a, b := &d.verts[i], &d.verts[i+3]
		lane := b.SrcY - b.DstY - 1
		padX := treeHeatMaxOffsetX*lane + bilinearPad
		padY := treeHeatMaxOffsetY*lane + bilinearPad
		d.read.add(max(a.ColorR, a.DstX-padX), max(a.ColorG, a.DstY-padY),
			min(a.ColorB, b.DstX+padX), min(a.ColorA, b.DstY+padY))
	}
}

func (r *Renderer) appendBlastWaves() {
	d := &r.distortion
	if d.blastDisabled {
		return
	}
	d.selectVisible(r.sched.txf(1), r.w, r.h, r.sched.txx(0), r.sched.txy(0))
	ox, oy := r.sched.txx(0), r.sched.txy(0)
	for i := 0; i < d.count; i++ {
		wave := d.waves[i]
		k := r.sched.txf(1)
		x, y := wave.x*k+ox, wave.y*k+oy
		radius, width, strength := wave.radius*k, wave.width*k, wave.strength*k
		reach := radius + width
		cx0, cy0, cx1, cy1 := float32(0), float32(0), float32(r.w), float32(r.h)
		if wave.hasClip {
			cx0, cy0 = max(cx0, float32(wave.clip.X)*k+ox), max(cy0, float32(wave.clip.Y)*k+oy)
			cx1, cy1 = min(cx1, float32(wave.clip.X+wave.clip.W)*k+ox), min(cy1, float32(wave.clip.Y+wave.clip.H)*k+oy)
		}
		x0, y0 := max(cx0, float32(math.Floor(float64(x-reach)))), max(cy0, float32(math.Floor(float64(y-reach))))
		x1, y1 := min(cx1, float32(math.Ceil(float64(x+reach)))), min(cy1, float32(math.Ceil(float64(y+reach))))
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		// Clip limits occupy colour lanes; the radial profile uses custom lanes.
		// Negative source-Y offset selects blasts; heat carries one plus scaled amplitude.
		base := uint32(len(d.verts))
		for _, p := range [4][2]float32{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
			d.verts = append(d.verts, ebiten.Vertex{DstX: p[0], DstY: p[1], SrcX: p[0], SrcY: p[1] - 1,
				ColorR: cx0, ColorG: cy0, ColorB: cx1, ColorA: cy1,
				Custom0: x, Custom1: y, Custom2: radius, Custom3: width})
			// Radius and width share custom lanes; the source coordinate offset carries
			// strength without introducing a uniform map per wave.
			d.verts[len(d.verts)-1].SrcX += strength
		}
		d.indices = append(d.indices, base, base+1, base+2, base+1, base+2, base+3)
		// The wave samples outward from its own ring, and the shader clamps every
		// sample into the same clip limits the quad was cut to, so the displaced
		// quad intersected with those limits covers every texel the batch reads.
		pad := blastMaxOffset*strength + bilinearPad
		d.read.add(max(cx0, x0-pad), max(cy0, y0-pad), min(cx1, x1+pad), min(cy1, y1+pad))
		r.modelStats.BlastWaves++
	}
}

func newDistortionShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(distortionShaderSource))
}

const distortionShaderSource = `//kage:unit pixels
package main

// Bilinear sampling keeps the final subpixel displacement fading smoothly.
func distortionSample(p, lo, hi vec2) vec4 {
 p = clamp(p, lo+vec2(0.5), hi-vec2(0.5))
 q := p-vec2(0.5)
 f := fract(q)
 a := floor(q)+vec2(0.5)
 b := min(a+vec2(1.0), hi-vec2(0.5))
 o := imageSrc0Origin()
 return mix(mix(imageSrc0At(a+o), imageSrc0At(vec2(b.x,a.y)+o), f.x),
  mix(imageSrc0At(vec2(a.x,b.y)+o), imageSrc0At(b+o), f.x), f.y)
}

func Fragment(dst vec4, src vec2, clip vec4, wave vec4) vec4 {
 p := dst.xy-imageDstOrigin()
 scale := src.y-imageSrc0Origin().y-p.y
 if scale > 0.0 {
  heat := treeHeatOffset(p, src, wave, max(scale-1.0, 0.0))
  if heat.z == 0.0 { discard() }
  return distortionSample(p+heat.xy, clip.xy, clip.zw)
 }
 delta := p-wave.xy
 distance := length(delta)
 band := (distance-wave.z)/wave.w
 if abs(band) >= 1.0 || distance < 0.001 { discard() }
 strength := src.x-imageSrc0Origin().x-p.x
 // Bipolar compression and rarefaction, with zero displacement at both edges.
 envelope := 1.0-band*band
 offset := delta/distance*(band*envelope*envelope*3.5*strength)
 return distortionSample(p+offset, clip.xy, clip.zw)
}
` + treeHeatShaderSource

// Select only after the final world transform is known. Offscreen explosions
// must never consume the visible wave budget, including during smooth zoom.
func (d *worldDistortion) selectVisible(k float32, w, h int, ox, oy float32) {
	d.count = 0
	for _, wave := range d.candidates {
		reach := (wave.radius + wave.width) * k
		x, y := wave.x*k+ox, wave.y*k+oy
		x0, y0, x1, y1 := float32(0), float32(0), float32(w), float32(h)
		if wave.hasClip {
			x0, y0 = max(x0, float32(wave.clip.X)*k+ox), max(y0, float32(wave.clip.Y)*k+oy)
			x1, y1 = min(x1, float32(wave.clip.X+wave.clip.W)*k+ox), min(y1, float32(wave.clip.Y+wave.clip.H)*k+oy)
		}
		if x0 >= x1 || y0 >= y1 || x+reach <= x0 || y+reach <= y0 || x-reach >= x1 || y-reach >= y1 {
			continue
		}
		at := d.count
		if at == blastLimit {
			at = 0
			for i := 1; i < d.count; i++ {
				if d.waves[i].strength <= d.waves[at].strength {
					at = i
				}
			}
			if wave.strength <= d.waves[at].strength {
				continue
			}
			copy(d.waves[at:], d.waves[at+1:d.count])
			at = d.count - 1
		} else {
			d.count++
		}
		d.waves[at] = wave
	}
}
