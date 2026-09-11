package camera

import "testing"

func feelCamera() *Camera {
	return &Camera{X: 1200, Z: 900, ViewW: 1024, ViewH: 768, MapW: 16384, MapH: 16384}
}

// One conventional wheel click advances one stop when the cooldown is ready
// (DESIGN_GPU_RENDERER §16.6), including the full journey through native zoom.
func TestWheelStepsThroughTheZoomList(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	now := uint32(0)
	for _, tc := range []struct {
		dy   float64
		want Zoom
	}{
		{1, ZoomMax}, {1, ZoomMax}, {-1, ZoomUnit},
		{-1, ZoomUnit / 2}, {-1, ZoomUnit / 2}, {1, ZoomUnit},
	} {
		z.Wheel(cam, 500, 300, tc.dy, now)
		if got := z.Target(cam); got != tc.want {
			t.Fatalf("scroll %v at %d: %s, want %s", tc.dy, now, got, tc.want)
		}
		now += 500
	}
}

// Fractional scroll input accumulates up to one unit and reversals discard
// the old direction's remainder (§16.6).
func TestWheelFractionsBankIntoWholeSteps(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.Wheel(cam, 500, 300, 0.4, 0)
	z.Wheel(cam, 500, 300, 0.599, 10)
	if got := z.Target(cam); got != ZoomUnit {
		t.Fatalf("scroll below threshold stepped to %s", got)
	}
	z.Wheel(cam, 500, 300, 0.001, 20)
	if got := z.Target(cam); got != ZoomMax {
		t.Fatalf("one scroll unit gave %s, want %s", got, ZoomMax)
	}
	z.Wheel(cam, 500, 300, 0.1, 520)
	z.Wheel(cam, 500, 300, -0.95, 530)
	if got := z.Target(cam); got != ZoomMax {
		t.Fatalf("reversal below threshold stepped to %s", got)
	}
	z.Wheel(cam, 500, 300, -0.05, 540)
	if got := z.Target(cam); got != ZoomUnit {
		t.Fatalf("one reversed scroll unit gave %s, want %s", got, ZoomUnit)
	}
}

// A large gesture cannot skip 1x; events during the 500ms hold neither queue
// travel nor extend the deadline. Host time also works across counter wrap.
func TestScrollCooldownHoldsNativeAndDiscardsExcess(t *testing.T) {
	for _, start := range []uint32{0, ^uint32(0) - 200} {
		for _, dy := range []float64{-20, 20} {
			cam := feelCamera()
			from, end := ZoomMax, ZoomUnit/2
			if dy > 0 {
				from, end = end, from
			}
			cam.Zoom, cam.Scale = from, from.Step()
			var z ZoomController
			z.Wheel(cam, 500, 300, dy, start)
			if z.Target(cam) != ZoomUnit {
				t.Fatal("large gesture skipped native")
			}
			for _, elapsed := range []uint32{1, 200, 499} {
				z.Wheel(cam, 500, 300, dy, start+elapsed)
				if z.Target(cam) != ZoomUnit {
					t.Fatal("cooldown let another step through")
				}
			}
			// Easing/idle cannot spend the ignored events after the hold ends.
			for i := 0; i < 100; i++ {
				z.Step(cam)
			}
			z.Wheel(cam, 500, 300, 0, start+500)
			if cam.EffectiveZoom() != ZoomUnit || z.Target(cam) != ZoomUnit {
				t.Fatal("ignored scroll was queued")
			}
			// At the deadline, start a fresh fractional gesture. Excess from
			// the original burst and the cooldown must not finish it early.
			z.Wheel(cam, 500, 300, dy/40, start+500)
			if z.Target(cam) != ZoomUnit {
				t.Fatal("old travel leaked into next gesture")
			}
			z.Wheel(cam, 500, 300, dy/40, start+500)
			if z.Target(cam) != end {
				t.Fatalf("deadline scroll gave %s, want %s", z.Target(cam), end)
			}
		}
	}
}

// F9 and reset are explicit controls; neither waits for a scroll cooldown.
func TestExplicitZoomClearsScrollCooldown(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.Wheel(cam, 500, 300, 1, 0)
	z.SetTarget(cam, 500, 300, ZoomUnit/2)
	if z.Target(cam) != ZoomUnit/2 {
		t.Fatal("explicit target delayed")
	}
	z.Wheel(cam, 500, 300, 1, 1)
	if z.Target(cam) != ZoomUnit {
		t.Fatal("explicit target retained cooldown")
	}
	z.Reset()
	z.Wheel(cam, 500, 300, 1, 2)
	if z.Target(cam) != ZoomMax {
		t.Fatal("reset retained cooldown")
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
	z.Wheel(cam, 500, 300, -1, 0)
	if got := z.Target(cam); got != minZ {
		t.Fatalf("a step out from 1x on a small map gave %s, want the floor %s", got, minZ)
	}
	z.Wheel(cam, 500, 300, -1, 500)
	if got := z.Target(cam); got != minZ {
		t.Fatalf("a step out from the floor gave %s, want it to stay", got)
	}
	z.Wheel(cam, 500, 300, 1, 500)
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
