package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Coastal water is an Enhanced presentation treatment (GPU design §26).
// The mask is in painted map pixels, not the world-height grid: the existing
// inverse projection accounts for the angled camera before classifying water.
const waterBlockSize = 128

type waterLayer struct {
	bedSprites         []drawlist.Sprite
	bedDrawn           bool
	disabled           bool
	shader, wakeShader *ebiten.Shader
	source             *world.Terrain
	mask               *ebiten.Image
	step, w, h         int
	blocks             []bool
	blockW, blockH     int
	record             drawlist.Terrain
}

// setWaterEffects is the executor gate the player's Water switch drives (§30).
func (r *Renderer) setWaterEffects(on bool) { r.water.disabled = !on }

func newWaterShader() (*ebiten.Shader, error) { return ebiten.NewShader([]byte(waterShaderSource)) }
func newSurfaceWakeShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(surfaceWakeShaderSource))
}

// waterMaskPixels builds a conservative, bounded-resolution projected mask.
// The surface includes acid and lava pools; shoreline foam is admitted
// separately for ordinary, non-void water.
// Red is liquid coverage, green is distance inward from the shore (0..32
// world pixels), blue is dry ground (1) or void liquid (0.5), and alpha is
// the damp band's ring term (waterRingField). The chamfer sweeps cost linear
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
	waterAllowed := t.SeaLevel != 0 || t.LavaWorld
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			px, py := int32(x*step+step/2), int32(y*step+step/2)
			wx, wy, wz := t.CursorToWorldMapPixels(px, py)
			ground := t.HeightAt(wx, wz)
			if ground < 0 {
				continue
			}
			submerged := ground < t.SeaLevelWorld()
			// Lava's impassable flood includes the level itself. Include flat pools
			// at height zero too, which occur on zero-level lava maps.
			if t.LavaWorld {
				submerged = ground <= t.SeaLevelWorld()
			}
			if !submerged {
				pixels[i*4+2] = 255
				continue
			}
			if !waterAllowed || wy != t.SeaLevelWorld() {
				continue
			}
			pixels[i*4] = 255
			if cell := t.PlotAt(world.WorldToCell(wx), world.WorldToCell(wz)); cell != nil && cell.IsVoid() {
				// Topological voids reject every surface movement class, regardless of
				// unit selection. Retain their liquid motion, but never add shore foam.
				pixels[i*4+2] = 128
			}
			distance[i] = 255
			blocks[(int(py)/waterBlockSize)*bw+int(px)/waterBlockSize] = true
		}
	}
	roundShoreline(pixels, distance, w, h, step)
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
	waterRingField(pixels, distance, w, h, step, blocks, bw, bh)
	return
}

// roundShoreline takes the water the shore distance is measured from back to a
// rounded coast (§32.3). Terrain height is a sixteen-pixel grid, and where a
// beach is steep the sea-level contour of its bilinear surface hugs the cell
// edges, so the strict wet set ends in a staircase that every wave front and
// the shallow tint then trace. Three box passes approximate a Gaussian of about
// eight world pixels; water whose blurred reading is under three quarters is
// dropped from the distance source. Three quarters is the reading at the tip of
// a square dry corner, so the rounded coast passes outside every such corner
// and about six world pixels off a straight shore. Only the distance source
// changes: red stays the strict wet set its other readers gate on.
func roundShoreline(pixels []byte, distance []uint16, w, h, step int) {
	radius := max(1, 8/step)
	field, scratch := make([]uint16, w*h), make([]uint16, w*h)
	for i := range field {
		field[i] = uint16(pixels[i*4])
	}
	window := 2*radius + 1
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			row := field[y*w : (y+1)*w]
			sum := 0
			for k := -radius; k <= radius; k++ {
				sum += int(row[min(max(k, 0), w-1)])
			}
			for x := 0; x < w; x++ {
				scratch[y*w+x] = uint16(sum / window)
				sum += int(row[min(x+radius+1, w-1)]) - int(row[max(x-radius, 0)])
			}
		}
		for x := 0; x < w; x++ {
			sum := 0
			for k := -radius; k <= radius; k++ {
				sum += int(scratch[min(max(k, 0), h-1)*w+x])
			}
			for y := 0; y < h; y++ {
				field[y*w+x] = uint16(sum / window)
				sum += int(scratch[min(y+radius+1, h-1)*w+x]) - int(scratch[max(y-radius, 0)*w+x])
			}
		}
	}
	for i, v := range field {
		if v < 191 {
			distance[i] = 0
		}
	}
}

