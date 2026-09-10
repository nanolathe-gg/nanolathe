package camera

import "testing"

// The free factor generalizes the three half steps: at each of them Project,
// Inverse and Px must answer exactly what ViewScale's own arithmetic answers,
// which is what makes a camera on a rest factor compose the picture the build
// before DESIGN_GPU_RENDERER §16 composed (§16.2).
func TestZoomAgreesWithTheStepsAtTheRestFactors(t *testing.T) {
	for _, s := range []ViewScale{ViewScaleNative, ViewScaleMid, ViewScaleDetail} {
		z := ZoomOf(s)
		for v := int32(-200); v <= 200; v++ {
			if got, want := z.Project(v), s.Project(v); got != want {
				t.Fatalf("%s Project(%d) = %d, want %d", s, v, got, want)
			}
			if got, want := z.Inverse(v), s.Inverse(v); got != want {
				t.Fatalf("%s Inverse(%d) = %d, want %d", s, v, got, want)
			}
			if got, want := z.Px(v), s.Px(v); got != want {
				t.Fatalf("%s Px(%d) = %d, want %d", s, v, got, want)
			}
		}
	}
}

// Picking is the projection's exact inverse at a rest factor — every screen
// pixel names one world pixel and every world pixel projects to the first
// screen pixel that picks it — and the obvious floor in flight, where several
// screen pixels share a world pixel or the other way round (§16.4).
func TestPickingIsTheExactInverseAtRestAndFloorsInFlight(t *testing.T) {
	cam := &Camera{X: 400, Z: 300, ViewW: 1024, ViewH: 768, MapW: 8192, MapH: 8192}
	for _, s := range []ViewScale{ViewScaleNative, ViewScaleMid, ViewScaleDetail} {
		cam.Scale, cam.Zoom = s, ZoomOf(s)
		for v := int32(0); v < 400; v++ {
			sx := s.Project(v) + OriginX
			x, _ := cam.ScreenToWorld(sx, OriginY)
			if got := int32(x>>16) - cam.X; got != v {
				t.Fatalf("%s: world %d projected to %d and picked back %d", s, v, sx, got)
			}
		}
	}
	// In flight the inverse is floor((screen − origin)/f) and nothing else: a
	// screen pixel names the world pixel that covers it.
	cam.Zoom, cam.Scale = ZoomUnit*7/10, ViewScaleNative
	for sx := int32(0); sx < 400; sx++ {
		x, _ := cam.ScreenToWorld(sx+OriginX, OriginY)
		want := cam.X + int32(floorDiv(int64(sx)*int64(ZoomUnit), int64(cam.Zoom)))
		if got := int32(x >> 16); got != want {
			t.Fatalf("in flight ScreenToWorld(%d) = %d, want %d", sx, got, want)
		}
	}
}

// Zooming about a screen point leaves the world point under it where it is,
// which is what makes wheel zoom feel anchored to the pointer (§16.5).
func TestZoomAboutAPointKeepsTheWorldPointFixed(t *testing.T) {
	for _, target := range []Zoom{ZoomUnit * 3 / 10, ZoomUnit * 7 / 10, ZoomUnit, ZoomUnit * 13 / 10, ZoomMax} {
		cam := &Camera{X: 1200, Z: 900, ViewW: 1024, ViewH: 768, MapW: 8192, MapH: 8192}
		mx, my := int32(600), int32(400)
		before, beforeZ := cam.ScreenToWorld(mx, my)
		cam.SetZoomAbout(mx, my, target)
		after, afterZ := cam.ScreenToWorld(mx, my)
		if after != before || afterZ != beforeZ {
			t.Errorf("zoom to %s moved the point under (%d,%d) from (%v,%v) to (%v,%v)",
				target, mx, my, before, beforeZ, after, afterZ)
		}
	}
}

// The minimum factor is the one at which the battle viewport, measured in world
// pixels, exactly covers the playable map in the axis that runs out first, so
// the clamp never has to letterbox (§16.7).
func TestMinZoomFitsTheMapWithoutLetterboxing(t *testing.T) {
	cam := &Camera{ViewW: 1024, ViewH: 768, MapW: 4096, MapH: 2048}
	minZ := cam.MinZoom()
	// Below the floor the request is refused outright.
	cam.SetZoomAbout(OriginX, OriginY, minZ/2)
	if got := cam.EffectiveZoom(); got != minZ {
		t.Fatalf("a request below the floor gave %s, want the floor %s", got, minZ)
	}
	// At the floor the view fits inside the map on both axes.
	cam.Zoom, cam.Scale = minZ, minZ.Step()
	w, h := cam.BattleView()
	if w > cam.MapW || h > cam.MapH {
		t.Fatalf("at the floor the battle view is %dx%d world pixels, larger than the %dx%d map", w, h, cam.MapW, cam.MapH)
	}
	// One unit below it, one axis would not fit — the floor is tight, not slack.
	cam.Zoom = minZ - 1
	w2, h2 := cam.BattleView()
	if w2 <= cam.MapW && h2 <= cam.MapH {
		t.Fatalf("one unit under the floor still fits (%dx%d in %dx%d); the floor is not tight", w2, h2, cam.MapW, cam.MapH)
	}
}

