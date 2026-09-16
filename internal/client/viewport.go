package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// ViewportTransform is the immutable boundary between logical input pixels,
// beam projection pixels, and authoritative world coordinates. The battle
// surface is the shell-relative rectangle whose beam origin is (128,32);
// logical coordinates remain the negotiated Ebitengine coordinates, normally
// 640×480 [03 §2.5][07 §8][07 §10].
//
// Camera is copied at construction. Updating a camera therefore requires a new
// transform, which prevents a presentation update from silently changing an
// already-issued order target (I6). Resolving a pointer against the ground is
// the session's own inverse search (Session.CursorToWorld); this type carries
// no terrain.
type ViewportTransform struct {
	Camera camera.Camera

	LogicalWidth  int32
	LogicalHeight int32
	Viewport      ViewportRect
	BeamOrigin    Point
}

// Point is a coordinate in one of the integer presentation spaces.
type Point struct {
	X, Y int32
}

// ViewportRect uses inclusive edges, matching the retail viewport and minimap bounds
// [03 §4.3][07 §8][07 §10].
type ViewportRect struct {
	Left, Top, Right, Bottom int32
}

// Contains reports whether a logical pointer is in the rectangle.
func (r ViewportRect) Contains(x, y int32) bool {
	return x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom
}

// NewViewportTransform constructs the canonical 640×480 transform. Width and
// height may be overridden for a negotiated logical surface. A nil camera is
// treated as the zero camera.
func NewViewportTransform(cam *camera.Camera, width, height int32) ViewportTransform {
	if width <= 0 {
		width = 640
	}
	if height <= 0 {
		height = 480
	}
	var c camera.Camera
	if cam != nil {
		c = *cam
	}
	// The battle viewport is the drawn-chrome region [07 §6][07 §8] step1:
	// left panel 129px (OriginX+1) plus top/bottom strips OriginY (32). Retail's
	// beam clip is [128,32]..[W-1,H-33] but the chrome's interactive region
	// starts one pixel in at 129, and the shell previously rebased this to
	// [0,0]..[W-129,H-65] which paired a rebased rect with unrebased pointers
	// [C-3]. Fix: keep logical coordinates [129,32]..[W-1,H-33] so the pointer
	// region and the drawn overWorld rect agree [07 §8][07 §10][C-3].
	left := camera.OriginX + 1 // 129 [07 §6] drawn panel edge
	top := camera.OriginY      // 32 [07 §6][03 §2.5]
	right := width - 1
	bottom := height - camera.OriginY - 1 // 447 for 480
	if right < left {
		right = left - 1
	}
	if bottom < top {
		bottom = top - 1
	}
	return ViewportTransform{
		Camera:        c,
		LogicalWidth:  width,
		LogicalHeight: height,
		Viewport:      ViewportRect{Left: left, Top: top, Right: right, Bottom: bottom},
		BeamOrigin:    Point{X: camera.OriginX, Y: camera.OriginY},
	}
}

// WorldToBeam applies the established fixed-point orthographic projection.
// The returned point includes the beam origin (128,32) [03 §2.5].
func (v ViewportTransform) WorldToBeam(x, y, z numeric.Fixed) Point {
	sx, sy := v.Camera.WorldToScreen(x, y, z)
	return Point{X: sx, Y: sy}
}

// WorldToSurface projects into the indexed battle surface whose origin is the
// upper-left of the logical framebuffer. It is the shared coordinate used by
// committed-frame picking, selection geometry, and world overlays.
func (v ViewportTransform) WorldToSurface(x, y, z numeric.Fixed) Point {
	p := v.WorldToBeam(x, y, z)
	return Point{X: p.X - v.BeamOrigin.X, Y: p.Y - v.BeamOrigin.Y}
}
