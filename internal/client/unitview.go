// Package client — unit screen projection and pick helpers for Gate 2.
//
// Presentation is the one deliberate divergence from retail [03 §2.4] (I6):
// the sim is authoritative at 30 Hz and the renderer interpolates
// Previous→Current at render fraction. Units are published as snapshot
// UnitViews after each completed sub-tick; the renderer interpolates with
// alpha clamped [0,1] via snapshot.Lerp and camera.WorldToScreen
// [03 §2.5][07 §9].
package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

// UnitViewToScreen projects a presentation UnitView to orthographic screen
// coordinates [03 §2.5] C1 via camera.WorldToScreen.
func UnitViewToScreen(cam *camera.Camera, v snapshot.UnitView) (int32, int32) {
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
// inclusive drag rectangle [07 §9] C6.
func IsUnitViewInRect(cam *camera.Camera, v snapshot.UnitView, rect Rect) bool {
	if cam == nil || rect.IsEmpty() {
		return false
	}
	sx, sy := UnitViewToScreen(cam, v)
	return rect.Contains(sx, sy)
}

// IsUnitInRect reports whether the live Unit's projected position is inside
// the inclusive drag rectangle [07 §9] C6.
func IsUnitInRect(cam *camera.Camera, u *units.Unit, rect Rect) bool {
	if cam == nil || u == nil || rect.IsEmpty() {
		return false
	}
	sx, sy := UnitToScreen(cam, u)
	return rect.Contains(sx, sy)
}

// FilterViewsInRect returns the indices of views whose projected positions
// fall inside rect inclusive [07 §9] C6. It does not mutate selection; it is a
// pick helper for tests and for the composition root to build a selection set.
func FilterViewsInRect(views []snapshot.UnitView, cam *camera.Camera, rect Rect) []int {
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
func ProjectedPositions(views []snapshot.UnitView, cam *camera.Camera) [][2]int32 {
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

// LerpUnitView interpolates between prev and cur at alpha via snapshot.Lerp
// [03 §2.4] C15/C16 (I6). It is presentation-only.
func LerpUnitView(prev, cur snapshot.UnitView, alpha float32) snapshot.UnitView {
	out := cur
	out.X = snapshot.Lerp(prev.X, cur.X, alpha)
	out.Y = snapshot.Lerp(prev.Y, cur.Y, alpha)
	out.Z = snapshot.Lerp(prev.Z, cur.Z, alpha)
	return out
}

// ProjectedLerpPositions returns interpolated screen positions for
// Previous→Current at alpha. When prev and cur lengths differ, the shorter
// prefix is interpolated and the remainder snaps to cur.
func ProjectedLerpPositions(prev, cur []snapshot.UnitView, cam *camera.Camera, alpha float32) [][2]int32 {
	if cam == nil || len(cur) == 0 {
		return nil
	}
	out := make([][2]int32, len(cur))
	for i, c := range cur {
		var p snapshot.UnitView
		if i < len(prev) && prev[i].Slot == c.Slot {
			p = prev[i]
		} else {
			p = c
		}
		lerped := LerpUnitView(p, c, alpha)
		sx, sy := UnitViewToScreen(cam, lerped)
		out[i][0] = sx
		out[i][1] = sy
	}
	return out
}

// Ensure imports are used.
var _ = numeric.Fixed(0)
