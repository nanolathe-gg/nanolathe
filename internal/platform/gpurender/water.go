package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Coastal water is an Enhanced presentation treatment (GPU design §26).
// The mask is in painted map pixels, not the world-height grid: the existing
// inverse projection accounts for the angled camera before classifying water.
const waterBlockSize = 128

type waterLayer struct {
	disabled           bool
	shader, wakeShader *ebiten.Shader
	source             *world.Terrain
	mask               *ebiten.Image
	step, w, h         int
	blocks             []bool
	blockW, blockH     int
	record             drawlist.Terrain
}

// SetWaterEffects is a presentation-only capture comparison control.
func (r *Renderer) SetWaterEffects(on bool) { r.water.disabled = !on }

func newWaterShader() (*ebiten.Shader, error) { return ebiten.NewShader([]byte(waterShaderSource)) }
func newSurfaceWakeShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(surfaceWakeShaderSource))
}

// waterMaskPixels builds a conservative, bounded-resolution projected mask.
// Red is foam-compatible water, green is distance inward from the shore (0..32
// world pixels), and blue is valid dry ground. The chamfer sweeps cost linear
// time and allocate once per terrain identity; they do not inspect or modify
// occupancy or simulation RNG.
func waterMaskPixels(t *world.Terrain) (pixels []byte, w, h, step int, blocks []bool, bw, bh int) {
	if t == nil || t.CellW <= 1 || t.CellH <= 1 || len(t.Plot) < int(t.CellW)*int(t.CellH) {
		return
	}
	pw, ph := int(t.CellW)*16, int(t.CellH)*16
	step = 1
	for (max(pw, ph)+step-1)/step > 2048 {
		step *= 2
	}
	w, h = (pw+step-1)/step, (ph+step-1)/step
	pixels = make([]byte, w*h*4)
	bw, bh = (pw+waterBlockSize-1)/waterBlockSize, (ph+waterBlockSize-1)/waterBlockSize
	blocks = make([]bool, bw*bh)
	distance := make([]uint16, w*h)
	waterAllowed := t.SeaLevel != 0 && !t.LavaWorld && !(t.WaterDoesDamage != 0 && t.WaterDamage != 0)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			pixels[i*4+3] = 255
			px, py := int32(x*step+step/2), int32(y*step+step/2)
			wx, wy, wz := t.CursorToWorldMapPixels(px, py)
			ground := t.HeightAt(wx, wz)
			if ground < 0 {
				continue
			}
			if ground >= t.SeaLevelWorld() {
				pixels[i*4+2] = 255
				continue
			}
			if !waterAllowed || wy != t.SeaLevelWorld() {
				continue
			}
			pixels[i*4] = 255
			distance[i] = 255
			blocks[(int(py)/waterBlockSize)*bw+int(px)/waterBlockSize] = true
		}
	}
	// Valid neighbours supply shore distance; no reads cross the source.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if distance[i] == 0 {
				continue
			}
			if x == 0 || y == 0 || x == w-1 || y == h-1 {
				continue
			}
			distance[i] = min(distance[i], distance[i-1]+3, distance[i-w]+3, distance[i-w-1]+4, distance[i-w+1]+4)
		}
	}
	for y := h - 1; y >= 0; y-- {
		for x := w - 1; x >= 0; x-- {
			i := y*w + x
			if distance[i] == 0 {
				continue
			}
			if x+1 < w {
				distance[i] = min(distance[i], distance[i+1]+3)
			}
			if y+1 < h {
				distance[i] = min(distance[i], distance[i+w]+3)
				if x > 0 {
					distance[i] = min(distance[i], distance[i+w-1]+4)
				}
				if x+1 < w {
					distance[i] = min(distance[i], distance[i+w+1]+4)
				}
			}
			pixels[i*4+1] = byte(min(int(distance[i])*step*255/(3*32), 255))
		}
	}
	// Smooth the distance field, independently of strict wet/dry classification.
	// A wider filter rounds the height-grid corners before a moving wave can
	// pick them out. Keep its support consistent on the two finest mask levels.
	blur := make([]byte, w*h)
	weights := [9]int{1, 4, 7, 10, 12, 10, 7, 4, 1}
	blurStride := max(1, 2/step)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 0
			for k, weight := range weights {
				v += int(pixels[(y*w+min(max(x+(k-4)*blurStride, 0), w-1))*4+1]) * weight
			}
			blur[y*w+x] = byte(v / 56)
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 0
			for k, weight := range weights {
				v += int(blur[min(max(y+(k-4)*blurStride, 0), h-1)*w+x]) * weight
			}
			pixels[(y*w+x)*4+1] = byte(v / 56)
		}
	}
	return
}

