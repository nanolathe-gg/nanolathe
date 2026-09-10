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
// Scale is the presentation-only view scale [F-P1-008] in half steps: zero
// and ViewScaleNative mean native, ViewScaleMid the 1.5x view and
// ViewScaleDetail the 2x detail view. The projection and its inverse are
// integer at every step (DESIGN_GPU_RENDERER §14.1).
type Camera struct {
	X, Z         int32
	ViewW, ViewH int32
	MapW, MapH   int32
	Scale        ViewScale // presentation RECORD step; zero == native [F-P1-008]

	// Zoom is the LIVE presentation zoom factor of DESIGN_GPU_RENDERER §16, in
	// 1/ZoomUnit units. Zero reads as Scale's own factor, which is what the
	// classic executor is always on: classic has no free zoom, so f == s there
	// and every projection below reduces to the build before §16.
	//
	// The invariant the whole design rests on is that Scale is the step the
	// RECORDER emits at while Zoom is the factor the player sees. The modern
	// executor scales the recorded world by Zoom/Scale; at Zoom == ZoomOf(Scale)
	// that scale is one and the recording reaches pixels untouched.
	Zoom Zoom

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
// any scale. The division is the scale's own floor inverse, integer at every
// step (DESIGN_GPU_RENDERER §14.2).
func (c *Camera) clampInsets() (leadX, trailX, leadZ, trailZ int32) { // [03 §4.1]
	// The LIVE factor, not the record step: the chrome covers the same
	// framebuffer pixels whatever the recorder emitted at, so the world it hides
	// is measured through what the player is actually seeing (§16.4).
	z := c.zoom()
	if z == ZoomUnit {
		return OriginX, 0, OriginY, OriginY
	}
	insetY := z.Inverse(OriginY)
	return z.Inverse(OriginX), 0, insetY, insetY
}

// scale returns the effective view scale, clamped to the three views with
// zero meaning native [F-P1-008] (DESIGN_GPU_RENDERER §14.1).
func (c *Camera) scale() ViewScale {
	if c == nil {
		return ViewScaleNative
	}
	return c.Scale.Norm()
}

// EffectiveScale returns the clamped presentation RECORD step [F-P1-008]. The
// recorder projects at it; it is not what the player sees once the modern
// executor's free zoom is off a rest step (§16.2). EffectiveZoom is the live
// factor.
func (c *Camera) EffectiveScale() ViewScale { // [F-P1-008]
	return c.scale()
}

// zoom returns the effective live zoom factor, reading a zero field as the
// record step's own factor so a camera that never sets one behaves exactly as
// the build before §16 (DESIGN_GPU_RENDERER §16.2).
func (c *Camera) zoom() Zoom {
	if c == nil {
		return ZoomUnit
	}
	if c.Zoom <= 0 {
		return ZoomOf(c.scale())
	}
	return c.Zoom.Norm()
}

// EffectiveZoom returns the clamped live presentation zoom factor [F-P1-008]
// (DESIGN_GPU_RENDERER §16.2). Presentation-only; sim never reads it [I6].
func (c *Camera) EffectiveZoom() Zoom { return c.zoom() }

// AtRestStep reports whether the live factor equals the record step's own
// factor, which is when the modern executor's world transform is the identity
// and the frame reaches pixels exactly as the build before §16 composed it.
func (c *Camera) AtRestStep() bool { return c.zoom() == ZoomOf(c.scale()) }

// EffectiveView returns the view size in world pixels after the LIVE zoom
// factor. At a magnified factor less world is visible, at a reduced one more.
// Clamp uses this.
func (c *Camera) EffectiveView() (int32, int32) {
	z := c.zoom()
	if z == ZoomUnit {
		return c.ViewW, c.ViewH
	}
	return z.Inverse(c.ViewW), z.Inverse(c.ViewH)
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
	// screen delta → world delta through the LIVE factor, truncated toward zero
	// as the whole-scale divide always was (§16.4).
	z := int64(c.zoom())
	wx := int32(int64(dx) * int64(ZoomUnit) / z)
	wz := int32(int64(dy) * int64(ZoomUnit) / z)
	// Drag direction: moving mouse right should pan world right → camera follows mouse
	c.Pan(-wx, -wz)
}

// SetScaleAbout sets the view scale, clamped to the three views, keeping the
// world point under screen position (mx, my) where it is [F-P1-008]
// (DESIGN_GPU_RENDERER §14.1). F9 cycles through it about the viewport
// centre; `--zoom` with `--shot-focus` uses it for captures.
//
// The arithmetic is the projection's own inverse, so the fixed point is exact:
// world = cam + Inverse(screen − origin), and the new origin is that world
// point less the same quantity at the new scale. (mx, my) are BEAM pixels —
// the framebuffer point plus (OriginX, OriginY) — exactly what ScreenToWorld
// takes [03 §2.5]; a caller holding a framebuffer point adds the offsets.
func (c *Camera) SetScaleAbout(mx, my int32, newS ViewScale) {
	if c == nil {
		return
	}
	// A step change is a zoom to that step's own factor: it sets the record step
	// and the live factor together, which is the classic executor's only mode
	// and F9's classic cycle (§16.8). The map-derived floor of MinZoom is NOT
	// applied here — a step is always at least 1x, and the view-larger-than-map
	// domain of clampAxis stays exactly where [07 §10] left it.
	c.setZoomAboutRaw(mx, my, ZoomOf(newS))
	c.Scale = newS.Norm()
}

// SetZoomAbout sets the LIVE zoom factor, keeping the world point under screen
// position (mx, my) where it is, and re-derives the record step from it
// (DESIGN_GPU_RENDERER §16.5). It is the generalization of SetScaleAbout: the
// fixed point is world = cam + Inverse_f(screen − origin), and the new origin
// is that world point less the same quantity at the new factor. (mx, my) are
// beam pixels, as for SetScaleAbout.
//
// The record step follows the factor (Zoom.Step): 2x above 1x, 1x at or below
// it. Changing the step does not move anything on screen, because the executor
// scales the recording by f/s and both sides change together.
func (c *Camera) SetZoomAbout(mx, my int32, newZ Zoom) {
	if c == nil {
		return
	}
	newZ = newZ.Norm()
	if minZ := c.MinZoom(); newZ < minZ {
		newZ = minZ
	}
	c.setZoomAboutRaw(mx, my, newZ)
}

// setZoomAboutRaw is SetZoomAbout without the map-derived floor, so the step
// path can keep clampAxis's view-larger-than-map domain untouched.
func (c *Camera) setZoomAboutRaw(mx, my int32, newZ Zoom) {
	oldZ := c.zoom()
	newZ = newZ.Norm()
	if newZ != oldZ {
		dx := mx - OriginX
		dy := my - OriginY
		wx := c.X + oldZ.Inverse(dx)
		wz := c.Z + oldZ.Inverse(dy)
		c.X = wx - newZ.Inverse(dx)
		c.Z = wz - newZ.Inverse(dy)
	}
	c.Zoom = newZ
	c.Scale = newZ.Step()
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
//	screenX = Project(worldX>>16 - cameraX) + originX
//	screenY = Project((worldZ>>16 - ((worldY>>16)>>1)) - cameraZ) + originY
//
// where Project is the scale's ceil((v·s)/2): the exact multiply at 1x and 2x
// and the first covering screen pixel at 1.5x (ViewScale.Project). The
// half-height shear ((worldY>>16)>>1) uses arithmetic shifts so negative
// worldY is handled as retail does, and it is applied BEFORE the scale, so a
// magnified view is the same picture at more pixels rather than a differently
// sheared one (DESIGN_GPU_RENDERER §14.1). Origin is taken from the viewport
// constants OriginX/Y (128,32 for the observed beam path) rather than inlining
// literals at call sites [03 §2.5]. At the native scale the expression reduces
// to the original integer path exactly.
func (c *Camera) WorldToScreen(x, y, z numeric.Fixed) (sx, sy int32) { // [03 §2.5]
	wx := int32(int64(x) >> 16)
	wy := int32(int64(y) >> 16)
	wz := int32(int64(z) >> 16)
	shear := wy >> 1 // arithmetic shift preserves sign for negative wy [03 §2.5]
	s := c.scale()
	if s.Native() {
		sx = wx - c.X + OriginX
		sy = wz - shear - c.Z + OriginY
		return
	}
	sx = s.Project(wx-c.X) + OriginX
	sy = s.Project(wz-shear-c.Z) + OriginY
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
// pair (-1, 0) onto world pixel 0 at 2x while leaving -1 unreachable
// [I3][03 §2.1]. With the floor inverse every screen pixel names exactly one
// world pixel and the round trip world → screen → world is the identity at
// every scale, 1.5x included (ViewScale.Inverse; DESIGN_GPU_RENDERER §14.1).
// Since DESIGN_GPU_RENDERER §16 the inverse goes through the LIVE zoom factor
// rather than the record step, because the pointer names a pixel of the
// PRESENTED picture: world = cameraOrigin + floor((screen − viewportOrigin)/f).
// At a rest factor that is still the exact inverse of WorldToScreen; in flight
// it is the obvious floor, and ScreenToRecord is the bridge for the pick tests
// that compare against projected record coordinates (§16.4).
func (c *Camera) ScreenToWorld(sx, sy int32) (x, z numeric.Fixed) { // [03 §2.5]
	zf := c.zoom()
	if zf == ZoomUnit {
		wx := int64(sx-OriginX+c.X) << 16
		wz := int64(sy-OriginY+c.Z) << 16
		return numeric.Fixed(wx), numeric.Fixed(wz)
	}
	wx := int64(c.X+zf.Inverse(sx-OriginX)) << 16
	wz := int64(c.Z+zf.Inverse(sy-OriginY)) << 16
	return numeric.Fixed(wx), numeric.Fixed(wz)
}

// ScreenToRecord maps a presented beam-space pointer to the beam-space
// coordinate the RECORDER would have projected the world pixel under it to
// (DESIGN_GPU_RENDERER §16.4).
//
// It exists because the picking helpers that cannot be expressed as a world
// point — the hover hull polygon and the drag rectangle's containment test —
// compare the pointer against corners produced by WorldToScreen, which is
// recorded at the step. Composing the live inverse with the record projection
// puts both sides in one space at any factor, and at a rest factor it is the
// identity, so nothing about picking changes there.
func (c *Camera) ScreenToRecord(sx, sy int32) (int32, int32) {
	if c == nil {
		return sx, sy
	}
	if c.AtRestStep() {
		return sx, sy
	}
	zf := c.zoom()
	s := c.scale()
	return s.Project(zf.Inverse(sx-OriginX)) + OriginX, s.Project(zf.Inverse(sy-OriginY)) + OriginY
}
