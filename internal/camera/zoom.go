package camera

import (
	"fmt"
	"strconv"
	"strings"
)

// Zoom is the LIVE presentation zoom factor [F-P1-008], in 1/ZoomUnit units —
// the number of screen pixels one world pixel covers, times 1024
// (DESIGN_GPU_RENDERER §16.2).
//
// It is the free counterpart of ViewScale. ViewScale stays the *recording*
// step: the recorder projects every world command at 1x or 2x and both
// executors replay those coordinates unchanged, which is what keeps the §6
// parity gate exact. Zoom is what the player actually sees; the modern
// executor scales the recorded world by Zoom/step before it reaches the phase
// scheduler, and everything that measures the view in world pixels — the
// clamp, the insets, picking, middle-drag, the minimap rectangle — measures it
// through Zoom.
//
// The unit is 1/1024 rather than 16.16 so that the three rest steps are exact
// small integers (1024, 1536, 2048) and every product below stays inside
// int64 without a shift convention of its own. It is presentation state; no
// simulation phase reads it [I6].
//
// At the three rest steps Project, Inverse and Px agree with ViewScale's own
// arithmetic pixel for pixel, so a camera whose Zoom is its step's factor
// composes exactly what the build before this type composed.
type Zoom int32

const (
	// ZoomUnit is f = 1.0, the retail picture.
	ZoomUnit Zoom = 1024
	// ZoomMax is f = 2.0, the detail view and the ceiling of the free range.
	ZoomMax Zoom = 2 * ZoomUnit
	// ZoomFloor is the lowest factor the flag layer accepts before the
	// map-derived minimum of MinZoomFor is applied. It exists only so a
	// nonsense `--zoom 0` or `--zoom 0.001` is rejected where it is typed; the
	// real floor is the map's own (§16.7).
	ZoomFloor Zoom = ZoomUnit / 16 // 0.0625
)

// ZoomOf is a rest step's factor: native 1024, mid 1536, detail 2048.
func ZoomOf(s ViewScale) Zoom {
	return Zoom(s.Norm()) * (ZoomUnit / 2)
}

// Norm clamps a factor to the usable range, reading zero (and anything at or
// below zero) as the native factor so a zero Zoom field means "follow the
// step".
func (z Zoom) Norm() Zoom {
	if z <= 0 {
		return ZoomUnit
	}
	if z > ZoomMax {
		return ZoomMax
	}
	return z
}

// Step is the RECORDING step a live factor records at (§16.2): the detail step
// above 1x, the native step at or below it. The recorder emits world commands
// at this step and the modern executor scales them by z/Step; at z == the
// step's own factor that scale is one and the output is byte-identical to the
// build before this type.
func (z Zoom) Step() ViewScale {
	if z.Norm() > ZoomUnit {
		return ViewScaleDetail
	}
	return ViewScaleNative
}

// Project scales a world-relative offset to the first screen pixel it covers:
// ceil(v·z/ZoomUnit). It is ViewScale.Project generalized, and equals it
// exactly at the three rest factors.
func (z Zoom) Project(v int32) int32 {
	n := int64(z.Norm())
	p := int64(v) * n
	if p >= 0 {
		return int32((p + int64(ZoomUnit) - 1) / int64(ZoomUnit))
	}
	// Truncation toward zero is ceil for a negative value.
	return int32(p / int64(ZoomUnit))
}

// Inverse maps a screen-relative offset back to the world pixel drawn there:
// floor(v·ZoomUnit/z) [I3][03 §2.1]. It is the exact inverse of Project at the
// rest factors and the obvious floor in flight (§16.4).
func (z Zoom) Inverse(v int32) int32 {
	n := int64(z.Norm())
	return int32(floorDiv(int64(v)*int64(ZoomUnit), n))
}

