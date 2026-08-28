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
// centers it in the effective viewport. The desired origin is clamped here;
// the current origin is deliberately not clamped by this helper [01 §4.4].
func (c *Camera) DesiredOrigin(target TargetPoint) Origin {
	if c == nil {
		return Origin{}
	}
	viewW, viewH := c.EffectiveView()
	return Origin{
		X: clampAxis(target.X-viewW/2, c.MapW, viewW),
		Z: clampAxis(target.Z-target.Y/2-viewH/2, c.MapH, viewH),
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