// The classic executor has no free zoom: its factor is always its record step's
// own factor, so every projection reduces to the build before §16 (§16.1). The
// step writer is what guarantees it.
func TestSetScaleAboutKeepsTheFactorOnTheStep(t *testing.T) {
	cam := &Camera{X: 400, Z: 300, ViewW: 1024, ViewH: 768, MapW: 8192, MapH: 8192}
	for _, s := range []ViewScale{ViewScaleMid, ViewScaleDetail, ViewScaleNative, ViewScaleMid} {
		cam.SetScaleAbout(500, 300, s)
		if cam.EffectiveScale() != s {
			t.Fatalf("SetScaleAbout(%s) recorded step %s", s, cam.EffectiveScale())
		}
		if !cam.AtRestStep() {
			t.Fatalf("SetScaleAbout(%s) left factor %s off the step", s, cam.EffectiveZoom())
		}
		if got, want := cam.EffectiveZoom(), ZoomOf(s); got != want {
			t.Fatalf("SetScaleAbout(%s) gave factor %s, want %s", s, got, want)
		}
	}
}

// The record step follows the factor for the modern executor: 2x above 1x, 1x
// at or below it (§16.2). A factor exactly on a step is at rest, which disarms
// the executor's transform.
func TestRecordStepFollowsTheFactor(t *testing.T) {
	for _, tc := range []struct {
		z    Zoom
		want ViewScale
	}{
		{ZoomUnit * 3 / 10, ViewScaleNative},
		{ZoomUnit, ViewScaleNative},
		{ZoomUnit + 1, ViewScaleDetail},
		{ZoomUnit * 3 / 2, ViewScaleDetail},
		{ZoomMax, ViewScaleDetail},
	} {
		if got := tc.z.Step(); got != tc.want {
			t.Errorf("%s records at %s, want %s", tc.z, got, tc.want)
		}
	}
	if !(&Camera{Zoom: ZoomMax, Scale: ViewScaleDetail}).AtRestStep() {
		t.Error("2x is not reported at rest")
	}
	if (&Camera{Zoom: ZoomUnit * 13 / 10, Scale: ViewScaleDetail}).AtRestStep() {
		t.Error("1.3x is reported at rest")
	}
}

// ScreenToRecord bridges the presented picture and the recorded one for the
// pick tests that compare a pointer against projected record coordinates. It
// must be the identity at every rest factor, so nothing about picking changes
// there (§16.4).
func TestScreenToRecordIsTheIdentityAtRest(t *testing.T) {
	cam := &Camera{X: 400, Z: 300, ViewW: 1024, ViewH: 768, MapW: 8192, MapH: 8192}
	for _, s := range []ViewScale{ViewScaleNative, ViewScaleMid, ViewScaleDetail} {
		cam.Scale, cam.Zoom = s, ZoomOf(s)
		for v := int32(0); v < 300; v += 7 {
			if x, y := cam.ScreenToRecord(v, v); x != v || y != v {
				t.Fatalf("%s: ScreenToRecord(%d,%d) = (%d,%d)", s, v, v, x, y)
			}
		}
	}
	// In flight it composes the live inverse with the record projection, so a
	// pointer names the record pixel of the world pixel under it.
	cam.Zoom, cam.Scale = ZoomUnit*13/10, ViewScaleDetail
	sx, _ := cam.ScreenToRecord(OriginX+130, OriginY)
	worldOff := cam.Zoom.Inverse(130)
	if want := cam.Scale.Project(worldOff) + OriginX; sx != want {
		t.Fatalf("in flight ScreenToRecord = %d, want %d", sx, want)
	}
}

// The anchor a zoom is taken about is a BEAM point — the framebuffer point plus
// (OriginX, OriginY) — and the contract the player feels is on the DRAWN pixel:
// the recorder draws world w at framebuffer Project_f(w − cam), so a world point
// drawn under the pointer before the zoom is drawn under it after (§16.5).
//
// ScreenToWorld's own round trip cannot stand in for this: both sides of that
// trip share whatever offset the caller chose, so it passes with the wrong one.
func TestZoomAboutABeamPointKeepsTheDrawnPixel(t *testing.T) {
	for _, target := range []Zoom{ZoomUnit * 3 / 10, ZoomUnit * 7 / 10, ZoomUnit + ZoomUnit/4, ZoomUnit * 13 / 10, ZoomMax} {
		cam := &Camera{X: 4000, Z: 3000, ViewW: 1024, ViewH: 768, MapW: 8192, MapH: 8192}
		// A world point drawn at framebuffer (fx, fy) at 1x.
		fx, fy := int32(600), int32(400)
		wx, wz := cam.X+fx, cam.Z+fy
		cam.SetZoomAbout(fx+OriginX, fy+OriginY, target)
		f := cam.EffectiveZoom()
		if f != target {
			t.Fatalf("zoom to %s landed on %s", target, f)
		}
		gx, gy := f.Project(wx-cam.X), f.Project(wz-cam.Z)
		if gx < fx-1 || gx > fx || gy < fy-1 || gy > fy {
			t.Errorf("zoom to %s about framebuffer (%d,%d) now draws that world point at (%d,%d)", target, fx, fy, gx, gy)
		}
	}
}
