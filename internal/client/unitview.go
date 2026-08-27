// Package client — unit screen projection and pick helpers for Gate 2.
//
// Presentation is the one deliberate divergence from retail [03 §2.4] (I6):
// the sim is authoritative at 30 Hz and the renderer consumes committed
// UnitViews after each completed sub-tick via camera.WorldToScreen [03 §2.5][07 §9].
package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// UnitViewToScreen projects a presentation UnitView to orthographic screen
// coordinates [03 §2.5] C1 via camera.WorldToScreen.
func UnitViewToScreen(cam *camera.Camera, v frame.UnitView) (int32, int32) {
	if cam == nil {
		return 0, 0
	}
	return cam.WorldToScreen(v.X, v.Y, v.Z)
}

// UnitToScreen projects a live simulation Unit to screen [03 §2.5] C1.
func UnitToScreen(cam *camera.Camera, u *units.Unit) (int32, int32) {
	if cam == nil || u == nil {
		return 0, 0
	}
	return cam.WorldToScreen(u.X, u.Y, u.Z)
}

// IsUnitViewInRect reports whether the projected UnitView falls inside the
// inclusive drag rectangle [07 §9] C6. Rect is shell (window) coords.
func IsUnitViewInRect(cam *camera.Camera, v frame.UnitView, rect Rect) bool {
	if cam == nil || rect.IsEmpty() {
		return false
	}
	sx0, sy0 := UnitViewToScreen(cam, v)             // beam
	sx, sy := sx0-camera.OriginX, sy0-camera.OriginY // shell [PLAN_04A C1]
	return rect.Contains(sx, sy)
}

// IsUnitInRect reports whether the live Unit's projected position is inside
// the inclusive drag rectangle [07 §9] C6. Rect is shell.
func IsUnitInRect(cam *camera.Camera, u *units.Unit, rect Rect) bool {
	if cam == nil || u == nil || rect.IsEmpty() {
		return false
	}
	sx0, sy0 := UnitToScreen(cam, u) // beam
	sx, sy := sx0-camera.OriginX, sy0-camera.OriginY
	return rect.Contains(sx, sy)
}

// FilterViewsInRect returns the indices of views whose projected positions
// fall inside rect inclusive [07 §9] C6. It does not mutate selection; it is a
// pick helper for tests and for the composition root to build a selection set.
func FilterViewsInRect(views []frame.UnitView, cam *camera.Camera, rect Rect) []int {
	if cam == nil || len(views) == 0 || rect.IsEmpty() {
		return nil
	}
	var out []int
	for i, v := range views {
		if IsUnitViewInRect(cam, v, rect) {
			out = append(out, i)
		}
	}
	return out
}

// ProjectedPositions returns the screen positions of all views in order.
// It is deterministic and preserves the input order (I1).
func ProjectedPositions(views []frame.UnitView, cam *camera.Camera) [][2]int32 {
	if cam == nil || len(views) == 0 {
		return nil
	}
	out := make([][2]int32, len(views))
	for i, v := range views {
		sx, sy := UnitViewToScreen(cam, v)
		out[i][0] = sx
		out[i][1] = sy
	}
	return out
}

// Ensure imports are used.
var _ = numeric.Fixed(0)
