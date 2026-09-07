package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
)

// PlaySizeForMinimap returns the playable map extents used by the radar lens.
func PlaySizeForMinimap(t *world.Terrain) (int32, int32) {
	return hud.PlaySizeForMinimap(t)
}

// appendMinimapPoint appends one framebuffer-clipped single-pixel write to the
// point arena, applying the same [0,width)x[0,height) guard the former putIndexed
// writer did: the PointPlain sink writes each recorded point unconditionally, so
// out-of-bounds points must be dropped here to stay byte-identical (WU-1.7b). The
// batch is closed and recorded by emitPoints as a self-owned arena sub-slice
// (WU-1.8).
func (c *Client) appendMinimapPoint(x, y int32, value byte) {
	if x < 0 || y < 0 || x >= int32(c.width) || y >= int32(c.height) {
		return
	}
	c.pointArena = append(c.pointArena, drawlist.Point{X: x, Y: y, Index: value})
}

// DrawMinimapLayout draws a radar surface through the canonical 126-pixel
// canvas. The display rectangle may be scaled, but its aspect and letterbox
// are always those in layout; bars remain untouched.
//
// It draws the radar surface only. The viewport rectangle is a separate figure
// with a separate producer and is stroked afterwards by
// DrawMinimapViewportRect, exactly as retail's minimap repaint pre-pass copies
// the FINAL surface and then strokes the camera-to-radar rectangle over it
// [03 R-MM-01 §1][03 R-COMP-02 §5].
//
// It draws no five-pixel cross. That figure belongs to the **world**
// composition: the world composer draws it, at debug display mode 2, over the
// game viewport at the screen position of the ground resolver's world point,
// and mode 2 is reachable only from film mode [03 §3.12].
func (c *Client) DrawMinimapLayout(surf *render.RadarSurface, dst hud.Rect, layout camera.Minimap) {
	if c == nil || surf == nil || surf.W <= 0 || surf.H <= 0 || len(surf.Bits) < surf.W*surf.H || layout.W <= 0 || layout.H <= 0 {
		return
	}
	dl, dt, dr, db := dst.Ordered()
	dw, dh := dr-dl+1, db-dt+1
	if dw <= 0 || dh <= 0 {
		return
	}
	// The radar surface is recorded as one plain single-pixel batch that replays
	// under the committed-frame list in per-frame order; the sampling and clip are
	// unchanged from the direct putIndexed loop (docs/DESIGN_GPU_RENDERER.md
	// §2.2)[03 R-MM-01 §1]. The batch is a self-owned sub-slice of the point arena,
	// immutable for the frame (WU-1.8).
	off := len(c.pointArena)
	for y := int32(0); y < dh; y++ {
		canvasY := y * camera.MinimapLongSide / dh
		if canvasY < layout.PadY || canvasY > layout.Bottom() {
			continue
		}
		sy := (canvasY - layout.PadY) * int32(surf.H) / layout.H
		if sy < 0 || sy >= int32(surf.H) {
			continue
		}
		for x := int32(0); x < dw; x++ {
			canvasX := x * camera.MinimapLongSide / dw
			if canvasX < layout.PadX || canvasX > layout.Right() {
				continue
			}
			sx := (canvasX - layout.PadX) * int32(surf.W) / layout.W
			if sx >= 0 && sx < int32(surf.W) {
				c.appendMinimapPoint(dl+x, dt+y, surf.Bits[int(sy)*surf.W+int(sx)])
			}
		}
	}
	c.emitPoints(off, drawlist.PointPlain)
}

