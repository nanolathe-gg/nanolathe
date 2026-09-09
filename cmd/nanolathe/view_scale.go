package main

// The detail view's runtime switches — DESIGN_GPU_RENDERER §14.6.
//
// The view scale is presentation-only [I6][F-P1-008]: the simulation, the tick
// fingerprint and the save image are identical at 1x and 2x, and only which
// pixels present the committed frame differs.

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// battleViewCentre is the screen point a scale change is taken about: the
// centre of the battle viewport, the world region `(128,32)..(W-1,H-33)` the
// chrome is painted over [03 §4.1][07 R-HUD-05]. Zooming about it keeps the
// point the player is looking at where it is, which the framebuffer centre
// would not once the side rail and the strips are accounted for.
//
// The capture route takes its own point instead — the framebuffer centre, or
// `--shot-focus` — because a capture frames a scene rather than continuing a
// view, and that choice predates this file (shot.go).
// The point is in framebuffer pixels, which is what SetScaleAbout takes, and
// it is the same point at either scale: the chrome is drawn in framebuffer
// pixels and does not move with the view scale, so the toggle is exactly
// reversible.
func battleViewCentre(cam *camera.Camera) (int32, int32) {
	if cam == nil {
		return camera.OriginX, camera.OriginY
	}
	// The viewport is `(128,32)..(W-1,H-33)` of the live surface [03 §4.1]
	// [07 R-HUD-05]: leading inset on X only, equal insets top and bottom.
	return (cam.ViewW + camera.OriginX) / 2, cam.ViewH / 2
}

// setBattleViewScale puts the battle on view scale s about the viewport centre.
func setBattleViewScale(b *battleSession, s int32) {
	if b == nil || b.cam == nil {
		return
	}
	mx, my := battleViewCentre(b.cam)
	b.cam.SetScaleAbout(mx, my, s)
}

// applyEntryZoom applies `--zoom` at battle entry for the windowed routes.
// A native run leaves the camera untouched, so nothing composed at scale 1
// changes (§14.1).
func applyEntryZoom(opts Options, b *battleSession) {
	if opts.Zoom <= 1 || b == nil || b.cam == nil {
		return
	}
	setBattleViewScale(b, int32(opts.Zoom))
}

// viewScaleOf is the battle's live view scale, for the diagnostics and scene
// metadata that record which one a measurement was taken at.
func viewScaleOf(b *battleSession) int32 {
	if b == nil || b.cam == nil {
		return 1
	}
	return b.cam.EffectiveScale()
}

// toggleViewScale is F9: 1 <-> 2 about the viewport centre. It is a Nanolathe
// binding, not a retail one — retail's dispatcher has no case for F9 or F10
// (§14.6).
func (b *battleSession) toggleViewScale() {
	if b == nil || b.cam == nil {
		return
	}
	next := int32(2)
	if b.cam.EffectiveScale() >= 2 {
		next = 1
	}
	setBattleViewScale(b, next)
	fmt.Fprintf(os.Stderr, "nanolathe: view scale %dx\n", next)
}

// requestRendererToggle is F10: ask the window adapter to swap executors. The
// client only publishes the request; the adapter owns both executors and makes
// the switch at its next Update (§14.6). A route with no adapter — a capture,
// a test — simply never services it.
func requestRendererToggle(cl *client.Client) {
	if cl == nil {
		return
	}
	cl.RequestRendererToggle()
}
