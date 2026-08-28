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
// Scale is presentation-only zoom (1 == no zoom) [F-P1-008]. Zero means 1.
type Camera struct {
	X, Z         int32
	ViewW, ViewH int32
	MapW, MapH   int32
	Scale        float32 // presentation zoom; 1 == native [F-P1-008]
}

// Direction is a scroll direction [07 §10].
type Direction int

const (
	DirectionLeft  Direction = iota // -X [07 §10]
	DirectionRight                  // +X
	DirectionUp                     // -Z
	DirectionDown                   // +Z
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

// scale returns effective presentation scale (1 when zero) [F-P1-008].
func (c *Camera) scale() float32 {
	if c == nil || c.Scale == 0 {
		return 1
	}
	if c.Scale < 0.25 {
		return 0.25
	}
	if c.Scale > 4 {
		return 4
	}
	return c.Scale
}

// EffectiveScale returns the clamped presentation scale [F-P1-008].
// Presentation-only; sim never reads it [I6].
func (c *Camera) EffectiveScale() float32 { // [F-P1-008]
	return c.scale()
}

// EffectiveView returns the view size in world pixels after zoom. When zoomed
// in, less world is visible; when zoomed out, more. Clamp uses this.
func (c *Camera) EffectiveView() (int32, int32) {
	s := c.scale()
	if s == 1 {
		return c.ViewW, c.ViewH
	}
	return int32(float32(c.ViewW) / s), int32(float32(c.ViewH) / s)
}

// Pan applies dx,dz to the camera and then clamps per [07 §10] C3.
// When terrain is available, MapW/MapH are the playable extents PlayRight/PlayBottom
// (Wpix-32/Hpix-128) set at void-fixup time [P1-15], not the raw Wpix/Hpix;
// the clamp maximum is mapSize - viewSize, so the max origin is PlayRight-ViewW etc.
// With zoom, clamp uses effective world view size View/Scale [F-P1-008].
func (c *Camera) Pan(dx, dz int32) { // [07 §10]
	if c == nil {
		return
	}
	c.X += dx
	c.Z += dz
	c.Clamp()
}

// Clamp applies the map-boundary clamp to the camera's current origin. It is
// public so presentation effects such as screen shake can mutate the real
// camera and then use exactly the same bounds as pan, zoom, and picking
// [03 §5.6][07 §10].
func (c *Camera) Clamp() {
	if c == nil {
		return
	}
	eW, eH := c.EffectiveView()
	c.X = clampAxis(c.X, c.MapW, eW)
	c.Z = clampAxis(c.Z, c.MapH, eH)
}

// Drag pans by screen-pixel delta via middle-drag, scaled by zoom [F-P1-008][07 §10].
// It is presentation-only and never touches sim.
func (c *Camera) Drag(dx, dy int32) {
	if c == nil {
		return
	}
	s := c.scale()
	// screen delta → world delta (inverse of zoom)
	wx := int32(float32(dx) / s)
	wz := int32(float32(dy) / s)
	// Drag direction: moving mouse right should pan world right → camera follows mouse
	c.Pan(-wx, -wz)
}

// AddZoom adjusts presentation zoom by wheel delta, centered at mx,my where
// practical [F-P1-008]. Positive dy zooms in; negative zooms out. It keeps the
// world point under the cursor stable when possible.
func (c *Camera) AddZoom(delta float32, mx, my int32) {
	if c == nil {
		return
	}
	if delta == 0 {
		return
	}
	oldS := c.scale()
	// Wheel step: each notch ~1.1x; clamp to [0.25,4].
	factor := float32(1.0)
	if delta > 0 {
		factor = 1.1
	} else if delta < 0 {
		factor = 1 / 1.1
	}
	newS := oldS * factor
	if newS < 0.25 {
		newS = 0.25
	}
	if newS > 4 {
		newS = 4
	}
	if newS == oldS {
		return
	}
	// Keep cursor point stable: world = (screen - Origin)/oldS + cam
	// New cam = world - (screen - Origin)/newS
	// Use float for subpixel before trunc.
	ox := float32(OriginX)
	oy := float32(OriginY)
	wx := float32(c.X) + (float32(mx)-ox)/oldS
	wz := float32(c.Z) + (float32(my)-oy)/oldS
	c.X = int32(wx - (float32(mx)-ox)/newS)
	c.Z = int32(wz - (float32(my)-oy)/newS)
	c.Scale = newS
	eW, eH := c.EffectiveView()
	c.X = clampAxis(c.X, c.MapW, eW)
	c.Z = clampAxis(c.Z, c.MapH, eH)
}

// NewFromTerrain creates a camera whose map extents are the playable insets [P1-15].
// PlayRight = Wpix-32, PlayBottom = Hpix-128 are the max extents set at void-fixup time [P1-15];
// they are the clamp maxima, so MapW/MapH are set to PlayRight/PlayBottom when non-zero
// (else fallback to raw terrainWpix/Hpix). The per-axis maximum is then MapW-ViewW / MapH-ViewH [07 §10].
func NewFromTerrain(terrainWpix, terrainHpix, playRight, playBottom, viewW, viewH int32) *Camera {
	// Use PlayRight/PlayBottom as the effective map size for clamp [P1-15].
	mapW := terrainWpix
	mapH := terrainHpix
	if playRight != 0 {
		mapW = playRight
	}
	if playBottom != 0 {
		mapH = playBottom
	}
	return &Camera{MapW: mapW, MapH: mapH, ViewW: viewW, ViewH: viewH}
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
// coordinates [03 §2.5] C1 with presentation zoom [F-P1-008]:
//
//	screenX = (worldX>>16 - cameraX)*Scale + originX
//	screenY = (worldZ>>16 - ((worldY>>16)>>1) - cameraZ)*Scale + originY
//
// The half-height shear ((worldY>>16)>>1) uses arithmetic shifts so negative
// worldY is handled as retail does. Origin is taken from the viewport
// constants OriginX/Y (128,32 for the observed beam path) rather than
// inlining literals at call sites [03 §2.5]. When Scale is 0 or 1 the
// original integer path is preserved for determinism of tests.
func (c *Camera) WorldToScreen(x, y, z numeric.Fixed) (sx, sy int32) { // [03 §2.5]
	wx := int32(int64(x) >> 16)
	wy := int32(int64(y) >> 16)
	wz := int32(int64(z) >> 16)
	shear := wy >> 1 // arithmetic shift preserves sign for negative wy [03 §2.5]
	s := c.scale()
	if s == 1 {
		sx = wx - c.X + OriginX
		sy = wz - shear - c.Z + OriginY
		return
	}
	sx = int32(float32(wx-c.X)*s) + OriginX
	sy = int32(float32(wz-shear-c.Z)*s) + OriginY
	return
}

// ScreenToWorld inverts WorldToScreen at ground height (worldY=0) [03 §2.5]
// with presentation zoom [F-P1-008].
// Retail's projection includes a half-height shear on Y; the inverse for
// picking assumes Y=0 so the shear term is zero. This is the ground-plane
// pick used by the minimap direct branch and cursor picking [07 §10].
func (c *Camera) ScreenToWorld(sx, sy int32) (x, z numeric.Fixed) { // [03 §2.5]
	s := c.scale()
	if s == 1 {
		wx := int64(sx-OriginX+c.X) << 16
		wz := int64(sy-OriginY+c.Z) << 16
		return numeric.Fixed(wx), numeric.Fixed(wz)
	}
	wx := int64(float32(sx-OriginX)/s+float32(c.X)) << 16
	wz := int64(float32(sy-OriginY)/s+float32(c.Z)) << 16
	return numeric.Fixed(wx), numeric.Fixed(wz)
}
