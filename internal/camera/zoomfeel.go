package camera

import "math"

// Stepped zoom's feel-tuning knobs and the state machine that spends them
// (DESIGN_GPU_RENDERER §16.6, §16.7).
//
// EVERY constant in this block is a FEEL-TUNING KNOB. None of them is a retail
// finding — retail has one view scale and no wheel zoom at all — and none of
// them is derived from anything. They live together here so they can be tuned
// by hand in one place; changing one changes only how the zoom feels.
const (
	// ZoomScrollThreshold is the travel required for one zoom step, in
	// thousandths of an Ebitengine wheel unit. Three units require a longer
	// trackpad gesture than the former one-unit threshold. Mouse wheels share
	// the same input path and threshold. Positive wheel Y zooms in.
	ZoomScrollThreshold int32 = 3000

	// ZoomEaseFraction is how much of the remaining gap the live factor closes
	// per host Update. It is an exponential ease: 0.30 closes about 83% of a gap
	// in five Updates, a sixth of a second at the 30 Hz Update grid.
	ZoomEaseFraction = 0.30

	// ZoomSettleEpsilon is how close the live factor has to be to the target
	// before it is snapped onto it and the animation stops, in 1/ZoomUnit units.
	// Without it the exponential ease never terminates.
	ZoomSettleEpsilon Zoom = 2
)

// ZoomSteps are the modern wheel and F9 targets, ascending (§16.6): a
// tactical overview, the default native view, and the detail view. The lowest
// target is clamped to MinZoom when the map cannot fill the viewport at 0.5x.
// These are presentation feel choices, like the constants above.
var ZoomSteps = [3]Zoom{
	ZoomUnit / 2, // 0.5x
	ZoomUnit,     // 1x
	ZoomMax,      // 2x
}

// NextZoomStep is the next zoom stop in the requested direction: the first step
// strictly above it when in is set, the first strictly below it otherwise, and
// false at either end of the list. A factor between two steps — the ease in
// flight, a clamped floor, a free `--zoom` — goes to the nearest step in the
// direction of travel, so the wheel always lands on a step.
func NextZoomStep(current Zoom, in bool) (Zoom, bool) {
	if in {
		for _, s := range ZoomSteps {
			if s > current {
				return s, true
			}
		}
		return 0, false
	}
	for i := len(ZoomSteps) - 1; i >= 0; i-- {
		if ZoomSteps[i] < current {
			return ZoomSteps[i], true
		}
	}
	return 0, false
}

// ZoomController is the stepped-zoom state machine of §16.6: a target the
// wheel and F9 write, always one of ZoomSteps or the map's floor, and a live
// factor that eases toward it on the host Update grid.
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
	// travel is the wheel movement banked toward the next zoom step, in
	// thousandths of a wheel unit, signed: a trackpad's fractions add up here
	// until they are worth a step.
	travel int32
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

// Wheel spends one host frame's wheel delta about the screen point (mx, my):
// every ZoomScrollThreshold of travel moves the target one step along ZoomSteps,
// in for a positive delta (a scroll up) and out for a negative one. Travel
// short of the threshold is banked; travel in the opposite direction discards what
// is banked, so a trackpad that drifts back does not step.
//
// The float64 is the wheel's own unit and is consumed here; the stored travel
// is an integer, so nothing holds a floating-point factor [I2].
func (z *ZoomController) Wheel(cam *Camera, mx, my int32, dy float64) {
	if z == nil || cam == nil || dy == 0 {
		return
	}
	units := int32(math.Round(dy * 1000))
	if units == 0 {
		return
	}
	if (units > 0) != (z.travel > 0) {
		z.travel = 0
	}
	z.travel += units
	for z.travel >= ZoomScrollThreshold {
		z.travel -= ZoomScrollThreshold
		z.stepTarget(cam, mx, my, true)
	}
	for z.travel <= -ZoomScrollThreshold {
		z.travel += ZoomScrollThreshold
		z.stepTarget(cam, mx, my, false)
	}
}

// stepTarget moves the target one step in the given direction, when there is
// one. Stepping out below the map's floor lands on the floor (setTarget
// clamps); from the floor a further step out is refused, because every step
// that remains is below it.
func (z *ZoomController) stepTarget(cam *Camera, mx, my int32, in bool) {
	current := z.Target(cam)
	next, ok := NextZoomStep(current, in)
	if !ok {
		return
	}
	if !in && current <= cam.MinZoom() {
		return
	}
	z.setTarget(cam, mx, my, next)
}

// SetTarget aims the zoom at a factor about (mx, my) without a wheel gesture —
// F9's cycle and the entry factor take this route (§16.8).
func (z *ZoomController) SetTarget(cam *Camera, mx, my int32, want Zoom) {
	if z == nil || cam == nil {
		return
	}
	z.setTarget(cam, mx, my, want)
	z.travel = 0
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
// the target about the stored anchor (§16.6). It reports whether the camera
// moved.
//
// The clock is the caller's Update, which the platform layer paces from host
// time; no simulation tick is read [I6].
func (z *ZoomController) Step(cam *Camera) bool {
	if z == nil || cam == nil || z.target <= 0 {
		return false
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