// waterRingField writes the damp band's ring term (§32) into the mask's spare
// alpha channel: how much liquid surrounds this texel, as the product
// of the two readings the band is gated on — a smooth step on the largest water
// reading around a ring of eight taps eight world pixels out, times one on the
// ring's mean. Both readings are bilinear, because the mask step varies with map
// size: the maximum alone would end the band on a whole texel, and on a large
// map, where a texel is eight world pixels, the ring spans a single texel and
// only the filtered reading resolves the band's width.
//
// The shader once did this per fragment — eight bilinear taps, thirty-two
// texture reads — and the water quad covers the whole viewport whenever any
// water is within a block of it, so every inland pixel paid for it. The ring
// depends only on the terrain, so it is built once per terrain identity here and
// the shader reads it out of the tap it already takes for the other channels.
// It is computed at texel centres and filtered back, so a fragment reads the
// ring's own numbers to within the blend across one texel.
//
// The term is written on the wet side as well, by the same formula, so that the
// bilinear blend across the wet/dry boundary stays continuous; the wet side is
// gated away by the dry term the shader multiplies it with.
func waterRingField(pixels []byte, distance []uint16, w, h, step int, blocks []bool, bw, bh int) {
	if bw == 0 || bh == 0 {
		return
	}
	radius := 8.0 / float64(step)
	// Water further from the shore than the ring reaches has water on every tap,
	// so its term is one without sampling. The chamfer distance is three per
	// texel of the axis step, and a bilinear tap reads one texel beyond its own
	// position.
	interior := uint16(3 * (int(math.Ceil(radius)) + 2))
	diag := radius * 0.70710678
	offsets := [8][2]float64{{radius, 0}, {-radius, 0}, {0, radius}, {0, -radius},
		{diag, diag}, {-diag, -diag}, {diag, -diag}, {-diag, diag}}
	// A texel with no water within a block of it has an empty ring, so only the
	// blocks beside a water block are worth sampling. One block of slack covers
	// the ring's eight world pixels, as a block is 128 of them.
	near := make([]bool, bw*bh)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			if !blocks[by*bw+bx] {
				continue
			}
			for ny := max(by-1, 0); ny <= min(by+1, bh-1); ny++ {
				for nx := max(bx-1, 0); nx <= min(bx+1, bw-1); nx++ {
					near[ny*bw+nx] = true
				}
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px, py := x*step+step/2, y*step+step/2
			if !near[(py/waterBlockSize)*bw+px/waterBlockSize] {
				continue
			}
			if distance[y*w+x] >= interior {
				pixels[(y*w+x)*4+3] = 255
				continue
			}
			u, v := float64(x)+0.5, float64(y)+0.5
			high, total := 0.0, 0.0
			for _, o := range offsets {
				t := maskWaterAt(pixels, w, h, u+o[0], v+o[1])
				high = max(high, t)
				total += t
			}
			term := waterSmoothstep(0.2, 1.0, high) * waterSmoothstep(0.10, 0.45, total*0.125)
			pixels[(y*w+x)*4+3] = byte(term*255 + 0.5)
		}
	}
}

// maskWaterAt is the shader's bilinear water reading on the CPU: texel centres
// are at half-integers and a tap outside the mask reads zero, as a source fetch
// outside the image does.
func maskWaterAt(pixels []byte, w, h int, u, v float64) float64 {
	qx, qy := u-0.5, v-0.5
	ax, ay := math.Floor(qx), math.Floor(qy)
	fx, fy := qx-ax, qy-ay
	x, y := int(ax), int(ay)
	top := maskWaterTexel(pixels, w, h, x, y)*(1-fx) + maskWaterTexel(pixels, w, h, x+1, y)*fx
	bottom := maskWaterTexel(pixels, w, h, x, y+1)*(1-fx) + maskWaterTexel(pixels, w, h, x+1, y+1)*fx
	return top*(1-fy) + bottom*fy
}

func maskWaterTexel(pixels []byte, w, h, x, y int) float64 {
	if x < 0 || y < 0 || x >= w || y >= h {
		return 0
	}
	return float64(pixels[(y*w+x)*4]) / 255
}

// waterSmoothstep is the shader's smoothstep, so the precomputed term is the
// number the per-fragment ring produced.
func waterSmoothstep(edge0, edge1, x float64) float64 {
	t := min(max((x-edge0)/(edge1-edge0), 0), 1)
	return t * t * (3 - 2*t)
}

