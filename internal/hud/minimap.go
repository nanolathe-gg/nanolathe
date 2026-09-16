package hud

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// The radar canvas itself is fixed in compiled-in coordinates — a 126-pixel
// square at the surface's top-left corner, with the map's aspect deciding only
// the letterbox pad inside it — and is one of the elements that does not move
// when the display mode changes, unlike everything anchored to `W` or `H`
// [03 §3.7][03 R-MM-01 §1][07 R-HUD-05]. `camera.LayoutMinimap` is the whole of
// that arithmetic and `camera.Minimap.HitTest` the matching inclusive hit test;
// this file adds only the viewport marker drawn over it.

// ViewportMarkerLogicalColor is the logical palette entry the minimap's
// viewport rectangle is stroked in: entry 14 of the logical→physical map,
// whose GUIPAL source is (255,255,85) and which resolves to physical 194 in
// the stock install [03 R-MM-01 §1][03 §4.3].
//
// It is the same entry the minimap's 1×1 projectile dot uses [03 §3.9]; the
// two figures share one colour-map slot. It is NOT entry 15 — that is the
// world composer's film-mode crosshair and the weapon/interceptor rings.
const ViewportMarkerLogicalColor byte = 14

// MinimapViewportRect returns the camera-to-radar rectangle: the game viewport
// projected through the radar lens, in inclusive display coordinates, ready
// for the one-pixel outline [03 R-MM-01 §1]. The rectangle is drawn against an
// explicit destination, which may have a nonzero origin or a negotiated size.
//
// Retail recomputes this record inside the per-axis camera clamp, so it always
// matches the origin the world was drawn from, and the HUD's minimap repaint
// pre-pass strokes it onto the destination surface after the FINAL radar
// surface is copied there. Canvas-space arithmetic is
//
//	left   = padX + trunc(RadarW · cameraX  / PlayRight)
//	top    = padY + trunc(RadarH · cameraZ  / PlayBottom)
//	right  = left - 1 + trunc(RadarW · viewWidth  / PlayRight)
//	bottom = top  - 1 + trunc(RadarH · viewHeight / PlayBottom)
//
// with no half-height shear: this is a camera origin, not a unit position
// [03 R-MM-01 §1].
//
// Two frames of reference meet here. Retail's camera origin is the world point
// at the *game viewport's* top-left; this build's Camera.X/Z is the world point
// at the *framebuffer's* top-left, because the world is composed across the
// whole framebuffer and the chrome painted over it (see Camera.BattleView and
// Camera.JumpToBattleViewCenter). Adding OriginX/OriginY converts one into the
// other, and Camera.BattleView supplies the matching viewport extents. Doing
// the conversion here — rather than halving the framebuffer — is what keeps
// the rectangle over the terrain the player is actually looking at, and it
// stays correct once a camera origin is allowed to go negative.
//
// The second return reports whether a rectangle exists at all; a caller with no
// camera, no lens or no play area draws nothing rather than a placeholder.
func MinimapViewportRect(cam *camera.Camera, m camera.Minimap, playW, playH int32, dst Rect) (Rect, bool) {
	if cam == nil || m.W <= 0 || m.H <= 0 || playW <= 0 || playH <= 0 {
		return Rect{}, false
	}
	viewW, viewH := cam.BattleView()
	if viewW <= 0 || viewH <= 0 {
		return Rect{}, false
	}
	// Retail holds the viewport's extents as cell counts — the subrect span
	// shifted right by four — and shifts them back left for this projection,
	// so a span that is not a multiple of 16 (the 536 of 800x600) is floored
	// to one (528). At 640x480 both spans are multiples of 16 [03 R-MM-01 §1]
	// [07 R-HUD-05].
	viewW &^= 15
	viewH &^= 15
	// Retail's camera origin, from this build's framebuffer-origin camera
	// [03 §4.1][03 R-MM-01 §1]. The leading inset is the camera's own, not the
	// authored constant: at the detail view the inset covers half as much world
	// (128 screen pixels are 64 world pixels), and BattleViewOrigin is the one
	// definition of that conversion — the same one BattleView above already
	// uses for the span (DESIGN_GPU_RENDERER §14.2).
	camX, camZ := cam.BattleViewOrigin()

	// Signed truncating divides throughout, as retail's are [03 R-MM-01 §1].
	left := m.PadX + int32(int64(camX)*int64(m.W)/int64(playW))
	top := m.PadY + int32(int64(camZ)*int64(m.H)/int64(playH))
	right := left - 1 + int32(int64(viewW)*int64(m.W)/int64(playW))
	bottom := top - 1 + int32(int64(viewH)*int64(m.H)/int64(playH))

	// The projection is canvas-local. Scale both endpoints into the
	// destination rectangle, which may have a nonzero origin or a negotiated
	// size [07 §10][03 §3.6]. CanvasToDisplay rejects out-of-canvas points, so
	// scale the endpoints directly and let the destination clip decide.
	hl, ht, hr, hb := dst.Ordered()
	dw, dh := hr-hl+1, hb-ht+1
	if dw <= 0 || dh <= 0 {
		return Rect{}, false
	}
	r := Rect{
		X1: hl + left*dw/camera.MinimapLongSide,
		Y1: ht + top*dh/camera.MinimapLongSide,
		X2: hl + right*dw/camera.MinimapLongSide,
		Y2: ht + bottom*dh/camera.MinimapLongSide,
	}
	if r.X1 > r.X2 {
		r.X1, r.X2 = r.X2, r.X1
	}
	if r.Y1 > r.Y2 {
		r.Y1, r.Y2 = r.Y2, r.Y1
	}
	// Retail clips the four segments to the destination surface, not to the
	// radar rect; in the ordinary domain the clamped camera keeps the rectangle
	// inside the radar rect anyway [03 R-MM-01 §1]. Reject only a rectangle
	// that misses the destination entirely — the stroke itself clips.
	if r.X2 < hl || r.X1 > hr || r.Y2 < ht || r.Y1 > hb {
		return Rect{}, false
	}
	return r, true
}
