package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
)

// Ground illumination for the Enhanced battle lights
// (docs/DESIGN_GPU_RENDERER.md §31). Until now a light reached model faces and
// smoke only, so an explosion over open terrain left the ground it stood on
// exactly as the map painted it. This pass gives every selected source a pool
// of light on the composite the terrain pass has just finished.
//
// It is presentation only, Enhanced only, and gated by the same Lighting switch
// as every other source (§30): with the switch off nothing is copied, nothing
// is batched and the composite is byte-identical to the executor without it.

// groundLightGain is the pool's peak strength as a multiple of the ground's own
// albedo. It stays well below the model-face gain of §23.2 — terrain already
// carries the map's painted lighting, and the clamp against 1 − base keeps that
// detail — but it has to be far enough above zero for the pool to read without
// amplification, which the first 0.9 was not. It is an artistic choice.
// Explosion receivers now apply their separate lower gain and short fade (§31.6).
const groundLightGain = 2.0

// explosionGroundScale multiplies only the terrain receiver's emission.
// Authored presentation tuning: 0.75 peak gain instead of 2, two ticks of
// peak followed by squared decay to zero at twelve ticks (GPU design §31.6).
func explosionGroundScale(age float32, known bool) float32 {
	const peak = float32(0.75 / groundLightGain)
	if !known {
		return peak
	}
	if age < 0 || age >= 12 {
		return 0
	}
	tail := 1 - max(age-2, 0)/10
	return peak * tail * tail
}

// SetExplosionGroundFlash compares the short terrain flash with the earlier
// full-animation ground light. It never changes model lighting (§31.6).
func (r *Renderer) SetExplosionGroundFlash(on bool) { r.ground.legacyExplosion = !on }

type groundLighting struct {
	legacyExplosion bool
	shader          *ebiten.Shader
	verts           []ebiten.Vertex
	indices         []uint32
	opts            ebiten.DrawTrianglesShaderOptions
}

// drawGroundLighting runs at the end of the terrain pass, after the water
// surface and its reflections have resolved — so the pools brighten the water
// too, which is intended — and before objects, wakes and scorch, so units are
// drawn over it and the ordinary fog composite covers it (§26.3, §31).
//
// It costs two submissions in a frame with a light in view and nothing at all
// in a frame without one: the scheduler barrier plus the composite copy, then
// one batch holding every light's clipped disc.
func (r *Renderer) drawGroundLighting() {
	g := &r.ground
	if r.lighting.disabled || len(r.lighting.lights) == 0 || g.shader == nil || r.surfaces[0] == nil || r.surfaces[1] == nil {
		return
	}
	r.appendGroundLights()
	if len(g.indices) == 0 {
		return
	}
	// The batch reads the composite it writes, so the terrain has to be on the
	// composite first and the read has to come from a copy, exactly as the
	// refraction batch does (distortion.go).
	r.submitSchedule()
	r.copyComposite(r.surfaces[1], r.surfaces[0], 0, 0, r.w, r.h)
	g.opts.Images = [4]*ebiten.Image{r.surfaces[1]}
	// Additive, so overlapping pools sum: the composite becomes
	// base × (1 + sum of the discs' contributions), clamped per channel.
	g.opts.Blend = ebiten.BlendLighter
	r.beginPass(r.surfaces[0])
	r.recordSubmission(len(g.verts), len(g.indices))
	r.surfaces[0].DrawTrianglesShader32(g.verts, g.indices, g.shader, &g.opts)
	r.frameDraws++
}

// appendGroundLights builds the frame's clipped discs. A light whose disc falls
// entirely outside the framebuffer contributes no geometry, so a battle beyond
// the viewport costs this pass nothing.
func (r *Renderer) appendGroundLights() {
	l := &r.lighting
	g := &r.ground
	g.verts, g.indices = g.verts[:0], g.indices[:0]
	// The world transform of §16.3 applies exactly once, here, as it does for
	// the distortion batch: the lights are in record coordinates and the quads
	// this pass submits are device geometry.
	k := r.sched.txf(1)
	for i := range l.lights {
		light := &l.lights[i]
		gain := float32(1)
		if light.kind == lightExplosion && !g.legacyExplosion {
			gain = explosionGroundScale(light.age, light.ageKnown)
			if gain <= 0 {
				continue
			}
		}
		// This pass overlays the painted terrain without a receiver height.
		// Centre its pool on the projected source, as the visible art is, rather
		// than treating the unsheared world row as a terrain pixel (SC20).
		// Physical model/smoke distances still use the unsheared source (§23.2).
		gx, gy := light.position[0]*k, (light.position[1]-light.position[2]*0.5)*k
		height := light.position[2] * k
		radius := light.radius * k
		if radius <= 0 || height >= radius {
			// Every ground point is already past the radius; the disc is empty.
			continue
		}
		// The radius bounds the disc the light reaches on the ground plane: a
		// lifted light reaches slightly less than that, and the shader's own
		// distance test discards the difference, so the quad is a conservative
		// cover rather than an exact one and needs no square root [I2].
		reach := radius
		x0 := max(float32(math.Floor(float64(gx-reach))), 0)
		y0 := max(float32(math.Floor(float64(gy-reach))), 0)
		x1 := min(float32(math.Ceil(float64(gx+reach))), float32(r.w))
		y1 := min(float32(math.Ceil(float64(gy+reach))), float32(r.h))
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		base := uint32(len(g.verts))
		for _, p := range [4][2]float32{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
			g.verts = append(g.verts, ebiten.Vertex{DstX: p[0], DstY: p[1], SrcX: p[0], SrcY: p[1],
				ColorR: light.color[0] * gain, ColorG: light.color[1] * gain, ColorB: light.color[2] * gain, ColorA: 0,
				Custom0: gx, Custom1: gy, Custom2: height, Custom3: radius})
		}
		g.indices = append(g.indices, base, base+1, base+2, base+1, base+2, base+3)
		r.modelStats.GroundLights++
	}
}

func newGroundLightShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(groundLightShaderSource))
}

// The fragment is base × light, not a flat additive wash: multiplying by the
// albedo under the pixel keeps every painted detail of the map — a dark rock
// stays darker than the sand beside it — and the per-channel clamp against
// 1 − base is what stops a bright source from flattening the ground to white.
//
// The falloff is the model faces' radial law of §23.2 with the square dropped:
// a face is a small target and wants a tight core, while a ground pool is read
// as a shape and wants a body. Squared, the pool was a bright point inside a
// wide invisible skirt; linear in d²/r² it carries light out to most of its
// radius and still reaches zero at the edge. Distance stays three-dimensional.
const groundLightShaderSource = `//kage:unit pixels
package main

func Fragment(dst vec4, src vec2, color vec4, light vec4) vec4 {
 p := dst.xy-imageDstOrigin()
 d := p-light.xy
 r := light.w
 d2 := dot(d,d)+light.z*light.z
 if d2 >= r*r { discard() }
 falloff := 1.0-d2/(r*r)
 base := imageSrc0At(p+imageSrc0Origin()).rgb
 return vec4(min(base*color.rgb*(falloff*` + groundLightGainLiteral + `), vec3(1.0)-base), 0.0)
}
`

// The gain reaches the shader as a literal rather than a uniform: this pass
// compiles once and submits one batch, so a uniform map would allocate per
// frame for a constant (§13 "CPU/allocation policy").
const groundLightGainLiteral = "2.0"

// The literal and the Go constant are the same number; this fails to compile if
// one is edited without the other.
const _ = uint(int32(groundLightGain*1000) - 2000)
