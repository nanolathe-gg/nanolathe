package client

// The per-family strip-object draw. Retail's composer walks one strip at each
// of the ten barriers of [03 §1] and calls every stored object's draw entry,
// which forwards the camera origin to each sub-record; the sub-record is what
// acquires a screen position [03 R-STRIP-01 §2]. Here the committed
// frame.StripView slice stands in for the strip vectors, and this file is the
// draw entry.
//
// Presentation only: nothing below writes simulation state, and the committed
// frame is read, never mutated [I6].

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// stripParticleSize is the side of the two-by-two rectangle a filling family
// paints. Retail's rectangle filler is inclusive on both edges, so the mark is
// two pixels by two, not one [03 R-FX-01 §3][R-P0-19-P].
const stripParticleSize = 2

// StripDrawStats reports what one barrier's strip objects did. Unresolved
// counts the records whose published (bank, entry) pair named no art this
// client could load: the record is skipped, never redrawn as a stand-in
// [I9]. Gated counts the records the one-point coverage gate rejected.
type StripDrawStats struct {
	Blitted    int
	Filled     int
	Unresolved int
	Gated      int
}

// drawStripSlot is one of the ten barriers of [03 §1]: the effect records
// routed to this strip, then the strip objects the simulation published for
// it.
//
// Retail's barrier has one source — the strip's own object vector — so the
// order between the two is ours, not a contract: the event-routed records are
// this build's own routing of producer events onto the strips
// (internal/frame's strip routes), and they draw first so a strip object's
// sprite composes over them the way a later object composes over an earlier
// one within the vector.
func (c *Client) drawStripSlot(cur *frame.Frame, strip int8) StripDrawStats {
	// Below the strategic cut the effect strips are not recorded at all: their
	// art is smaller than a marker and the layer is one of §16.10's drops.
	if c.strategicView() {
		return StripDrawStats{}
	}
	c.drawEffectStrip(cur, strip)
	stats := c.drawStripBarrier(cur, strip)
	c.addStripStats(stats)
	return stats
}

// drawStripBarrier draws the committed strip objects of one barrier, in the
// committed order [03 §1][03 R-FX-02 §1].
//
// The three researched draw forms differ in more than their art:
//
//   - geothermal steam blits without a coverage test [03 R-FX-02 §3];
//   - weapon smoke and both flame classes blit after the per-particle
//     one-point coverage gate [03 R-FX-01 §3][03 R-FX-02 §2];
//   - the sprinkle and nanolathe families fill a two-by-two rectangle after
//     that same gate, with their colour byte written raw [03 R-FX-01 §3]
//     [03 §5.5].
//
// Both blitting families go through the tinted blitter, the ALP-blend family,
// not the opaque keyed one [03 R-FX-02 §2][03 R-FX-02 §3][R-COMP-01 §2].
//
// Coverage admission precedes sprite recording, so classic and modern receive
// the same visible puffs [03 R-FX-01 §3].
func (c *Client) drawStripBarrier(cur *frame.Frame, strip int8) StripDrawStats {
	var stats StripDrawStats
	if c == nil || cur == nil || c.cam == nil || len(c.indexed) == 0 {
		return stats
	}
	return c.drawStripViews(cur, stripBarrierRun(cur.Strips, strip))
}

// drawStripViews draws one run of strip sub-records: the published objects of
// one barrier, or the presentation-owned debris-trail store's own containers at
// barrier 9 (debris_trail_store.go). Both sources reach the same per-family
// draw, because retail has only one — the strip's object vector.
func (c *Client) drawStripViews(cur *frame.Frame, views []frame.StripView) StripDrawStats {
	var stats StripDrawStats
	if c == nil || cur == nil || c.cam == nil || len(c.indexed) == 0 || len(views) == 0 {
		return stats
	}
	// The gate reads the same published coverage every world-space
	// presentation pixel reads: the byte grid when the publication carries
	// one, else the local player's bit in the word grid [03 §3.2][03 §5.4].
	mode := uint8(0)
	if cur.Visibility.CoverageBytes {
		mode = ProjectileVisibilityModeBytes
	}
	local := cur.ViewingPlayer
	for i := range views {
		v := views[i]
		switch v.Family {
		case frame.StripFamilySmokePuff, frame.StripFamilyVentSteam:
			if v.Family == frame.StripFamilySmokePuff && !PointVisible(cur.Visibility, v.X, v.Y, v.Z, mode, local) {
				stats.Gated++
				continue
			}
			if !c.blitStripFrame(v) {
				stats.Unresolved++
				continue
			}
			stats.Blitted++
		case frame.StripFamilyFlame, frame.StripFamilyFlameTrail:
			if !PointVisible(cur.Visibility, v.X, v.Y, v.Z, mode, local) {
				stats.Gated++
				continue
			}
			if !c.blitStripFrame(v) {
				stats.Unresolved++
				continue
			}
			stats.Blitted++
		case frame.StripFamilySprinkle, frame.StripFamilyNano:
			if v.Fill == 0 {
				stats.Unresolved++
				continue
			}
			if !PointVisible(cur.Visibility, v.X, v.Y, v.Z, mode, local) {
				stats.Gated++
				continue
			}
			sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
			// The colour byte is written raw: the sprinkle and nano ramps are NOT
			// passed through the logical-to-physical remap the
			// beam and lightning colours use [03 R-FX-01 §3]. The two-by-two mark
			// is a plain solid rect, so it records as a FillSolid.
			// A fill's extents take the view scale, so the two-by-two mark stays
			// two world pixels square (DESIGN_GPU_RENDERER §14.2).
			side := c.viewScale().Px(stripParticleSize)
			rw, rh := c.recordExtent()
			c.emitFill(drawlist.Fill{
				Rect: drawlist.Rect{
					X: sx - camera.OriginX, Y: sy - camera.OriginY,
					W: side, H: side,
				},
				Index:         v.Fill,
				Style:         drawlist.FillSolid,
				Nano:          v.Family == frame.StripFamilyNano,
				WorldHeight:   float32(v.Y.Raw()) / 65536 * float32(c.viewScale().Float()),
				LightingScale: float32(c.viewScale().Float()),
				Clip:          drawlist.Rect{W: int32(rw), H: int32(rh)},
			})
			stats.Filled++
		default:
			// A family with no established draw draws nothing rather than
			// borrowing another family's [I9].
			stats.Unresolved++
		}
	}
	return stats
}

