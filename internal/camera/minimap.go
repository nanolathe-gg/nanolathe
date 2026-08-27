// Package camera minimap lens extends LayoutMinimap with retail lens arithmetic
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
package camera

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const MinimapLongSide = 126 // [07 §10] long side

// minimapLetterboxFill is the palette index used to fill letterbox bars beyond RadarW×RadarH.
// TODO(question): bars beyond RadarW×RadarH retain heap bytes — inference 0 black pending capture. Assume 0 [03 §3.6].
const minimapLetterboxFill = 0 // TODO(question) assume 0 black

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Wpix = Wcells*16, Hpix = Hcells*16. The minimap divisors are PlayRight/Bottom,
// not raw Wpix/Hpix — camera.MapW/MapH are already PlayRight/Bottom when created
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func PlayRight(wPix int32) int32 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return wPix - 32
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func PlayBottom(hPix int32) int32 { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return hPix - 128
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func PlaySize(wPix, hPix int32) (int32, int32) {
	return PlayRight(wPix), PlayBottom(hPix)
}

// PlaySizeFromCells returns PlayRight, PlayBottom from cell counts [03 §3.4].
// Wpix = Wcells*16, Hpix = Hcells*16, then PlayRight = Wpix-32, PlayBottom = Hpix-128.
func PlaySizeFromCells(wCells, hCells int32) (int32, int32) {
	return PlayRight(wCells * 16), PlayBottom(hCells * 16)
}

// HitTest reports whether (x,y) lies inside the inclusive radar rectangle
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// The rectangle is PadX..PadX+W-1 by PadY..PadY+H-1 inclusive.
func (m Minimap) HitTest(x, y int32) bool { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if m.W <= 0 || m.H <= 0 {
		return false
	}
	return x >= m.PadX && x <= m.Right() && y >= m.PadY && y <= m.Bottom()
}

// WorldToRadar projects world map pixels to radar canvas coordinates
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
//	world→radar rx = worldX*RadarW/PlayRight + OriginX
//	            ry = (worldZ - worldYHalf)*RadarH/PlayBottom + OriginY
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// worldX/worldZ are map pixels in [0,PlayRight/Bottom). OriginX/Y are
// Minimap.PadX/PadY inside the 126×126 canvas [03 §3.6]. PlayW/PlayH
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This variant assumes worldY == 0 (ground). Use WorldToRadarWithY for height-aware.
func (m Minimap) WorldToRadar(worldX, worldZ int32, playW, playH int32) (rx, ry int32) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return m.WorldToRadarWithY(worldX, 0, worldZ, playW, playH)
}

// WorldToRadarWithY is the height-aware form of WorldToRadar
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (m Minimap) WorldToRadarWithY(worldX, worldY, worldZ int32, playW, playH int32) (rx, ry int32) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if playW == 0 || playH == 0 || m.W == 0 || m.H == 0 {
		return m.PadX, m.PadY
	}
	shear := worldY >> 1 // arithmetic shift preserves sign [03 §2.5]
	adjZ := worldZ - shear
	rx = m.PadX + int32(int64(worldX)*int64(m.W)/int64(playW))
	ry = m.PadY + int32(int64(adjZ)*int64(m.H)/int64(playH))
	return
}

// WorldToRadarWithHeight is an alias for WorldToRadarWithY for callers using height terminology.
func (m Minimap) WorldToRadarWithHeight(worldX, worldY, worldZ int32, playW, playH int32) (rx, ry int32) {
	return m.WorldToRadarWithY(worldX, worldY, worldZ, playW, playH)
}

// RadarToWorld inverts WorldToRadar: canvas radar → world map pixels
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
//	worldX = (rx-OriginX)*PlayRight/RadarW
//	worldZ = (ry-OriginY)*PlayBottom/RadarH
func (m Minimap) RadarToWorld(rx, ry int32, playW, playH int32) (wx, wz int32) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if m.W == 0 || m.H == 0 {
		return 0, 0
	}
	wx = int32(int64(rx-m.PadX) * int64(playW) / int64(m.W))
	wz = int32(int64(ry-m.PadY) * int64(playH) / int64(m.H))
	return
}

// ToWorldPlay converts a mouse position inside the 126×126 canvas to world/map
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This is the Play-aware variant of ToWorld; ToWorld's mapW/mapH are conceptually
// PlayRight/Bottom when the camera was created via NewFromTerrain, but this
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TRUNC via IDIV; inclusive rect handled by caller via HitTest.
func (m Minimap) ToWorldPlay(mouseX, mouseY, playW, playH int32) (wx, wz int32) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return m.RadarToWorld(mouseX, mouseY, playW, playH)
}

// ToCameraPlay is the historical centered helper retained for callers that
// explicitly request a view-centered camera. The exact retail lens branch is
// exposed as ToCameraLensPlay below. [07 §10]
func (m Minimap) ToCameraPlay(mouseX, mouseY, playW, playH, viewW, viewH int32) (cx, cz int32) { // [07 §10]
	wx, wz := m.ToWorldPlay(mouseX, mouseY, playW, playH)
	cx = wx - viewW/2
	cz = wz - viewH/2
	cx = clampAxis(cx, playW, viewW)
	cz = clampAxis(cz, playH, viewH)
	return
}

// ToCameraLensPlay is the exact minimap lens branch: the inverse-projected
// point becomes the camera origin without a view-half recenter term. [03 §3.11]
func (m Minimap) ToCameraLensPlay(mouseX, mouseY, playW, playH, viewW, viewH int32) (cx, cz int32) {
	cx, cz = m.ToWorldPlay(mouseX, mouseY, playW, playH)
	cx = clampAxis(cx, playW, viewW)
	cz = clampAxis(cz, playH, viewH)
	return
}
