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
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// presentationRNG is a presentation-only CRT with the same recurrence as the
// retail CRT stream but isolated from the authoritative session CRT [DET-01].
type presentationRNG struct{ state uint32 }

// Rand draws one value from this presentation-only CRT copy [01 §7.2] [I4].
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
		box0, box1 := c.nanoTargetBox(cur, e)
		if e.NanolatheBoxAtSource && e.NanolatheTargetBoxKnown {
			// The reversed direction [05 R-WORK-01 §8]: the six-word box is the
			// SOURCE end and the published target point is the destination, so
			// the particles leave the whole footprint of the thing being
			// reclaimed and converge on the builder's nano piece. Reading the
			// box as the destination in both directions collapsed a feature
			// reclaim's spray into the tree's own cell, where every particle
			// lived one tick and nothing reached the builder.
			dst := [3]numeric.Fixed{e.TargetX, e.TargetY, e.TargetZ}
			c.nano.AddBoxes(box0, box1, dst, dst, cur.Tick)
			continue
		}
		c.nano.Add([3]numeric.Fixed{e.X, e.Y, e.Z}, box0, box1, cur.Tick)
	}
	// DET-01: nanolathe spray is presentation-only; use a presentation-only
	// RNG seeded from the committed tick so render cadence does not affect sim.
	rng := presentationRNG{state: cur.Tick*214013 + 2531011}
	c.nano.Tick(cur.Tick, func() int32 { return rng.Rand() })
}

// nanoTargetBox resolves the target's world bounding box. Every unit-target
// work step — forward (build, repair, help-build/assist, resurrection-into-
// unit) and reversed (unit reclaim, capture) alike — now publishes its own
// box on the event [05 R-WORK-01 §8][02 R-CAT-01 §7], so the model-bounds
// derivation below only ever runs for an event that carries none (a
// script-emitted nano segment with no unit target). Real model geometry is
// the wrong shape for that box regardless: the published record is
// footprint-derived in X/Z, not the model's silhouette. A target whose model
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
			// One particle is a plain solid FillSolid rectangle [03 §5.5]; it
			// reads nothing from the destination, so it converts in this unit.
			c.emitFill(drawlist.Fill{
				Rect: drawlist.Rect{
					X: sx - camera.OriginX,
					Y: sy - camera.OriginY,
					W: render.NanoParticleSize,
					H: render.NanoParticleSize,
				},
				Index: p.Color,
				Style: drawlist.FillSolid,
			})
		}
	}
}
