package client

// The presentation-owned strip-9 container store.
//
// Retail's two debris-draw producers do not blit and forget: each makes a
// CONTAINER on strip 9, which survives on the strip list, is advanced by the
// per-tick strip update, and is drawn again at barrier 9 on every later frame
// until it retires `[04 R-COB-04 §2]` `[03 R-FX-01 §3]`. That persistence is
// the trail — a puff made three ticks ago is still on screen, three frames
// further into its animation and a little further downwind, behind the piece
// that made it.
//
// The containers the simulation owns ride the publication boundary in
// `Frame.Strips` [I6]. These two cannot: their producer is the draw pass, so
// the simulation never makes them and has nothing to publish. Presentation
// therefore keeps its own list, steps it on the COMMITTED TICK, and draws it at
// the same barrier the published strip-9 objects draw at.
//
// Presentation only: nothing here writes simulation state, and every random
// value comes from the client's PRIVATE presentation CRT copy, never the
// session stream (DET-01) [I4][I6].

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// debrisTrailSteadyCap is the strip's eviction threshold: when the pre-insert
// count exceeds 400 the oldest container is destroyed first, so a strip holds
// at most 401 `[03 "Strip storage and lifecycle"]` `[03 R-STRIP-01 §1]`.
//
// Divergence, stated rather than invented: retail's strip 9 is ONE vector and
// the 1000-slot container pool `[03 R-FX-02 §4]` is shared between these
// containers and every simulation-side strip-9 producer (weapon puffs, the
// corpse column, the emit-sfx points). This build splits the two lists, so the
// presentation store carries the per-strip bound on its own and the two lists
// cannot crowd each other out the way retail's single vector does.
const debrisTrailSteadyCap = 400

// debrisTrailCatchUp bounds how many committed ticks one step may replay. A
// larger gap is a discontinuity — a load, a seek, a new battle — and the store
// is cleared instead, the same way the other retained presentation histories
// are retired when their producer's continuity breaks.
const debrisTrailCatchUp = 8

// debrisTrailStore holds the live containers, the committed tick they have been
// stepped to, and the per-slot emission stamp.
type debrisTrailStore struct {
	live []presentationrender.DebrisTrailContainer
	// views is the reusable barrier-9 view buffer.
	views []frame.StripView
	// tick is the committed tick every live container has been stepped to, and
	// valid says whether tick has been established at all.
	tick  uint32
	valid bool
	// emitted[slot] is ONE MORE than the committed tick that debris slot last
	// produced containers on; zero means never. The stamp is what keeps a
	// re-rendered or interpolated frame from producing a second set of
	// containers for a tick that already has them — see emitDebrisTrails.
	emitted [presentationrender.WholeDebrisSlots]uint32
	// evicted counts containers the steady cap destroyed, for diagnostics.
	evicted int
}

func (s *debrisTrailStore) reset() {
	*s = debrisTrailStore{live: s.live[:0], views: s.views[:0]}
}

// append inserts one container at the vector end, destroying the oldest first
// when the pre-insert count exceeds the steady cap `[03 R-STRIP-01 §1]`.
func (s *debrisTrailStore) append(c presentationrender.DebrisTrailContainer) {
	if len(s.live) > debrisTrailSteadyCap {
		// Same-strip order among survivors equals insertion order, so the
		// oldest goes and the rest slide left.
		copy(s.live, s.live[1:])
		s.live = s.live[:len(s.live)-1]
		s.evicted++
	}
	s.live = append(s.live, c)
}

