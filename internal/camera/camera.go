// Package camera implements the orthographic camera and minimap conversions
// for the Nanolathe client shell [03 §2.5][07 §10].
package camera

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Viewport origin for the observed beam/line projection [03 §2.5].
// The scale and half-height shear are general; the offsets 128,32 are
// the established values for that path. Callers should treat these as the
// viewport origin rather than scattering literals [PLAN_04A C1].
const (
	OriginX int32 = 128 // [03 §2.5] screenX offset
	OriginY int32 = 32  // [03 §2.5] screenY offset
)

// Camera is the integer orthographic camera state [07 §10].
// X,Z are the camera origin in map pixels.
// ViewW,ViewH are the viewport size in the same units.
// MapW,MapH are the map extents in map pixels.
type Camera struct {
	X, Z          int32
	ViewW, ViewH  int32
	MapW, MapH    int32
}

// Direction is a scroll direction [07 §10].
type Direction int

const (
	DirectionLeft Direction = iota // -X [07 §10]
	DirectionRight                 // +X
	DirectionUp                    // -Z
	DirectionDown                  // +Z
)

// Aliases for robustness across callers that may use different naming
// conventions. All map to the canonical iota values above.
const (
	DirLeft  = DirectionLeft
	DirRight = DirectionRight
	DirUp    = DirectionUp
	DirDown  = DirectionDown

	Left  = DirectionLeft
	Right = DirectionRight
	Up    = DirectionUp
	Down  = DirectionDown

	DirectionWest  = DirectionLeft
	DirectionEast  = DirectionRight
	DirectionNorth = DirectionUp
	DirectionSouth = DirectionDown
)

// clampAxis implements the per-axis retail clamp order [07 §10]:
//
//	maximum = mapSize - viewSize
//	if camera < 0 → 0 else if camera > maximum → maximum
//
// The ordered form controls the negative-maximum (viewSize > mapSize) domain:
// a negative camera is forced to 0 before the maximum is considered, so a
// positive camera in that domain clamps to the negative maximum.
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

// Pan applies dx,dz to the camera and then clamps per [07 §10] C3.
func (c *Camera) Pan(dx, dz int32) { // [07 §10]
	c.X += dx
	c.Z += dz
	c.X = clampAxis(c.X, c.MapW, c.ViewW)
	c.Z = clampAxis(c.Z, c.MapH, c.ViewH)
}

// Scroll moves the camera in dir by magnitude = setting * rawDelta capped at
// 128 [07 §10] C2. Zero rawDelta skips movement entirely.
func (c *Camera) Scroll(setting byte, rawDelta int32, dir Direction) { // [07 §10]
	if rawDelta == 0 {
		return
	}
	mag := int32(setting) * rawDelta // [07 §10] delta = setting * rawDelta
	if mag < 0 {
		mag = -mag
	}
	if mag > 128 {
		mag = 128 // capped at 128 [07 §10]
	}
	switch dir {
	case DirectionLeft:
		c.Pan(-mag, 0)
	case DirectionRight:
		c.Pan(mag, 0)
	case DirectionUp:
		c.Pan(0, -mag)
	case DirectionDown:
		c.Pan(0, mag)
	}
}

// WorldToScreen projects fixed-point world (x,y,z) to orthographic screen
// coordinates [03 §2.5] C1:
//
//	screenX = (worldX>>16) - cameraX + originX
//	screenY = (worldZ>>16) - ((worldY>>16)>>1) - cameraZ + originY
//
// The half-height shear ((worldY>>16)>>1) uses arithmetic shifts so negative
// worldY is handled as retail does. Origin is taken from the viewport
// constants OriginX/Y (128,32 for the observed beam path) rather than
// inlining literals at call sites [03 §2.5].
func (c *Camera) WorldToScreen(x, y, z numeric.Fixed) (sx, sy int32) { // [03 §2.5]
	wx := int32(int64(x) >> 16)
	wy := int32(int64(y) >> 16)
	wz := int32(int64(z) >> 16)
	shear := wy >> 1 // arithmetic shift preserves sign for negative wy [03 §2.5]
	sx = wx - c.X + OriginX
	sy = wz - shear - c.Z + OriginY
	return
}

