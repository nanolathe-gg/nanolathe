package gpurender

import (
	"fmt"
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

// groundKindScale is each family's share of groundLightGain at the TERRAIN
// receiver; model and smoke receivers keep the full colour they always had
// (§31.6, §31.7). Terrain is the receiver that reads as a shape — a pool on
// open ground is a circle the eye finds — so the families that stand for a
// small, moving, short-lived source are held well below the standing ones.
//
// Every value is an authored presentation choice, not retail arithmetic. Fire
// and spark were tuned AFTER the receiver height of §31.7 landed: measured from
// the sea datum a pool on high ground carried a large standing attenuation, and
// a share picked against that reads bleached once the attenuation is gone.
var groundKindScale = [lightKindCount]float32{
	lightExplosion:  0.75 / groundLightGain, // §31.6's peak, before its envelope
	lightNano:       1,
	lightFire:       0.3, // a burning place still lights the ground it stands on
	lightProjectile: 1,
	lightWreck:      1,
	lightSpark:      0.15, // a burning fragment lights almost nothing
}

// explosionGroundScale multiplies only the terrain receiver's emission.
// Authored presentation tuning: 0.75 peak gain instead of 2, two ticks of
// peak followed by squared decay to zero at twelve ticks (GPU design §31.6).
func explosionGroundScale(age float32, known bool) float32 {
	peak := groundKindScale[lightExplosion]
	if !known {
		return peak
	}
	if age < 0 || age >= 12 {
		return 0
	}
	tail := 1 - max(age-2, 0)/10
	return peak * tail * tail
}

// groundScale is the terrain receiver's whole per-source multiplier: the
// family's share, the explosion's own envelope, and the source's own remaining
// emission where its producer carried one (§31.7).
func groundScale(light *battleLight) float32 {
	// add() rejects any kind at or past lightKindCount, so the table index is
	// always in range and needs no bound of its own.
	scale := groundKindScale[light.kind]
	if light.kind == lightExplosion {
		// The explosion's share is the table's entry as well, taken through the
		// envelope §31.6 wraps around it.
		scale = explosionGroundScale(light.age, light.ageKnown)
	}
	if light.fadeKnown {
		scale *= light.fade
	}
	return scale
}

type groundLighting struct {
	shader  *ebiten.Shader
	verts   []ebiten.Vertex
	indices []uint32
	opts    ebiten.DrawTrianglesShaderOptions
	// read is the union of the discs' clipped quads. The shader samples the
	// fragment's own pixel and nothing else, so the quads are exactly the
	// region the copy has to carry: a couple of explosions no longer cost a
	// full-frame blit (readcopy.go).
	read readRect
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
	// Additive, so overlapping pools sum: the composite becomes
	// base × (1 + sum of the discs' contributions), clamped per channel.
	r.drawOverComposite(g.read, g.verts, g.indices, g.shader, &g.opts, ebiten.BlendLighter)
}

// appendGroundLights builds the frame's clipped discs. A light whose disc falls
// entirely outside the framebuffer contributes no geometry, so a battle beyond
// the viewport costs this pass nothing.
func (r *Renderer) appendGroundLights() {
	l := &r.lighting
	g := &r.ground
	g.verts, g.indices = g.verts[:0], g.indices[:0]
	g.read.reset()
	// The world transform of §16.3 applies exactly once, here, as it does for
	// the distortion batch: the lights are in record coordinates and the quads
	// this pass submits are device geometry.
	k := r.sched.txf(1)
	for i := range l.lights {
		light := &l.lights[i]
		gain := groundScale(light)
		if gain <= 0 {
			continue
		}
		// This pass overlays the painted terrain without a receiver height.
		// Centre its pool on the projected source, as the visible art is, rather
		// than treating the unsheared world row as a terrain pixel (SC20).
		// Physical model/smoke distances still use the unsheared source (§23.2).
		gx, gy := r.sched.txx(light.position[0]), r.sched.txy(light.position[1]-light.position[2]*0.5)
		// The source's height ABOVE THE GROUND under it. Measured from the sea
		// datum instead — as this pass had to before the producers carried a
		// receiver height — every pool narrower than the map's own elevation is
		// discarded outright by the reach test below, which is what suppressed
		// a spark's pool anywhere the ground rises (§31.5, §31.7).
		height := max(light.position[2]-light.ground, 0) * k
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
		g.read.add(x0, y0, x1, y1)
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
// Pure base × light is a coloured FILTER, though, and a filter amplifies
// whatever the surface already is: a warm fire over saturated grass multiplies
// the one channel that is already high, drives it into the clamp, and the pool
// reads as poison green rather than as firelight. The physical answer is that a
// lit surface returns the LIGHT's spectrum scaled by its own reflectance, so the
// fragment mixes the albedo product with luma(base) × light — the same quantity
// on a neutral surface, and the light's own hue on a coloured one. At
// groundHueMix the pool keeps the map's painted structure (luma varies pixel to
// pixel exactly as the albedo does) and stops inheriting its hue (§31.7).
//
// The falloff is the model faces' radial law of §23.2 with the square dropped:
// a face is a small target and wants a tight core, while a ground pool is read
// as a shape and wants a body. Squared, the pool was a bright point inside a
// wide invisible skirt; linear in d²/r² it carries light out to most of its
// radius and still reaches zero at the edge. Distance stays three-dimensional.
//
// That linear law reaches zero with a slope, and a brightening that stops at a
// slope is a rim: the eye reads the termination as the outline of a disc, which
// is what made a pool on open ground look like a drawn circle. Smoothstepping
// it — f²(3 − 2f) — lands at zero with zero slope at the rim and at full with
// zero slope at the core, so the pool ends in nothing at all while keeping the
// body the linear law was chosen for (§31.7). Two multiplies and a subtract.
// The source is BUILT from the Go constants rather than carrying literals that
// restate them. A coupling assertion can only constrain the side it names, so a
// pair of hand-written numbers lets the shader ship a value the design document
// does not describe; formatting them once at package init removes the second
// copy entirely. It is one allocation for the life of the process, not per
// frame, so §13's CPU/allocation policy is unaffected.
var groundLightShaderSource = fmt.Sprintf(groundLightShaderTemplate, groundHueMix, groundLightGain)

const groundLightShaderTemplate = `//kage:unit pixels
package main

func Fragment(dst vec4, src vec2, color vec4, light vec4) vec4 {
 p := dst.xy-imageDstOrigin()
 d := p-light.xy
 r := light.w
 d2 := dot(d,d)+light.z*light.z
 if d2 >= r*r { discard() }
 falloff := 1.0-d2/(r*r)
 falloff = falloff*falloff*(3.0-2.0*falloff)
 base := imageSrc0At(p+imageSrc0Origin()).rgb
 lit := mix(base, vec3(dot(base,vec3(0.299,0.587,0.114))), %[1]v)*color.rgb
 return vec4(min(lit*(falloff*%[2]v), vec3(1.0)-base), 0.0)
}
`

// groundHueMix is how far the fragment moves from the albedo filter toward the
// neutral-surface response: 0 is the pure base × light of §31.3, 1 discards the
// surface's hue entirely and keeps only its brightness. It is an artistic
// choice. Both it and the gain reach the shader through the template above
// rather than as a uniform: this pass compiles once and submits one batch, so a
// uniform map would allocate per frame for a constant (§13).
const groundHueMix = 0.75
