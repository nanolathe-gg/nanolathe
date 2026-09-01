package client

// Snapshot-driven effect presentation.  Effects are admitted by simulation
// event producers and copied into the immutable frame; this adapter performs
// only indexed framebuffer work and never mutates the event source [I6].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
)

// EffectDrawOptions supplies authored asset and LHT metadata.  Unknown frame
// or halo geometry is represented by a false return; the adapter does not
// invent durations, radii, or palette rows [I9].
type EffectDrawOptions struct {
	ResolveFrame func(frame.EffectView, int32) (*formats.GAFFrame, bool)
	LHTGeometry  func(frame.EffectView) (radius, level int, ok bool)
	// TerrainCoverage admits only indexed pixels belonging to the already
	// composed terrain. A nil predicate leaves a light effect unresolved; the
	// halo must never brighten units, effects, or HUD pixels [03 §4.3.1].
	TerrainCoverage func(x, y int) bool
}

// EffectDrawStats reports actual effect work.  Skipped means the immutable
// effect existed but its authored graphic or LHT geometry was unresolved.
type EffectDrawStats struct {
	Admitted int
	Sprites  int
	Halos    int
	Skipped  int
	Strokes  int
}

// effectStripCount is the number of established strip barriers, 0..9
// [03 R-STRIP-01 §1–§3].
const effectStripCount = 10

// classifyEffectStrips sorts the committed effect slice into the client's
// reusable per-strip buckets, once per composed frame.
//
// The composer visits ten barriers plus the fixed pool, and each used to scan
// the whole effect slice twice and allocate a fresh slice for its matches —
// eleven filtered allocations and twenty-two scans every frame. One pass fills
// all eleven buckets instead, and the buckets keep their capacity between
// frames.
//
// Source order within a bucket is the committed order, which is what the
// barrier contract requires: effects must not drift into a single
// after-the-world pass, and within a strip they draw in producer admission
// order [03 §1][03 R-STRIP-01 §1–§3]. The committed frame is never mutated
// [I6]; the buckets hold copies of the views.
func (c *Client) classifyEffectStrips(cur *frame.Frame) {
	if c.stripsFrame == cur && c.stripsTick == cur.Tick && c.stripsValid {
		return
	}
	for i := range c.stripBuckets {
		c.stripBuckets[i] = c.stripBuckets[i][:0]
	}
	c.stripUnstripped = c.stripUnstripped[:0]
	for i := range cur.Effects {
		strip := cur.Effects[i].Strip
		switch {
		case strip < 0 || int(strip) >= effectStripCount:
			// A negative strip is the published unresolved/unstripped
			// sentinel, distinct from every barrier and drawn at the
			// fixed-pool position [03 R-STRIP-01 §3].
			c.stripUnstripped = append(c.stripUnstripped, cur.Effects[i])
		default:
			c.stripBuckets[strip] = append(c.stripBuckets[strip], cur.Effects[i])
		}
	}
	c.stripsFrame, c.stripsTick, c.stripsValid = cur, cur.Tick, true
}

// effectViewsForStrip returns one committed strip without changing source
// order. The composer calls this at each retail barrier so effects cannot
// drift into a single after-the-world pass [03 §1][03 R-STRIP-01 §1–§3].
func (c *Client) effectViewsForStrip(cur *frame.Frame, strip int8) []frame.EffectView {
	if strip < 0 || int(strip) >= effectStripCount {
		return nil
	}
	c.classifyEffectStrips(cur)
	return c.stripBuckets[strip]
}

// unstrippedEffectViews returns the fixed-pool records [03 §1]
// [03 R-STRIP-01 §3].
func (c *Client) unstrippedEffectViews(cur *frame.Frame) []frame.EffectView {
	c.classifyEffectStrips(cur)
	return c.stripUnstripped
}

// drawEffectStrip consumes the immutable records assigned to one established
// barrier. It deliberately does no admission or simulation work [I6].
func (c *Client) drawEffectStrip(cur *frame.Frame, strip int8) {
	if c == nil || cur == nil || c.cam == nil {
		return
	}
	effects := c.effectViewsForStrip(cur, strip)
	if len(effects) == 0 {
		return
	}
	c.DrawEffectViews(effects, c.effectDrawOptions())
}

// drawFixedEffects consumes the unstripped fixed-effect pool at its one
// established location, after projectiles and before strip 7 [03 §1]
// [03 R-STRIP-01 §3].
func (c *Client) drawFixedEffects(cur *frame.Frame) {
	if c == nil || cur == nil || c.cam == nil {
		return
	}
	effects := c.unstrippedEffectViews(cur)
	if len(effects) == 0 {
		return
	}
	c.DrawEffectViews(effects, c.effectDrawOptions())
}

