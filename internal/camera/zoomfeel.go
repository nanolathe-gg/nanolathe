package camera

import "math"

// Smooth zoom's feel-tuning knobs and the state machine that spends them
// (DESIGN_GPU_RENDERER §16.6, §16.7).
//
// EVERY constant in this block is a FEEL-TUNING KNOB. None of them is a retail
// finding — retail has one view scale and no wheel zoom at all — and none of
// them is derived from anything. They live together here so they can be tuned
// by hand in one place; changing one changes only how the zoom feels.
const (
	// ZoomWheelExponent is the log-scale wheel sensitivity: one wheel unit
	// multiplies the target by 2^ZoomWheelExponent, so the factor doubles over
	// 1/ZoomWheelExponent units and a trackpad's fractional deltas compose the
	// same way a notched wheel's whole ones do. Wheel-up (a positive Ebitengine
	// wheel Y) zooms in.
	ZoomWheelExponent = 0.12

	// ZoomEaseFraction is how much of the remaining gap the live factor closes
	// per host Update. It is an exponential ease: 0.30 closes about 83% of a gap
	// in five Updates, a sixth of a second at the 30 Hz Update grid.
	ZoomEaseFraction = 0.30

	// ZoomSettleEpsilon is how close the live factor has to be to the target
	// before it is snapped onto it and the animation stops, in 1/ZoomUnit units.
	// Without it the exponential ease never terminates.
	ZoomSettleEpsilon Zoom = 2

	// ZoomIdleUpdates is how many host Updates the wheel must be quiet before
	// the target eases onto a rest step, at the 30 Hz Update grid — 6 is a fifth
	// of a second.
	ZoomIdleUpdates = 6

	// ZoomSnapBand is how near a rest step the target has to be for the idle
	// snap to take it, in 1/ZoomUnit units. 40/1024 is a shade under 4%.
	ZoomSnapBand Zoom = 40
)

// ZoomRestSteps are the factors the idle snap eases onto (§16.6). There is
// deliberately nothing below 1x in the list: the strategic range is continuous
// and has no preferred stopping point, and snapping there would fight a player
// pulling out to look at the map.
var ZoomRestSteps = [5]Zoom{
	ZoomUnit,                // 1x
	ZoomUnit + ZoomUnit/4,   // 1.25x
	ZoomUnit + ZoomUnit/2,   // 1.5x
	ZoomUnit + 3*ZoomUnit/4, // 1.75x
	ZoomMax,                 // 2x
}

// ZoomController is the smooth-zoom state machine of §16.6: a target the wheel
// and F9 write, a live factor that eases toward it on the host Update grid, and
// the idle snap onto a rest step.
//
// It is presentation state and is driven from the platform layer's Update, so
// its clock is host Updates and never simulation time [I6]. It holds no camera:
// each call takes the one it drives, which keeps the battle session the single
// owner of the camera.
type ZoomController struct {
	// target is the factor the live one is easing toward; zero means "no zoom
	// in flight", which is the state a camera that has never been zoomed is in.
	target Zoom
	// anchorX, anchorY is the beam-space screen point the zoom is taken about,
	// so the world point under it stays put for the whole animation.
	anchorX, anchorY int32
	anchored         bool
	// idle counts host Updates since the last wheel movement; snapped records
	// that the idle snap has already fired for this gesture.
	idle    int32
	snapped bool
}

// Target reports the factor the controller is easing toward, falling back to
// the camera's live factor when nothing is in flight.
func (z *ZoomController) Target(cam *Camera) Zoom {
	if z == nil || z.target <= 0 {
		return cam.EffectiveZoom()
	}
	return z.target
}

// Active reports whether a zoom is still in flight, which is what the caller
// uses to decide whether this Update has to touch the camera at all.
func (z *ZoomController) Active(cam *Camera) bool {
	if z == nil || z.target <= 0 || cam == nil {
		return false
	}
	return z.target != cam.EffectiveZoom()
}

