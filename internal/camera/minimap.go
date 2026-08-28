// Package camera minimap lens owns the single aspect/layout and world mapping
// used by radar drawing and minimap input [07 §10][03 §3.4][03 §3.6][03 §3.11].
package camera

// MinimapLongSide is the fixed long side of the radar canvas [07 §10][03 §3.6].
const MinimapLongSide = 126 // [07 §10] long side

// Minimap is the aspect-preserving radar rectangle inside the fixed logical
// canvas. PadX/PadY are canvas-local letterbox origins and W/H are inclusive
// radar dimensions [03 §3.6][07 §10].
type Minimap struct {
	PadX, PadY int32
	W, H       int32
}

// LayoutMinimap fits the playable map into the fixed 126×126 logical canvas.
// The longer axis occupies 126 pixels; the shorter axis uses truncating
// integer scale and is centered by truncating half-padding [03 §3.6][07 §10].
func LayoutMinimap(mapW, mapH int32) Minimap {
	if mapW <= 0 || mapH <= 0 {
		return Minimap{}
	}
	if mapW < mapH {
		w := int32(int64(mapW) * MinimapLongSide / int64(mapH))
		if w < 1 {
			w = 1
		}
		if w > MinimapLongSide {
			w = MinimapLongSide
		}
		return Minimap{PadX: (MinimapLongSide - w) / 2, W: w, H: MinimapLongSide}
	}
	h := int32(int64(mapH) * MinimapLongSide / int64(mapW))
	if h < 1 {
		h = 1
	}
	if h > MinimapLongSide {
		h = MinimapLongSide
	}
	return Minimap{PadY: (MinimapLongSide - h) / 2, W: MinimapLongSide, H: h}
}

// Right and Bottom return the inclusive radar edges in canvas coordinates.
func (m Minimap) Right() int32  { return m.PadX + m.W - 1 }
func (m Minimap) Bottom() int32 { return m.PadY + m.H - 1 }

// CanvasToDisplay converts a canvas-local point to an inclusive logical HUD
// rectangle. It returns false for points outside the fixed 126-pixel canvas.
func (m Minimap) CanvasToDisplay(x, y, left, top, width, height int32) (int32, int32, bool) {
	if width <= 0 || height <= 0 || x < 0 || x >= MinimapLongSide || y < 0 || y >= MinimapLongSide {
		return 0, 0, false
	}
	return left + x*width/MinimapLongSide, top + y*height/MinimapLongSide, true
}

// DisplayToCanvas converts an inclusive logical HUD point to the fixed canvas.
// The caller performs the minimap rectangle hit-test; this method only handles
// the common origin/scale arithmetic and clamps the inclusive endpoint.
func (m Minimap) DisplayToCanvas(x, y, left, top, width, height int32) (int32, int32, bool) {
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	cx := (x - left) * MinimapLongSide / width
	cy := (y - top) * MinimapLongSide / height
	if cx < 0 {
		cx = 0
	} else if cx >= MinimapLongSide {
		cx = MinimapLongSide - 1
	}
	if cy < 0 {
		cy = 0
	} else if cy >= MinimapLongSide {
		cy = MinimapLongSide - 1
	}
	return cx, cy, true
}

// PlayRight returns the playable width PlayRight = Wpix-32 [03 §3.4].
// Wpix = Wcells*16, Hpix = Hcells*16. The minimap divisors are PlayRight/Bottom,
// not raw Wpix/Hpix — camera.MapW/MapH are already PlayRight/Bottom when created
// via NewFromTerrain [03 §3.4].
func PlayRight(wPix int32) int32 { // [03 §3.4]
	return wPix - 32
}

// PlayBottom returns the playable height PlayBottom = Hpix-128 [03 §3.4].
func PlayBottom(hPix int32) int32 { // [03 §3.4]
	return hPix - 128
}

// PlaySize returns PlayRight, PlayBottom from pixel dimensions [03 §3.4].
func PlaySize(wPix, hPix int32) (int32, int32) {
	return PlayRight(wPix), PlayBottom(hPix)
}

// PlaySizeFromCells returns PlayRight, PlayBottom from cell counts [03 §3.4].
// Wpix = Wcells*16, Hpix = Hcells*16, then PlayRight = Wpix-32, PlayBottom = Hpix-128.
func PlaySizeFromCells(wCells, hCells int32) (int32, int32) {
	return PlayRight(wCells * 16), PlayBottom(hCells * 16)
}

// HitTest reports whether (x,y) lies inside the inclusive radar rectangle
// [07 §10][03 §3.11].
// The rectangle is PadX..PadX+W-1 by PadY..PadY+H-1 inclusive.
func (m Minimap) HitTest(x, y int32) bool { // [07 §10]
	if m.W <= 0 || m.H <= 0 {
		return false
	}
	return x >= m.PadX && x <= m.Right() && y >= m.PadY && y <= m.Bottom()
}

// WorldToRadar projects world map pixels to radar canvas coordinates
// [03 §3.9][03 §3.11].
//
//	world→radar rx = worldX*RadarW/PlayRight + OriginX
//	            ry = (worldZ - worldYHalf)*RadarH/PlayBottom + OriginY
//	all operations truncate toward zero.
//
// worldX/worldZ are map pixels in [0,PlayRight/Bottom). OriginX/Y are
// Minimap.PadX/PadY inside the 126×126 canvas [03 §3.6]. PlayW/PlayH
// are PlayRight/Bottom, not raw Wpix/Hpix [03 §3.4].
// This variant assumes worldY == 0 (ground). Use WorldToRadarWithY for height-aware.
func (m Minimap) WorldToRadar(worldX, worldZ int32, playW, playH int32) (rx, ry int32) { // [03 §3.11]
	return m.WorldToRadarWithY(worldX, 0, worldZ, playW, playH)
}

// WorldToRadarWithY is the height-aware form of WorldToRadar
// [03 §3.9].
// ry uses (worldZ - (worldY>>1)) with arithmetic shift [03 §2.5][03 §3.9].
func (m Minimap) WorldToRadarWithY(worldX, worldY, worldZ int32, playW, playH int32) (rx, ry int32) { // [03 §3.9]
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
// [03 §3.11] with truncating integer arithmetic.
//
//	worldX = (rx-OriginX)*PlayRight/RadarW
//	worldZ = (ry-OriginY)*PlayBottom/RadarH
func (m Minimap) RadarToWorld(rx, ry int32, playW, playH int32) (wx, wz int32) { // [03 §3.11]
	if m.W == 0 || m.H == 0 {
		return 0, 0
	}
	wx = int32(int64(rx-m.PadX) * int64(playW) / int64(m.W))
	wz = int32(int64(ry-m.PadY) * int64(playH) / int64(m.H))
	return
}

// ToWorldPlay converts a mouse position inside the 126×126 canvas to world/map
// coordinates using PlayRight/Bottom divisors [07 §10][03 §3.11].
// This is the Play-aware variant of ToWorld; ToWorld's mapW/mapH are conceptually
// PlayRight/Bottom when the camera was created via NewFromTerrain, but this
// variant makes the divisor explicit [03 §3.4].
// TRUNC via IDIV; inclusive rect handled by caller via HitTest.
func (m Minimap) ToWorldPlay(mouseX, mouseY, playW, playH int32) (wx, wz int32) { // [07 §10]
	return m.RadarToWorld(mouseX, mouseY, playW, playH)
}
