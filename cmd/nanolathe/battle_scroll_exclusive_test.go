package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// The scroll pass runs one exclusive test per axis, not four independent
// direction blocks [07 §10][07 R-CRD-006 §1]. The Left predicate — the Left
// arrow held with TALK.GUI absent, or the pointer in the left edge band — is
// evaluated first, and when it holds the pass subtracts the magnitude and goes
// straight to the vertical axis without evaluating the Right predicate at all.
// The vertical axis has the same shape with Up before Down. Both directions of
// a pair satisfied therefore moves the camera once, toward Left/Up.
//
// Before this fix the pass ran four independent blocks, so every one of the
// opposing cases below cancelled to zero: holding two opposing arrows, or
// resting the pointer on one screen edge while holding the opposite arrow,
// froze the camera instead of scrolling. The table asserts the exact
// displacement, not only its sign, because "moves once" is as much of the
// contract as "moves toward Left/Up" — a doubled step would be just as wrong.
//
// The same table covers the beyond-edge forced strip's joint gate: the pointer
// must be less than 100 pixels beyond the right edge AND less than 100 pixels
// beyond the bottom edge, with the window focused, before either axis is
// forced onto its edge [07 §10]. Host overshoot and letterbox cases cover
// the presentation adaptation in DESIGN_INTERFACE_HUD_INPUT §3.1.
func TestScrollPassAxisExclusivityAndBeyondEdgeGate(t *testing.T) {
	const (
		screenW = 640
		screenH = 480
		// One host frame worth 34 ms is exactly one scaled unit of raw delta,
		// so a direction that scrolls moves by the setting byte [07 §10] (C2).
		frameMS = 34
	)
	setting := int32(32)
	unit := setting

	for _, tc := range []struct {
		name               string
		keys               []input.Key
		mouseX, mouseY     float32
		focused            bool
		outsideW, outsideH int
		wantX, wantZ       int32
	}{
		// Host overshoot must reach the leading edges as well as the trailing ones.
		{name: "beyond left edge", mouseX: -10, mouseY: 240, focused: true, wantX: -unit},
		{name: "beyond top edge", mouseX: 320, mouseY: -10, focused: true, wantZ: -unit},
		{name: "beyond top left", mouseX: -10, mouseY: -10, focused: true, wantX: -unit, wantZ: -unit},
		{name: "left strip inner boundary", mouseX: -99, mouseY: 240, focused: true, wantX: -unit},
		{name: "left strip outer boundary", mouseX: -100, mouseY: 240, focused: true},
		{name: "right strip inner boundary", mouseX: screenW + 99, mouseY: 240, focused: true, wantX: unit},
		{name: "right strip outer boundary", mouseX: screenW + 100, mouseY: 240, focused: true},
		{name: "far outside left", mouseX: -101, mouseY: 240, focused: true},
		{name: "far outside top", mouseX: 320, mouseY: -101, focused: true},
		{name: "left overshoot without focus", mouseX: -10, mouseY: 240},
		// A 640x480 canvas on a 1920x1080 host has 106 2/3 logical
		// pixels of padding at either side. The physical edge is outside
		// the old strip on both sides; the HUD sample must remain untouched.
		{name: "wide host left edge", outsideW: 1920, outsideH: 1080, mouseX: -106, mouseY: 240, focused: true, wantX: -unit},
		{name: "wide host right edge", outsideW: 1920, outsideH: 1080, mouseX: 746, mouseY: 240, focused: true, wantX: unit},
		{name: "tall host top edge", outsideW: 640, outsideH: 960, mouseX: 320, mouseY: -240, focused: true, wantZ: -unit},
		{name: "tall host bottom edge", outsideW: 640, outsideH: 960, mouseX: 320, mouseY: 719, focused: true, wantZ: unit},
		{name: "letterbox without focus", outsideW: 1920, outsideH: 1080, mouseX: -106, mouseY: 240},
		{name: "letterbox but far below host", outsideW: 1920, outsideH: 1080, mouseX: -106, mouseY: 800, focused: true},
		// Opposing arrows: the Left/Up predicate wins outright.
		{name: "left and right held", keys: []input.Key{input.KeyLeft, input.KeyRight},
			mouseX: 320, mouseY: 240, wantX: -unit},
		{name: "up and down held", keys: []input.Key{input.KeyUp, input.KeyDown},
			mouseX: 320, mouseY: 240, wantZ: -unit},
		{name: "all four arrows held", keys: []input.Key{input.KeyLeft, input.KeyRight, input.KeyUp, input.KeyDown},
			mouseX: 320, mouseY: 240, wantX: -unit, wantZ: -unit},

		// One predicate satisfied by its edge band, the other by the opposite
		// arrow. The Left/Up predicate still wins, whichever arm supplies it.
		{name: "left edge with right held", keys: []input.Key{input.KeyRight},
			mouseX: 0, mouseY: 240, focused: true, wantX: -unit},
		{name: "right edge with left held", keys: []input.Key{input.KeyLeft},
			mouseX: screenW - 1, mouseY: 240, focused: true, wantX: -unit},
		{name: "top edge with down held", keys: []input.Key{input.KeyDown},
			mouseX: 320, mouseY: 0, focused: true, wantZ: -unit},
		{name: "bottom edge with up held", keys: []input.Key{input.KeyUp},
			mouseX: 320, mouseY: screenH - 1, focused: true, wantZ: -unit},

		// The Right and Down predicates still run when the Left and Up ones
		// fail, so the unopposed cases are unchanged.
		{name: "right arrow alone", keys: []input.Key{input.KeyRight},
			mouseX: 320, mouseY: 240, wantX: unit},
		{name: "down arrow alone", keys: []input.Key{input.KeyDown},
			mouseX: 320, mouseY: 240, wantZ: unit},
		{name: "left edge alone", mouseX: 0, mouseY: 240, focused: true, wantX: -unit},
		{name: "bottom right corner", mouseX: screenW - 1, mouseY: screenH - 1, focused: true,
			wantX: unit, wantZ: unit},

		// The beyond-edge forced strip is gated jointly. Ten pixels past the
		// right edge is inside the strip on its own axis, but three hundred
		// past the bottom edge fails the vertical half of the gate, so neither
		// axis is forced and nothing scrolls.
		{name: "beyond right edge but far below bottom", mouseX: screenW + 10, mouseY: screenH + 300, focused: true},
		{name: "beyond bottom edge but far right", mouseX: screenW + 300, mouseY: screenH + 10, focused: true},
		{name: "beyond both edges inside the strip", mouseX: screenW + 10, mouseY: screenH + 10, focused: true,
			wantX: unit, wantZ: unit},
		{name: "beyond both edges without focus", mouseX: screenW + 10, mouseY: screenH + 10},
		// A pointer past the right edge but inside the window vertically is
		// forced on the horizontal axis alone; its real y keeps the vertical
		// axis quiet.
		{name: "beyond right edge only", mouseX: screenW + 10, mouseY: 240, focused: true, wantX: unit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(300, 300))
			millis := &fakeMillisSource{}
			b.millisSource = millis
			b.scrollSpeedPrimed, b.scrollSpeedByte = true, byte(setting)
			cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: screenW, Height: screenH})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetCamera(b.cam)
			cl.SetOutsideSize(tc.outsideW, tc.outsideH)
			cl.SetFocused(tc.focused)
			b.cam.Zoom = camera.ZoomUnit
			cl.Input().Mouse.SetPosition(tc.mouseX, tc.mouseY)
			for _, k := range tc.keys {
				cl.Input().Kbd.SetKey(k, true)
			}
			// One priming frame seeds the scroll clock's anchor, as retail's
			// budget step has already been running when battle mode takes over.
			b.viewerStep(0, cl)
			b.cam.X, b.cam.Z = 1000, 1000

			millis.ms += frameMS
			b.viewerStep(float64(frameMS)/1000, cl)

			if mouse := cl.Input().Mouse; mouse.X != tc.mouseX || mouse.Y != tc.mouseY {
				t.Fatal("camera edge mapping changed the pointer used for picking")
			}
			gotX, gotZ := b.cam.X-1000, b.cam.Z-1000
			if gotX != tc.wantX || gotZ != tc.wantZ {
				t.Fatalf("one frame moved (%d,%d) map pixels, want (%d,%d)", gotX, gotZ, tc.wantX, tc.wantZ)
			}
		})
	}
}

// Letterbox adaptation belongs only to edge panning. Wheel zoom must still
// require the raw pointer to be inside the world viewport (DESIGN_GPU_RENDERER §16.6).
func TestEdgeScrollLetterboxDoesNotAdmitWheelZoom(t *testing.T) {
	for _, x := range []float32{320, 746} {
		b := newTestBattle(testCatalogON05(), testWorldON05(300, 300))
		b.millisSource = &fakeMillisSource{}
		cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetCamera(b.cam)
		cl.SetOutsideSize(1920, 1080)
		cl.SetFocused(true)
		cl.SetEnhanced(true)
		b.cam.Zoom = camera.ZoomUnit
		cl.Input().Mouse.SetPosition(x, 240)
		cl.Input().Mouse.ZoomScrollY = -1
		b.viewerStep(0, cl)
		changed := b.zoom.Target(b.cam) != camera.ZoomUnit
		if changed != (x == 320) {
			t.Fatalf("wheel at x=%g changed zoom=%v", x, changed)
		}
	}
}