func (r *Renderer) prepareWater(c drawlist.Terrain) {
	st := &r.water
	st.record = c
	// Aircraft soft shadows read this mask whatever the Water and Marks
	// switches say, so terrain alone decides whether it is built
	// (GPU design §29, §34).
	if c.Terrain == nil {
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
	x0 := max(numeric.FloorDiv(int(c.OriginX), waterBlockSize)-1, 0)
	y0 := max(numeric.FloorDiv(int(c.OriginY), waterBlockSize)-1, 0)
	x1 := min(numeric.FloorDiv(int(c.OriginX+scale.Inverse(c.DstW)), waterBlockSize)+1, st.blockW-1)
	y1 := min(numeric.FloorDiv(int(c.OriginY+scale.Inverse(c.DstH)), waterBlockSize)+1, st.blockH-1)
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
	shoreFoam, waterColour := float32(1), float32(1)
	if c.Terrain != nil {
		if c.Terrain.LavaWorld || (c.Terrain.WaterDoesDamage != 0 && c.Terrain.WaterDamage != 0) {
			shoreFoam = 0
		}
		if c.Terrain.LavaWorld {
			waterColour = 0
		}
	}
	r.sched.quad(schedDest, 0, 0, float32(w), float32(h), float32(c.OriginX), float32(c.OriginY), float32(c.OriginX)+float32(w)/scale, float32(c.OriginY)+float32(h)/scale,
		[4]float32{time, c.Water.TidalDriftX, c.Water.TidalDriftZ, c.Water.Energy}, [4]float32{float32(st.step), effective, shoreFoam, waterColour})
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
	ox, oy := r.sched.inverseOrigin(float32(st.record.OriginX), float32(st.record.OriginY), scale)
	mapping := [4]float32{ox, oy, 1 / scale, float32(st.step)}
	for _, m := range batch.Marks {
		if m.Foam {
			// The selected foam opacity is independent of surface opacity (§26).
			m.Alpha *= 0.6
		}
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

// Selected presentation values (GPU design §26.3).
const patternSize = 0.5
const surfaceOpacity = 0.5
const currentScale = 3.0
const rippleDeformation = 5.0
const shoreFoamOpacity = 0.6
const edgeFadePixels = 11.2
const surfaceEnergy = 0.5

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

func wetMask(p vec2) vec4 {
 // Coordinates are map pixels divided by the cached mask step, in source-0
 // space for source 1 even though the two images have different atlas origins.
 // Blue is dry ground at 1, void liquid at 0.5, and ordinary liquid at 0.
 // The void marker suppresses foam without removing animated surface coverage.
 // Alpha is the damp band's ring term, built per terrain identity by
 // waterRingField and continuous across the boundary, so it filters like the
 // rest.
 q := p-vec2(0.5)
 a := floor(q)
 f := fract(q)
 o := imageSrc0Origin()
 return mix(mix(imageSrc1AtFromSrc0Pos(o+a),imageSrc1AtFromSrc0Pos(o+a+vec2(1,0)),f.x),mix(imageSrc1AtFromSrc0Pos(o+a+vec2(0,1)),imageSrc1AtFromSrc0Pos(o+a+vec2(1,1)),f.x),f.y)
}

func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 screen := dst.xy-imageDstOrigin()
 origin := imageSrc0Origin()
 base := imageSrc0At(origin+screen)
 world := src-origin
 mask := wetMask(world/custom.x)
 coverage := smoothstep(0.8,1.0,mask.x)
 t := color.r
 drift := color.gb*currentScale
 pattern := world/patternSize
 shoreDistance := mask.y
 original := base
 strength := surfaceEnergy
 // Damp shoreline band (§32). Ground the water has just washed keeps a darker
 // tone, so dry pixels within about eight world pixels of water lose up to
 // twelve percent of their brightness, pulsing on the phase the shore foam
 // carries at the boundary. This runs before the dry early-out because the
 // band lives on dry texels; a pixel with no water near it returns the painted
 // colour unchanged, and the mask's alpha says so without a second lookup.
 // The blue gate only has to exclude terrain that is neither medium — invalid
 // ground and excluded liquid carry no dry flag at all — so it stays well below
 // the boundary's own bilinear ramp, which is where the band belongs.
 dry := smoothstep(0.05,0.5,mask.z)*(1.0-coverage)
 damp := mask.w*dry*custom.w
 if damp>0.0 {
  lapDry := pow(max(0.0,sin(t*1.6+noise(pattern*0.025)*3.0)),2.0)
  base = vec4(base.rgb*(1.0-0.12*damp*(0.5+0.5*lapDry)),base.a)
 }
 // The height grid and painted shoreline can disagree. Fade the whole
 // surface treatment in from the rounded coast instead of revealing the
 // strict coverage cutoff. The selected 0.7 edge factor gives an 11.2-pixel
 // fade, independent of pattern size, foam width and zoom. The shared strict
 // mask stays unchanged for reflections and other readers.
 coverage *= smoothstep(0.0,edgeFadePixels/32.0,mask.y)
 if coverage<=0 { return vec4(clamp(mix(original.rgb,base.rgb,surfaceOpacity),vec3(0),vec3(base.a)),base.a) }
 // Current translates a fixed world-space lattice; it never rotates the
 // texture when wind changes. Three drift multiples give surface parallax.
 // The current is integrated from tidal speed and eased wind direction (§26).
 gust := smoothstep(0.30,0.80,noise((pattern-drift*22.0)*0.0055+vec2(3.0,7.0)))
 // Bounded in-place deformation keeps zero-tidal surfaces alive without
 // introducing a directional scroll unrelated to the current.
 p := (pattern-drift*6.0)*0.07
 a := noise(pattern*0.018+vec2(7.0,13.0))*6.283185
 b := noise(pattern*0.023+vec2(31.0,3.0))*6.283185
 warp := vec2(0.5)+vec2(sin(t*0.65+a),sin(t*0.83+b))*0.5*rippleDeformation
 p += (warp-vec2(0.5))*1.6
 broad := noise(p)
 // The fine lattice is rotated 37 degrees about the map origin — a fixed
 // rotation, applied once, not a wind-following one — so its cell rows never
 // line up with the broad lattice and the pair stops reading as a grid.
 q := (pattern-drift*11.0)*0.166
 q = vec2(q.x*0.7986-q.y*0.6018,q.x*0.6018+q.y*0.7986)+(warp-vec2(0.5))*0.9
 fine := noise(q)
 ripple := broad*0.65+fine*0.35-0.5
 deep := smoothstep(0.05,0.55,mask.y)
 offset := vec2(broad-0.5,fine-0.5)*custom.y*(3.2+strength*2.4)*(0.7+0.5*gust)*deep*coverage
 sample := clamp(screen+offset,vec2(0.5),imageSrc0Size()-vec2(0.5))
 // Subpixel filtering prevents nearest-neighbour displacement from snapping.
 warped := terrainLinear(sample)
 shade := 1.0+(ripple*(0.112+strength*0.08)*(0.7+0.5*gust)*deep-0.05*strength*gust*deep)
 // Moving highlights make the surface readable even when the painted detail
 // is too fine to reveal displacement. Their broken shape follows both fields.
 crest := smoothstep(0.10,0.32,ripple)
 result := mix(warped.rgb*shade,vec3(0.40,0.67,0.78),clamp(custom.w*crest*(0.04+strength*0.04)*deep,0,1))
 // Shore foam keeps the original world-space pattern and clock. Surface
 // current, in-place deformation, size and opacity must not change its pace
 // or base opacity. The common soft edge still fades it at the shoreline.
 shore := (1.0-smoothstep(0.35,0.95,shoreDistance))*smoothstep(0.0,0.25,shoreDistance)
 shorePatch := noise(world*0.025)
 lap := pow(max(0.0,sin(shoreDistance*10.0+color.r*1.6+shorePatch*3.0)),2.0)
 foam := shore*lap*(0.09+color.a*0.105)*(0.50+0.50*shorePatch)*coverage*custom.z*(1.0-smoothstep(0.1,0.4,mask.z))
 // Shallow tint (§32): water lightens toward a pale cyan as the bottom rises.
 // It rises from nothing at the rounded coast the distance is measured from,
 // like every other term here, so the strict wet boundary — a staircase on a
 // steep beach — is never the edge of anything drawn.
 result = mix(result,vec3(0.62,0.80,0.84),clamp(custom.w*0.08*smoothstep(0.0,0.10,shoreDistance)*(1.0-smoothstep(0.10,0.40,shoreDistance)),0,1))
 effect := mix(base.rgb,min(result,vec3(base.a)),coverage)
 surface := clamp(mix(original.rgb,effect,surfaceOpacity),vec3(0),vec3(base.a))
 surface = mix(surface,vec3(0.72,0.84,0.87)*base.a,clamp(foam*shoreFoamOpacity*coverage,0,1))
 return vec4(surface,base.a)
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
  // Small white sprinkle quads lighten the existing terrain, clipped to
  // dry ground. Their size and drift come from the COB-driven recorder.
  coverage = smoothstep(0.8,1.0,mask.b)
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

// prepareWaterBed borrows only this replay's already-admitted ground sprites.
// Clear their frame pointers at replay end; keep just the reusable slice storage.
func (r *Renderer) prepareWaterBed(list *drawlist.List) {
	st := &r.water
	st.bedDrawn = false
	st.bedSprites = st.bedSprites[:0]
	if st.disabled {
		return
	}
	list.VisitSprites(func(sp drawlist.Sprite) {
		if sp.SubmergedGround && sp.Frame != nil &&
			(sp.Kind == drawlist.BlitFeatureNormal || sp.Kind == drawlist.BlitFeatureShadow) {
			st.bedSprites = append(st.bedSprites, sp)
		}
	})
}

func (r *Renderer) drawWaterBed(c drawlist.Terrain) {
	st := &r.water
	if st.disabled || !c.Water.Enabled || st.shader == nil || st.mask == nil || !st.visibleWater(c) {
		return
	}
	for _, sp := range st.bedSprites {
		sp.SubmergedGround = false
		r.Sprite(sp)
	}
	st.bedDrawn = true
}