// blitStripFrame resolves one sub-record's (bank, entry) pair and stamps its
// current frame through the tinted blitter. It reports whether pixels could be
// drawn; a pair that resolves to no art draws nothing at all [I9].
func (c *Client) blitStripFrame(v frame.StripView) bool {
	if v.Entry == "" {
		return false
	}
	entry, ok := c.effectEntry(v.Bank, v.Entry)
	if !ok {
		return false
	}
	// The cursor is the sub-record's own animation frame. Retail's cursor
	// cannot leave its entry — the flame families take it modulo the frame
	// count and the puff retires at a last frame drawn from that count — so an
	// out-of-range value here belongs to a container built before its frame
	// count was known, and is clamped rather than dropped, exactly as the
	// effect pool's cursor is.
	index := v.Frame
	if index < 0 {
		index = 0
	}
	if int(index) >= len(entry.Frames) {
		index = int32(len(entry.Frames) - 1)
	}
	art := entry.Frames[index].Frame
	if art == nil {
		return false
	}
	sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	lightingKind := drawlist.SpriteLightingNone
	var lightingTime, lightingFade float32
	hasLightingFade := false
	switch v.Family {
	case frame.StripFamilySmokePuff, frame.StripFamilyVentSteam:
		lightingKind = drawlist.SpriteLightingSmoke
	case frame.StripFamilyFlame:
		// The strip-5 flame-stream segment [03 R-FX-02 §2]: the burning world's
		// own standing light. It reaches here only through the one-point
		// coverage gate above, so an unseen fire never lights a visible
		// receiver (§31). Emission flickers, so the record carries the
		// committed time the executor phases it by. A burning FEATURE is not
		// this family — its light comes from the feature blit (world_draw.go).
		lightingKind = drawlist.SpriteLightingFire
		lightingTime = c.lightingTime()
		lightingFade, hasLightingFade = stripLightingFade(v.Remaining), v.HasRemaining
	case frame.StripFamilyFlameTrail:
		// A trail particle is a spark in flight, not a place that is burning:
		// the flame a burning debris piece drags behind it, and equally the COB
		// emit-sfx 0/1 VTOL and thrust wakes, which are the same strips-7/9
		// family [03 R-FX-01 §3]. Both take the spark family's own reach and
		// carry no flicker, whose phase hash is a position hash and so re-rolls
		// under anything that moves (§31.5, §31.7).
		lightingKind = drawlist.SpriteLightingSpark
		lightingFade, hasLightingFade = stripLightingFade(v.Remaining), v.HasRemaining
	}
	scale := float32(c.viewScale().Float())
	// Only an emitter has a use for the receiver height, and this runs for every
	// strip blit in the frame — the smoke puffs included — on both executors and
	// with the Lighting switch off, so the terrain sample is taken only when
	// something will read it (§31.7).
	var lightingGround float32
	if lightingKind.Emitter() {
		lightingGround = c.lightingGround(v.X, v.Z, scale)
	}
	// Emit the translucent frame blit; the sink runs the raw tintedBlitAnchor, so
	// this is the blit's only execution [03 R-COMP-01 §2][03 R-FX-02 §2]. The
	// returned bool mirrors tintedBlitAnchor's own gate (ALP table and surface
	// present): a resolved identity that could run counts Blitted even when every
	// pixel clipped away, and only a missing table counts Unresolved.
	c.emitSprite(drawlist.Sprite{
		// The strip frame's 2x variant in the detail view; the tinted blitter
		// places by the anchor, and the variant's authored offsets are already
		// doubled (DESIGN_GPU_RENDERER §14.2, §14.3).
		Frame: c.viewFrame(art),
		X:     sx - camera.OriginX,
		Y:     sy - camera.OriginY,
		Kind:  drawlist.BlitTinted,
		// Strip art is fire, smoke and explosion animation: a light source for the
		// Enhanced glow layer, which keeps only its bright texels (§19).
		Emissive:        true,
		LightingKind:    lightingKind,
		LightingTime:    lightingTime,
		LightingFade:    lightingFade,
		HasLightingFade: hasLightingFade,
		WorldHeight:     float32(v.Y.Raw()) / 65536 * scale,
		LightingGround:  lightingGround,
		LightingScale:   scale,
	})
	return c.pal != nil && len(c.indexed) != 0
}

