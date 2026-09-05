package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// ViewportTransform is the immutable boundary between logical input pixels,
// beam projection pixels, and authoritative world coordinates. The battle
// surface is the shell-relative rectangle whose beam origin is (128,32);
// logical coordinates remain the negotiated Ebitengine coordinates, normally
// 640×480 [03 §2.5][07 §8][07 §10].
//
// Camera is copied at construction. Updating a camera therefore requires a new
// transform, which prevents a presentation update from silently changing an
// already-issued order target (I6). Terrain is read-only geometry used by the
// retail inverse ground search; it is never mutated by this type.
type ViewportTransform struct {
	Camera  camera.Camera
	Terrain *world.Terrain

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

// Width returns the number of pixels an inclusive rectangle spans across.
func (r ViewportRect) Width() int32 {
	if r.Right < r.Left {
		return 0
	}
	return r.Right - r.Left + 1
}

// Height returns the number of pixels an inclusive rectangle spans down.
func (r ViewportRect) Height() int32 {
	if r.Bottom < r.Top {
		return 0
	}
	return r.Bottom - r.Top + 1
}

// Contains reports whether a logical pointer is in the rectangle.
func (r ViewportRect) Contains(x, y int32) bool {
	return x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom
}

// HUDRegion identifies which input region consumes a logical pointer.
type HUDRegion uint8

const (
	RegionWorld HUDRegion = iota
	RegionHUD
)

// NewViewportTransform constructs the canonical 640×480 transform. Width and
// height may be overridden for a negotiated logical surface. A nil camera is
// treated as the zero camera; a nil terrain retains the ground-plane inverse.
func NewViewportTransform(cam *camera.Camera, terrain *world.Terrain, width, height int32) ViewportTransform {
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
		Terrain:       terrain,
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

// SurfaceToBeam converts an indexed-surface point back to projection space.
func (v ViewportTransform) SurfaceToBeam(p Point) Point {
	return Point{X: p.X + v.BeamOrigin.X, Y: p.Y + v.BeamOrigin.Y}
}

// BeamToViewport rebases a beam point to shell-relative battle viewport
// coordinates. It does not clamp or change the point.
func (v ViewportTransform) BeamToViewport(p Point) Point {
	return Point{X: p.X - v.BeamOrigin.X + v.Viewport.Left, Y: p.Y - v.BeamOrigin.Y + v.Viewport.Top}
}

// ViewportToBeam converts shell-relative battle coordinates to beam pixels.
func (v ViewportTransform) ViewportToBeam(p Point) Point {
	return Point{X: p.X - v.Viewport.Left + v.BeamOrigin.X, Y: p.Y - v.Viewport.Top + v.BeamOrigin.Y}
}

// WorldToViewport composes WorldToBeam and BeamToViewport. This is the only
// projection a world presentation overlay should need.
func (v ViewportTransform) WorldToViewport(x, y, z numeric.Fixed) Point {
	return v.BeamToViewport(v.WorldToBeam(x, y, z))
}

// ViewportToWorldOnTerrain resolves a logical pointer against the terrain.
// The bool is false for HUD/outside-viewport input; callers must not dispatch
// an order from such a point. For world input, the terrain resolver performs
// retail's clamp, nine-probe, and projected-row interpolation [07 §8].
func (v ViewportTransform) ViewportToWorldOnTerrain(p Point) (x, y, z numeric.Fixed, ok bool) {
	if !v.Viewport.Contains(p.X, p.Y) {
		return 0, 0, 0, false
	}
	beam := v.ViewportToBeam(p)
	fx, fz := v.Camera.ScreenToWorld(beam.X, beam.Y)
	if v.Terrain == nil {
		return fx, 0, fz, true
	}
	returnTerrainX, returnTerrainY, returnTerrainZ := v.Terrain.CursorToWorldMapPixels(int32(fx>>16), int32(fz>>16))
	return returnTerrainX, returnTerrainY, returnTerrainZ, true
}

// OrderTargetFromViewport is the explicit cursor/order bridge. It is an alias
// with a domain name so input code cannot accidentally use the height-zero
// inverse when issuing a terrain order [07 §8].
func (v ViewportTransform) OrderTargetFromViewport(p Point) (x, y, z numeric.Fixed, ok bool) {
	return v.ViewportToWorldOnTerrain(p)
}

// OrderTargetToViewport projects an authoritative order target for cursor,
// marker, and round-trip diagnostics.
func (v ViewportTransform) OrderTargetToViewport(x, y, z numeric.Fixed) Point {
	return v.WorldToViewport(x, y, z)
}

// ViewportRectToWorld converts a shell-relative selection rectangle to a
// ground-plane world rectangle. Endpoints are independently sorted, as retail
// does for rubber-band selection [07 §9]. It intentionally does not run the
// terrain height search: selection bounds are a presentation rectangle, not an
// order target.
func (v ViewportTransform) ViewportRectToWorld(r ViewportRect) (minX, minZ, maxX, maxZ numeric.Fixed, ok bool) {
	if !v.Viewport.Contains(r.Left, r.Top) && !v.Viewport.Contains(r.Right, r.Bottom) {
		return 0, 0, 0, 0, false
	}
	a := v.ViewportToBeam(Point{X: r.Left, Y: r.Top})
	b := v.ViewportToBeam(Point{X: r.Right, Y: r.Bottom})
	ax, az := v.Camera.ScreenToWorld(a.X, a.Y)
	bx, bz := v.Camera.ScreenToWorld(b.X, b.Y)
	if ax > bx {
		ax, bx = bx, ax
	}
	if az > bz {
		az, bz = bz, az
	}
	return ax, az, bx, bz, true
}

// ViewportToHUDRegion is the single region predicate for cursor routing. A
// pointer outside the shell battle rectangle is HUD, including the lower
// panel band; no terrain inverse is attempted there [07 §8].
func (v ViewportTransform) ViewportToHUDRegion(p Point) HUDRegion {
	if v.Viewport.Contains(p.X, p.Y) {
		return RegionWorld
	}
	return RegionHUD
}

// ViewportContains reports whether an input point may reach world picking.
func (v ViewportTransform) ViewportContains(p Point) bool {
	return v.Viewport.Contains(p.X, p.Y)
}
