package gpurender

import (
	"fmt"
	"image"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Aircraft soft shadows are an authored Enhanced treatment, not retail optics
// (GPU design §34). Filtering covers only each aircraft's expanded rectangle.
// The treatment is always on in Enhanced: it softens a shadow the executor
// draws either way, so it is a quality of the existing shadow rather than an
// effect family with a switch of its own (§30, §34). Ground units, structures,
// clipped silhouettes and Original keep the ordinary silhouette route.
type aircraftShadowLayer struct {
	shader *ebiten.Shader
	// water is the retained uniform storage for the frame's water phase,
	// integrated wind drift and mask step. One terrain command a frame sets
	// them, so they are the same for every aircraft in the frame and need no
	// vertex lane of their own; the four lanes they would have taken carry each
	// aircraft's own rectangle on the atlas page instead. The slice is written
	// in place rather than reboxed into an interface, as the water reflections'
	// retained uniforms are (§13 "CPU/allocation policy").
	water    []float32
	uniforms map[string]any
}

// aircraftShadowIndices is the layer's one quad, shared by every aircraft: the
// scheduler copies indices into its own batch storage and keeps nothing of the
// caller's, so one package value serves the whole frame.
var aircraftShadowIndices = [6]uint32{0, 1, 2, 1, 3, 2}

func newAircraftShadowShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(aircraftShadowSource))
}

func aircraftShadowRadius(height, scale float32) float32 {
	if scale <= 0 {
		return 0
	}
	clearance := max(height/scale, 0)
	// Keep the reviewed low-flight curve, then add 1.5 world pixels smoothly
	// over the upper altitude band (Enhanced design §34).
	t := min(max((clearance-120)/80, 0), 1)
	return (min(clearance/60, 3) + 1.5*t*t*(3-2*t)) * scale
}

func (r *Renderer) commitAircraftShadow(g *drawlist.ModelGeometry, body modelDirectRegion) bool {
	st := &r.aircraftShadow
	if st.shader == nil || g.AircraftShadowHeight <= 0 || g.AircraftShadowScale <= 0 || g.Shadow.SilhouetteClip != 0 {
		return false
	}
	scale := g.AircraftShadowScale
	radius := aircraftShadowRadius(g.AircraftShadowHeight, scale)
	// The whole page binds and the silhouette's rectangle on it rides the custom
	// lanes, which is how the projected shadow commit already samples a page
	// (model_direct.go). The fragment clamps its own taps to that rectangle, so
	// every tap outside this subject stays transparent, adjacent atlas slots
	// included. A sub-image view gave that bound for free, but Ebitengine
	// allocates and caches one image per distinct rectangle — a moving aircraft
	// asks for a new rectangle every frame — and a distinct image splits the
	// batch, so each aircraft also cost a device draw of its own (§34).
	bb := body.bounds
	rect := [4]float32{float32(body.x), float32(body.y),
		float32(body.x) + 2*float32(bb.Dx()), float32(body.y) + 2*float32(bb.Dy())}
	sb := modelWorldBounds(g.Shadow)
	margin := int(math.Ceil(float64(radius + 2*scale + 1)))
	expanded := sb.Inset(-margin).Intersect(image.Rect(0, 0, r.clipW(), r.clipH()))
	if expanded.Empty() {
		return true
	}
	var mask *ebiten.Image
	water := &r.water
	step, phase, driftX, driftZ := float32(0), float32(0), float32(0), float32(0)
	if water.source != nil && water.source == water.record.Terrain && water.mask != nil {
		mask, step = water.mask, float32(water.step)
		if water.record.Water.Enabled && !water.disabled {
			phase = (float32(water.record.Water.Tick) + float32(water.record.Water.Fraction16)/65536) / 30
			driftX, driftZ = water.record.Water.DriftX, water.record.Water.DriftZ
		}
	}
	if !r.sched.beginBlended(schedDest, expanded.Min.X, expanded.Min.Y, expanded.Max.X, expanded.Max.Y,
		[4]*ebiten.Image{r.modelDirect.pages[body.page].colour, mask, r.tables.atlas}, st.shader, blendComposite, schedReadNone) {
		return true
	}
	// The scheduler's runs share one options value, so the frame's water
	// parameters are installed on it here, before the segment is submitted. The
	// frame has one terrain command, so every aircraft compiled into a
	// submission was compiled against the values this writes.
	if st.uniforms == nil {
		st.water = make([]float32, 4)
		st.uniforms = map[string]any{"AircraftWater": st.water}
	}
	st.water[0], st.water[1], st.water[2], st.water[3] = phase, driftX, driftZ, step
	r.sceneOpts.Uniforms = st.uniforms
	wb := modelWorldBounds(g)
	rx := float32(body.x) + 2*float32(wb.Min.X-bb.Min.X)
	ry := float32(body.y) + 2*float32(wb.Min.Y-bb.Min.Y)
	terrainScale := float32(water.record.Scale.Float())
	if terrainScale <= 0 {
		terrainScale = scale
	}
	var vertices [4]ebiten.Vertex
	xs := [4]int{expanded.Min.X, expanded.Max.X, expanded.Min.X, expanded.Max.X}
	ys := [4]int{expanded.Min.Y, expanded.Min.Y, expanded.Max.Y, expanded.Max.Y}
	for i := range vertices {
		x, y := float32(xs[i]), float32(ys[i])
		vertices[i] = ebiten.Vertex{DstX: x, DstY: y,
			SrcX: rx + 2*(x-float32(sb.Min.X)), SrcY: ry + 2*(y-float32(sb.Min.Y)),
			ColorR: float32(water.record.OriginX) + x/terrainScale, ColorG: float32(water.record.OriginY) + y/terrainScale,
			ColorB: radius, ColorA: scale,
			Custom0: rect[0], Custom1: rect[1], Custom2: rect[2], Custom3: rect[3]}
	}
	r.sched.tris(schedDest, vertices[:], aircraftShadowIndices[:])
	return true
}