// stepDebrisTrails advances the store to the committed tick of the frame being
// composed. It runs once per COMMITTED TICK, never once per rendered frame:
// this build pre-records frames and composes the same committed tick more than
// once (the pre-record's re-record after a miss, the `--shot-renderer both`
// route, and every interpolated frame between two ticks), and a container that
// stepped once per rendered frame would animate and drift at the host's refresh
// rate instead of the tick rate `[03 R-FX-01 §3]`. The stored tick is the key;
// a repeat of a tick already stepped does nothing.
//
// The sweep is retail's phase-11 update: per container, the removal verdict
// FIRST, then the update work `[03 R-STRIP-01 §2]`. It is called before the
// debris pass produces this frame's containers, so a container is first stepped
// on the tick AFTER the one it was born on, which is where retail's sweep first
// sees a container its draw pass made.
func (c *Client) stepDebrisTrails(cur *frame.Frame) {
	if c == nil || cur == nil {
		return
	}
	s := &c.debrisTrails
	tick := cur.Tick
	if !s.valid {
		s.valid, s.tick = true, tick
		return
	}
	if tick == s.tick {
		return
	}
	if tick < s.tick || tick-s.tick > debrisTrailCatchUp {
		s.reset()
		s.valid, s.tick = true, tick
		return
	}
	windX, windZ := presentationrender.DebrisTrailWind(cur.Wind)
	gravityWord := int32(0)
	if c.terrain != nil {
		gravityWord = c.terrain.AuthoredGravity
	}
	for t := s.tick + 1; t <= tick; t++ {
		s.sweep(t, windX, windZ, gravityWord, c.presentationCRT())
	}
	s.tick = tick
}

// sweep is one tick of the strip update over the store: the removal verdict is
// evaluated BEFORE the update work, a positive verdict destroys the container
// with stable left compaction, and a zero verdict runs the update and keeps it
// `[03 R-STRIP-01 §2]`.
func (s *debrisTrailStore) sweep(tick uint32, windX, windZ, gravityWord int32, crt presentationrender.CRTRandomSource) {
	write := 0
	for read := range s.live {
		o := s.live[read]
		if o.Retired(tick) {
			continue
		}
		o.Step(tick, windX, windZ, gravityWord, crt)
		s.live[write] = o
		write++
	}
	for i := write; i < len(s.live); i++ {
		s.live[i] = presentationrender.DebrisTrailContainer{}
	}
	s.live = s.live[:write]
}

// presentationCRT narrows the bound stream to the interface the producers take,
// keeping a nil pointer a nil INTERFACE: a typed nil would pass the producers'
// own "is a stream bound" test and then draw from nothing.
func (c *Client) presentationCRT() presentationrender.CRTRandomSource {
	if c == nil || c.crt == nil {
		return nil
	}
	return c.crt
}

// debrisTrailFrameCounts resolves the two bound entries' frame counts less one,
// the quantity retail's container stores at init `[03 R-FX-01 §3]`. An entry
// this install cannot resolve yields zero, which leaves the puff without a last
// frame and the segment without a wrap point rather than substituting a count
// [I9].
func (c *Client) debrisTrailFrameCounts() presentationrender.DebrisTrailFrameCounts {
	var counts presentationrender.DebrisTrailFrameCounts
	if entry, ok := c.effectEntry(presentationrender.DebrisTrailBank, presentationrender.DebrisSmokePuffEntry); ok {
		counts.Smoke = int32(len(entry.Frames) - 1)
	}
	if entry, ok := c.effectEntry(presentationrender.DebrisTrailBank, presentationrender.DebrisFlameTrailEntry); ok {
		counts.Flame = int32(len(entry.Frames) - 1)
	}
	return counts
}

// drawDebrisTrails is barrier 9's second source: the store's live containers,
// in insertion order, drawn the way every other strip-9 smoke and flame record
// is drawn — the one-point coverage gate, then the tinted blit of the
// sub-record's own animation frame `[03 §1]` `[03 R-FX-01 §3]`.
//
// It follows the published strip-9 objects so a container made this frame
// composes over them, which is the order a later object in one vector has over
// an earlier one.
func (c *Client) drawDebrisTrails(cur *frame.Frame) StripDrawStats {
	var stats StripDrawStats
	if c == nil || cur == nil {
		return stats
	}
	// Below the strategic cut the effect strips are not recorded at all; the
	// store still steps, so turning the view back lands on the right frames.
	if c.strategicView() {
		return stats
	}
	s := &c.debrisTrails
	s.views = s.views[:0]
	for i := range s.live {
		s.views = s.live[i].AppendViews(s.views)
	}
	stats = c.drawStripViews(cur, s.views)
	c.addStripStats(stats)
	return stats
}