// ScreenToWorld inverts WorldToScreen at ground height (worldY=0) [03 §2.5].
// Retail's projection includes a half-height shear on Y; the inverse for
// picking assumes Y=0 so the shear term is zero. This is the ground-plane
// pick used by the minimap direct branch and cursor picking [07 §10].
func (c *Camera) ScreenToWorld(sx, sy int32) (x, z numeric.Fixed) { // [03 §2.5]
	wx := int64(sx - OriginX + c.X) << 16
	wz := int64(sy - OriginY + c.Z) << 16
	return numeric.Fixed(wx), numeric.Fixed(wz)
}

// Minimap is the 126-pixel letterboxed radar geometry [07 §10] C4.
type Minimap struct {
	PadX, PadY int32 // letterbox padding inside the 126×126 square
	W, H       int32 // radarWidth, radarHeight
}

// LayoutMinimap computes the aspect-preserving letterbox inside a 126×126
// square [07 §10] C4.
//
//	radarHeight=126, radarWidth=floor(mapWidth*126/mapHeight) when mapWidth < mapHeight
//	else the transpose
//	padX = trunc((126-radarWidth)/2) or padY accordingly
//	rectangle is inclusive: right=padX+radarWidth-1
func LayoutMinimap(mapW, mapH int32) Minimap { // [07 §10]
	const longSide = 126
	if mapW <= 0 || mapH <= 0 {
		return Minimap{PadX: 0, PadY: 0, W: longSide, H: longSide}
	}
	if mapW < mapH {
		// tall: preserve width
		w := int32(int64(mapW) * longSide / int64(mapH)) // floor [07 §10]
		if w < 1 {
			w = 1
		}
		if w > longSide {
			w = longSide
		}
		padX := (longSide - w) / 2 // trunc [07 §10]
		return Minimap{PadX: padX, PadY: 0, W: w, H: longSide}
	}
	// wide or square
	h := int32(int64(mapH) * longSide / int64(mapW)) // floor [07 §10]
	if h < 1 {
		h = 1
	}
	if h > longSide {
		h = longSide
	}
	padY := (longSide - h) / 2 // trunc [07 §10]
	return Minimap{PadX: 0, PadY: padY, W: longSide, H: h}
}

// Right returns the inclusive right edge padX+W-1 [07 §10].
func (m Minimap) Right() int32 { return m.PadX + m.W - 1 }

// Bottom returns the inclusive bottom edge padY+H-1 [07 §10].
func (m Minimap) Bottom() int32 { return m.PadY + m.H - 1 }

// ToWorld converts a mouse position inside the 126×126 canvas to world/map
// coordinates [07 §10] C4:
//
//	worldX = (mouseX-padX)*mapWidth/radarWidth
//	worldZ = (mouseY-padY)*mapHeight/radarHeight
//
// Signed integer division truncates toward zero. Caller may then derive the
// camera with worldX-viewWidth/2 and the standard clamp [07 §10].
func (m Minimap) ToWorld(mouseX, mouseY, mapW, mapH int32) (wx, wz int32) { // [07 §10]
	if m.W == 0 || m.H == 0 {
		return 0, 0
	}
	wx = (mouseX - m.PadX) * mapW / m.W
	wz = (mouseY - m.PadY) * mapH / m.H
	return
}

// ToCamera converts a minimap click directly to a clamped camera origin
// [07 §10] C4. It composes ToWorld with the view-centering and standard clamp:
//
//	cameraX = worldX - viewWidth/2
//	cameraZ = worldZ - viewHeight/2
//	then clamp per clampAxis order.
func (m Minimap) ToCamera(mouseX, mouseY, mapW, mapH, viewW, viewH int32) (cx, cz int32) { // [07 §10]
	wx, wz := m.ToWorld(mouseX, mouseY, mapW, mapH)
	cx = wx - viewW/2
	cz = wz - viewH/2
	cx = clampAxis(cx, mapW, viewW)
	cz = clampAxis(cz, mapH, viewH)
	return
}
