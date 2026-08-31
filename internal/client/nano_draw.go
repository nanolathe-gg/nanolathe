package client

// Nanolathe particle presentation [03 §5.5][05 "R-P0-06 §5 addendum"].
//
// Each accepted construction, repair, or reclaim work step publishes one nano
// segment event. The client turns that event into a particle emitter: the
// source is the builder's nano piece, the target is the target's world
// bounding box, and the spray's cone comes from particles landing anywhere in
// the middle of that box. Everything here is presentation — the particles draw
// from the CRT stream and never touch simulation state (I6).

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// presentationRNG is a presentation-only CRT with the same recurrence as the
// retail CRT stream but isolated from the authoritative session CRT [DET-01].
type presentationRNG struct{ state uint32 }

func (p *presentationRNG) Rand() int32 {
	p.state = p.state*214013 + 2531011
	return int32((p.state >> 16) & 0x7FFF)
}

// tickNanolathe admits this tick's nano segments and advances the field once
// per committed tick, so repainting one snapshot neither spawns particles
// twice nor consumes CRT draws again.
func (c *Client) tickNanolathe(cur *frame.Frame) {
	if c == nil || cur == nil || cur.Tick == c.lastNanoTick {
		return
	}
	c.lastNanoTick = cur.Tick
	for i := range cur.Effects {
		e := &cur.Effects[i]
		if e.Kind != frame.EventKindNanolathe.String() || e.Strip != 6 || e.StartTick != cur.Tick {
			continue
		}
		if !e.NanolatheGeometryKnown {
			continue
		}
		min, max := c.nanoTargetBox(cur, e)
		c.nano.Add([3]numeric.Fixed{e.X, e.Y, e.Z}, min, max, cur.Tick)
	}
	// DET-01: nanolathe spray is presentation-only; use a presentation-only
	// RNG seeded from the committed tick so render cadence does not affect sim.
	rng := presentationRNG{state: cur.Tick*214013 + 2531011}
	c.nano.Tick(cur.Tick, func() int32 { return rng.Rand() })
}

// nanoTargetBox resolves the target's world bounding box. A target whose model
// is not resolvable collapses to the published target point, which keeps the
// spray a straight line rather than inventing a spread [I9].
func (c *Client) nanoTargetBox(cur *frame.Frame, e *frame.EffectView) (min, max [3]numeric.Fixed) {
	if e.NanolatheTargetBoxKnown {
		return e.NanolatheTargetMin, e.NanolatheTargetMax
	}
	point := [3]numeric.Fixed{e.TargetX, e.TargetY, e.TargetZ}
	for i := range cur.Units {
		u := &cur.Units[i]
		if u.Slot != e.Target {
			continue
		}
		m := c.unitModelFor(u.Model)
		if m == nil || m.compiled == nil {
			break
		}
		lo, hi := render.ModelBounds(m.compiled)
		origin := [3]numeric.Fixed{u.X, u.Y, u.Z}
		for a := 0; a < 3; a++ {
			min[a], max[a] = origin[a]+lo[a], origin[a]+hi[a]
		}
		return min, max
	}
	return point, point
}

// drawNanolathe paints every live particle, gated by the local player's
// coverage at that particle's own tile [03 §5.5].
//
// A particle fills the rectangle from its pixel to one pixel right and down,
// and retail's rectangle fill is inclusive on both edges — so the mark is two
// by two, not a single pixel. At one pixel the spray reads as a thin dotted
// line instead of the dense cone retail draws.
func (c *Client) drawNanolathe(cur *frame.Frame) {
	if c == nil || cur == nil || c.cam == nil || len(c.nano.Records) == 0 {
		return
	}
	mode := uint8(0)
	if cur.Visibility.CoverageBytes {
		mode = ProjectileVisibilityModeBytes
	}
	local := cur.Selection.LocalPlayer
	for ri := range c.nano.Records {
		for _, p := range c.nano.Records[ri].Particles {
			if !PointVisible(cur.Visibility, p.X, p.Y, p.Z, mode, local) {
				continue
			}
			sx, sy := c.cam.WorldToScreen(p.X, p.Y, p.Z)
			c.fillIndexedRect(int(sx-camera.OriginX), int(sy-camera.OriginY),
				render.NanoParticleSize, render.NanoParticleSize, p.Color)
		}
	}
}
