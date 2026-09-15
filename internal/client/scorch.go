package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// These are authored presentation choices (DESIGN_GPU_RENDERER §29), not
// retail decal behavior. Only committed primary art supplies an impact [I6].
const (
	scorchHeightTolerance = 12 * numeric.FixedOne
	// Preserve the reviewed per-publication identity budget independently of
	// retained marks, so later same-tick births can evict the oldest marks.
	scorchIdentityLimit = 512
)

type scorchMark struct {
	x, y, z numeric.Fixed
	radius  float32
	born    uint32
	variant uint32
}

type scorchIdentity struct {
	id       uint32
	sequence uint64
}

type scorchState struct {
	marks       []scorchMark
	head, count int
	tick        uint32
	viewer      uint8
	valid       bool
	seen        map[scorchIdentity]struct{}
	arena       []drawlist.ScorchMark
}

func (st *scorchState) push(m scorchMark) {
	if st.marks == nil {
		st.marks = make([]scorchMark, drawlist.ScorchMarkLimit)
	}
	if st.count == drawlist.ScorchMarkLimit {
		st.head = (st.head + 1) % drawlist.ScorchMarkLimit
		st.count--
	}
	st.marks[(st.head+st.count)%drawlist.ScorchMarkLimit] = m
	st.count++
}

// observeScorchMarks runs at every publication, including ticks between
// draws. EffectService supplies nonzero stable IDs, paired with EventSeq to
// distinguish queued cues from direct admissions; StartTick identifies the
// birth tick. Requiring that tick makes hidden, unresolved and pre-existing
// effects permanently ineligible, so no unbounded retired-ID cache is needed.
// Primary liveness/art remain separate from the calculated flash [03 §1]
// [06 R-WFX-01 §2]; neither flash size nor a generic glow flag admits a mark.
func (c *Client) observeScorchMarks(cur *frame.Frame) {
	if c == nil || cur == nil || !c.enhanced || c.terrain == nil {
		return
	}
	st := &c.scorch
	if st.valid && (cur.Tick < st.tick || cur.ViewingPlayer != st.viewer) {
		*st = scorchState{}
	}
	if st.valid && cur.Tick == st.tick {
		return
	}
	st.tick, st.viewer, st.valid = cur.Tick, cur.ViewingPlayer, true
	clear(st.seen)
	for st.count > 0 && cur.Tick-st.marks[st.head].born >= drawlist.ScorchLifeTicks {
		st.marks[st.head] = scorchMark{}
		st.head = (st.head + 1) % drawlist.ScorchMarkLimit
		st.count--
	}
	for _, v := range cur.Effects {
		if v.ID == 0 || v.StartTick != cur.Tick {
			continue
		}
		identity := scorchIdentity{id: v.ID, sequence: v.EventSeq}
		if _, exists := st.seen[identity]; exists || len(st.seen) == scorchIdentityLimit {
			continue
		}
		if st.seen == nil {
			st.seen = make(map[scorchIdentity]struct{})
		}
		st.seen[identity] = struct{}{}
		if !visibleExplosionSource(v, cur) {
			continue
		}
		art, ok := c.resolveEffectFrame(v, v.SeqA)
		if !ok || art == nil {
			continue
		}
		extent := c.resolveBlastSize(v)
		if extent <= 0 {
			continue
		}
		y, ok := c.scorchSurface(v.X, v.Z)
		if !ok || v.Y < y-scorchHeightTolerance || v.Y > y+scorchHeightTolerance {
			continue
		}
		// The projected ground anchor may occupy a different visibility cell
		// than the event above it [03 §2.5]. Both must be visible at birth.
		if !PointVisible(cur.Visibility, v.X, y, v.Z, 0, cur.ViewingPlayer) {
			continue
		}
		st.push(scorchMark{x: v.X, y: y, z: v.Z, born: v.StartTick,
			radius:  min(max(extent*0.55, 8), 64),
			variant: v.ID ^ v.StartTick})
	}
}

func (c *Client) scorchSurface(x, z numeric.Fixed) (numeric.Fixed, bool) {
	t := c.terrain
	// HeightAt's missing-grid fallback is zero; require real geometry before
	// sampling so absence cannot become dry ground. Negative is invalid [03 §2.3].
	if t == nil || t.CellW <= 1 || t.CellH <= 1 || int64(len(t.Plot)) < int64(t.CellW)*int64(t.CellH) {
		return 0, false
	}
	y := t.HeightAt(x, z)
	return y, y >= 0 && y >= t.SeaLevelWorld()
}

func (c *Client) drawScorchMarks(cur *frame.Frame) {
	if c == nil || cur == nil || !c.enhanced || c.cam == nil || c.terrain == nil || c.strategicView() {
		return
	}
	c.observeScorchMarks(cur)
	st := &c.scorch
	st.arena = st.arena[:0]
	if mark, ok := c.arrivalScorchMark(); ok {
		st.arena = append(st.arena, mark)
	}
	scale := float32(c.cam.EffectiveScale().Float())
	w, h := c.recordExtent()
	for n := 0; n < st.count; n++ {
		m := st.marks[(st.head+n)%drawlist.ScorchMarkLimit]
		age := float32(cur.Tick - m.born)
		if c.interpolation {
			age += c.TickFraction()
		}
		if age >= drawlist.ScorchLifeTicks {
			continue
		}
		sx, sy := c.cam.WorldToScreen(m.x, m.y, m.z)
		x, y := float32(sx-camera.OriginX), float32(sy-camera.OriginY)
		radius := m.radius * scale
		// The dry scorch envelope has a 0.7 vertical aspect (§29).
		margin := radius
		if x+margin < 0 || y+margin*0.7 < 0 || x-margin >= float32(w) || y-margin*0.7 >= float32(h) {
			continue
		}
		st.arena = append(st.arena, drawlist.ScorchMark{X: x, Y: y, Radius: radius,
			Age: age, Variant: m.variant})
	}
	if len(st.arena) > 0 {
		c.list.RecordScorchMarks(drawlist.ScorchMarks{Marks: st.arena})
	}
}
