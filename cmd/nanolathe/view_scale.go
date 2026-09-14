package main

// The detail view's and smooth zoom's runtime switches — DESIGN_GPU_RENDERER
// §14.6 and §16.8.
//
// The view scale is presentation-only [I6][F-P1-008]: the simulation, the tick
// fingerprint and the save image are identical at every factor, and only which
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
// The point is in framebuffer pixels; beamAnchor converts it for the camera.
// It is the same point at either scale: the chrome is drawn in framebuffer
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

// beamAnchor converts a framebuffer point — the pointer, the viewport centre,
// `--shot-focus` — to the beam pixels the camera's anchor writers and
// ScreenToWorld take: the recorder stores a world point at its beam position
// less (OriginX, OriginY), so the offsets go back on for the camera's inverse
// [03 §2.5] (DESIGN_GPU_RENDERER §16.5). Every zoom writer in this file goes
// through it, so a caller thinks in the pixels it can see.
func beamAnchor(x, y int32) (int32, int32) {
	return x + camera.OriginX, y + camera.OriginY
}

// setBattleViewScale puts the battle on view scale s about the viewport centre.
// It writes the record step and the live factor together, which is what the
// classic executor is always on (§16.8).
func setBattleViewScale(b *battleSession, s camera.ViewScale) {
	if b == nil || b.cam == nil {
		return
	}
	mx, my := beamAnchor(battleViewCentre(b.cam))
	b.cam.SetScaleAbout(mx, my, s)
	b.zoom.Reset()
}

// wheelZoom spends one host frame's wheel delta on the zoom target about the
// framebuffer pointer (x, y), so the world under the pointer stays put while
// the factor changes (§16.6).
func (b *battleSession) wheelZoom(x, y int32, dy float64) {
	if b == nil || b.cam == nil {
		return
	}
	mx, my := beamAnchor(x, y)
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	b.zoom.Wheel(b.cam, mx, my, dy, b.millisSource.Millis32())
}

// setBattleZoom aims the battle at a free factor about the viewport centre,
// animated. It is the modern executor's F9 (§16.8).
func setBattleZoom(b *battleSession, z camera.Zoom) {
	if b == nil || b.cam == nil {
		return
	}
	mx, my := beamAnchor(battleViewCentre(b.cam))
	b.zoom.SetTarget(b.cam, mx, my, z)
}

// jumpBattleZoom puts the battle on a factor outright, with no animation,
// about the framebuffer point (mx, my). Battle entry, a restart and the capture
// route take it: there is no motion to smooth.
//
// The executor decides how the factor is recorded (§16.8). The classic one has
// no free zoom, so a factor it is given is one of the three views and is set as
// that VIEW SCALE — its own art and its own arithmetic, exactly as before §16.
// The modern one derives the record step from the factor and scales the
// recording, so its 1.5x is the 2x step shrunk rather than the 1.5x variant set.
func jumpBattleZoom(b *battleSession, mx, my int32, z camera.Zoom, modern bool) {
	if b == nil || b.cam == nil {
		return
	}
	mx, my = beamAnchor(mx, my)
	if !modern {
		if s, ok := camera.ViewScaleForZoom(z); ok {
			b.cam.SetScaleAbout(mx, my, s)
			b.zoom.Reset()
			return
		}
	}
	b.cam.SetZoomAbout(mx, my, z)
	b.zoom.Reset()
}

// The classic window's default view scale follows its resolution (§14.6): the
// retail 640x480 and 800x600 modes keep the native picture, and anything
// larger opens at 1.5x, where a 1080p window shows about the world a 1280x720
// native one would.
const (
	defaultZoomMaxNativeW = 800
	defaultZoomMaxNativeH = 600
)

// defaultViewScale is the view scale a window of this size opens at when
// `--zoom` is not given.
func defaultViewScale(viewW, viewH int32) camera.ViewScale {
	if viewW > defaultZoomMaxNativeW || viewH > defaultZoomMaxNativeH {
		return camera.ViewScaleMid
	}
	return camera.ViewScaleNative
}

// entryZoom resolves the window's start-up factor: `--zoom` when given, else
// 1x in modern or the resolution default in classic (§16.8).
func entryZoom(opts Options, cam *camera.Camera) camera.Zoom {
	if opts.Zoom != 0 {
		return opts.Zoom
	}
	if modernRenderer(opts) || cam == nil {
		return camera.ZoomUnit
	}
	return camera.ZoomOf(defaultViewScale(cam.ViewW, cam.ViewH))
}

// applyEntryZoom applies the start-up factor at battle entry for the windowed
// routes. A native factor leaves the camera untouched, so nothing composed at
// scale 1 changes (§14.1).
func applyEntryZoom(opts Options, b *battleSession) {
	if b == nil || b.cam == nil {
		return
	}
	z := entryZoom(opts, b.cam)
	if z == camera.ZoomUnit {
		return
	}
	mx, my := battleViewCentre(b.cam)
	jumpBattleZoom(b, mx, my, z, modernRenderer(opts))
}

// viewScaleOf is the battle's live RECORD step, for the diagnostics and scene
// metadata that record which one a measurement was taken at.
func viewScaleOf(b *battleSession) camera.ViewScale {
	if b == nil || b.cam == nil {
		return camera.ViewScaleNative
	}
	return b.cam.EffectiveScale()
}

// viewZoomOf is the battle's live factor, which is what a measurement records:
// the record step is an implementation detail of the recording, and two
// benchmark runs are comparable when they were taken at the same FACTOR
// (DESIGN_GPU_RENDERER §16.8).
func viewZoomOf(b *battleSession) camera.Zoom {
	if b == nil || b.cam == nil {
		return camera.ZoomUnit
	}
	return b.cam.EffectiveZoom()
}

// toggleViewScale is F9. In the classic executor it is the unchanged 1x, 1.5x,
// 2x step cycle about the viewport centre; modern cycles 1x, 2x, 0.25x as
// animated zoom targets (§16.8). It is a Nanolathe binding,
// not a retail one — retail's dispatcher has no case for F9 or F10 (§14.6).
func (b *battleSession) toggleViewScale(modern bool) {
	if b == nil || b.cam == nil {
		return
	}
	if !modern {
		next := b.cam.EffectiveScale().Next()
		setBattleViewScale(b, next)
		fmt.Fprintf(os.Stderr, "nanolathe: view scale %s\n", next)
		return
	}
	next := nextZoomTarget(b.cam.RequestedZoom())
	setBattleZoom(b, next)
	fmt.Fprintf(os.Stderr, "nanolathe: view scale %s\n", next)
}

// nextZoomTarget is the modern F9 cycle, 1x -> 2x -> 0.25x -> 1x (§16.8).
// It shares the wheel's targets; a free factor goes to the first step above
// it, wrapping to the lowest step when there is none.
func nextZoomTarget(current camera.Zoom) camera.Zoom {
	if next, ok := camera.NextZoomStep(current, true); ok {
		return next
	}
	return camera.ZoomSteps[0]
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

// modernRenderer reports whether the modern executor is the one this run
// presents through, which is what decides whether `--zoom` accepts a free
// factor (DESIGN_GPU_RENDERER §16.8). A capture follows its own
// `--shot-renderer` when one is given, because that is the executor the frame
// is drawn by; "both" counts as modern, since its classic half is recorded at
// the record step and is exact there whatever the factor.
func modernRenderer(opts Options) bool {
	if opts.Shot != "" || opts.ShotModel != "" {
		switch effectiveShotRenderer(opts) {
		case "modern", "both":
			return true
		default:
			return false
		}
	}
	return opts.Renderer == "modern"
}
