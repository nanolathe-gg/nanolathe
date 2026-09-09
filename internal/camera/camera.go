package camera

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
// Scale is the presentation-only view scale [F-P1-008]: 0 and 1 mean native,
// 2 the detail view. It is an integer so the projection and its inverse are
// exact (DESIGN_GPU_RENDERER §14.1).
type Camera struct {
	X, Z         int32
	ViewW, ViewH int32
	MapW, MapH   int32
	Scale        int32 // presentation view scale; 0 or 1 == native [F-P1-008]

	// Follow is the rest of the retail camera block: the desired origin, the
	// tracked object and the four bookmark slots [07 R-CAM-01 §12]. It is
	// presentation state; no simulation phase reads it back [I6].
	Follow FollowState
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

// clampAxis implements the per-axis retail camera clamp [07 §10], expressed in
// this build's frame of reference.
//
// Retail's clamp is
//
//	maximum = mapSize - viewportSpan
//	if camera < 0 → 0 else if camera > maximum → maximum
//
// where a retail camera origin is the world point drawn at the *battle
// viewport's* top-left corner and viewportSpan is that subrect's own size —
// 512x416 inside a 640x480 display, the rectangle (128,32)..(W-1,H-33) of
// [03 §4.1]. This build's origin is instead the world point drawn at the
// framebuffer's top-left corner (the world is composed across the whole
// framebuffer and the chrome painted over it; every draw site takes
// WorldToScreen minus OriginX/Y, and picking re-adds them), so the two origins
// differ by the viewport's leading inset. Substituting
// `retailCamera = camera + leading` into retail's two bounds gives
//
//	minimum = -leading
//	maximum = mapSize - viewportSpan - leading
//
// The viewportSpan passed in is BattleView's, so there is one definition of the
// visible span in this package. The insets are the subrect's: leading 128 /
// trailing 0 on X, leading 32 / trailing 32 on Z. On X the maximum is
// numerically unchanged (mapSize - viewSize, since the trailing inset is zero);
// on Z it gains the bottom inset, and both floors go negative. That is what makes the whole
// playable area reachable: at the minimum the world's column/row 0 sits exactly
// at the viewport's leading edge, and at the maximum its last playable
// column/row sits exactly at the trailing edge.
//
// Until 2026-08-31 this function took retail's bounds unconverted, so the
// visible world started 128 map pixels in from the west edge and 32 from the
// north, and stopped 32 short of the south — a map's westmost features could
// not be brought on screen at all (defect PT5-01; on Great Divide the map's
// only geothermal vent, anchored at map pixel 104, was unreachable).
// Commit "Place the battle-start camera from the map's start position" made the
// same conversion for JumpToBattleViewCenter and did not carry it into the
// clamp.
//
// The ordered form is preserved exactly: the floor test runs before the maximum
// test, which is the only established behavior in the view-larger-than-map
// domain (a negative maximum), and [07 §10]'s "Unknown" list still carries that
// domain as an open question. Nothing here closes it.
func clampAxis(camera, mapSize, viewportSpan, leading int32) int32 { // [07 §10][03 §4.1]
	minimum := -leading
	// Both bounds are Established [07 R-CAM-01 §13]. The floor: a retail capture
	// on Great Divide scrolled hard west shows the map's column 0 on the
	// viewport's left edge. The maximum subtracts the battle viewport SUBRECT's
	// span, not the negotiated display's — retail's battle setup derives that
	// span as right−left+1 / bottom−top+1 from the subrect corners and holds it
	// in words distinct from the display size, and the clamp's maximum reads
	// those. The same span halved is every recenter's operand, so one span
	// definition serves clamp, jump and recenter. (Traced 2026-09-04; the
	// display reading, now rejected, would have left the last 128 playable
	// columns and 64 rows permanently off screen.)
	maximum := mapSize - viewportSpan - leading
	if camera < minimum {
		return minimum
	}
	if camera > maximum {
		return maximum
	}
	return camera
}

// clampInsets returns the battle viewport's leading and trailing insets on each
// axis, measured in world pixels [03 §4.1]. At native scale they are the
// OriginX/OriginY constants: the chrome covers the framebuffer's leftmost 128
// columns and its top and bottom 32 rows, and nothing at the right edge.
//
// Presentation zoom is not a retail concept [F-P1-008]. The chrome is drawn in
// framebuffer pixels, so a magnified camera sees fewer world pixels behind the
// same chrome and the insets divide by the effective scale, exactly as
// EffectiveView does — which keeps "every playable pixel is reachable" true at
// either scale. The division is integer, as the scale is
// (DESIGN_GPU_RENDERER §14.2).
func (c *Camera) clampInsets() (leadX, trailX, leadZ, trailZ int32) { // [03 §4.1]
	s := c.scale()
	if s == 1 {
		return OriginX, 0, OriginY, OriginY
	}
	insetY := OriginY / s
	return OriginX / s, 0, insetY, insetY
}

// scale returns the effective view scale, clamped to [1, 2] with zero meaning
// native [F-P1-008] (DESIGN_GPU_RENDERER §14.1).
func (c *Camera) scale() int32 {
	if c == nil || c.Scale <= 1 {
		return 1
	}
	if c.Scale > 2 {
		return 2
	}
	return c.Scale
}

// EffectiveScale returns the clamped presentation view scale, 1 or 2
// [F-P1-008]. Presentation-only; sim never reads it [I6].
func (c *Camera) EffectiveScale() int32 { // [F-P1-008]
	return c.scale()
}

// EffectiveView returns the view size in world pixels after the view scale.
// At the detail scale less world is visible. Clamp uses this.
func (c *Camera) EffectiveView() (int32, int32) {
	s := c.scale()
	if s == 1 {
		return c.ViewW, c.ViewH
	}
	return c.ViewW / s, c.ViewH / s
}

// BattleView returns the size of the *battle viewport* in map pixels — the
// part of the framebuffer the world is actually seen through, which is smaller
// than the framebuffer this camera reports through EffectiveView.
//
// Retail rebuilds the game viewport subrect on every mode change as
// left = 128, top = 32, right = W-1, bottom = H-33 — a width of W-128 and a
// height of H-64, so 512x416 at 640x480 [03 §4.1]. Those insets are the
// OriginX/OriginY constants above.
//
// The zero floor is presentation robustness for a framebuffer smaller than the
// chrome; retail has no such mode.
func (c *Camera) BattleView() (int32, int32) { // [03 §4.1]
	if c == nil {
		return 0, 0
	}
	viewW, viewH := c.EffectiveView()
	leadX, trailX, leadZ, trailZ := c.clampInsets()
	w := viewW - leadX - trailX // left inset only; the viewport runs to the framebuffer edge
	h := viewH - leadZ - trailZ // equal top and bottom insets [03 §4.1]
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return w, h
}

// BattleViewOrigin returns the map-pixel point at the battle beam's top-left
// corner. Camera X/Z are this build's framebuffer origin, whereas retail's
// audio placement uses the beam origin; the leading chrome inset is therefore
// part of this conversion [03 §4.1][07 R-CAM-01 §13].
//
// Presentation zoom is a host extension. It uses the same scale-adjusted
// inset as BattleView and Clamp, so the beam's world extent and origin remain
// one consistent presentation policy at every zoom [F-P1-008].
func (c *Camera) BattleViewOrigin() (int32, int32) { // [03 §4.1]
	if c == nil {
		return 0, 0
	}
	leadX, _, leadZ, _ := c.clampInsets()
	return c.X + leadX, c.Z + leadZ
}

// JumpTo is the retail camera *jump*: the current origin is written outright
// and clamped, and the clamped result is copied into the desired origin so no
// glide survives the jump [07 R-CAM-01 §12]. It does not clear the tracked
// object — the writers that do are named in that section's table, and each
// calls ClearFollow itself.
func (c *Camera) JumpTo(x, z int32) { // [07 R-CAM-01 §12]
	if c == nil {
		return
	}
	c.X, c.Z = x, z
	c.Clamp()
	c.Follow.Desired = Origin{X: c.X, Z: c.Z}
}

// BattleViewCenterOrigin converts a map-pixel point to the camera origin that
// shows that point at the centre of the battle viewport, in this build's frame
// of reference [07 R-CAM-01 §12][03 §4.1].
//
// Retail's contract is `origin = point - viewportSpan/2`, because a retail
// camera origin is the world point drawn at the viewport's top-left corner.
// This build's origin is the world point drawn at the framebuffer's top-left
// corner, so the leading inset comes off as well:
//
//	origin = point - leadingInset - viewportSpan/2
//
// At 640x480 that is point - 384 on X and point - 240 on Z. Halving the
// framebuffer instead — point - 320, point - 240 — is right only by accident on
// Z, where the insets are symmetric; on X it lands the target 64 pixels right of
// centre. Both halvings are truncating integer divides, as retail's are.
func (c *Camera) BattleViewCenterOrigin(x, z int32) (int32, int32) { // [07 R-CAM-01 §12]
	if c == nil {
		return x, z
	}
	viewW, viewH := c.BattleView()
	leadX, _, leadZ, _ := c.clampInsets()
	return x - leadX - viewW/2, z - leadZ - viewH/2
}

// JumpToBattleViewCenter jumps so that the map-pixel point (x, z) is seen at
// the centre of the battle viewport [07 R-CAM-01 §12][03 §4.1]. It is the
// battle-start placement writer for both the campaign start-position special
// and the skirmish commander. The conversion into this build's frame of
// reference is BattleViewCenterOrigin's.
func (c *Camera) JumpToBattleViewCenter(x, z int32) { // [07 R-CAM-01 §12]
	if c == nil {
		return
	}
	c.JumpTo(c.BattleViewCenterOrigin(x, z))
}

// Pan applies dx,dz to the camera and then clamps per [07 §10] C3.
// When terrain is available, MapW/MapH are the playable extents PlayRight/PlayBottom
// (Wpix-32/Hpix-128) set at void-fixup time [P1-15], not the raw Wpix/Hpix;
// the clamp bounds are clampAxis's, so the max origin is PlayRight-ViewW on X
// and PlayBottom-ViewH+OriginY on Z, and the floors are -OriginX and -OriginY.
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
	spanW, spanH := c.BattleView()
	leadX, _, leadZ, _ := c.clampInsets()
	c.X = clampAxis(c.X, c.MapW, spanW, leadX)
	c.Z = clampAxis(c.Z, c.MapH, spanH, leadZ)
}

