package camera

import (
	"math"
	"testing"
)

func TestPreciseZoomAnchorSurvivesCyclesAndExternalPan(t *testing.T) {
	c := &Camera{X: 4000, Z: 4000, ViewW: 1920, ViewH: 1080, MapW: 16384, MapH: 16384}
	const ax, ay = 813, 487
	wx, wz := float64(c.X)+ax, float64(c.Z)+ay
	var control ZoomController
	for _, target := range []Zoom{ZoomMax, ZoomUnit / 4, ZoomUnit, ZoomMax, ZoomUnit} {
		control.SetTarget(c, ax+OriginX, ay+OriginY, target)
		for control.Step(c) {
			v := c.PresentationView()
			if math.Abs((wx-v.X)*v.Factor-ax) > 1e-8 || math.Abs((wz-v.Z)*v.Factor-ay) > 1e-8 {
				t.Fatalf("anchor drift: %+v", v)
			}
		}
	}
	c.X += 100
	v := c.PresentationView()
	if v.X != float64(c.X) || v.Z != float64(c.Z) {
		t.Fatal("external camera move kept stale fractional placement")
	}
}

func TestPreciseZoomRespectsClampedAxis(t *testing.T) {
	c := &Camera{X: 0, Z: 4000, ViewW: 640, ViewH: 480, MapW: 16384, MapH: 16384}
	c.SetZoomAbout(500+OriginX, 200+OriginY, ZoomUnit/4)
	v := c.PresentationView()
	if v.X != float64(c.X) {
		t.Fatalf("clamped camera has extra displacement: %+v vs %d", v, c.X)
	}
	// The free axis still holds its anchor.
	if math.Abs((4200-v.Z)*v.Factor-200) > 1e-8 {
		t.Fatal("clamping X discarded the Z anchor")
	}
}
