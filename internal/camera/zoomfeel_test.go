package camera

import (
	"math"
	"testing"
)

func feelCamera() *Camera {
	return &Camera{X: 1200, Z: 900, ViewW: 1024, ViewH: 768, MapW: 16384, MapH: 16384}
}

// The wheel is a LOG-scale control: one unit multiplies the target by
// 2^ZoomWheelExponent whatever the target already is, and a trackpad's
// fractional deltas compose the same way a notched wheel's whole ones do
// (§16.6). Composing two half-units must land where one whole unit lands.
func TestWheelAccumulatesOnTheLogScale(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomUnit)
	z.Wheel(cam, 500, 300, 1)
	one := z.Target(cam)
	want := Zoom(math.Round(float64(ZoomUnit) * math.Exp2(ZoomWheelExponent)))
	if one != want {
		t.Fatalf("one wheel unit from 1x gave %d, want %d", one, want)
	}

	cam2 := feelCamera()
	var z2 ZoomController
	z2.SetTarget(cam2, 500, 300, ZoomUnit)
	z2.Wheel(cam2, 500, 300, 0.5)
	z2.Wheel(cam2, 500, 300, 0.5)
	if got := z2.Target(cam2); got < one-2 || got > one+2 {
		t.Fatalf("two half units gave %d, want about %d", got, one)
	}

	// Wheel-up zooms in, wheel-down out, and the two are inverse to rounding.
	z.Wheel(cam, 500, 300, -1)
	if got := z.Target(cam); got < ZoomUnit-2 || got > ZoomUnit+2 {
		t.Fatalf("up then down gave %d, want about %d", got, ZoomUnit)
	}
}

// After the wheel has been idle the target eases onto a rest step, but only
// when it is inside the snap band and only at or above 1x: the strategic range
// has no preferred stopping point (§16.6).
func TestIdleSnapOnlyInsideTheBandAndOnlyAboveOneX(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Zoom
		want   Zoom
		snap   bool
	}{
		{"just under 1.5x", ZoomRestSteps[2] - ZoomSnapBand + 1, ZoomRestSteps[2], true},
		{"just over 1.25x", ZoomRestSteps[1] + ZoomSnapBand - 1, ZoomRestSteps[1], true},
		{"outside every band", ZoomRestSteps[1] + ZoomSnapBand + 8, 0, false},
		{"below 1x", ZoomUnit - 2, 0, false},
		{"well below 1x", ZoomUnit / 2, 0, false},
	} {
		got, ok := SnapRestStep(tc.target)
		if ok != tc.snap || (ok && got != tc.want) {
			t.Errorf("%s: SnapRestStep(%d) = %d,%v; want %d,%v", tc.name, tc.target, got, ok, tc.want, tc.snap)
		}
	}
}

// The snap does not fire until the wheel has been quiet for the idle delay, and
// it fires once (§16.6). Stepping before the delay must leave the target where
// the wheel left it.
func TestSnapWaitsForTheIdleDelay(t *testing.T) {
	cam := feelCamera()
	var z ZoomController
	z.SetTarget(cam, 500, 300, ZoomUnit)
	// A wheel gesture that lands just under 1.5x.
	near := ZoomRestSteps[2] - ZoomSnapBand + 2
	z.Wheel(cam, 500, 300, 0)
	z.setTarget(cam, 500, 300, near)
	z.idle, z.snapped = 0, false

	for i := 0; i < ZoomIdleUpdates-1; i++ {
		z.Step(cam)
		if z.Target(cam) != near {
			t.Fatalf("the target snapped after %d updates, before the %d-update delay", i+1, ZoomIdleUpdates)
		}
	}
	z.Step(cam)
	if got := z.Target(cam); got != ZoomRestSteps[2] {
		t.Fatalf("after the idle delay the target is %d, want the 1.5x step %d", got, ZoomRestSteps[2])
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