// Px scales a screen extent or authored offset — a radius, a half-width — and
// rounds half away from zero so a symmetric extent stays symmetric. It is
// ViewScale.Px generalized.
func (z Zoom) Px(v int32) int32 {
	n := int64(z.Norm())
	p := int64(v) * n
	half := int64(ZoomUnit) / 2
	if p >= 0 {
		return int32((p + half) / int64(ZoomUnit))
	}
	return int32((p - half) / int64(ZoomUnit))
}

// Float is the factor as a plain number, for a shader lane, a metadata record
// or a diagnostic line. It is presentation-only, so it is not an I2 concern.
func (z Zoom) Float() float64 { return float64(z.Norm()) / float64(ZoomUnit) }

// String is the factor the user reads, trimmed of trailing zeroes: "1x",
// "1.5x", "0.45x".
func (z Zoom) String() string {
	s := strconv.FormatFloat(z.Float(), 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s + "x"
}

// ParseZoom reads a free factor the way `--zoom` spells it for the modern
// executor: any decimal in (0, 2], with an optional trailing "x". The classic
// executor takes ParseViewScale instead, which accepts only the three steps
// (§16.8).
func ParseZoom(text string) (Zoom, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(text), "x")
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("view scale must be a factor in %.4g..2, got %q", ZoomFloor.Float(), text)
	}
	z := Zoom(v*float64(ZoomUnit) + 0.5)
	if z < ZoomFloor || z > ZoomMax {
		return 0, fmt.Errorf("view scale must be a factor in %.4g..2, got %q", ZoomFloor.Float(), text)
	}
	return z, nil
}

// MinZoomFor is the minimum zoom of §16.7: the factor at which the view in
// world pixels equals the playable map in whichever axis would first exceed
// it, so the camera clamp never has to letterbox.
//
// The battle viewport is measured in framebuffer pixels; the world it shows is
// that many pixels divided by the factor, so the view fits the map when
// f >= viewSpan/mapSpan in both axes, and the floor is the larger of the two.
// A degenerate map or viewport has no floor and returns ZoomFloor.
//
// The viewport spans passed in are the battle viewport's own — the framebuffer
// less the chrome insets — because that is the rectangle the world is seen
// through [03 §4.1].
func MinZoomFor(viewW, viewH, mapW, mapH int32) Zoom {
	z := ZoomFloor
	if mapW > 0 && viewW > 0 {
		// ceil so the view never ends up one world pixel wider than the map.
		zx := Zoom((int64(viewW)*int64(ZoomUnit) + int64(mapW) - 1) / int64(mapW))
		if zx > z {
			z = zx
		}
	}
	if mapH > 0 && viewH > 0 {
		zy := Zoom((int64(viewH)*int64(ZoomUnit) + int64(mapH) - 1) / int64(mapH))
		if zy > z {
			z = zy
		}
	}
	if z > ZoomMax {
		z = ZoomMax
	}
	return z
}

// MinZoom is MinZoomFor for this camera: the battle viewport measured in
// framebuffer pixels against the playable map extents the clamp uses.
//
// The viewport span is taken at the native factor deliberately — the chrome is
// drawn in framebuffer pixels and does not move with the zoom, so the span
// being fitted is a constant of the window, not of the current factor.
func (c *Camera) MinZoom() Zoom {
	if c == nil {
		return ZoomFloor
	}
	viewW := c.ViewW - OriginX
	viewH := c.ViewH - 2*OriginY
	return MinZoomFor(viewW, viewH, c.MapW, c.MapH)
}

// ViewScaleForZoom names the rest STEP a factor is exactly on, and false for
// every factor between them. The classic executor takes this route: it has no
// free zoom, so a factor it is given must be one of the three views and is
// recorded — and drawn — at that step's own art and arithmetic
// (DESIGN_GPU_RENDERER §16.8). The modern executor takes Zoom.Step instead,
// which records 1.5x at the 2x step and shrinks it.
func ViewScaleForZoom(z Zoom) (ViewScale, bool) {
	for _, s := range [3]ViewScale{ViewScaleNative, ViewScaleMid, ViewScaleDetail} {
		if ZoomOf(s) == z.Norm() {
			return s, true
		}
	}
	return ViewScaleNative, false
}
