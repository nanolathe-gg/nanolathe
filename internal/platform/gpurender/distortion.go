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
}

// SetBlastDistortion is an executor comparison control for the modern prototype.
func (r *Renderer) SetBlastDistortion(on bool) { r.distortion.blastDisabled = !on }

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
		radius, width, strength := blastShape(sp.BlastAge, sp.BlastSize, sp.LightingScale)
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
	r.appendBlastWaves()
	r.appendTreeHeat()
	if len(d.indices) == 0 {
		return
	}
	r.submitSchedule()
	r.copyComposite(r.surfaces[1], r.surfaces[0], 0, 0, r.w, r.h)
	d.opts.Images = [4]*ebiten.Image{r.surfaces[1]}
	d.opts.Blend = ebiten.BlendCopy
	r.beginPass(r.surfaces[0])
	r.recordSubmission(len(d.verts), len(d.indices))
	r.surfaces[0].DrawTrianglesShader32(d.verts, d.indices, d.shader, &d.opts)
	r.frameDraws++
}

func (r *Renderer) appendBlastWaves() {
	d := &r.distortion
	if d.blastDisabled {
		return
	}
	d.selectVisible(r.sched.txf(1), r.w, r.h)
	for i := 0; i < d.count; i++ {
		wave := d.waves[i]
		k := r.sched.txf(1)
		x, y := wave.x*k, wave.y*k
		radius, width, strength := wave.radius*k, wave.width*k, wave.strength*k
		reach := radius + width
		cx0, cy0, cx1, cy1 := float32(0), float32(0), float32(r.w), float32(r.h)
		if wave.hasClip {
			cx0, cy0 = max(cx0, float32(wave.clip.X)*k), max(cy0, float32(wave.clip.Y)*k)
			cx1, cy1 = min(cx1, float32(wave.clip.X+wave.clip.W)*k), min(cy1, float32(wave.clip.Y+wave.clip.H)*k)
		}
		x0, y0 := max(cx0, float32(math.Floor(float64(x-reach)))), max(cy0, float32(math.Floor(float64(y-reach))))
		x1, y1 := min(cx1, float32(math.Ceil(float64(x+reach)))), min(cy1, float32(math.Ceil(float64(y+reach))))
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		// Clip limits occupy colour lanes; the radial profile uses custom lanes.
		// Negative source-Y offset selects blasts; heat carries a positive scale.
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
  heat := treeHeatOffset(p, src, wave, scale)
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
func (d *worldDistortion) selectVisible(k float32, w, h int) {
	d.count = 0
	for _, wave := range d.candidates {
		reach := (wave.radius + wave.width) * k
		x, y := wave.x*k, wave.y*k
		x0, y0, x1, y1 := float32(0), float32(0), float32(w), float32(h)
		if wave.hasClip {
			x0, y0 = max(x0, float32(wave.clip.X)*k), max(y0, float32(wave.clip.Y)*k)
			x1, y1 = min(x1, float32(wave.clip.X+wave.clip.W)*k), min(y1, float32(wave.clip.Y+wave.clip.H)*k)
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