var aircraftShadowSource = `//kage:unit pixels
package main

// The frame's water phase, integrated wind drift and mask step. They are frame
// constants, so they ride a uniform and leave the custom lanes to each
// aircraft's own rectangle on the atlas page.
var AircraftWater vec4

// tap reads one texel centre of the body raster, transparent outside the
// subject's own rectangle lo..hi on the page. That is the bound a sub-image
// view used to apply, on the same half-open test the view applied.
func tap(p vec2, lo vec2, hi vec2) float {
 in := step(lo,p)-step(hi,p)
 return imageSrc0At(p).a*in.x*in.y
}
func coverage(p vec2, lo vec2, hi vec2) float {
 q := p-lo-vec2(0.5)
 a := floor(q)+lo+vec2(0.5)
 f := fract(q)
 return mix(mix(tap(a,lo,hi),tap(a+vec2(1,0),lo,hi),f.x),mix(tap(a+vec2(0,1),lo,hi),tap(a+vec2(1,1),lo,hi),f.x),f.y)
}
func wet(p vec2) float {
 q := p-vec2(0.5)
 a := floor(q)+imageSrc0Origin()+vec2(0.5)
 f := fract(q)
 return mix(mix(imageSrc1AtFromSrc0Pos(a).r,imageSrc1AtFromSrc0Pos(a+vec2(1,0)).r,f.x),mix(imageSrc1AtFromSrc0Pos(a+vec2(0,1)).r,imageSrc1AtFromSrc0Pos(a+vec2(1,1)).r,f.x),f.y)
}
func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 o := imageSrc0Origin()
 lo := o+custom.xy
 hi := o+custom.zw
 world := color.rg
 water := 0.0
 if AircraftWater.w>0.0 { water = smoothstep(0.8,1.0,wet(world/AircraftWater.w)) }
 // World-anchored waves share the committed phase and integrated wind drift
 // used by the water and its reflections. Their bounded reach fits the quad.
 t := AircraftWater.x
 p := world-AircraftWater.yz*6.0
 warp := vec2(sin(p.y*0.14+t*0.8)+0.35*sin(p.x*0.09-t*0.55),0.45*sin((p.x+p.y)*0.11+t*0.65))
 src += warp*(water*color.a*2.0)
 radius := color.b+0.5*water*color.a
 // Normalized binomial filter. The 2x body raster supplies subpixel edges;
 // bounded bilinear taps keep narrow wings connected as the radius grows.
 weights := [5]float{1,4,6,4,1}
 sum := 0.0
 for y:=0; y<5; y++ {
  for x:=0; x<5; x++ {
   sum += coverage(src+vec2(float(x-2),float(y-2))*radius,lo,hi)*weights[x]*weights[y]
  }
 }
 alpha := sum/256.0*mix(0.5,0.20,water)
 tint := imageSrc2AtFromSrc0Pos(o+vec2(0.5,` + fmt.Sprint(tableRowPAL) + `.5)).rgb
 return vec4(tint*alpha,alpha)
}
`