func (r *Renderer) prepareWater(c drawlist.Terrain) {
	st := &r.water
	st.record = c
	// Scorch shares the dry channel even when water animation is disabled (§29).
	if c.Terrain == nil || ((!c.Water.Enabled || st.disabled) && !r.scorchEnabled) {
		return
	}
	if st.source == c.Terrain {
		return
	}
	if st.mask != nil {
		st.mask.Deallocate()
		st.mask = nil
	}
	st.source = c.Terrain
	pixels, w, h, step, blocks, bw, bh := waterMaskPixels(c.Terrain)
	st.w, st.h, st.step, st.blocks, st.blockW, st.blockH = w, h, step, blocks, bw, bh
	if w == 0 || h == 0 {
		return
	}
	st.mask = ebiten.NewImage(w, h)
	st.mask.WritePixels(pixels)
}

func (st *waterLayer) visibleWater(c drawlist.Terrain) bool {
	if st.blockW == 0 || st.blockH == 0 {
		return false
	}
	// One block of slack on each side: the damp shoreline band of §32 lies on
	// dry ground beside the water, so a coast just past the viewport edge still
	// has to run the pass for the band to reach the visible strip.
	scale := c.Scale.Norm()
	x0 := max(floorDivInt(int(c.OriginX), waterBlockSize)-1, 0)
	y0 := max(floorDivInt(int(c.OriginY), waterBlockSize)-1, 0)
	x1 := min(floorDivInt(int(c.OriginX+scale.Inverse(c.DstW)), waterBlockSize)+1, st.blockW-1)
	y1 := min(floorDivInt(int(c.OriginY+scale.Inverse(c.DstH)), waterBlockSize)+1, st.blockH-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if st.blocks[y*st.blockW+x] {
				return true
			}
		}
	}
	return false
}

func (r *Renderer) drawWater(c drawlist.Terrain) {
	st := &r.water
	if st.disabled || !c.Water.Enabled || st.shader == nil || st.mask == nil || !st.visibleWater(c) {
		return
	}
	w, h := min(int(c.DstW), r.clipW()), min(int(c.DstH), r.clipH())
	if w <= 0 || h <= 0 {
		return
	}
	if !r.sched.beginBlended(schedDest, 0, 0, w, h, [4]*ebiten.Image{1: st.mask}, st.shader, blendComposite, 0) {
		return
	}
	scale := float32(c.Scale.Float())
	effective := scale
	if r.sched.worldOn {
		effective *= r.sched.worldScale
	}
	time := (float32(c.Water.Tick) + float32(c.Water.Fraction16)/65536) / 30
	r.sched.quad(schedDest, 0, 0, float32(w), float32(h), float32(c.OriginX), float32(c.OriginY), float32(c.OriginX)+float32(w)/scale, float32(c.OriginY)+float32(h)/scale,
		[4]float32{time, c.Water.DriftX, c.Water.DriftZ, c.Water.Energy}, [4]float32{float32(st.step), effective, 0, 0})
}

