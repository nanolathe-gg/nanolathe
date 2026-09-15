package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Authored opening choreography, not retail behavior (GPU design §36). Only
// the completed, fogged scene is sampled; the shared read surface (§25) means
// this short-lived composite owns no images and leaves no steady-state pass.
type arrivalLayer struct {
	shader   *ebiten.Shader
	packet   drawlist.Arrival
	resolved bool
	clip     [4]float32
	params   [8]float32
	verts    [4]ebiten.Vertex
	opts     ebiten.DrawTrianglesShaderOptions
}

func (r *Renderer) prepareArrival(w drawlist.WorldSpace) {
	a := w.Arrival
	r.arrival.packet = drawlist.Arrival{}
	r.arrival.resolved = false
	if !a.Active || !(a.Seconds >= 0 && a.Seconds < drawlist.ArrivalDurationSeconds) || !(a.Scale > 0) {
		return
	}
	// Positions receive the single §16 affine transform; lengths receive only
	// its scale. The viewport was already recorded in framebuffer pixels.
	a.X, a.Y = r.sched.txx(a.X), r.sched.txy(a.Y)
	a.GridX, a.GridY = r.sched.txx(a.GridX), r.sched.txy(a.GridY)
	a.Scale = r.sched.txf(a.Scale)
	if !(a.Scale > 0) {
		return
	}
	c := [4]float32{0, 0, float32(r.w), float32(r.h)}
	if w.Viewport.W > 0 && w.Viewport.H > 0 {
		c = [4]float32{max(0, float32(w.Viewport.X)), max(0, float32(w.Viewport.Y)),
			min(float32(r.w), float32(w.Viewport.X+w.Viewport.W)),
			min(float32(r.h), float32(w.Viewport.Y+w.Viewport.H))}
	}
	if c[0] >= c[2] || c[1] >= c[3] {
		return
	}
	r.arrival.packet, r.arrival.clip = a, c
}

func (r *Renderer) resolveArrival() {
	a := &r.arrival
	if !a.packet.Active || a.resolved || a.shader == nil || r.surfaces[0] == nil {
		return
	}
	p, c := a.packet, a.clip
	a.params = [8]float32{p.X, p.Y, p.GridX, p.GridY, p.Seconds, p.Scale,
		drawlist.ArrivalImpactSeconds, drawlist.ArrivalDurationSeconds}
	if a.opts.Uniforms == nil {
		a.opts.Uniforms = map[string]any{"Arrival": a.params[:], "Clip": a.clip[:]}
	}
	for i, xy := range [4][2]float32{{c[0], c[1]}, {c[2], c[1]}, {c[0], c[3]}, {c[2], c[3]}} {
		a.verts[i] = ebiten.Vertex{DstX: xy[0], DstY: xy[1], SrcX: xy[0], SrcY: xy[1], ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1}
	}
	var rr readRect
	rr.add(c[0], c[1], c[2], c[3])
	r.drawOverComposite(rr, a.verts[:], r.copyIdx[:], a.shader, &a.opts, ebiten.BlendCopy)
	// A second World End must not composite the arrival again.
	a.resolved = true
}

func newArrivalShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(arrivalShaderSource))
}

const arrivalShaderSource = `//kage:unit pixels
package main

var Arrival [8]float
var Clip vec4

// Sampling stays inside the copied viewport, including every bilinear tap.
func arrivalAt(p vec2) vec4 {
	p = clamp(p, Clip.xy+vec2(0.5), Clip.zw-vec2(0.5))
	return imageSrc0At(p + imageSrc0Origin())
}

func arrivalLinear(p vec2) vec4 {
	b := floor(p-vec2(0.5))+vec2(0.5)
	f := fract(p-vec2(0.5))
	return mix(mix(arrivalAt(b), arrivalAt(b+vec2(1,0)), f.x),
		mix(arrivalAt(b+vec2(0,1)), arrivalAt(b+vec2(1,1)), f.x), f.y)
}

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	p := src-imageSrc0Origin()
	base := imageSrc0At(src)
	// Never move lit scene pixels into black unknown fog, or illuminate it.
	visibility := smoothstep(0.003, 0.055, max(base.r, max(base.g, base.b)))
	if visibility == 0 {
		return base
	}
	center := vec2(Arrival[0], Arrival[1])
	grid := vec2(Arrival[2], Arrival[3])
	t, scale, impact, duration := Arrival[4], Arrival[5], Arrival[6], Arrival[7]
	if t >= duration-0.05 {
		return base
	}
	delta := (p-center)/scale
	d := length(delta)
	cyan := vec3(0.42, 0.85, 1)
	if t < impact {
		// The vertical corridor keeps the descending commander legible against
		// the dim map. The soft edges never cut rectangular holes in the scene.
		column := exp(-delta.x*delta.x/450.0)*(1-smoothstep(2, 34, delta.y))
		core := exp(-delta.x*delta.x/18.0)*(1-smoothstep(0, 18, delta.y))
		charge := smoothstep(0, impact, t)
		glow := (column*0.07+core*0.14)*charge
		foot := exp(-d*d/450.0)*0.12*charge
		rgb := base.rgb*(0.10+column*0.90)+cyan*(glow+foot)*visibility*base.a
		return vec4(min(rgb, vec3(base.a)), base.a)
	}
	// Use the farthest viewport corner to finish the outward wave before the
	// final settling interval, including origins displaced by camera movement.
	far := max(max(length(Clip.xy-center), length(Clip.zw-center)),
		max(length(vec2(Clip.x, Clip.w)-center), length(vec2(Clip.z, Clip.y)-center)))/scale
	span := duration-impact
	elapsed := t-impact
	speed := max(far+48, 240)/(span*0.66)
	radius := elapsed*speed
	cell := floor((p-grid)/(32*scale))
	cellCenter := grid+(cell+vec2(0.5))*32*scale
	cellDistance := length((cellCenter-center)/scale)
	local := elapsed-cellDistance/speed
	reveal := smoothstep(-0.035, 0.26, local)
	// A small vertical inverse warp makes the complete chunk (trees, water,
	// terrain) rise and settle. Sampling a complete scene avoids exposed seams.
	u := clamp(local/0.52, 0, 1)
	lift := sin(u*3.14159265)*3.4*(1-u)*scale
	band := (d-radius)/15
	ring := exp(-band*band)
	fade := 1-smoothstep(span*0.68, span*0.96, elapsed)
	direction := delta/max(d, 1)
	shift := direction*ring*1.5*scale*fade
	sample := arrivalLinear(p+vec2(0,lift)+shift)
	// Keep the commander neighborhood readable through the impact, while the
	// cell front restores the rest of the map in expanding rings.
	near := exp(-d*d/1800.0)*(1-smoothstep(0, 0.35, elapsed))
	light := max(reveal, near)
	rgb := sample.rgb*(0.10+0.90*light)
	flash := exp(-d*d/1600.0)*exp(-elapsed*11)*0.75
	rim := ring*0.25*fade*smoothstep(0, 0.05, elapsed)
	rgb += cyan*(flash+rim)*visibility*base.a
	return vec4(min(rgb, vec3(base.a)), base.a)
}
`
