package camera

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// TargetPoint is a map-pixel point used by the phase-10 follow writer. Y is
// the vertical/shear component, not a screen coordinate [01 §4.4][07 §10].
type TargetPoint struct {
	X, Y, Z int32
}

// Origin is a camera origin in map pixels.
type Origin struct {
	X, Z int32
}

// DesiredOrigin converts a target point to the clamped camera origin that
// centers it in the battle viewport. The desired origin is clamped here;
// the current origin is deliberately not clamped by this helper [01 §4.4].
//
// Both halves of the conversion are the battle viewport's, not the
// framebuffer's: the recentre is BattleViewCenterOrigin's (retail's
// `point - viewportSpan/2` plus this build's leading-inset term, [07 R-CAM-01
// §12][03 §4.1]) and the clamp is the shared per-axis clamp of [07 §10]. Before
// 2026-08-31 this halved the framebuffer and clamped to framebuffer bounds, so a
// followed unit settled 64 map pixels right of the viewport's centre and could
// not be centred at all within 128 pixels of the map's west edge (defect
// PT5-01, the same frame-of-reference error the battle-start jump had).
//
// The height shear stays `target.Y/2` — a truncation toward zero — which is not
// the arithmetic shift [07 R-CRD-006 §1] specifies; that is a separate
// divergence and is deliberately not changed here.
func (c *Camera) DesiredOrigin(target TargetPoint) Origin {
	if c == nil {
		return Origin{}
	}
	spanW, spanH := c.BattleView()
	leadX, _, leadZ, _ := c.clampInsets()
	x, z := c.BattleViewCenterOrigin(target.X, target.Z-target.Y/2)
	return Origin{
		X: clampAxis(x, c.MapW, spanW, leadX),
		Z: clampAxis(z, c.MapH, spanH, leadZ),
	}
}

// LatchTracked captures the currently tracked object for this presentation
// frame's follow application and returns it. Retail runs phase 10 (follow and
// shake) once per completed sub-tick, strictly *before* that same host
// frame's hotkey dispatch step — so a `t`/`T` or Ctrl+C press only changes
// which object phase 10 chases starting with the *next* frame's pass, never
// the frame the key was pressed on [07 R-CAM-01 §1 steps 3-4][07 R-CAM-01
// §12 "Ctrl+C and t/T do not move the camera themselves"].
//
// Nanolathe's camera is presentation-only and does not interleave with
// simulation phases the way retail's phase 10 does [I6]; instead a frame's
// follow work is split across the presentation frame's own tick boundary:
// LatchTracked runs before this build's hotkey dispatch (preserving the
// next-frame timing above), and the caller applies the latch, via
// LatchedTracked, only after this frame's own simulation tick has published
// — so the position used is the one the frame's composer is about to draw,
// not the previous publish (defect PT6-01: reading the previous publish's
// position here made a followed unit's screen position swing by a
// tick's worth of its own motion every frame, reading as shake, sharpest
// whenever the frame's tick count varied, which the fixed presentation
// cadence of the "Run presentation work at 30 Hz" change did not by itself
// prevent — the two reads were still on either side of that frame's own
// publish).
func (c *Camera) LatchTracked() pool.Handle {
	if c == nil {
		return 0
	}
	c.Follow.latched = c.Follow.Tracked
	return c.Follow.latched
}

// LatchedTracked returns the object most recently captured by LatchTracked.
func (c *Camera) LatchedTracked() pool.Handle {
	if c == nil {
		return 0
	}
	return c.Follow.latched
}

// FollowTo steps the current camera origin toward target and returns the
// clamped desired origin. It does not clamp the resulting current origin;
// callers apply Camera.Clamp after any later follow-adjacent effects such as
// shake [01 §4.4][03 §5.6].
func (c *Camera) FollowTo(target TargetPoint) Origin {
	if c == nil {
		return Origin{}
	}
	desired := c.DesiredOrigin(target)
	c.X = stepAxis(c.X, desired.X)
	c.Z = stepAxis(c.Z, desired.Z)
	return desired
}

// stepAxis applies the phase-10 bounded signed half-step. The wider delta
// keeps absolute-value handling correct for all int32 origins [01 §4.4].
func stepAxis(current, desired int32) int32 {
	delta := int64(desired) - int64(current)
	switch {
	case delta > 320:
		return int32(int64(current) + 320)
	case delta < -320:
		return int32(int64(current) - 320)
	default:
		return int32(int64(current) + delta/2)
	}
}
