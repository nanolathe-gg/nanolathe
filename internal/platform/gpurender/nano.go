package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Enhanced artistic choices, not retail constants (GPU design §23.5).
const (
	nanoClusterReach   = 24
	nanoLightRadius    = 80
	nanoParticleEnergy = 0.06
	nanoClusterEnergy  = 0.7
	nanoGlowGain       = 0.45
)

type nanoLightCluster struct {
	light battleLight
	count int
}

func nanoScale(f drawlist.Fill) float32 {
	if f.LightingScale > 0 {
		return f.LightingScale
	}
	return 1
}

// Only a particle whose core intersects the recorded viewport may emit.
// The producer already admitted its committed world position through LOS.
func (r *Renderer) nanoInView(f drawlist.Fill) bool {
	clip := f.Clip
	if clip.W <= 0 || clip.H <= 0 {
		clip = drawlist.Rect{W: int32(r.clipW()), H: int32(r.clipH())}
	}
	return f.Rect.W > 0 && f.Rect.H > 0 && f.Rect.X+f.Rect.W > clip.X && f.Rect.Y+f.Rect.H > clip.Y && f.Rect.X < clip.X+clip.W && f.Rect.Y < clip.Y+clip.H
}

// prepareNanoLighting groups the spray in physical space, with fixed scratch
// storage. A group follows its particles' mean position, preserving camera
// translation and recording scale without screen-grid snapping. Broad illumination
// uses the mean palette ramp: the looping shimmer stays on the tiny particle
// cores and their glow, instead of flashing the entire factory (GPU design §23.5).
func (r *Renderer) prepareNanoLighting(list *drawlist.List) {
	l := &r.lighting
	l.nanoCount = 0
	color := nanoLightColor(&r.displayPalette)
	list.VisitNanoSources(func(f drawlist.Fill) {
		if f.NanoSubmerged || !r.nanoInView(f) {
			return
		}
		scale := nanoScale(f)
		pos := [3]float32{float32(f.Rect.X), float32(f.Rect.Y) + f.WorldHeight*0.5, f.WorldHeight}
		at := -1
		nearest := float32(nanoClusterReach*nanoClusterReach) * scale * scale
		for i := 0; i < l.nanoCount; i++ {
			p := l.nano[i].light.position
			dx, dy, dh := p[0]-pos[0], p[1]-pos[1], p[2]-pos[2]
			d2 := dx*dx + dy*dy + dh*dh
			if d2 < nearest {
				at, nearest = i, d2
			}
		}
		if at < 0 {
			if l.nanoCount == len(l.nano) {
				return
			}
			at = l.nanoCount
			l.nanoCount++
			l.nano[at] = nanoLightCluster{light: battleLight{radius: nanoLightRadius * scale, kind: lightNano}}
		}
		group := &l.nano[at]
		group.count++
		for j := range pos {
			group.light.position[j] += (pos[j] - group.light.position[j]) / float32(group.count)
			group.light.color[j] += color[j] * nanoParticleEnergy
		}
	})
	for i := 0; i < l.nanoCount; i++ {
		light := l.nano[i].light
		peak := lightPower(light)
		if peak == 0 {
			continue
		}
		if peak > nanoClusterEnergy {
			for j := range light.color {
				light.color[j] *= nanoClusterEnergy / peak
			}
		}
		l.add(light)
	}
}

// nanoLightColor averages the displayed seven-entry nano ramp [03 §5.5].
// This is an Enhanced emission policy, not a change to the recorded palette
// index. Sampling the palette each gather also honors palette updates without
// retained time, source identities or illumination surviving expired particles.
func nanoLightColor(pal *[256][4]byte) (color [3]float32) {
	for index := 0xa1; index <= 0xa7; index++ {
		for j := range color {
			color[j] += float32(pal[index][j]) / (255 * 7)
		}
	}
	return color
}

// glowNano widens the emission footprint around the existing two-pixel core.
// The existing blur and fog composite soften it; the particle draw is untouched.
func (r *Renderer) glowNano(f drawlist.Fill) {
	if f.NanoSubmerged || !r.glowActive() || !r.nanoInView(f) {
		return
	}
	pad := 2 * nanoScale(f)
	x0, y0 := max(float32(f.Rect.X)-pad, 0), max(float32(f.Rect.Y)-pad, 0)
	x1 := min(float32(f.Rect.X+f.Rect.W)+pad, float32(r.clipW()))
	y1 := min(float32(f.Rect.Y+f.Rect.H)+pad, float32(r.clipH()))
	s := &r.sched
	r.glow.rect([4]*ebiten.Image{1: r.tables.atlas},
		s.txx(x0), s.txy(y0), s.txx(x1), s.txy(y1), 0, 0, 0, 0,
		[4]float32{float32(f.Index), nanoGlowGain * glowGain, 0, 0},
		[4]float32{0, 0, 0, glowOpSolid})
}
