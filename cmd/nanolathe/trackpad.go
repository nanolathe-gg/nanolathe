package main

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Modern feel choices (DESIGN_GPU_RENDERER §16.6). Magnification moves in
// log space around the preferred lock; arriving there holds the rest of a
// gesture, and a fresh gesture leaves it immediately.
const (
	pinchSensitivity = 2.0
	pinchThreshold   = 0.12 // one stop per gesture in stepped and legacy controls
)

type battleGestures struct {
	panX, panY       float64 // fractional world pixels carried between direct deltas
	pinchActive      bool
	pinchLatched     bool
	pinchZoom        camera.Zoom // last requested factor, before the next delta
	pinchPosition    float64
	magnification    float64
	anchorX, anchorY int32
}

func (b *battleSession) applyTrackpadGestures(mouse *input.MouseState, allowed bool, x, y int32) {
	if b == nil {
		return
	}
	g := &b.gestures
	if !allowed || b.cam == nil || mouse == nil {
		*g = battleGestures{}
		return
	}
	if b.buildDragWheelOwned() {
		// A build row or grid spends precise scrolling on its gap (§3.11 of
		// DESIGN_INTERFACE_HUD_INPUT); the camera must not also pan.
		g.panX, g.panY = 0, 0
	} else if mouse.PanX != 0 || mouse.PanY != 0 {
		factor := float64(camera.ZoomUnit) / float64(b.cam.EffectiveZoom())
		g.panX += mouse.PanX * factor
		g.panY += mouse.PanY * factor
		dx, dy := int32(g.panX), int32(g.panY)
		g.panX -= float64(dx)
		g.panY -= float64(dy)
		oldX, oldZ := b.cam.X, b.cam.Z
		b.cam.Pan(-dx, -dy)
		// Do not bank motion into an edge; reversing should respond immediately.
		if b.cam.X != oldX-dx {
			g.panX = 0
		}
		if b.cam.Z != oldZ-dy {
			g.panY = 0
		}
		b.cam.ClearFollow()
		b.pendingFollowInput = nil
	}
	style := b.cameraControlStyle()
	lock := b.zoomLock()
	if style.disabled() {
		g.pinchActive = false
		return
	}
	for _, event := range mouse.Pinches {
		if event.Began {
			if style.modern() {
				b.zoom.CancelWheel()
			}
			g.pinchActive = true
			g.pinchLatched = false
			g.magnification = 0
			live := b.cam.EffectiveZoom()
			g.pinchZoom = live
			g.pinchPosition = pinchPositionAt(live, lock)
			g.anchorX, g.anchorY = beamAnchor(x, y)
			if event.Positioned {
				g.anchorX, g.anchorY = beamAnchor(event.X, event.Y)
			}
		}
		if event.Cancelled {
			g.pinchActive = false
		}
		if g.pinchActive && !g.pinchLatched {
			if style != battleZoomSmooth {
				g.magnification += event.Delta
				if math.Abs(g.magnification) >= pinchThreshold {
					// Each gesture spends one stop, including one against a limit.
					g.pinchLatched = true
					var next camera.Zoom
					var ok bool
					if style == battleZoomLegacy {
						next, ok = camera.NextZoomStep(b.cam.RequestedZoom(), g.magnification > 0)
					} else {
						next, ok = camera.NextZoomStop(b.zoom.Target(b.cam), b.cam.MinZoom(), lock, g.magnification > 0)
					}
					if ok {
						if style == battleZoomLegacy {
							b.zoom.SetTargetLegacy(b.cam, g.anchorX, g.anchorY, next)
						} else {
							b.zoom.SetTarget(b.cam, g.anchorX, g.anchorY, next)
						}
						b.cam.ClearFollow()
						b.pendingFollowInput = nil
					}
				}
			} else {
				g.pinchPosition += event.Delta * pinchSensitivity
				// Discard excess at either limit so reversing responds immediately.
				g.pinchPosition = min(max(g.pinchPosition, pinchPositionAt(b.cam.MinZoom(), lock)), pinchPositionAt(camera.ZoomMax, lock))
				want := camera.Zoom(math.Round(math.Exp(g.pinchPosition) * float64(lock)))
				if g.pinchZoom < lock && want >= lock || g.pinchZoom > lock && want <= lock {
					// Stop only on arrival or crossing, leaving nearby factors freely
					// selectable (DESIGN_GPU_RENDERER §16.6). Hold until finger lift.
					want = lock
					g.pinchLatched = true
				}
				g.pinchZoom = want
				// Follow the fingers directly; only wheel input uses the short ease.
				b.cam.SetZoomAbout(g.anchorX, g.anchorY, want)
				b.cam.ClearFollow()
				b.pendingFollowInput = nil
				b.zoom.Reset()
			}
		}
		if event.Ended {
			g.pinchActive = false
		}
	}
}

// Start each gesture from the live factor without snapping nearby positions.
func pinchPositionAt(zoom, lock camera.Zoom) float64 {
	return math.Log(float64(zoom) / float64(lock))
}
