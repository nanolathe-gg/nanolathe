package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
)

// PlaySizeForMinimap returns the playable map extents used by the radar lens.
func PlaySizeForMinimap(t *world.Terrain) (int32, int32) {
	return hud.PlaySizeForMinimap(t)
}

func (c *Client) putIndexed(x, y int32, value byte) {
	if x < 0 || y < 0 || x >= int32(c.width) || y >= int32(c.height) {
		return
	}
	c.indexed[int(y)*c.width+int(x)] = value
}

// DrawMinimapLayout draws a radar surface through the canonical 126-pixel
// canvas. The display rectangle may be scaled, but its aspect and letterbox
// are always those in layout; bars remain untouched. markerCenterX/Y are
// canvas-local coordinates and markerMode 2 draws the five-pixel cross.
func (c *Client) DrawMinimapLayout(surf *render.RadarSurface, dst hud.Rect, layout camera.Minimap, markerMode byte, markerCenterX, markerCenterY int32, paletteViewport byte) {
	if c == nil || surf == nil || surf.W <= 0 || surf.H <= 0 || len(surf.Bits) < surf.W*surf.H || layout.W <= 0 || layout.H <= 0 {
		return
	}
	dl, dt, dr, db := dst.Ordered()
	dw, dh := dr-dl+1, db-dt+1
	if dw <= 0 || dh <= 0 {
		return
	}
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
				c.putIndexed(dl+x, dt+y, surf.Bits[int(sy)*surf.W+int(sx)])
			}
		}
	}
	if markerMode != 2 || paletteViewport == 0 {
		return
	}
	// The marker is authored in the fixed 126-pixel canvas. Clip each arm
	// pixel against the inclusive fitted radar rect before converting it to
	// the display rectangle; clipping only the crossing lets the ±2 arms leak
	// into aspect letterbox bars at the extreme map edges [03 §3.12][07 §10].
	for _, p := range [][2]int32{{0, 0}, {-2, 0}, {2, 0}, {0, -2}, {0, 2}} {
		canvasX, canvasY := markerCenterX+p[0], markerCenterY+p[1]
		if !layout.HitTest(canvasX, canvasY) {
			continue
		}
		displayX, displayY, ok := layout.CanvasToDisplay(canvasX, canvasY, dl, dt, dw, dh)
		if ok {
			c.putIndexed(displayX, displayY, paletteViewport)
		}
	}
}

// CameraIntent is a presentation-only camera target. The client computes the
// target from the minimap lens, while the battle composition owner applies it
// to the canonical camera [07 §10][I6].
type CameraIntent struct {
	X, Z int32
}

// MinimapCameraIntent computes the direct-origin lens target. Screen input is
// first converted to the same local 126-pixel canvas used for drawing;
// letterbox padding is removed before world scaling. The alternate drag path
// uses the established viewport-delta form. No camera state is mutated here.
func MinimapCameraIntent(currentX, currentZ int32, layout camera.Minimap, dst, viewport hud.Rect, playW, playH int32, mouseX, mouseY int32, isInside, dragActive bool) (CameraIntent, bool) {
	dl, dt, dr, db := dst.Ordered()
	dw, dh := dr-dl+1, db-dt+1
	if dw <= 0 || dh <= 0 {
		return CameraIntent{}, false
	}
	drag := dragActive
	canvasX, canvasY, _ := layout.DisplayToCanvas(mouseX, mouseY, dl, dt, dw, dh)
	if isInside && !layout.HitTest(canvasX, canvasY) {
		isInside = false
	}
	if !isInside && !drag {
		return CameraIntent{}, false
	}
	if isInside && !drag {
		// The lens writes the projected world point directly as the camera
		// origin. The canonical camera owner applies its clamp [03 §3.11].
		x, z := layout.ToWorldPlay(canvasX, canvasY, playW, playH)
		return CameraIntent{X: x, Z: z}, true
	}
	// TODO(question): the retail alternate minimap drag/current-camera branch
	// boundary and clamp vectors are not fully reduced. Preserve the established
	// viewport-delta shape until a trace settles its exact gate.
	vl, vt, vr, vb := viewport.Ordered()
	clampedX, clampedY := mouseX, mouseY
	if clampedX < vl {
		clampedX = vl
	} else if clampedX > vr {
		clampedX = vr
	}
	if clampedY < vt {
		clampedY = vt
	} else if clampedY > vb {
		clampedY = vb
	}
	return CameraIntent{X: currentX + clampedX - vl, Z: currentZ + clampedY - vt}, true
}
