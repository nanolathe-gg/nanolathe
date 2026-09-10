package client

// Snapshot-driven effect presentation.  Effects are admitted by simulation
// event producers and copied into the immutable frame; this adapter performs
// only indexed framebuffer work and never mutates the event source [I6].

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
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
	// Fragments are immutable geometry joined by the one-based effect slot.
	Fragments []frame.FragmentView
}

// EffectDrawStats reports actual effect work.  Skipped means the immutable
// effect existed but its authored graphic or LHT geometry was unresolved.
type EffectDrawStats struct {
	Admitted int
	Sprites  int
	Models   int
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
	// Below the strategic cut this layer is not recorded at all: the marker
	// layer stands in for it (DESIGN_GPU_RENDERER §16.10).
	if c.strategicView() {
		return
	}
	// The 100-slot whole-piece table precedes both fixed-effect category walks.
	// Keep DrawEffectViews intact: it retains all calculated secondaries before
	// all named primary/model records [04 R-COB-04 §2][03 §1].
	for i := range cur.Debris {
		c.drawDebrisModel(cur.Debris[i])
	}
	effects := c.unstrippedEffectViews(cur)
	if len(effects) == 0 {
		return
	}
	options := c.effectDrawOptions()
	options.Fragments = cur.Fragments
	c.DrawEffectViews(effects, options)
}

// DrawEffectViews draws snapshot effects in stable producer admission order.
// The caller supplies asset resolution because the content catalog is owned
// outside the client; no generic explosion/smoke sprite is selected here.
func (c *Client) DrawEffectViews(effects []frame.EffectView, options EffectDrawOptions) EffectDrawStats {
	var stats EffectDrawStats
	if c == nil || c.cam == nil {
		return stats
	}
	// The draw records live only for this call, so they are built into the
	// client's retained buffer rather than a fresh list per pass
	// (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU").
	c.effectDraws = render.BuildEffectDrawsInto(c.effectDraws, effects)
	draws := c.effectDraws
	stats.Admitted = len(draws)
	// Two walks over the pool, in retail's order [06 R-WFX-01 §2]: every
	// record's SECONDARY (calculated) frame first through the flash blitter,
	// then every record's PRIMARY (named art) frame through the ordinary frame
	// blitter — so the named art always composes over the calculated disc. One
	// interleaved walk would let an early impact's art be brightened by a later
	// impact's disc.
	for _, d := range draws {
		if !d.HasCalculatedFlash || !d.ActiveB {
			// The disc is drawn by the SECONDARY animation walk, so it stops
			// the moment that player's sequence pointer is cleared [03 §1] —
			// even though the record lives on while the primary art plays. The
			// cursor it leaves behind is index 0, the widest frame of the
			// table, so a gate on the frame index alone would re-light the
			// ground under a finished flash [03 §4.4][06 R-WFX-01 §2].
			continue
		}
		x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
		if c.drawCalculatedFlash(int(d.CalculatedTable), d.FrameB, int(x-128), int(y-32), options.TerrainCoverage) {
			stats.Halos++
		}
	}
	var fragments [render.FixedEffectCap + 1]*frame.FragmentView
	for i := range options.Fragments {
		v := &options.Fragments[i]
		if v.Slot > 0 && int(v.Slot) < len(fragments) {
			fragments[v.Slot] = v
		}
	}
	for i, d := range draws {
		view := effects[i]
		if view.FragmentSlot != 0 {
			drawn := false
			if int(view.FragmentSlot) < len(fragments) {
				if fragment := fragments[view.FragmentSlot]; fragment != nil {
					drawn = c.drawFragment(*fragment)
				}
			}
			if drawn {
				stats.Models++
			} else {
				stats.Skipped++
			}
			continue
		}
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
			// The halo is a lit disc in screen pixels, so its radius takes the view
			// scale while its centre comes through the projection
			// (DESIGN_GPU_RENDERER §14.2).
			c.drawLHTHalo(int(x-128), int(y-32), int(c.viewScale().Px(int32(radius))), level, options.TerrainCoverage)
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
		if !d.ActiveA {
			// The primary player finished while the calculated flash kept the
			// record alive [03 §1]. Its cursor has been reset to 0, so drawing
			// on the strength of the art name alone would restart the
			// animation's first frame under the fading disc [03 §4.4]. This is
			// not a Skipped: nothing failed to resolve, the layer is simply
			// over.
			continue
		}
		frame, ok := options.ResolveFrame(view, d.FrameA)
		if !ok || frame == nil {
			stats.Skipped++
			continue
		}
		x, y := c.cam.WorldToScreen(d.X, d.Y, d.Z)
		// The named effect art is a plain keyed frame-anchor blit [03 §1]; Anchored
		// selects the offset-subtracting UIBlitAnchor placement (WU-1.7b).
		// The load-time remaster covers feature banks only, so an effect frame
		// resolves to its nearest-doubled variant in the detail view; at the
		// native scale viewFrame is the identity (DESIGN_GPU_RENDERER §14.3).
		c.emitSprite(drawlist.Sprite{Frame: c.viewFrame(frame), X: x - 128, Y: y - 32, Kind: drawlist.BlitKeyed, Anchored: true})
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
	// The halo is a single LHT level applied to every covered pixel in the disc.
	// LightLookup clamps the level to 0..31, so clamping the recorded row here is
	// byte-identical and keeps the operand inside Point.Index [03 §4.3.1].
	row := level
	if row > 31 {
		row = 31
	}
	off := len(c.pointArena)
	r2 := radius * radius
	recW, recH := c.recordExtent()
	for dy := -radius; dy <= radius; dy++ {
		py := cy + dy
		if py < 0 || py >= recH {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy > r2 {
				continue
			}
			px := cx + dx
			if px < 0 || px >= recW {
				continue
			}
			if !terrainCoverage(px, py) {
				continue
			}
			c.pointArena = append(c.pointArena, drawlist.Point{X: int32(px), Y: int32(py), Index: uint8(row)})
		}
	}
	// The disc brightens the terrain already composed under it; record the level
	// and let the sink fold each pixel through the LHT row [03 §4.3.1]. The batch
	// is a self-owned arena sub-slice, immutable for the frame (WU-1.8).
	c.emitPoints(off, drawlist.PointLit)
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
	// The two-by-two mark is the same clipped solid rectangle fillIndexedRect
	// writes; drew reports whether any of its pixels land on the surface, which
	// is exactly whether the rect clips to a non-empty span [03 R-STRIP-01 §2].
	// A fill's extents take the view scale, so the two-by-two mark stays two
	// world pixels square (DESIGN_GPU_RENDERER §14.2).
	side := int(c.viewScale().Px(stripParticleSize))
	recW, recH := c.recordExtent()
	drew := false
	for dy := 0; dy < side && !drew; dy++ {
		py := y + dy
		if py < 0 || py >= recH {
			continue
		}
		for dx := 0; dx < side; dx++ {
			px := x + dx
			if px >= 0 && px < recW {
				drew = true
				break
			}
		}
	}
	if !drew {
		return false
	}
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: int32(x), Y: int32(y), W: int32(side), H: int32(side)},
		Index: color,
		Style: drawlist.FillSolid,
	})
	return true
}