// DrawMinimapViewportRect strokes the camera-to-radar rectangle as a one-pixel
// outline in the supplied physical palette index, clipped to the destination
// [03 R-MM-01 §1].
//
// Retail joins the four inclusive corners with four clipped one-pixel Bresenham
// segments through the shared rectangle-outline primitive; the figure is never
// filled. Callers resolve the colour through the logical→physical map from
// hud.ViewportMarkerLogicalColor.
func (c *Client) DrawMinimapViewportRect(dst hud.Rect, marker hud.Rect, color byte) {
	if c == nil {
		return
	}
	dl, dt, dr, db := dst.Ordered()
	ml, mt, mr, mb := marker.Ordered()
	if mr < dl || ml > dr || mb < dt || mt > db {
		return
	}
	// The four inclusive edges are recorded as one plain single-pixel batch that
	// replays after the radar surface, exactly as retail strokes the rectangle over
	// the copied surface (docs/DESIGN_GPU_RENDERER.md §2.2)[03 R-MM-01 §1].
	// Horizontal runs at top and bottom, vertical runs at left and right; the spans
	// are inclusive and every pixel is clipped to the destination. The batch is a
	// self-owned sub-slice of the point arena, immutable for the frame (WU-1.8).
	off := len(c.pointArena)
	for x := ml; x <= mr; x++ {
		if x < dl || x > dr {
			continue
		}
		if mt >= dt && mt <= db {
			c.appendMinimapPoint(x, mt, color)
		}
		if mb >= dt && mb <= db {
			c.appendMinimapPoint(x, mb, color)
		}
	}
	for y := mt; y <= mb; y++ {
		if y < dt || y > db {
			continue
		}
		if ml >= dl && ml <= dr {
			c.appendMinimapPoint(ml, y, color)
		}
		if mr >= dl && mr <= dr {
			c.appendMinimapPoint(mr, y, color)
		}
	}
	c.emitPoints(off, drawlist.PointPlain)
}

// CameraIntent is a presentation-only camera target. The client computes the
// target from the minimap lens, while the battle composition owner applies it
// to the canonical camera [07 §10][I6].
type CameraIntent struct {
	X, Z int32
}

// MinimapPointerWorld is step 1 of the host frame for a pointer over the
// minimap: the pointer's world position is the lens conversion
//
//	worldX = (ptrX − padX) · PlayRight  / RadarW
//	worldZ = (ptrY − padY) · PlayBottom / RadarH
//
// with signed truncating divisions and **no** half-viewport term
// [07 R-CAM-01 §11]. This one conversion feeds every consumer: the hover
// target, the order the left button issues at that point, and the camera jump
// the right button latches. Screen input is first converted to the same local
// 126-pixel canvas used for drawing, so drawing and input can never disagree.
//
// ok is false unless the pointer is inside the fitted radar rectangle; the
// letterbox bars are part of the canvas but not of the lens.
func MinimapPointerWorld(layout camera.Minimap, dst hud.Rect, playW, playH int32, mouseX, mouseY int32) (int32, int32, bool) {
	dl, dt, dr, db := dst.Ordered()
	dw, dh := dr-dl+1, db-dt+1
	if dw <= 0 || dh <= 0 || mouseX < dl || mouseX > dr || mouseY < dt || mouseY > db {
		return 0, 0, false
	}
	canvasX, canvasY, ok := layout.DisplayToCanvas(mouseX, mouseY, dl, dt, dw, dh)
	if !ok || !layout.HitTest(canvasX, canvasY) {
		return 0, 0, false
	}
	wx, wz := layout.ToWorldPlay(canvasX, canvasY, playW, playH)
	return wx, wz, true
}

// MinimapCameraIntent computes the minimap latch's camera origin: the clicked
// map point becomes the **centre** of the view [07 R-CAM-01 §11].
//
// Corrects the superseded reading this function used to implement — "the lens
// writes the projected world point directly as the camera origin", which put
// the clicked point at the view's top-left corner. [07 R-CAM-01 §11] traces the
// camera write as
//
//	cameraX = (ptrX − padX) · PlayRight  / RadarW − trunc(viewWidth  / 2)
//	cameraZ = (ptrY − padY) · PlayBottom / RadarH − trunc(viewHeight / 2)
//
// and names doc 03 §3.11's matching sentence as carrying the same error; the
// recenter-free form is the *pointer's* world position above, not the camera.
// Camera.JumpToBattleViewCenter owns that conversion for this build's
// framebuffer-origin camera, so this returns the world point and the caller
// hands it to the canonical camera writer — there is one recenter, in one
// place, and it stays right when the clamp's floor changes.
//
// The former drag arm and its marker are gone. [07 R-CAM-01 §11]
// establishes that the minimap has no drag branch of its own: while the latch
// is held every host frame re-runs this same jump from the pointer record, so
// dragging across the minimap pans continuously. The "alternate drag branch"
// that TODO preserved is the world view's Ctrl+right cursor-warp drag-scroll,
// which never fires over the minimap.
func MinimapCameraIntent(layout camera.Minimap, dst hud.Rect, playW, playH int32, mouseX, mouseY int32) (CameraIntent, bool) {
	wx, wz, ok := MinimapPointerWorld(layout, dst, playW, playH, mouseX, mouseY)
	if !ok {
		return CameraIntent{}, false
	}
	return CameraIntent{X: wx, Z: wz}, true
}
