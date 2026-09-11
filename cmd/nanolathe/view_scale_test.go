package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// zoomTestBattle is the smallest battle the two view-scale bindings need: a
// camera on a map large enough that the clamp does not move the origin.
func zoomTestBattle() *battleSession {
	return &battleSession{cam: &camera.Camera{X: 400, Z: 300, ViewW: 640, ViewH: 480, MapW: 8192, MapH: 8192}}
}

// F9 keeps the viewport's centre anchored through the renderer's target
// cycle (DESIGN_GPU_RENDERER §16.8), including the modern wrap to 0.5x.
func TestF9CyclesTheViewScaleAboutTheViewportCentre(t *testing.T) {
	for _, modern := range []bool{false, true} {
		b := zoomTestBattle()
		b.cam.X, b.cam.Z = 2000, 1500
		mx, my := battleViewCentre(b.cam)
		wx, wz := b.cam.X+mx, b.cam.Z+my
		wants := []camera.Zoom{camera.ZoomOf(camera.ViewScaleMid), camera.ZoomMax, camera.ZoomUnit}
		if modern {
			wants = []camera.Zoom{camera.ZoomMax, camera.ZoomUnit / 2, camera.ZoomUnit}
		}
		for i, want := range wants {
			b.toggleViewScale(modern)
			for j := 0; j < 400 && b.zoom.Active(b.cam); j++ {
				b.zoom.Step(b.cam)
			}
			if got := b.cam.EffectiveZoom(); got != want {
				t.Fatalf("modern=%v: zoom after %d F9 = %s, want %s", modern, i+1, got, want)
			}
			// The animated path re-anchors through integer rounding each Update.
			if gx, gy := want.Project(wx-b.cam.X), want.Project(wz-b.cam.Z); gx < mx-2 || gx > mx || gy < my-2 || gy > my {
				t.Errorf("modern=%v: after %d F9 the centre (%d,%d) is drawn at (%d,%d)", modern, i+1, mx, my, gx, gy)
			}
		}
	}
}

// The wheel zooms about the pointer: a world point drawn under it before the
// gesture is drawn under it once the ease has settled (§16.5, §16.6). The
// pointer is a framebuffer point; wheelZoom is the seam that converts it to the
// camera's beam pixels, and the judge is the drawn pixel, not the inverse.
func TestWheelZoomKeepsTheWorldUnderThePointer(t *testing.T) {
	b := zoomTestBattle()
	b.cam.X, b.cam.Z = 2000, 1500
	px, py := int32(400), int32(250)
	wx, wz := b.cam.X+px, b.cam.Z+py
	for _, dy := range []float64{3, -8} {
		b.wheelZoom(px, py, dy)
		for i := 0; i < 400 && b.zoom.Active(b.cam); i++ {
			b.zoom.Step(b.cam)
		}
		if b.zoom.Active(b.cam) {
			t.Fatalf("wheel %v: the ease never settled", dy)
		}
		f := b.cam.EffectiveZoom()
		if (dy > 0) != (f > camera.ZoomUnit) {
			t.Fatalf("wheel %v settled on %s", dy, f)
		}
		// The ease re-anchors every Update through floor and the projection
		// ceils, so the drawn pixel may sit up to two pixels inside the pointer.
		if gx, gy := f.Project(wx-b.cam.X), f.Project(wz-b.cam.Z); gx < px-2 || gx > px || gy < py-2 || gy > py {
			t.Errorf("wheel %v to %s: the world under the pointer (%d,%d) is now drawn at (%d,%d)", dy, f, px, py, gx, gy)
		}
	}
}

// Modern opens at 1x at every resolution; classic keeps its resolution
// default. An explicit --zoom overrides either (§16.8).
func TestEntryZoomDefaultsByResolution(t *testing.T) {
	for _, tc := range []struct {
		w, h int32
		zoom camera.Zoom
		want camera.Zoom
	}{
		{640, 480, 0, camera.ZoomUnit},
		{800, 600, 0, camera.ZoomUnit},
		{1024, 768, 0, camera.ZoomOf(camera.ViewScaleMid)},
		{1920, 1080, 0, camera.ZoomOf(camera.ViewScaleMid)},
		{1920, 1080, camera.ZoomUnit, camera.ZoomUnit},
		{640, 480, camera.ZoomMax, camera.ZoomMax},
		// A free factor is a start-up factor too, for the modern executor
		// (DESIGN_GPU_RENDERER §16.8).
		{1024, 768, camera.ZoomUnit * 7 / 10, camera.ZoomUnit * 7 / 10},
	} {
		for _, renderer := range []string{"classic", "modern"} {
			b := zoomTestBattle()
			b.cam.ViewW, b.cam.ViewH = tc.w, tc.h
			want := tc.want
			if renderer == "modern" && tc.zoom == 0 {
				want = camera.ZoomUnit
			}
			applyEntryZoom(Options{Zoom: tc.zoom, Renderer: renderer}, b)
			if got := b.cam.EffectiveZoom(); got != want {
				t.Errorf("%s %dx%d with --zoom %v opened at %s, want %s", renderer, tc.w, tc.h, tc.zoom, got, want)
			}
		}
	}
}