// Drag pans by a screen-pixel delta via middle-drag, converted to world pixels
// by the inverse of the view scale so the world stays under the pointer
// [F-P1-008][07 §10] (DESIGN_GPU_RENDERER §14.2). It is presentation-only and
// never touches sim.
func (c *Camera) Drag(dx, dy int32) {
	if c == nil {
		return
	}
	s := c.scale()
	// screen delta → world delta (inverse of the view scale)
	wx := dx / s
	wz := dy / s
	// Drag direction: moving mouse right should pan world right → camera follows mouse
	c.Pan(-wx, -wz)
}

// SetScaleAbout sets the view scale, clamped to [1, 2], keeping the world
// point under screen position (mx, my) where it is [F-P1-008]
// (DESIGN_GPU_RENDERER §14.1). F9 toggles through it about the viewport
// centre; `--zoom` with `--shot-focus` uses it for captures.
//
// The arithmetic is the projection's own inverse, so the fixed point is exact:
// world = cam + floorDiv(screen − origin, s), and the new origin is that world
// point less the same quantity at the new scale.
func (c *Camera) SetScaleAbout(mx, my, newS int32) {
	if c == nil {
		return
	}
	oldS := c.scale()
	if newS < 1 {
		newS = 1
	}
	if newS > 2 {
		newS = 2
	}
	if newS == oldS {
		c.Scale = newS
		return
	}
	dx := int64(mx) - int64(OriginX)
	dy := int64(my) - int64(OriginY)
	wx := int64(c.X) + floorDiv(dx, int64(oldS))
	wz := int64(c.Z) + floorDiv(dy, int64(oldS))
	c.X = int32(wx - floorDiv(dx, int64(newS)))
	c.Z = int32(wz - floorDiv(dy, int64(newS)))
	c.Scale = newS
	c.Clamp()
}

