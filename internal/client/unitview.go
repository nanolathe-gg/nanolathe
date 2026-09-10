package client

// Unit screen projection and pick helpers.
//
// Presentation samples the committed frame with no interpolation [03 §2.4]
// [I6]: the simulation is authoritative at 30 Hz and the renderer consumes
// committed UnitViews after each completed sub-tick [03 §2.5] [07 §9].

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// IsUnitViewInRect reports whether the projected UnitView falls inside the
// inclusive drag rectangle [07 §9] C6. Rect is shell (window) coords — the
// PRESENTED picture's own pixels — while the projection is at the record step,
// so the rectangle converts first, exactly as SnapshotUnitHandlesInRect's does
// (DESIGN_GPU_RENDERER §16.4). recordRect is the identity at every rest factor.
func IsUnitViewInRect(cam *camera.Camera, v frame.UnitView, rect Rect) bool {
	if cam == nil || rectEmpty(rect) {
		return false
	}
	rect = recordRect(cam, rect)
	p := NewViewportTransform(cam, nil, 0, 0).WorldToSurface(v.X, v.Y, v.Z)
	return rect.Contains(p.X, p.Y)
}

// IsUnitInRect reports whether the live Unit's projected position is inside
// the inclusive drag rectangle [07 §9] C6. Rect is shell, and converts the same
// way IsUnitViewInRect's does (§16.4).
func IsUnitInRect(cam *camera.Camera, u *units.Unit, rect Rect) bool {
	if cam == nil || u == nil || rectEmpty(rect) {
		return false
	}
	rect = recordRect(cam, rect)
	p := NewViewportTransform(cam, nil, 0, 0).WorldToSurface(u.X, u.Y, u.Z)
	return rect.Contains(p.X, p.Y)
}
