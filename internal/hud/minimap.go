package hud

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/world"
)

// MinimapHUD holds retail minimap HUD state that is presentation-only (I6).
// It owns the HUD rect (derived from side anchors or fallback) and the dirty/blink word.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type MinimapHUD struct {
	Rect           Rect
	DirtyBlink     uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	BlinkCountdown int16  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// NewMinimapHUD creates a HUD minimap state.
//
// Anchors may be zero value → fallback is used [PLAN_12][07 §6].
// Fallback is the inclusive HUD rect for the 126 canvas (0,0,126,126 inclusive fallback per spec) [07 §10][minimap §4].
// When anchors is non-zero the fallback still wins if non-zero, because the 30 mandatory anchors
// do not name a minimap anchor; search for first non-empty anchor only when fallback is zero.
func NewMinimapHUD(anchors Anchors, fallback Rect) *MinimapHUD {
	// Default fallback per spec: 0,0,126,126 inclusive [07 §10][minimap §4] 126 long side.
	defaultFallback := Rect{X1: 0, Y1: 0, X2: 126, Y2: 126}
	if fallback == (Rect{}) {
		fallback = defaultFallback
	}
	var r Rect
	if anchors == (Anchors{}) {
		r = fallback
	} else {
		// Anchors non-zero but no dedicated minimap anchor; prefer explicit fallback.
		if fallback != (Rect{}) {
			r = fallback
		} else {
			for _, a := range anchors {
				if a != (Rect{}) {
					r = a
					break
				}
			}
			if r == (Rect{}) {
				r = defaultFallback
			}
		}
	}
	if r == (Rect{}) {
		r = defaultFallback
	}
	return &MinimapHUD{
		Rect:           r,
		DirtyBlink:     0,
		BlinkCountdown: 7, // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (h *MinimapHUD) HitTest(x, y int32) bool {
	if h == nil {
		return false
	}
	left, top, right, bottom := h.Rect.Ordered()
	return x >= left && x <= right && y >= top && y <= bottom
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Conversion is TRUNC via IDIV [07 §10][minimap §8] using camera.Minimap.WorldToRadar.
// rx = worldX*RadarW/PlayRight + OriginX, ry = (worldZ - (worldY>>1))*RadarH/PlayBottom + OriginY with Y==0 here [minimap §7].
func (h *MinimapHUD) WorldToMinimap(worldX, worldZ int32, playW, playH int32, m camera.Minimap) (rx, ry int32) {
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return m.WorldToRadar(worldX, worldZ, playW, playH)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (h *MinimapHUD) MinimapToWorld(rx, ry int32, playW, playH int32, m camera.Minimap) (wx, wz int32) {
	return m.RadarToWorld(rx, ry, playW, playH)
}

// ViewportRect returns the camera viewport rect projected onto minimap canvas (inclusive) for drawing the lens rectangle
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// (DIRECT-STATIC for DDA vs DD5 per research/minimap/gaps/02-contacts-viewport.md §1) [minimap §8][07 §10].
// TODO(question): hiColor DDA vs DD5 exact RGB remains TODO(question) pending palette dump; placeholder kept.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	r := Rect{X1: left, Y1: top, X2: right, Y2: bottom}
	// Clip to HUD rect inclusive [07 §10][minimap §4].
	hl, ht, hr, hb := h.Rect.Ordered()
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Wpix = CellW*16, Hpix = CellH*16. Uses Terrain.PlayRight/PlayBottom when valid, else computes.
func PlaySizeForMinimap(t *world.Terrain) (playW, playH int32) {
	if t == nil {
		return 0, 0
	}
	if t.PlayRight != 0 || t.PlayBottom != 0 {
		return t.PlayRight, t.PlayBottom
	}
	// Fallback compute from CellW/H [03 §3.4][minimap §4].
	return t.CellW*16 - 32, t.CellH*16 - 128 // [03 §3.4] PlayRight=Wpix-32, PlayBottom=Hpix-128
}