// Wheel spends one host frame's wheel delta on the target, on the log scale of
// ZoomWheelExponent, about the screen point (mx, my). dy is Ebitengine's wheel
// Y: positive is a scroll up, which zooms in.
//
// The float64 is a transient of the exponential; the stored target is the
// integer Zoom, so nothing here holds a floating-point factor [I2].
func (z *ZoomController) Wheel(cam *Camera, mx, my int32, dy float64) {
	if z == nil || cam == nil || dy == 0 {
		return
	}
	base := z.Target(cam)
	next := Zoom(math.Round(base.Float() * math.Exp2(ZoomWheelExponent*dy) * float64(ZoomUnit)))
	z.setTarget(cam, mx, my, next)
	z.idle = 0
	z.snapped = false
}

// SetTarget aims the zoom at a factor about (mx, my) without a wheel gesture —
// F9's cycle and the entry factor take this route (§16.8). It counts as an
// intentional target, so the idle snap leaves it alone.
func (z *ZoomController) SetTarget(cam *Camera, mx, my int32, want Zoom) {
	if z == nil || cam == nil {
		return
	}
	z.setTarget(cam, mx, my, want)
	z.idle = 0
	z.snapped = true
}

// setTarget clamps a requested factor into the camera's usable range and
// records the anchor the animation is taken about.
func (z *ZoomController) setTarget(cam *Camera, mx, my int32, want Zoom) {
	if want > ZoomMax {
		want = ZoomMax
	}
	if minZ := cam.MinZoom(); want < minZ {
		want = minZ
	}
	z.target = want
	z.anchorX, z.anchorY, z.anchored = mx, my, true
}

// Reset abandons any zoom in flight, which is what a route that jumps the
// camera outright (a battle restart, a save restore) wants.
func (z *ZoomController) Reset() {
	if z == nil {
		return
	}
	*z = ZoomController{}
}

// Step advances the zoom by one host Update: it eases the live factor toward
// the target about the stored anchor, and once the wheel has been idle for
// ZoomIdleUpdates it eases the target itself onto a rest step when it is inside
// ZoomSnapBand of one (§16.6). It reports whether the camera moved.
//
// The clock is the caller's Update, which the platform layer paces from host
// time; no simulation tick is read [I6].
func (z *ZoomController) Step(cam *Camera) bool {
	if z == nil || cam == nil || z.target <= 0 {
		return false
	}
	if z.idle < ZoomIdleUpdates {
		z.idle++
	}
	if z.idle >= ZoomIdleUpdates && !z.snapped {
		z.snapped = true
		if rest, ok := SnapRestStep(z.target); ok {
			z.target = rest
		}
	}
	live := cam.EffectiveZoom()
	if live == z.target {
		return false
	}
	next := easeZoom(live, z.target)
	mx, my := z.anchorX, z.anchorY
	if !z.anchored {
		mx, my = OriginX, OriginY
	}
	cam.SetZoomAbout(mx, my, next)
	// The camera's own floor may have refused the target outright (a map the
	// view already covers); adopt what it took so the ease terminates.
	if got := cam.EffectiveZoom(); got != next && (got > z.target) == (next > z.target) {
		z.target = got
	}
	return true
}

// easeZoom is one Update of the exponential ease: close ZoomEaseFraction of the
// remaining gap, always move at least one unit so an integer factor cannot
// stall, and settle outright inside ZoomSettleEpsilon.
func easeZoom(live, target Zoom) Zoom {
	gap := int64(target) - int64(live)
	if gap <= int64(ZoomSettleEpsilon) && gap >= -int64(ZoomSettleEpsilon) {
		return target
	}
	stepped := int64(float64(gap) * ZoomEaseFraction)
	if stepped == 0 {
		if gap > 0 {
			stepped = 1
		} else {
			stepped = -1
		}
	}
	return live + Zoom(stepped)
}

// SnapRestStep reports the rest step a target eases onto after the idle delay,
// and false when there is none: nothing below 1x snaps, and nothing further
// than ZoomSnapBand from a step does either (§16.6).
func SnapRestStep(target Zoom) (Zoom, bool) {
	if target < ZoomUnit {
		return 0, false
	}
	for _, rest := range ZoomRestSteps {
		d := target - rest
		if d < 0 {
			d = -d
		}
		if d <= ZoomSnapBand {
			return rest, true
		}
	}
	return 0, false
}
