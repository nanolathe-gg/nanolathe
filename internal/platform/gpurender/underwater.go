package gpurender

import (
	"fmt"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Underwater refraction is an authored Enhanced treatment, not retail optics
// (GPU design §26.5). A subject under the blue waterline tint [03 R-WATER-01 §2]
// commits through this shader instead of the scene shader's plain resolve:
// the texels the colour pass marked submerged are refracted and shaded by the
// same water field the water pass draws around them (water_field.go), so a
// hull below the surface moves with the water instead of sitting on top of it.
// Above-water texels resolve exactly as the ordinary commit does. The Water
// switch gates it with the rest of the water treatment (§30).
type underwaterLayer struct {
	water []float32
}

// underwaterMaxOffset bounds waterOffset in world pixels: |broad-0.5| ≤ 0.5,
// (3.2 + 0.5×2.4) = 4.4 and the gust factor ≤ 1.2, so 0.5 × 4.4 × 1.2.
const underwaterMaxOffset = 2.64

// modelRefraction scales the water's displacement for hulls, whose sharp
// silhouettes read the field far more strongly than painted terrain does.
// Selected presentation value (§26.5).
const modelRefraction = 0.5

var underwaterIndices = [6]uint32{0, 1, 2, 1, 3, 2}

// sceneWaterUniforms is the one uniform map the scene runs share: the
// scheduler installs a single options value, so every custom shader that
// reads a frame water uniform must find its key in the same map.
func (r *Renderer) sceneWaterUniforms() map[string]any {
	st := &r.aircraftShadow
	if st.uniforms == nil {
		st.water = make([]float32, 4)
		r.underwater.water = make([]float32, 4)
		st.uniforms = map[string]any{"AircraftWater": st.water, "UnderwaterWater": r.underwater.water}
	}
	return st.uniforms
}

// commitUnderwater commits one blue-waterline subject through the underwater
// shader. It reports false when the treatment does not apply, and the caller
// takes the ordinary commit.
func (r *Renderer) commitUnderwater(g *drawlist.ModelGeometry, region modelDirectRegion) bool {
	st := &r.underwater
	water := &r.water
	if g.Waterline != drawlist.ModelWaterlineBlue ||
		g.WreckEmission != [3]float32{} || water.disabled || !water.record.Water.Enabled ||
		water.mask == nil || water.source == nil || water.source.LavaWorld || water.source != water.record.Terrain {
		return false
	}
	scale := float32(water.record.Scale.Float())
	if scale <= 0 {
		return false
	}
	b := region.bounds
	pad := int(math.Ceil(float64(underwaterMaxOffset*modelRefraction*scale))) + 1
	x0, y0 := max(b.Min.X-pad, 0), max(b.Min.Y-pad, 0)
	x1, y1 := min(b.Max.X+pad, r.clipW()), min(b.Max.Y+pad, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return true
	}
	page := r.modelDirect.pages[region.page].colour
	// The scene shader's own op, the page in the ordinary commit's slot and the
	// mask in slot 3, which no model command binds, so the subject joins the
	// open opaque run instead of splitting it.
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{2: page, 3: water.mask}) {
		return true
	}
	r.sceneOpts.Uniforms = r.sceneWaterUniforms()
	w := water.record.Water
	st.water[0] = (float32(w.Tick) + float32(w.Fraction16)/65536) / 30
	st.water[1], st.water[2], st.water[3] = w.TidalDriftX, w.TidalDriftZ, float32(water.step)
	opacity := float32(1)
	if g.Cloaked {
		opacity = 0.5
	}
	rx, ry := float32(region.x), float32(region.y)
	// The subject's page rectangle, packed x·4096 + y: its origin and its
	// size, both below 4,096 and so exact in a float lane.
	lo := rx*4096 + ry
	size := float32(2*b.Dx())*4096 + float32(2*b.Dy())
	ox, oy := float32(water.record.OriginX), float32(water.record.OriginY)
	// World coordinates are affine in the record pixel, so the corners carry
	// them and interpolation supplies every fragment's; tris appends to the run
	// begin just selected.
	var vertices [4]ebiten.Vertex
	xs := [4]int{x0, x1, x0, x1}
	ys := [4]int{y0, y0, y1, y1}
	for i := range vertices {
		x, y := float32(xs[i]), float32(ys[i])
		vertices[i] = ebiten.Vertex{DstX: x, DstY: y,
			SrcX: rx + 2*(x-float32(b.Min.X)), SrcY: ry + 2*(y-float32(b.Min.Y)),
			ColorR: ox + x/scale, ColorG: oy + y/scale, ColorB: 2 * scale, ColorA: opacity,
			Custom0: lo, Custom1: size, Custom3: sceneOpUnderwaterCommit}
	}
	r.sched.tris(schedOpaque, vertices[:], underwaterIndices[:])
	r.modelStats.UnderwaterCommits++
	return true
}

