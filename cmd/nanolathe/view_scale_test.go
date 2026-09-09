package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// zoomTestBattle is the smallest battle the two view-scale bindings need: a
// camera on a map large enough that the clamp does not move the origin.
func zoomTestBattle() *battleSession {
	return &battleSession{cam: &camera.Camera{X: 400, Z: 300, ViewW: 640, ViewH: 480, MapW: 8192, MapH: 8192}}
}

// F9 is a Nanolathe binding, not retail's (DESIGN_GPU_RENDERER §14.6). It
// cycles the view scale 1x, 1.5x, 2x and back, and keeps the world point at
// the battle viewport's centre where it is, so the player is looking at the
// same thing after each press as before it.
func TestF9CyclesTheViewScaleAboutTheViewportCentre(t *testing.T) {
	b := zoomTestBattle()
	mx, my := battleViewCentre(b.cam)
	before, beforeZ := b.cam.ScreenToWorld(mx, my)

	for i, want := range []camera.ViewScale{camera.ViewScaleMid, camera.ViewScaleDetail, camera.ViewScaleNative} {
		pressKeys(b, input.KeyF9)
		if got := b.cam.EffectiveScale(); got != want {
			t.Fatalf("view scale after %d F9 = %s, want %s", i+1, got, want)
		}
		if x, z := b.cam.ScreenToWorld(mx, my); x != before || z != beforeZ {
			t.Errorf("after %d F9 the centre world point moved to (%v,%v), want (%v,%v)", i+1, x, z, before, beforeZ)
		}
	}
}

// The window opens at 1.5x above the retail 800x600 mode and natively at or
// below it, unless `--zoom` says otherwise (§14.6). A capture never takes
// the default: its scale is `--zoom` or native.
func TestEntryZoomDefaultsByResolution(t *testing.T) {
	for _, tc := range []struct {
		w, h int32
		zoom camera.ViewScale
		want camera.ViewScale
	}{
		{640, 480, 0, camera.ViewScaleNative},
		{800, 600, 0, camera.ViewScaleNative},
		{1024, 768, 0, camera.ViewScaleMid},
		{1920, 1080, 0, camera.ViewScaleMid},
		{1920, 1080, camera.ViewScaleNative, camera.ViewScaleNative},
		{640, 480, camera.ViewScaleDetail, camera.ViewScaleDetail},
	} {
		b := zoomTestBattle()
		b.cam.ViewW, b.cam.ViewH = tc.w, tc.h
		applyEntryZoom(Options{Zoom: tc.zoom}, b)
		if got := b.cam.EffectiveScale(); got != tc.want {
			t.Errorf("%dx%d with --zoom %v opened at %s, want %s", tc.w, tc.h, tc.zoom, got, tc.want)
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
	applyEntryZoom(Options{Zoom: camera.ViewScaleNative}, native)
	if *native.cam != before {
		t.Errorf("--zoom 1 moved the camera: %+v, want %+v", *native.cam, before)
	}
	detail := zoomTestBattle()
	applyEntryZoom(Options{Zoom: camera.ViewScaleDetail}, detail)
	if got := detail.cam.EffectiveScale(); got != camera.ViewScaleDetail {
		t.Errorf("--zoom 2 gave view scale %s, want 2x", got)
	}
}