// SurfaceWakes shares the projected wet/dry mask with the water surface. The
// scheduler preserves the under-object order and applies free zoom once.
func (r *Renderer) SurfaceWakes(batch drawlist.SurfaceWakes) {
	st := &r.water
	if st.disabled || !st.record.Water.Enabled || st.mask == nil || st.wakeShader == nil || len(batch.Marks) == 0 {
		return
	}
	scale := float32(st.record.Scale.Float())
	if r.sched.worldOn {
		scale *= r.sched.worldScale
	}
	mapping := [4]float32{float32(st.record.OriginX), float32(st.record.OriginY), 1 / scale, float32(st.step)}
	for _, m := range batch.Marks {
		if m.Alpha <= 0 || (!m.Dust && !m.Foam) {
			continue
		}
		xs := [4]float32{m.X - m.AxisX - m.CrossX, m.X + m.AxisX - m.CrossX, m.X - m.AxisX + m.CrossX, m.X + m.AxisX + m.CrossX}
		ys := [4]float32{m.Y - m.AxisY - m.CrossY, m.Y + m.AxisY - m.CrossY, m.Y - m.AxisY + m.CrossY, m.Y + m.AxisY + m.CrossY}
		x0, y0, x1, y1 := xs[0], ys[0], xs[0], ys[0]
		for i := 1; i < 4; i++ {
			x0 = min(x0, xs[i])
			y0 = min(y0, ys[i])
			x1 = max(x1, xs[i])
			y1 = max(y1, ys[i])
		}
		if !r.sched.beginBlended(schedDest, max(int(math.Floor(float64(x0))), 0), max(int(math.Floor(float64(y0))), 0), min(int(math.Ceil(float64(x1))), r.clipW()), min(int(math.Ceil(float64(y1))), r.clipH()), [4]*ebiten.Image{0: st.mask}, st.wakeShader, blendComposite, schedReadNone) {
			continue
		}
		kind := min(max(m.Age, 0), 1)
		if m.Dust {
			kind += 2
		}
		if m.Foam {
			kind = 4 + min(max(m.Age, 0), 1)
		}
		r.sched.quadCorners(schedDest, xs, ys, mapping, [4][4]float32{{-1, -1, m.Alpha, kind}, {1, -1, m.Alpha, kind}, {-1, 1, m.Alpha, kind}, {1, 1, m.Alpha, kind}})
	}
}