// lightingTime is the committed tick plus the presentation fraction, wrapped to
// a bounded window so the value stays small and exact in a float32. It is the
// only clock any Enhanced light source reads: replay and pausing reproduce it,
// and no wall clock reaches presentation [I6].
func (c *Client) lightingTime() float32 {
	t := float32(c.frameTick % 3600)
	if c.interpolation {
		t += c.TickFraction()
	}
	return t
}

// tintedBlitAnchor is retail's translucent frame blit at a frame anchor: every
// source pixel that is not the transparent key resolves the destination to
// `ALP[src × 256 + dst]`, and the frame's authored placement offsets apply
// [R-COMP-01 §2][R-REN-03D §4][03 R-FX-02 §2].
//
// The family's gate is "ALP table present": with no palette loaded the blitter
// draws nothing at all rather than falling back to an opaque copy
// [R-COMP-01 §2].
func (c *Client) tintedBlitAnchor(f *formats.GAFFrame, x, y int) bool {
	if c == nil || f == nil || c.pal == nil || len(c.indexed) == 0 {
		return false
	}
	if len(f.Subframes) != 0 {
		for _, child := range f.Subframes {
			c.tintedBlitAnchor(child, x, y)
		}
		return true
	}
	x -= int(f.XOffset)
	y -= int(f.YOffset)
	for row := 0; row < int(f.Height); row++ {
		py := y + row
		if py < 0 || py >= c.height {
			continue
		}
		for col := 0; col < int(f.Width); col++ {
			px := x + col
			if px < 0 || px >= c.width {
				continue
			}
			src, ok := f.At(col, row)
			if !ok {
				continue
			}
			idx := py*c.width + px
			c.indexed[idx] = c.pal.Alpha[int(src)*256+int(c.indexed[idx])]
		}
	}
	// A record whose frame fell entirely outside the surface, or whose every
	// pixel was the transparent key, still RESOLVED: the identity was good and
	// the clip is the shell's, so it is not counted unresolved [R-COMP-01 §2].
	return true
}

// stripBarrierRun returns the contiguous run of one barrier's records. The
// committed slice is ordered by strip ascending [03 §1], which the publisher
// guarantees, so the run is found by two boundary searches and no scan of the
// whole slice [I1].
func stripBarrierRun(views []frame.StripView, strip int8) []frame.StripView {
	if len(views) == 0 || strip < 0 {
		return nil
	}
	lo := sort.Search(len(views), func(i int) bool { return views[i].Strip >= strip })
	hi := sort.Search(len(views), func(i int) bool { return views[i].Strip > strip })
	if lo >= hi {
		return nil
	}
	return views[lo:hi]
}

// stripLightingFadeTicks is the tail a flame's ground pool is taken out over,
// in committed ticks. It is SHORT because these records are: a burning debris
// piece lays flame segments that live one to three ticks, so a tail longer than
// that takes a pool to nothing while its flame is still on screen — which the
// first six-tick tail did. The animation cursor cannot serve as the clock: the
// flame families take it modulo their frame count, so it is a looping phase and
// not a lifetime [03 R-FX-01 §3][03 R-FX-02 §2].
const stripLightingFadeTicks = 3

// stripLightingFade is a strip particle's remaining emission for the terrain
// receiver: full while it has a tail's worth of life left, ramping down as its
// expiry arrives, so a pool leaves with the flame instead of switching off with
// the particle (DESIGN_GPU_RENDERER §31.7).
//
// The record's LAST drawn tick has zero ticks remaining and still emits a third
// of the pool, because it is still drawn. Counting it as spent is what made a
// visibly burning piece lay no light at all.
func stripLightingFade(remaining uint32) float32 {
	// Compared without the increment, so a remaining count near the width of
	// its type cannot wrap to zero and switch a live flame's light off.
	if remaining >= stripLightingFadeTicks-1 {
		return 1
	}
	return float32(remaining+1) / stripLightingFadeTicks
}
