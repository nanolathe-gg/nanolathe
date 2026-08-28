package hud

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/world"
)

// MinimapHUD holds retail minimap HUD state that is presentation-only (I6).
// It owns the HUD rect (derived from side anchors or fallback) and the dirty/blink word.
// Rect is inclusive [07 §10][03 §3.6]. DirtyBlink carries blink phase plus
// FINAL and MAPPED dirty bits; BlinkCountdown drives the eight-frame blink
// cadence [03 §3.6].
type MinimapHUD struct {
	Rect           Rect
	DirtyBlink     uint16 // blink phase and FINAL/MAPPED dirty bits [03 §3.6]
	BlinkCountdown int16  // countdown 7..0 [03 §3.6]
}

// NewMinimapHUD creates a HUD minimap state. The minimap rail anchor is not
// one of the 30 SIDEDATA anchors, so callers must supply an authored rect from
// the UI surface. An empty fallback remains empty until that route is traced;
// no synthetic canvas-sized rectangle is installed [07 §6][07 §10].
func NewMinimapHUD(anchors Anchors, fallback Rect) *MinimapHUD {
	_ = anchors
	// TODO(question): identify the authored rail minimap anchor in the GUI
	// surface; the 30 side anchors contain no minimap record [07 §6].
	r := fallback
	return &MinimapHUD{
		Rect:           r,
		DirtyBlink:     0,
		BlinkCountdown: 7, // 7..0 drives blink ^=1 every 8 host frames [03 §3.6]
	}
}

// HitTest reports whether (x,y) lies inside the inclusive HUD rect [07 §10][03 §3.11].
func (h *MinimapHUD) HitTest(x, y int32) bool {
	if h == nil {
		return false
	}
	left, top, right, bottom := h.Rect.Ordered()
	return x >= left && x <= right && y >= top && y <= bottom
}

// WorldToMinimap projects world map pixels to minimap canvas coordinates [03 §3.9][03 §3.11].
// worldX,worldZ are in map pixels [0,PlayRight/Bottom). playW/playH are PlayRight/Bottom = Wpix-32/Hpix-128 [03 §3.4].
// Conversion truncates through camera.Minimap.WorldToRadar [07 §10][03 §3.11].
// rx = worldX*RadarW/PlayRight + OriginX, ry = (worldZ - (worldY>>1))*RadarH/PlayBottom + OriginY with Y==0 here [03 §3.9].
func (h *MinimapHUD) WorldToMinimap(worldX, worldZ int32, playW, playH int32, m camera.Minimap) (rx, ry int32) {
	// Delegates to camera lens arithmetic [07 §10][03 §3.11].
	return m.WorldToRadar(worldX, worldZ, playW, playH)
}

// MinimapToWorld inverts WorldToMinimap with truncating integer arithmetic [03 §3.11].
func (h *MinimapHUD) MinimapToWorld(rx, ry int32, playW, playH int32, m camera.Minimap) (wx, wz int32) {
	return m.RadarToWorld(rx, ry, playW, playH)
}

// ViewportRect returns the camera viewport rect projected onto the inclusive
// minimap canvas for the one-pixel viewport marker [03 §3.12][07 §10]. It is
// clipped to the HUD rect and not filled. TODO(question): the exact authored
// viewport-marker palette color remains unknown; callers supply the placeholder.
func (h *MinimapHUD) ViewportRect(cam *camera.Camera, m camera.Minimap, playW, playH int32) Rect {
	if h == nil || cam == nil || m.W <= 0 || m.H <= 0 || playW <= 0 || playH <= 0 {
		if h != nil {
			return h.Rect
		}
		return Rect{}
	}
	eW, eH := cam.ViewW, cam.ViewH
	if cam.Scale != 0 {
		eW, eH = cam.EffectiveView()
	}
	if eW < 1 {
		eW = 1
	}
	if eH < 1 {
		eH = 1
	}
	// Project top-left and bottom-right inclusive with the minimap shear [03 §3.9].
	rx0, ry0 := m.WorldToRadar(cam.X, cam.Z, playW, playH)
	rx1, ry1 := m.WorldToRadar(cam.X+eW-1, cam.Z+eH-1, playW, playH)
	left, right := rx0, rx1
	if left > right {
		left, right = right, left
	}
	top, bottom := ry0, ry1
	if top > bottom {
		top, bottom = bottom, top
	}
	// The projection is canvas-local. Convert both endpoints through the
	// destination rectangle before clipping; the HUD rect is display-space and
	// may have a nonzero origin or a negotiated scale [07 §10][03 §3.6].
	hl, ht, hr, hb := h.Rect.Ordered()
	dw, dh := hr-hl+1, hb-ht+1
	dx0, dy0, ok0 := m.CanvasToDisplay(left, top, hl, ht, dw, dh)
	dx1, dy1, ok1 := m.CanvasToDisplay(right, bottom, hl, ht, dw, dh)
	if !ok0 || !ok1 {
		return h.Rect
	}
	r := Rect{X1: dx0, Y1: dy0, X2: dx1, Y2: dy1}
	if r.X1 > r.X2 {
		r.X1, r.X2 = r.X2, r.X1
	}
	if r.Y1 > r.Y2 {
		r.Y1, r.Y2 = r.Y2, r.Y1
	}
	// Clip to HUD rect inclusive [07 §10][03 §3.6].
	if r.X1 < hl {
		r.X1 = hl
	}
	if r.Y1 < ht {
		r.Y1 = ht
	}
	if r.X2 > hr {
		r.X2 = hr
	}
	if r.Y2 > hb {
		r.Y2 = hb
	}
	if r.X1 > r.X2 || r.Y1 > r.Y2 {
		// Degenerate clipped away; return clamped single pixel at centre of HUD intersection
		// Keep 1-pixel minimum [07 §6] lens 1-pixel Bresenham.
		r.X1 = hl
		r.Y1 = ht
		r.X2 = hl
		r.Y2 = ht
	}
	return r
}

// PlaySizeForMinimap returns PlayRight = Wpix-32, PlayBottom = Hpix-128 in map pixels [03 §3.4].
// Wpix = CellW*16, Hpix = CellH*16. The map loader must have populated
// PlayRight/PlayBottom; absent authored extents are not reconstructed here.
func PlaySizeForMinimap(t *world.Terrain) (playW, playH int32) {
	if t == nil {
		return 0, 0
	}
	return t.PlayRight, t.PlayBottom
}