const waterShaderSource = `//kage:unit pixels
package main

func noise(p vec2) float {
 f := fract(p)
 // Time and the integrated wind drift scroll this lattice without bound, so
 // the hashed cell index has to be wrapped before it reaches the sine: an
 // unbounded argument loses all float precision over a long game and the
 // pattern degrades. Wrapping every corner on one period keeps the lattice
 // continuous instead of jumping — the field simply repeats every 289 cells,
 // which at the scales used here (0.0055 gust, 0.0161 warp, 0.07 broad,
 // 0.166 fine and 0.025 shore cells per world pixel) is about 1,700 world
 // pixels for the finest layer and tens of thousands for the coarse ones
 // (GPU design §26.3).
 a := floor(p)
 a = a-floor(a/289.0)*289.0
 b := a+vec2(1.0)
 b = b-floor(b/289.0)*289.0
 f = f*f*(3.0-2.0*f)
 h := vec4(dot(a,vec2(43.17,97.53)),dot(vec2(b.x,a.y),vec2(43.17,97.53)),dot(vec2(a.x,b.y),vec2(43.17,97.53)),dot(b,vec2(43.17,97.53)))
 h = fract(sin(h)*17341.23)
 return mix(mix(h.x,h.y,f.x),mix(h.z,h.w,f.x),f.y)
}

func terrainLinear(p vec2) vec4 {
 a := floor(p-vec2(0.5))
 f := fract(p-vec2(0.5))
 o := imageSrc0Origin()
 return mix(mix(imageSrc0At(o+a),imageSrc0At(o+a+vec2(1,0)),f.x),mix(imageSrc0At(o+a+vec2(0,1)),imageSrc0At(o+a+vec2(1,1)),f.x),f.y)
}

func wetMask(p vec2) vec3 {
 // Coordinates are map pixels divided by the cached mask step, in source-0
 // space for source 1 even though the two images have different atlas origins.
 // Blue is the independent dry-ground channel; it is only ever read as a gate,
 // so bilinear mixing across the boundary cannot corrupt the wet-side terms.
 q := p-vec2(0.5)
 a := floor(q)
 f := fract(q)
 o := imageSrc0Origin()
 return mix(mix(imageSrc1AtFromSrc0Pos(o+a).rgb,imageSrc1AtFromSrc0Pos(o+a+vec2(1,0)).rgb,f.x),mix(imageSrc1AtFromSrc0Pos(o+a+vec2(0,1)).rgb,imageSrc1AtFromSrc0Pos(o+a+vec2(1,1)).rgb,f.x),f.y)
}

func maskWater(p vec2) float {
 q := p-vec2(0.5)
 a := floor(q)
 f := fract(q)
 o := imageSrc0Origin()
 return mix(mix(imageSrc1AtFromSrc0Pos(o+a).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,0)).r,f.x),mix(imageSrc1AtFromSrc0Pos(o+a+vec2(0,1)).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,1)).r,f.x),f.y)
}

// waterRing reports how much ordinary water surrounds a dry map pixel: x is the
// largest water reading on a ring of eight taps, y the ring's mean. The mask
// carries no dry-side distance field — its alpha is opaque and a dry-side green
// would corrupt the wet-side shore terms under bilinear filtering — so the dry
// band measures nearness by sampling instead. Both numbers are needed, and both
// taps are bilinear, because the mask step varies with map size: the maximum
// alone would end the band on a whole texel, and on a large map, where the step
// is eight world pixels, the ring spans a single texel and only the filtered
// reading still resolves the band's width (§32).
func waterRing(p vec2, r float) vec2 {
 d := r*0.70710678
 s := vec4(maskWater(p+vec2(r,0.0)),maskWater(p-vec2(r,0.0)),maskWater(p+vec2(0.0,r)),maskWater(p-vec2(0.0,r)))
 u := vec4(maskWater(p+vec2(d,d)),maskWater(p-vec2(d,d)),maskWater(p+vec2(d,-d)),maskWater(p-vec2(d,-d)))
 m := max(max(max(s.x,s.y),max(s.z,s.w)),max(max(u.x,u.y),max(u.z,u.w)))
 return vec2(m,(s.x+s.y+s.z+s.w+u.x+u.y+u.z+u.w)*0.125)
}

func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 screen := dst.xy-imageDstOrigin()
 origin := imageSrc0Origin()
 base := imageSrc0At(origin+screen)
 world := src-origin
 mask := wetMask(world/custom.x)
 coverage := smoothstep(0.8,1.0,mask.x)
 t := color.r
 drift := color.gb
 strength := color.a
 // Damp shoreline band (§32). Ground the water has just washed keeps a darker
 // tone, so dry pixels within about eight world pixels of water lose up to
 // twelve percent of their brightness, pulsing on the phase the shore foam
 // carries at the boundary. This runs before the dry early-out because the
 // band lives on dry texels; a pixel with no water on its ring returns the
 // painted colour unchanged.
 // The blue gate only has to exclude terrain that is neither medium — invalid
 // ground and excluded liquid carry no dry flag at all — so it stays well below
 // the boundary's own bilinear ramp, which is where the band belongs.
 dry := smoothstep(0.05,0.5,mask.z)*(1.0-coverage)
 if dry>0.0 {
  ring := waterRing(world/custom.x,8.0/custom.x)
  damp := smoothstep(0.2,1.0,ring.x)*smoothstep(0.10,0.45,ring.y)*dry
  if damp>0.0 {
   lapDry := pow(max(0.0,sin(t*1.6+noise(world*0.025)*3.0)),2.0)
   base = vec4(base.rgb*(1.0-0.12*damp*(0.5+0.5*lapDry)),base.a)
  }
 }
 if coverage<=0 { return base }
 // Every layer travels only on the integrated wind drift, each at its own
 // multiple, so the direction of travel is always the wind and the differing
 // speeds give the surface parallax. Nothing scrolls on a fixed velocity: in
 // calm wind a fixed scroll dominates the drift and the whole sea slides in a
 // direction unrelated to the wind (§26.3). The field is never rotated by the
 // wind either — the retail heading re-rolls to a fresh random value every
 // 150 to 420 ticks
 // [05 "The wind phase, its draws, and the generator notification"], so an
 // oriented field would swing through a new angle on every re-roll.
 //
 // Gust patches ("cat's paws"): a very coarse layer gliding downwind fastest,
 // darkening and roughening the water it crosses.
 gust := smoothstep(0.30,0.80,noise((world-drift*22.0)*0.0055+vec2(3.0,7.0)))
 // A slow domain warp makes the broad ripple churn in place rather than only
 // translate: the warp lattice is the one thing that moves on time alone, and
 // it deforms the layers beneath it, so cells stretch, split and merge.
 p := (world-drift*6.0)*0.07
 warp := vec2(noise(p*0.23+vec2(t*0.076,5.0-t*0.052)),noise(p*0.23+vec2(9.0-t*0.064,t*0.084)))
 p += (warp-vec2(0.5))*1.6
 broad := noise(p)
 // The fine lattice is rotated 37 degrees about the map origin — a fixed
 // rotation, applied once, not a wind-following one — so its cell rows never
 // line up with the broad lattice and the pair stops reading as a grid.
 q := (world-drift*11.0)*0.166
 q = vec2(q.x*0.7986-q.y*0.6018,q.x*0.6018+q.y*0.7986)+(warp-vec2(0.5))*0.9
 fine := noise(q)
 ripple := broad*0.65+fine*0.35-0.5
 deep := smoothstep(0.05,0.55,mask.y)
 offset := vec2(broad-0.5,fine-0.5)*custom.y*(3.2+strength*2.4)*(0.7+0.5*gust)*deep*coverage
 sample := clamp(screen+offset,vec2(0.5),imageSrc0Size()-vec2(0.5))
 // Subpixel filtering prevents nearest-neighbour displacement from snapping.
 warped := terrainLinear(sample)
 shade := 1.0+ripple*(0.112+strength*0.08)*(0.7+0.5*gust)*deep-0.05*strength*gust*deep
 // Moving highlights make the surface readable even when the painted detail
 // is too fine to reveal displacement. Their broken shape follows both fields.
 crest := smoothstep(0.10,0.32,ripple)
 result := mix(warped.rgb*shade,vec3(0.40,0.67,0.78),crest*(0.04+strength*0.04)*deep)
 // Wave fronts travel down the smoothed shore-distance field. Broad crests
 // dissolve before the wet/dry boundary, so they do not trace its texel steps.
 shore := (1.0-smoothstep(0.35,0.95,mask.y))*smoothstep(0.03,0.22,mask.y)
 shorePatch := noise(world*0.025)
 lap := pow(max(0.0,sin(mask.y*10.0+t*1.6+shorePatch*3.0)),2.0)
 foam := shore*lap*(0.09+strength*0.105)*(0.50+0.50*shorePatch)*coverage
 result = mix(result,vec3(0.72,0.84,0.87),foam)
 // Shallow tint (§32): water lightens toward a pale cyan as the bottom rises.
 result = mix(result,vec3(0.62,0.80,0.84),0.08*(1.0-smoothstep(0.0,0.35,mask.y)))
 return vec4(mix(base.rgb,min(result,vec3(base.a)),coverage),base.a)
}
`

