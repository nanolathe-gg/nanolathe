package camera

import "testing"

func feelCamera() *Camera {
	return &Camera{X: 1200, Z: 900, ViewW: 1024, ViewH: 768, MapW: 16384, MapH: 16384}
}

// The wheel is a STEPPED control: each threshold moves the target to the next of
// ZoomSteps, in for a scroll up and out for a scroll down, and a run of
// steps walks the list end to end without overshooting it (§16.6).
func TestWheelStepsThroughTheZoomList(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomUnit)
	want := []Zoom{ZoomMax, ZoomMax}
	for i, w := range want {
		z.Wheel(cam, 500, 300, 3)
		if got := z.Target(cam); got != w {
			t.Fatalf("step %d in from 1x gave %s, want %s", i+1, got, w)
		}
	}
	want = []Zoom{ZoomUnit, ZoomUnit / 2, ZoomUnit / 2}
	for i, w := range want {
		z.Wheel(cam, 500, 300, -3)
		if got := z.Target(cam); got != w {
			t.Fatalf("step %d out from 2x gave %s, want %s", i+1, got, w)
		}
	}
}

// Scroll fractions bank until the three-unit threshold is reached; reversing
// direction discards the remainder (§16.6). Small gestures must not zoom.
func TestWheelFractionsBankIntoWholeSteps(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomUnit)
	z.Wheel(cam, 500, 300, 1.4)
	z.Wheel(cam, 500, 300, 1.4)
	if got := z.Target(cam); got != ZoomUnit {
		t.Fatalf("2.8 scroll units stepped to %s", got)
	}
	z.Wheel(cam, 500, 300, 0.199)
	if got := z.Target(cam); got != ZoomUnit {
		t.Fatalf("scroll just below the threshold stepped to %s", got)
	}
	z.Wheel(cam, 500, 300, 0.001)
	if got := z.Target(cam); got != ZoomMax {
		t.Fatalf("three scroll units gave %s, want %s", got, ZoomMax)
	}
	z.Wheel(cam, 500, 300, 0.1)
	// Reversal discards the 0.1 remainder. A new 2.95-unit gesture is still
	// short of a step. The next 0.05 must complete it without the old remainder.
	z.Wheel(cam, 500, 300, -2.95)
	if got := z.Target(cam); got != ZoomMax {
		t.Fatalf("a reversal below the threshold stepped to %s", got)
	}
	z.Wheel(cam, 500, 300, -0.05)
	if got := z.Target(cam); got != ZoomUnit {
		t.Fatalf("three reversed scroll units gave %s, want %s", got, ZoomUnit)
	}
	// One large delta spends multiple steps and stops at the lowest target.
	z.Wheel(cam, 500, 300, -6)
	if got := z.Target(cam); got != ZoomUnit/2 {
		t.Fatalf("two steps out from 1x gave %s, want %s", got, ZoomUnit/2)
	}
}

// A target between steps — the ease in flight, a free --zoom — goes to the
// nearest step in the direction of travel, so the wheel always lands on a step
// (§16.6).
func TestWheelFromBetweenStepsLandsOnAStep(t *testing.T) {
	for _, tc := range []struct {
		from Zoom
		in   bool
		want Zoom
	}{
		{ZoomUnit * 13 / 10, true, ZoomMax},
		{ZoomUnit * 13 / 10, false, ZoomUnit},
		{ZoomUnit * 3 / 5, false, ZoomUnit / 2},
		{ZoomUnit * 3 / 5, true, ZoomUnit},
	} {
		got, ok := NextZoomStep(tc.from, tc.in)
		if !ok || got != tc.want {
			t.Errorf("from %s in=%v: %s,%v want %s", tc.from, tc.in, got, ok, tc.want)
		}
	}
}

// Stepping out stops at the map's own floor: the step below it is clamped to
// the floor, and from the floor a further step out is refused rather than
// aimed below it (§16.7).
func TestWheelOutStopsAtTheMapFloor(t *testing.T) {
	cam := &Camera{X: 100, Z: 100, ViewW: 1024, ViewH: 768, MapW: 1280, MapH: 1280}
	minZ := cam.MinZoom()
	if minZ <= ZoomSteps[0] || minZ >= ZoomSteps[1] {
		t.Fatalf("fixture floor %s is not between the two lowest steps", minZ)
	}
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomSteps[1])
	z.Wheel(cam, 500, 300, -3)
	if got := z.Target(cam); got != minZ {
		t.Fatalf("a step out from 1x on a small map gave %s, want the floor %s", got, minZ)
	}
	z.Wheel(cam, 500, 300, -3)
	if got := z.Target(cam); got != minZ {
		t.Fatalf("a step out from the floor gave %s, want it to stay", got)
	}
	z.Wheel(cam, 500, 300, 3)
	if got := z.Target(cam); got != ZoomSteps[1] {
		t.Fatalf("a step in from the floor gave %s, want %s", got, ZoomSteps[1])
	}
}

// The ease terminates: repeated Updates reach the target exactly and then stop
// reporting movement, so the camera does not creep for the rest of the battle
// (§16.6).
func TestTheEaseReachesTheTargetAndStops(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomUnit)
	z.Step(cam)
	z.SetTarget(cam, 500, 300, ZoomMax)
	for i := 0; i < 200; i++ {
		if !z.Step(cam) {
			break
		}
	}
	if got := cam.EffectiveZoom(); got != ZoomMax {
		t.Fatalf("the ease settled at %s, want %s", got, ZoomMax)
	}
	if z.Step(cam) {
		t.Fatal("the ease still reports movement after settling")
	}
	if !cam.AtRestStep() {
		t.Fatalf("settling on 2x left the camera off its step: factor %s, step %s", cam.EffectiveZoom(), cam.EffectiveScale())
	}
}
