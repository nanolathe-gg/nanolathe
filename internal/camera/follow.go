package camera

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
