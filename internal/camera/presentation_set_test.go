package camera

import (
	"math"
	"testing"
)

// A scripted push must hold its centre exactly. Through the integer jump the
// recovered centre wanders by up to a world pixel per sample, which at 2x is a
// two-pixel lateral shimmer; the exact view keeps it to rounding noise.
func TestSetPresentationViewHoldsAScriptedCentre(t *testing.T) {
	const w, h = 1408, 784
	const cx, cz = 3000.25, 2000.75
	centre := func(v PresentationView) (float64, float64) {
		return v.X + float64(w+OriginX)/(2*v.Factor), v.Z + float64(h)/(2*v.Factor)
	}
	exact, jumped := 0.0, 0.0
	for tick := 0; tick <= 120; tick++ {
		f := 0.6 * math.Pow(2.0/0.6, float64(tick)/120)
		c := NewFromTerrain(8000, 8000, 0, 0, w, h)
		c.SetPresentationView(PresentationView{X: cx - float64(w+OriginX)/(2*f), Z: cz - float64(h)/(2*f), Factor: f})
		gx, gz := centre(c.PresentationView())
		exact = math.Max(exact, math.Max(math.Abs(gx-cx), math.Abs(gz-cz)))

		j := NewFromTerrain(8000, 8000, 0, 0, w, h)
		j.SetZoomAbout(OriginX, OriginY, Zoom(f*float64(ZoomUnit)+0.5))
		j.JumpToBattleViewCenter(int32(math.Floor(cx)), int32(math.Floor(cz)))
		jx, jz := centre(j.PresentationView())
		jumped = math.Max(jumped, math.Max(math.Abs(jx-cx), math.Abs(jz-cz)))
	}
	if exact > 1e-6 {
		t.Fatalf("exact view drifted %.6f world pixels from its scripted centre", exact)
	}
	if jumped < 0.5 {
		t.Fatalf("integer jump drifted only %.3f: this test no longer shows the defect it guards", jumped)
	}
}