// underwaterCommitSource is spliced into the scene shader (scene2DShaderSource).
// Lanes: colour RG the world map pixel, B page texels per world pixel, A the
// opacity; Custom0 the page rectangle's origin and Custom1 its size, packed.
var underwaterCommitSource = `
// The frame's water phase, tidal drift and mask step.
var UnderwaterWater vec4

const edgeFadePixels = 11.2

const modelRefraction = ` + fmt.Sprint(modelRefraction) + `
` + waterFieldSource + `
// uwTexel reads one page texel centre, transparent outside the subject's own
// rectangle so neighbouring atlas slots never leak in.
func uwTexel(p vec2, lo vec2, hi vec2) vec4 {
 in := step(lo,p)-step(hi,p)
 return imageSrc2AtFromSrc0Pos(imageSrc0Origin()+p)*in.x*in.y
}

func uwWetMask(p vec2) vec4 {
 q := p-vec2(0.5)
 a := floor(q)+imageSrc0Origin()+vec2(0.5)
 f := fract(q)
 return mix(mix(imageSrc3AtFromSrc0Pos(a),imageSrc3AtFromSrc0Pos(a+vec2(1,0)),f.x),mix(imageSrc3AtFromSrc0Pos(a+vec2(0,1)),imageSrc3AtFromSrc0Pos(a+vec2(1,1)),f.x),f.y)
}

func underwaterCommit(srcPos vec2, color vec4, custom vec4) vec4 {
 src := srcPos-imageSrc0Origin()
 lo := vec2(floor(custom.x/4096.0), custom.x-floor(custom.x/4096.0)*4096.0)
 hi := lo+vec2(floor(custom.y/4096.0), custom.y-floor(custom.y/4096.0)*4096.0)
 // Above-water texels resolve in place, as the ordinary commit does.
 start := floor(src-vec2(0.5))
 aboveSum := vec3(0)
 aboveCover := 0.0
 for j := 0; j < 2; j++ {
  for i := 0; i < 2; i++ {
   c := uwTexel(start+vec2(float(i)+0.5,float(j)+0.5),lo,hi)
   if c.a > 0.999 {
    aboveSum += c.rgb
    aboveCover += 1.0
   }
  }
 }
 // Submerged texels are gathered from the refracted position, with the same
 // field, depth ramp and coast fade the water pass applies to the terrain.
 world := color.rg
 mask := uwWetMask(world/UnderwaterWater.w)
 coverage := smoothstep(0.8,1.0,mask.x)*smoothstep(0.0,edgeFadePixels/32.0,mask.y)
 deep := smoothstep(0.05,0.55,mask.y)
 broad, fine, gust := waterField(world/patternSize, UnderwaterWater.yz*currentScale, UnderwaterWater.x)
 off := waterOffset(broad, fine, gust)*deep*coverage*color.b*modelRefraction
 // The pixel's two-texel box at the displaced centre, area weighted over the
 // three texels it straddles on each axis, so the hull slides smoothly
 // rather than jumping a whole texel at a time.
 corner := src+off-vec2(1.0)
 start = floor(corner)
 f := corner-start
 wx := [3]float{1.0-f.x,1.0,f.x}
 wy := [3]float{1.0-f.y,1.0,f.y}
 subSum := vec3(0)
 subCover := 0.0
 for j := 0; j < 3; j++ {
  for i := 0; i < 3; i++ {
   c := uwTexel(start+vec2(float(i)+0.5,float(j)+0.5),lo,hi)
   if c.a > 0.5 && c.a <= 0.999 {
    w := wx[i]*wy[j]
    subSum += c.rgb*w
    subCover += w
   }
  }
 }
 if aboveCover == 0.0 && subCover <= 0.0 {
  return vec4(0)
 }
 sub := subSum/max(subCover,1e-4)
 surface := mix(sub,mix(sub,waterShade(sub,broad,fine,gust,deep,1.0),coverage),surfaceOpacity)
 fa := aboveCover/4.0
 fs := (1.0-fa)*subCover/4.0
 return vec4(aboveSum/4.0+surface*fs,fa+fs)*color.a
}
`
