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
type aircraftShadowLayer struct {
	shader   *ebiten.Shader
	disabled bool
}

// SetAircraftShadows provides a capture comparison control.
func (r *Renderer) SetAircraftShadows(on bool) { r.aircraftShadow.disabled = !on }

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
	if st.disabled || st.shader == nil || g.AircraftShadowHeight <= 0 || g.AircraftShadowScale <= 0 || g.Shadow.SilhouetteClip != 0 {
		return false
	}
	scale := g.AircraftShadowScale
	radius := aircraftShadowRadius(g.AircraftShadowHeight, scale)
	// A subimage is a view of the existing body raster. Its bounds make every
	// filter tap outside this subject transparent, including adjacent atlas slots.
	bb := body.bounds
	rect := image.Rect(int(body.x), int(body.y), int(body.x)+2*bb.Dx(), int(body.y)+2*bb.Dy())
	silhouette := r.modelDirect.pages[body.page].colour.SubImage(rect).(*ebiten.Image)
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
		[4]*ebiten.Image{silhouette, mask, r.tables.atlas}, st.shader, blendComposite, schedReadNone) {
		return true
	}
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
			Custom0: phase, Custom1: driftX, Custom2: driftZ, Custom3: step}
	}
	r.sched.tris(schedDest, vertices[:], []uint32{0, 1, 2, 1, 3, 2})
	return true
}

var aircraftShadowSource = `//kage:unit pixels
package main

func coverage(p vec2) float {
 o := imageSrc0Origin()
 q := p-o-vec2(0.5)
 a := floor(q)+o+vec2(0.5)
 f := fract(q)
 return mix(mix(imageSrc0At(a).a,imageSrc0At(a+vec2(1,0)).a,f.x),mix(imageSrc0At(a+vec2(0,1)).a,imageSrc0At(a+vec2(1,1)).a,f.x),f.y)
}
func wet(p vec2) float {
 q := p-vec2(0.5)
 a := floor(q)+imageSrc0Origin()+vec2(0.5)
 f := fract(q)
 return mix(mix(imageSrc1AtFromSrc0Pos(a).r,imageSrc1AtFromSrc0Pos(a+vec2(1,0)).r,f.x),mix(imageSrc1AtFromSrc0Pos(a+vec2(0,1)).r,imageSrc1AtFromSrc0Pos(a+vec2(1,1)).r,f.x),f.y)
}
func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 world := color.rg
 water := 0.0
 if custom.w>0.0 { water = smoothstep(0.8,1.0,wet(world/custom.w)) }
 // World-anchored waves share the committed phase and integrated wind drift
 // used by the water and its reflections. Their bounded reach fits the quad.
 t := custom.x
 p := world-custom.yz*6.0
 warp := vec2(sin(p.y*0.14+t*0.8)+0.35*sin(p.x*0.09-t*0.55),0.45*sin((p.x+p.y)*0.11+t*0.65))
 src += warp*(water*color.a*2.0)
 radius := color.b+0.5*water*color.a
 // Normalized binomial filter. The 2x body raster supplies subpixel edges;
 // bounded bilinear taps keep narrow wings connected as the radius grows.
 weights := [5]float{1,4,6,4,1}
 sum := 0.0
 for y:=0; y<5; y++ {
  for x:=0; x<5; x++ {
   sum += coverage(src+vec2(float(x-2),float(y-2))*radius)*weights[x]*weights[y]
  }
 }
 alpha := sum/256.0*mix(0.5,0.20,water)
 tint := imageSrc2AtFromSrc0Pos(imageSrc0Origin()+vec2(0.5,` + fmt.Sprint(tableRowPAL) + `.5)).rgb
 return vec4(tint*alpha,alpha)
}
`
