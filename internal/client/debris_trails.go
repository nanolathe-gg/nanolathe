package client

// The debris draw's own two producers. `[04 R-COB-04 §2]`: the SMOKE and FIRE
// engine bits of a whole-piece debris record are read only by the draw pass —
// a rendered frame that draws a debris piece makes one strip-9 smoke-puff
// container (bit 1) and/or one flame-stream trail container (bit 0) at the
// piece. Neither touches simulation state or the simulation stream, so a
// headless simulation emits none.
//
// The containers go to the presentation-owned store of debris_trail_store.go,
// which steps them on the committed tick and draws them at barrier 9. That
// persistence IS the trail: a container made three ticks ago is still drawn,
// further into its animation and a little further downwind.
//
// Presentation only: nothing here writes simulation state, and every random
// value comes from the client's PRIVATE presentation CRT copy, never the
// session stream (DET-01) [I4][I6].

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// emitDebrisTrails runs both producers for one piece the debris pass has just
// drawn, and reports how many containers of each kind were created.
//
// The gate is the debris draw itself: the caller emits only after its own
// origin-visibility test has admitted the piece, which is the "rendered frame
// that draws a debris piece" of the contract. The containers themselves are
// re-tested at barrier 9 against the one-point coverage grid, like every other
// strip-9 smoke and flame record `[03 R-FX-01 §3]` — by the time a puff is
// three ticks old it has drifted away from the point that produced it.
//
// Cadence, and the one stated divergence. Retail's producer runs per RENDERED
// FRAME, so "one container is created per frame per burning piece, all
// overlapping" `[03 R-FX-01 §3]`. This build composes several frames per
// committed tick — interpolated frames between two ticks, the pre-record's
// re-record after a miss, and the `--shot-renderer both` route — while the
// containers are stepped by the TICK. Admitting one per rendered frame would
// therefore scale smoke density with the host's refresh rate and with whether a
// pre-record hit, so the producers run once per burning piece per committed
// tick, stamped per debris slot. That is retail's density at a frame rate equal
// to its tick rate; at a higher one retail's smoke is denser than ours. See
// docs/DESIGN_PRESENTATION_CLIENT.md C2.2.
func (c *Client) emitDebrisTrails(v frame.DebrisView) (smoke, fire int) {
	if c == nil || c.cam == nil {
		return 0, 0
	}
	if !v.Smoke && !v.Fire {
		return 0, 0
	}
	s := &c.debrisTrails
	// The per-slot stamp holds the producers to one run per committed tick. A
	// slot outside the fixed debris table is not stampable, so it is refused
	// rather than emitted unstamped: an unstamped piece would produce a
	// container on every rendered frame of the tick.
	if v.Slot < 0 || v.Slot >= len(s.emitted) {
		return 0, 0
	}
	if s.emitted[v.Slot] == c.frameTick+1 {
		return 0, 0
	}
	s.emitted[v.Slot] = c.frameTick + 1

	emission := presentationrender.DebrisTrails(v, c.presentationCRT(), c.frameTick, c.debrisTrailFrameCounts())
	for i := 0; i < emission.Count; i++ {
		container := emission.Containers[i]
		switch container.Family {
		case frame.StripFamilySmokePuff:
			smoke++
		case frame.StripFamilyFlameTrail:
			fire++
		}
		s.append(container)
	}
	return smoke, fire
}
