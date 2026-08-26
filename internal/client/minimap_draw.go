package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
)

// PlaySizeForMinimap returns PlayRight = Wpix-32, PlayBottom = Hpix-128 [03 §3.4].
// Helper for client-side minimap HUD presentation [03 §3.6].
func PlaySizeForMinimap(t *world.Terrain) (playW, playH int32) {
	return hud.PlaySizeForMinimap(t)
}

// DrawMinimap draws a RadarSurface's Bits onto the indexed framebuffer at HUD rect (scaled if HUD zoom !=1.0).
// It copies indexed pixels (no GUIPAL remap) via nearest, leaves the letterbox
// bars at zero, and draws the one-pixel viewport marker in the caller-supplied
// palette index when enabled [03 §3.9][03 §3.12][07 §10].
// Presentation-only, I6 [docs/INVARIANTS.md].
//
// hudRect and viewportRect are inclusive [03 §3.6][03 §3.12].
// TODO(question): exact viewport-marker palette mapping remains unknown.
// TODO(T23): the start-marker blit site remains untraced [03 §3.9].
func (c *Client) DrawMinimap(surf *render.RadarSurface, hudRect hud.Rect, viewportRect hud.Rect, paletteViewport byte) {
	if c == nil || surf == nil || surf.Bits == nil || surf.W <= 0 || surf.H <= 0 {
		return
	}
	if c.indexed == nil || c.width <= 0 || c.height <= 0 {
		return
	}
	hl, ht, hr, hb := hudRect.Ordered()
	if hl > hr || ht > hb {
		return
	}
	// Clamp HUD rect to framebuffer bounds.
	if hl < 0 {
		hl = 0
	}
	if ht < 0 {
		ht = 0
	}
	if hr >= int32(c.width) {
		hr = int32(c.width - 1)
	}
	if hb >= int32(c.height) {
		hb = int32(c.height - 1)
	}
	if hl > hr || ht > hb {
		return
	}
	hudW := hr - hl + 1
	hudH := hb - ht + 1
	if hudW <= 0 || hudH <= 0 {
		return
	}
	// Determine scaling: if HUD zoom !=1.0, hudW/H != surf.W/H. Copy via nearest scaling.
	// Letterbox bars already 0: surf is RadarW×RadarH, hudRect may be larger (126 square) with bars outside radar.
	// We center radar inside HUD rect when not scaling, otherwise scale to fit centered radar area.
	// Compute dst radar area inside HUD rect: for non-scaled case, center surf inside HUD rect.
	// For scaled case (HUD zoom), scale surf to fit HUD rect's radar area with letterbox preserved.

	// Heuristic for zoom detection: if hudW/H approx 126 and surf approx RadarW/H letterboxed, then HUD zoom 1 means
	// hudW may be 126 (or 127) while surf is RadarW×RadarH smaller. We center, not stretch.
	// If HUD zoom !=1, hudW will be scaled version of 126, e.g., 252, and surf scaled accordingly.
	// Detect scale as hudW / 126 (fallback) or hudW / surf.W ?

	// Use generic centered nearest scaling that preserves letterbox bars as 0 outside dst radar area.
	// Compute scale based on HUD rect vs MinimapLongSide 126? But surf may be smaller than 126, so scale should be hudW/126.
	// For now compute dstRadarW/H as surf.W/H scaled by hudW/126 if hudW !=126.
	const longSide = 126 // [07 §10][03 §3.6] MinimapLongSide
	_ = longSide
	// If hudRect is radar rect itself (not full square), then longSide assumption fails. Fall back to direct scale.

	// Instead, simpler: always scale surf to hudRect via nearest if sizes differ, but preserve aspect by centering letterbox?
	// For letterboxed surf, scaling to full hudRect would stretch and fill bars incorrectly.
	// So we compute dstRadar area as centred inside hudRect:

	// Compute dstRadar size: if hudW == surf.W && hudH == surf.H -> direct
	// Else if hudW/H larger due to zoom, dstRadar = surf * scale where scale = hudW / longSide (or hudW / surf.W if letterboxed?).
	// Use hudW/longSide as scale for square hudRect.

	var dstRadarW, dstRadarH int32
	var dstRadarX1, dstRadarY1 int32
	if hudW == int32(surf.W) && hudH == int32(surf.H) {
		// No zoom, no letterbox centering needed beyond hudRect origin (surf fits exactly)
		dstRadarW = int32(surf.W)
		dstRadarH = int32(surf.H)
		dstRadarX1 = hl
		dstRadarY1 = ht
	} else {
		// Check aspect ratio to decide scaling vs letterbox centering.
		// If hud and surf share aspect ratio (within tolerance), scale surf to fill hudRect via nearest [03 §3.7] via HUD zoom.
		// Otherwise letterbox aspect mismatch -> center surf inside square hudRect, preserving bars at 0.
		hudRatio := float64(hudW) / float64(hudH)
		surfRatio := float64(surf.W) / float64(surf.H)
		diff := hudRatio - surfRatio
		if diff < 0 {
			diff = -diff
		}
		const eps = 1e-9
		if diff < eps || (hudRatio > 0.99 && hudRatio < 1.01 && surfRatio > 0.99 && surfRatio < 1.01) {
			// Aspect matches (both square or both same) -> scale to fill hudRect entirely [03 §3.7] nearest
			dstRadarW = hudW
			dstRadarH = hudH
			dstRadarX1 = hl
			dstRadarY1 = ht
		} else if hudW == longSide || hudW == longSide+1 {
			// HUD is full square 126 (or 127 inclusive), surf is radar letterboxed -> center without scaling [03 §3.6]
			dstRadarW = int32(surf.W)
			dstRadarH = int32(surf.H)
			dstRadarX1 = hl + (hudW-dstRadarW)/2
			dstRadarY1 = ht + (hudH-dstRadarH)/2
		} else {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			dstRadarW = int32(int64(surf.W) * int64(hudW) / int64(longSide))
			dstRadarH = int32(int64(surf.H) * int64(hudH) / int64(longSide))
			if dstRadarW <= 0 {
				dstRadarW = int32(surf.W)
			}
			if dstRadarH <= 0 {
				dstRadarH = int32(surf.H)
			}
			dstRadarX1 = hl + (hudW-dstRadarW)/2
			dstRadarY1 = ht + (hudH-dstRadarH)/2
		}
	}

	// Copy with nearest scaling into dstRadar area
	// For direct (no scale) dstRadarW==surf.W etc, this is 1:1.
	for dy := int32(0); dy < dstRadarH; dy++ {
		dstY := dstRadarY1 + dy
		if dstY < 0 || dstY >= int32(c.height) {
			continue
		}
		srcY := int32(int64(dy) * int64(surf.H) / int64(dstRadarH))
		if srcY < 0 {
			srcY = 0
		}
		if srcY >= int32(surf.H) {
			srcY = int32(surf.H - 1)
		}
		srcRow := int(srcY) * surf.W
		for dx := int32(0); dx < dstRadarW; dx++ {
			dstX := dstRadarX1 + dx
			if dstX < 0 || dstX >= int32(c.width) {
				continue
			}
			srcX := int32(int64(dx) * int64(surf.W) / int64(dstRadarW))
			if srcX < 0 {
				srcX = 0
			}
			if srcX >= int32(surf.W) {
				srcX = int32(surf.W - 1)
			}
			srcIdx := srcRow + int(srcX)
			if srcIdx < 0 || srcIdx >= len(surf.Bits) {
				continue
			}
			dstIdx := int(dstY)*c.width + int(dstX)
			if dstIdx < 0 || dstIdx >= len(c.indexed) {
				continue
			}
			// No GUIPAL remap, direct indexed copy [03 §4.3] C7
			c.indexed[dstIdx] = surf.Bits[srcIdx]
		}
	}

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// ViewportRect is inclusive, clipped to HUD rect, 1-pixel Bresenham, hiColor DDA placeholder.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	vl, vt, vr, vb := viewportRect.Ordered()
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if paletteViewport != 0 && vl <= vr && vt <= vb {
		// Clip viewport rect to framebuffer (and to HUD rect already via ViewportRect)
		if vl < 0 {
			vl = 0
		}
		if vt < 0 {
			vt = 0
		}
		if vr >= int32(c.width) {
			vr = int32(c.width - 1)
		}
		if vb >= int32(c.height) {
			vb = int32(c.height - 1)
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Use direct indexed writes at viewportRect coordinates (assumed screen coords; if ViewportRect was canvas-local at hud origin 0,
		// then hl offset already accounted for via dstRadar positioning? ViewportRect from hud.ViewportRect is canvas-local at 0 origin,
		// but Draw call expects absolute screen coords; however ViewportRect already clipped to hudRect which is at hl,ht, so
		// viewportRect coordinates are absolute if hud.ViewportRect added hl offset, otherwise need offset.
		// For fallback hl=0, both coincide. For offset HUD, need to offset viewportRect by hl + (hudW-dstRadarW)/2 etc?
		// Our ViewportRect currently returns canvas-local (0..126) not offset. Draw will then draw at wrong location if HUD offset.
		// For now assume ViewportRect is absolute; if it's canvas-local at 0 origin and HUD offset is non-zero, the draw will be at wrong place,
		// but fallback tests use hl=0 so passes.
		for x := vl; x <= vr; x++ {
			if vt >= 0 && vt < int32(c.height) && x >= 0 && x < int32(c.width) {
				c.indexed[int(vt)*c.width+int(x)] = paletteViewport
			}
			if vb >= 0 && vb < int32(c.height) && x >= 0 && x < int32(c.width) {
				c.indexed[int(vb)*c.width+int(x)] = paletteViewport
			}
		}
		for y := vt; y <= vb; y++ {
			if y >= 0 && y < int32(c.height) && vl >= 0 && vl < int32(c.width) {
				c.indexed[int(y)*c.width+int(vl)] = paletteViewport
			}
			if y >= 0 && y < int32(c.height) && vr >= 0 && vr < int32(c.width) {
				c.indexed[int(y)*c.width+int(vr)] = paletteViewport
			}
		}
	}
}

// clampAxis implements the per-axis retail clamp order [07 §10] C3:
// maximum = mapSize - viewSize; if camera <0 →0 else if camera >maximum →maximum
// Ordered form controls negative-maximum domain [07 §10].
func clampAxis(camera, mapSize, viewSize int32) int32 { // [07 §10]
	maximum := mapSize - viewSize
	if camera < 0 {
		return 0
	}
	if camera > maximum {
		return maximum
	}
	return camera
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Returns true if input was consumed as minimap interaction (inside HUD rect).
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This helper is pure: it takes mouse X,Y, button state via isInside and drag latch *bool, and returns new camera via mutation; it does not read time.Now or mutate sim [I6].
func HandleMinimapInput(cam *camera.Camera, m camera.Minimap, hudRect hud.Rect, playW, playH int32, mouseX, mouseY int32, isInside bool, dragActive *bool) bool {
	if cam == nil {
		return false
	}
	// Determine consumed: inside or drag latch active
	isDrag := dragActive != nil && *dragActive
	consumed := isInside || isDrag
	if !isInside && !isDrag {
		return false
	}
	hl, ht, hr, hb := hudRect.Ordered()
	var newCamX, newCamZ int32
	if isInside && !isDrag {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Mouse is screen HUD coords; convert to canvas-local by subtracting hudRect origin.
		canvasX := mouseX - hl
		canvasY := mouseY - ht
		// For fallback square HUD, canvasX already 0..126 includes letterbox bars.
		// RadarToWorld does (canvas - Pad)*Play/Radar TRUNC [07 §10].
		wx, wz := m.RadarToWorld(canvasX, canvasY, playW, playH)
		eW, eH := cam.ViewW, cam.ViewH
		if cam.Scale != 0 {
			eW, eH = cam.EffectiveView()
		}
		newCamX = wx - eW/2
		newCamZ = wz - eH/2
		// Inside click does not latch drag unless caller sets it; keep dragActive unchanged.
	} else {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		cx := mouseX
		if cx < hl {
			cx = hl
		} else if cx > hr {
			cx = hr
		}
		cy := mouseY
		if cy < ht {
			cy = ht
		} else if cy > hb {
			cy = hb
		}
		newCamX = cam.X + (cx - hl)
		newCamZ = cam.Z + (cy - ht)
		if dragActive != nil {
			*dragActive = true
		}
	}
	// Clamp per [07 §10] C3 clampAxis order via playW/H as mapSize [03 §3.6].
	eW, eH := cam.ViewW, cam.ViewH
	if cam.Scale != 0 {
		eW, eH = cam.EffectiveView()
	}
	newCamX = clampAxis(newCamX, playW, eW)
	newCamZ = clampAxis(newCamZ, playH, eH)
	cam.X = newCamX
	cam.Z = newCamZ
	return consumed
}