const surfaceWakeShaderSource = `//kage:unit pixels
package main

func surfaceMask(p vec2) vec3 {
 q := p-vec2(0.5)
 a := floor(q)+imageSrc0Origin()
 f := fract(q)
 return mix(mix(imageSrc0At(a).rgb,imageSrc0At(a+vec2(1,0)).rgb,f.x),mix(imageSrc0At(a+vec2(0,1)).rgb,imageSrc0At(a+vec2(1,1)).rgb,f.x),f.y)
}

func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 screen := dst.xy-imageDstOrigin()
 world := color.rg+screen*color.b
 mask := surfaceMask(world/color.a)
 u,v := custom.x,custom.y
 age := custom.w
 foam := age>=4.0
 coverage := 0.0
 tint := vec3(1.0)
 if !foam {
  age-=2.0
  // Several soft lobes make each expanding particle less like a stamped oval.
  r := length(vec2(u,v))
  lobes := 0.78+0.22*sin(u*7.0+age*2.0)*sin(v*6.0-age*1.5)
  coverage = pow(max(0.0,1.0-r*r),2.0)*lobes*smoothstep(0.8,1.0,mask.b)
 } else {
  age-=4.0
  // Loose elliptical arcs suggest water displaced around the base without
  // drawing the rectangular build footprint. Two phases spread and dissolve.
  r := length(vec2(u,v))
  phase := fract(age*2.0)
  other := fract(phase+0.5)
  ring := (1.0-smoothstep(0.015,0.09,abs(r-(0.58+phase*0.30))))*sin(phase*3.141593)
  ring += (1.0-smoothstep(0.015,0.09,abs(r-(0.58+other*0.30))))*sin(other*3.141593)
  patches := smoothstep(-0.1,0.8,sin(u*8.0+sin(v*5.0)+age*6.283185))
  coverage = ring*patches*smoothstep(0.8,1.0,mask.r)
  tint = vec3(0.70,0.83,0.87)
 }
 alpha := clamp(coverage*custom.z,0,1)
 return vec4(tint*alpha,alpha)
}
`
