package main

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Presentation feel choice: about 12% net magnification triggers one stop.
// This is intentionally independent of wheel cooldown (DESIGN_GPU_RENDERER §16.6).
const pinchThreshold = 0.12

type battleGestures struct {
	panX, panY             float64 // fractional world pixels carried between direct deltas
	pinchActive, pinchUsed bool
	magnification          float64
	anchorX, anchorY       int32
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
	if mouse.PanX != 0 || mouse.PanY != 0 {
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
	for _, event := range mouse.Pinches {
		if event.Began {
			g.pinchActive, g.pinchUsed, g.magnification = true, false, 0
			g.anchorX, g.anchorY = beamAnchor(x, y)
		}
		if event.Cancelled {
			g.pinchActive = false
		}
		if g.pinchActive && !g.pinchUsed {
			g.magnification += event.Delta
			if math.Abs(g.magnification) >= pinchThreshold {
				// Spend the gesture even at a zoom limit. Reversing or holding the same
				// pinch cannot issue a second step, however long the gesture lasts.
				g.pinchUsed = true
				if next, ok := camera.NextZoomStep(b.zoom.Target(b.cam), g.magnification > 0); ok {
					b.zoom.SetTarget(b.cam, g.anchorX, g.anchorY, next)
				}
			}
		}
		if event.Ended {
			g.pinchActive = false
		}
	}
}