// DrawEffectViews draws snapshot effects in stable producer admission order.
// The caller supplies asset resolution because the content catalog is owned
// outside the client; no generic explosion/smoke sprite is selected here.
func (c *Client) DrawEffectViews(effects []frame.EffectView, options EffectDrawOptions) EffectDrawStats {
	var stats EffectDrawStats
	if c == nil || c.cam == nil {
		return stats
	}
	draws := render.BuildEffectDraws(effects)
	stats.Admitted = len(draws)
	// Two walks over the pool, in retail's order [06 R-WFX-01 §2]: every
	// record's SECONDARY (calculated) frame first through the flash blitter,
	// then every record's PRIMARY (named art) frame through the ordinary frame
	// blitter — so the named art always composes over the calculated disc. One
	// interleaved walk would let an early impact's art be brightened by a later
	// impact's disc.
	for _, d := range draws {
		if !d.HasCalculatedFlash {
			continue
		}
		x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
		if c.drawCalculatedFlash(int(d.CalculatedTable), d.FrameB, int(x-128), int(y-32), options.TerrainCoverage) {
			stats.Halos++
		}
	}
	for i, d := range draws {
		view := effects[i]
		if d.Kind == frame.EventKindNanolathe.String() && d.Strip == 6 {
			// A nano segment is an emitter, not a line. Its particles are owned
			// by the nanolathe field, which advances once per committed tick and
			// paints itself before this pass [03 §5.5]. A segment whose geometry
			// producer is unresolved emits nothing at all.
			if !view.NanolatheGeometryKnown {
				stats.Skipped++
				continue
			}
			stats.Strokes++
			continue
		}
		if d.Light {
			radius, level, ok := 0, 0, false
			if options.LHTGeometry != nil {
				radius, level, ok = options.LHTGeometry(view)
			} else if view.HasFlashDisc {
				radius, level, ok = int(view.FlashRadius), int(view.FlashLevel), true
			}
			// LHT row 0 is authored and near-identity; only negative rows are
			// invalid. A resolver returning ok=true, level=0 must still admit the
			// halo [03 §4.3.1].
			if !ok || radius <= 0 || level < 0 || options.TerrainCoverage == nil {
				stats.Skipped++
				continue
			}
			x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
			c.drawLHTHalo(int(x-128), int(y-32), radius, level, options.TerrainCoverage)
			stats.Halos++
		}
		if d.StripFill != 0 {
			// A mirrored strip sub-record whose family fills rather than blits:
			// a two-by-two rectangle in the family's palette colour, at the
			// ordinary projection [03 R-STRIP-01 §2].
			x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
			if c.fillStripParticle(int(x-128), int(y-32), d.StripFill) {
				stats.Sprites++
			} else {
				stats.Skipped++
			}
			continue
		}
		if d.Graphic == "" || options.ResolveFrame == nil {
			if !d.Light {
				stats.Skipped++
			}
			continue
		}
		frame, ok := options.ResolveFrame(view, d.FrameA)
		if !ok || frame == nil {
			stats.Skipped++
			continue
		}
		x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
		c.UIBlitAnchor(frame, int(x-128), int(y-32))
		stats.Sprites++
	}
	return stats
}

// drawLHTHalo applies the authored LHT row to the existing indexed terrain
// inside an explicitly supplied circular mask.  It does not mutate terrain
// or choose a radius/row; those come from the event/content resolver [03
// §4.3.1][F-P0-036].
func (c *Client) drawLHTHalo(cx, cy, radius, level int, terrainCoverage func(x, y int) bool) {
	if c == nil || c.pal == nil || radius <= 0 || level < 0 {
		return
	}
	if terrainCoverage == nil {
		return
	}
	r2 := radius * radius
	for dy := -radius; dy <= radius; dy++ {
		py := cy + dy
		if py < 0 || py >= c.height {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy > r2 {
				continue
			}
			px := cx + dx
			if px < 0 || px >= c.width {
				continue
			}
			if !terrainCoverage(px, py) {
				continue
			}
			idx := py*c.width + px
			c.indexed[idx] = c.pal.LightLookup(level, c.indexed[idx])
		}
	}
}

// fillStripParticle draws one strip sub-record of a filling family: the
// two-by-two rectangle of [03 R-STRIP-01 §2], in the family's palette colour,
// at the projected point.
//
// The nano ramp `0xa1..0xa7` and the impact-sprinkle pair `0x61`/`0x67` are the
// only fill colours the census names, and the sub-record carries whichever step
// of its own ramp it has walked to. Nothing is drawn off the framebuffer; a
// rectangle straddling an edge draws the pixels that land inside it.
func (c *Client) fillStripParticle(x, y int, color uint8) bool {
	if c == nil || len(c.indexed) == 0 || color == 0 {
		return false
	}
	drew := false
	for dy := 0; dy < 2; dy++ {
		py := y + dy
		if py < 0 || py >= c.height {
			continue
		}
		for dx := 0; dx < 2; dx++ {
			px := x + dx
			if px < 0 || px >= c.width {
				continue
			}
			c.indexed[py*c.width+px] = color
			drew = true
		}
	}
	return drew
}