// floorDiv is the floor division of [I3]: the screen-to-world inverse must
// floor so a beam offset left of the origin maps to the world pixel that
// covers it rather than to the one after it [03 §2.1].
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// NewFromTerrain creates a camera whose map extents are the playable insets [P1-15].
// PlayRight = Wpix-32, PlayBottom = Hpix-128 are the max extents set at void-fixup time [P1-15];
// they are the clamp's map size, so MapW/MapH are set to PlayRight/PlayBottom when non-zero
// (else fallback to raw terrainWpix/Hpix). The per-axis bounds are then clampAxis's [07 §10].
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
// 128 map pixels [07 §10] C2. Zero rawDelta skips movement entirely.
//
// rawDelta is the scroll pass's raw wall-clock delta: thirtieths of a second
// elapsed since the previous host frame, the same delta the tick budget
// consumes [07 §10] [01 §4.1]. It is NOT milliseconds; at the default setting
// byte 32 a delta of 1 is 32 map pixels and the sustained rate is 32*30 = 960
// map pixels per second at any frame rate. The 128 cap is a low-frame-rate
// limiter, not the normal case.
//
// The cap is a signed comparison and there is no absolute value: a negative
// rawDelta (a wrapped host tick count) keeps its sign and scrolls the opposite
// way for one frame, exactly as retail does [07 §10].
func (c *Camera) Scroll(setting byte, rawDelta int32, dir Direction) { // [07 §10]
	if rawDelta == 0 {
		return
	}
	mag := int32(setting) * rawDelta // [07 §10] delta = setting * rawDelta
	if mag > 128 {
		mag = 128 // capped at 128 [07 §10]; signed test, negatives pass through
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
// coordinates [03 §2.5] C1 at the presentation view scale [F-P1-008]:
//
//	screenX = (worldX>>16 - cameraX)*s + originX
//	screenY = ((worldZ>>16 - ((worldY>>16)>>1)) - cameraZ)*s + originY
//
// The half-height shear ((worldY>>16)>>1) uses arithmetic shifts so negative
// worldY is handled as retail does, and it is applied BEFORE the scale, so a
// doubled view is the same picture at twice the pixels rather than a differently
// sheared one (DESIGN_GPU_RENDERER §14.1). Origin is taken from the viewport
// constants OriginX/Y (128,32 for the observed beam path) rather than inlining
// literals at call sites [03 §2.5]. At s = 1 the expression reduces to the
// original integer path exactly.
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
	sx = (wx-c.X)*s + OriginX
	sy = (wz-shear-c.Z)*s + OriginY
	return
}

// ScreenToWorld inverts WorldToScreen at ground height (worldY=0) [03 §2.5] at
// the presentation view scale [F-P1-008].
// Retail's projection includes a half-height shear on Y; the inverse for
// picking assumes Y=0 so the shear term is zero. This is the ground-plane
// pick used by the minimap direct branch and cursor picking [07 §10].
//
// The divide FLOORS rather than truncating: a beam offset left of or above the
// viewport origin is negative, and truncation toward zero would map the whole
// pair (-1, 0) onto world pixel 0 at s = 2 while leaving -1 unreachable
// [I3][03 §2.1]. With floor division every screen pixel names exactly one world
// pixel and the round trip world → screen → world is the identity
// (DESIGN_GPU_RENDERER §14.1).
func (c *Camera) ScreenToWorld(sx, sy int32) (x, z numeric.Fixed) { // [03 §2.5]
	s := c.scale()
	if s == 1 {
		wx := int64(sx-OriginX+c.X) << 16
		wz := int64(sy-OriginY+c.Z) << 16
		return numeric.Fixed(wx), numeric.Fixed(wz)
	}
	wx := (int64(c.X) + floorDiv(int64(sx)-int64(OriginX), int64(s))) << 16
	wz := (int64(c.Z) + floorDiv(int64(sy)-int64(OriginY), int64(s))) << 16
	return numeric.Fixed(wx), numeric.Fixed(wz)
}
