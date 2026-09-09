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
// swaps the view scale between 1 and 2 and keeps the world point at the battle
// viewport's centre where it is, so the player is looking at the same thing
// after the toggle as before it.
func TestF9TogglesTheViewScaleAboutTheViewportCentre(t *testing.T) {
	b := zoomTestBattle()
	mx, my := battleViewCentre(b.cam)
	before, beforeZ := b.cam.ScreenToWorld(mx, my)

	pressKeys(b, input.KeyF9)
	if got := b.cam.EffectiveScale(); got != 2 {
		t.Fatalf("view scale after one F9 = %d, want 2", got)
	}
	if x, z := b.cam.ScreenToWorld(mx, my); x != before || z != beforeZ {
		t.Errorf("centre world point moved to (%v,%v), want (%v,%v)", x, z, before, beforeZ)
	}

	pressKeys(b, input.KeyF9)
	if got := b.cam.EffectiveScale(); got != 1 {
		t.Fatalf("view scale after two F9 = %d, want 1", got)
	}
	if x, z := b.cam.ScreenToWorld(mx, my); x != before || z != beforeZ {
		t.Errorf("centre world point moved back to (%v,%v), want (%v,%v)", x, z, before, beforeZ)
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
	applyEntryZoom(Options{Zoom: 1}, native)
	if *native.cam != before {
		t.Errorf("--zoom 1 moved the camera: %+v, want %+v", *native.cam, before)
	}
	detail := zoomTestBattle()
	applyEntryZoom(Options{Zoom: 2}, detail)
	if got := detail.cam.EffectiveScale(); got != 2 {
		t.Errorf("--zoom 2 gave view scale %d, want 2", got)
	}
}