// F10 does not switch anything itself: the client owns neither executor, so
// the key only publishes a request the window adapter polls at its next Update
// (§14.6). One press is one request.
func TestF10PublishesOneRendererRequestPerPress(t *testing.T) {
	b := zoomTestBattle()
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if got := cl.RendererToggleCount(); got != 0 {
		t.Fatalf("fresh client has %d renderer requests, want 0", got)
	}
	// pressKeys drives the dispatcher with no client, and the request needs the
	// live one, so each press goes through the same handler with it installed.
	press := func() {
		in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
		in.Kbd.SetKey(input.KeyF10, true)
		b.handleInput(in, cl)
	}
	press()
	if got := cl.RendererToggleCount(); got != 1 {
		t.Fatalf("renderer requests after one F10 = %d, want 1", got)
	}
	press()
	if got := cl.RendererToggleCount(); got != 2 {
		t.Fatalf("renderer requests after two F10 = %d, want 2", got)
	}
}

// --zoom is a start-up view scale for the window, applied once at battle entry
// (§14.6). A native run must leave the camera exactly as composition left it,
// which is what keeps every 1x capture and every 1x window frame unchanged.
func TestEntryZoomAppliesOnlyAtTheDetailScale(t *testing.T) {
	native := zoomTestBattle()
	before := *native.cam
	applyEntryZoom(Options{Zoom: camera.ZoomUnit}, native)
	if *native.cam != before {
		t.Errorf("--zoom 1 moved the camera: %+v, want %+v", *native.cam, before)
	}
	detail := zoomTestBattle()
	applyEntryZoom(Options{Zoom: camera.ZoomMax}, detail)
	if got := detail.cam.EffectiveScale(); got != camera.ViewScaleDetail {
		t.Errorf("--zoom 2 gave view scale %s, want 2x", got)
	}
}

// The on-screen list Ctrl+S selects from is judged on the PRESENTED position
// against the viewport in framebuffer pixels (DESIGN_GPU_RENDERER §16.4): a
// unit drawn inside the viewport is on screen at any factor, one drawn under
// the side rail or past the right edge is not. The earlier test compared a
// record-step projection against the view in world pixels from zero, which
// admitted units under the rail and refused the rightmost rail's width.
func TestOnScreenUnitIsJudgedWhereTheUnitIsDrawn(t *testing.T) {
	for _, z := range []camera.Zoom{camera.ZoomUnit, camera.ZoomUnit / 2, camera.ZoomUnit * 13 / 10, camera.ZoomMax} {
		b := zoomTestBattle()
		b.cam.X, b.cam.Z = 2000, 1500
		b.cam.Zoom, b.cam.Scale = z, z.Step()
		unitAt := func(fx, fy int32) frame.UnitView {
			// The world point drawn at framebuffer (fx, fy): the inverse of the
			// presented projection, on flat ground.
			return frame.UnitView{Slot: 1,
				X: numeric.FixedFromInt(int64(b.cam.X + z.Inverse(fx))),
				Z: numeric.FixedFromInt(int64(b.cam.Z + z.Inverse(fy)))}
		}
		if !b.onScreenUnit(unitAt(camera.OriginX+4, camera.OriginY+4)) {
			t.Errorf("at %s a unit just inside the viewport corner is not on screen", z)
		}
		if !b.onScreenUnit(unitAt(b.cam.ViewW-4, b.cam.ViewH-camera.OriginY-4)) {
			t.Errorf("at %s a unit just inside the far corner is not on screen", z)
		}
		if b.onScreenUnit(unitAt(camera.OriginX-8, 200)) {
			t.Errorf("at %s a unit under the side rail is on screen", z)
		}
		if b.onScreenUnit(unitAt(b.cam.ViewW+8, 200)) {
			t.Errorf("at %s a unit past the right edge is on screen", z)
		}
	}
}
