package client

// The world health bar and the control-group digit — retail's "unit labels"
// pass. The composer walks its on-screen unit list between the strip-8 and the
// strip-9 walks, which is before the fog composite: fog covers everything
// world-drawn below it, and strip-9 smoke therefore composes over the bars
// [03 §1][03 R-FX-01 §6].
//
// The bar is drawn only for units belonging to the viewing player. Other
// players' units never get one, whatever the option, and there is no
// line-of-sight test beyond the list's own admission [03 R-FX-01 §6];
// [04 R-SPEC-01 §6] states the same rule from the `hidedamage` side, where the
// information panel's bar is gated on the owner slot equalling the local
// player slot.

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// damageBars is bit 0 of the interface-flags word: the persistent "label every
// unit" option, read from the stored `damagebars` value at settings load and
// rewritten immediately whenever a battle key command flips it
// [07 R-HUD-03 §7][03 R-FX-01 §6]. Retail holds it in one process-global
// interface word rather than per view, and so does this: it is one option for
// the whole runtime, never per frame or per committed unit.
var damageBars bool

// DamageBars reports the current state of the option bit.
func DamageBars() bool { return damageBars }

// SetDamageBars installs the bit read at settings load [07 R-HUD-03 §7].
func SetDamageBars(on bool) { damageBars = on }

// ToggleDamageBars flips the bit and returns its new state. The caller writes
// the settings back immediately, which is what the key command does
// [07 R-HUD-03 §7].
func ToggleDamageBars() bool {
	damageBars = !damageBars
	return damageBars
}

// Health-bar geometry, exactly as [03 R-FX-01 §6] gives it: the bar row is
// `sy0 + 42` and the group digit's row `sy0 + 46`.
//
// Two coordinate frames meet here. The section's `sx = Xword − viewX + 128`
// carries the viewport's X origin, while its `sy0 = Zword − Yword/2 − viewZ`
// is the projection of [03 §2.5] *without* the +32 Y origin — which is what
// makes the section's own gloss true: the unit's anchor row is `sy0 + 32`, so
// `sy0 + 42` is ten rows below it. Nanolathe's world layer is drawn with the
// viewport origin already subtracted (modelAnchor, the shadow pass and the
// nano spray all take `WorldToScreen − (OriginX, OriginY)`), so each of the
// section's rows is converted once, at the call site, by subtracting the same
// Y origin. The constants stay as the section writes them.
//
// The playtest report that retail draws bars *above* the unit is not
// reproduced: the section is Established direct-static and wins. What it puts
// on screen is a bar ten rows below the unit's model anchor.
const (
	healthBarRowOffset    = 42 // sy0 + 42 [03 R-FX-01 §6]
	groupDigitRowOffset   = 46 // sy0 + 46 [03 R-FX-01 §6]
	healthBarOuterHalfW   = 17 // outer spans [sx−17 .. sx+17] — 35 px
	healthBarOuterHalfH   = 2  // outer spans [y−2 .. y+2] — 5 px
	healthBarInnerLeft    = 16 // inner starts at sx−16
	healthBarInnerHalfH   = 1  // inner spans [y−1 .. y+1] — 3 px
	healthBarFillShift    = 5  // w = (hp << 5) / maxdamage
	healthBarOuterLogical = 0  // dcb[0]
)

// drawUnitLabels is the unit-label walk of [03 §1] item 9: it runs after the
// unconditional strip-8 draw and before strip 9 and the fog composite. Each
// label is gated on the option byte and on the labelled owner equalling the
// local player slot [03 §1][03 R-FX-01 §6].
//
// The walk admits a unit that has the option bit set **or** a nonzero group
// number; the bar is drawn when the bit is set, and the digit when the unit
// also carries a group. With the bit clear only grouped units are labelled,
// and they get the digit alone [07 R-CAM-01 §2].
//
// Presentation only: nothing here writes simulation state [I6]. The walk is
// over the committed unit slice in slot order, so it holds no map iteration
// [I1].
func (c *Client) drawUnitLabels(cur *frame.Frame, ok bool) {
	if c == nil || !ok || cur == nil || c.cam == nil || len(c.indexed) != c.width*c.height {
		return
	}
	bars := damageBars
	viewer := cur.ViewingPlayer
	for i := range cur.Units {
		u := &cur.Units[i]
		if !bars && u.Group == 0 {
			continue
		}
		if u.Owner != viewer {
			continue
		}
		sx, sy := c.cam.WorldToScreen(u.X, u.Y, u.Z)
		// sy0 is the section's projection, the anchor row less the viewport's
		// Y origin; both rows are then carried into the surface's
		// origin-removed space. See the geometry note above.
		//
		// Each row offset is an authored screen offset from the unit's model
		// anchor, so it takes the view scale: at the detail scale the model is
		// twice as tall and the bar has to sit twice as far below the anchor to
		// stay clear of it (DESIGN_GPU_RENDERER §14.2). The offset that scales is
		// the one measured from the ANCHOR, which is the section's row less the
		// viewport's Y origin — ten rows for the bar and fourteen for the digit —
		// so the two constants are combined before the multiply rather than
		// after. The GLYPH itself is not scaled: text is interface, and only the
		// anchor it is drawn at moves.
		s := c.viewScale()
		x := sx - camera.OriginX
		sy0 := sy - camera.OriginY
		if bars {
			c.drawHealthBar(x, sy0+s.Px(healthBarRowOffset-camera.OriginY), u.Health, u.MaxHealth)
		}
		if u.Group != 0 {
			c.drawGroupDigit(x, sy0+s.Px(groupDigitRowOffset-camera.OriginY), u.Group)
		}
	}
}

// drawHealthBar rasterizes one bar at (sx, y) from the unit's signed 16-bit
// current health and its definition's 32-bit `maxdamage` [03 R-FX-01 §6]:
//
//	if hp <= 0: draw nothing
//	outer = [sx−17 .. sx+17] × [y−2 .. y+2]   filled with dcb[0]
//	w     = (hp << 5) / maxdamage             (unsigned, truncating)
//	inner = [sx−16 .. sx−16+w] × [y−1 .. y+1] filled with
//	           dcb[10] if hp > 2·(maxdamage/3)   (maxdamage/3 unsigned, truncating)
//	           dcb[14] if hp > maxdamage/3
//	           dcb[12] otherwise
//
// Both fills go through the inclusive rectangle filler, so the outer bar is
// 35 × 5 and the inner fill w+1 × 3: full health fills 33 pixels and leaves a
// one-pixel border each side, and a unit at 1 of 3000 still shows a one-pixel
// fill. The comparisons are signed and strict.
func (c *Client) drawHealthBar(sx, y, health, maxDamage int32) {
	// The section names the input as "the unit's signed 16-bit current health"
	// [03 R-FX-01 §6], so the committed value is narrowed to that width before
	// every comparison and before the shift.
	hp := int32(int16(health))
	if hp <= 0 { // death hides the bar the same frame [03 R-FX-01 §6]
		return
	}
	// Record then execute inline: classicSink.Fill's FillSolidInclusive style
	// runs the same inclusive-bounds filler fillRectInclusive this used to call
	// directly. The rect is carried in extent form; the sink reconstructs the
	// inclusive right/bottom [03 R-FX-01 §6].
	// The bar is a fill, so its extents take the view scale while its anchor
	// comes through the projection (DESIGN_GPU_RENDERER §14.2). The fill width
	// w is derived from the same scaled half-extent, so a full bar still leaves
	// a one-pixel border at either scale.
	s := c.viewScale()
	halfW, halfH := s.Px(healthBarOuterHalfW), s.Px(healthBarOuterHalfH)
	c.emitFillInclusive(sx-halfW, y-halfH, sx+halfW, y+halfH, c.paletteIndex(healthBarOuterLogical))
	if maxDamage <= 0 {
		// Retail's two divisions are unguarded and would fault on a zero or
		// negative `maxdamage`; refusing to divide is the bounds-check
		// exception of [I11], noted here rather than silently diverging.
		return
	}
	w := int32(uint32(hp<<healthBarFillShift) / uint32(maxDamage))
	third := int32(uint32(maxDamage) / 3)
	fill := c.paletteIndex(12)
	switch {
	case hp > 2*third:
		fill = c.paletteIndex(10)
	case hp > third:
		fill = c.paletteIndex(14)
	}
	innerLeft, innerHalfH := s.Px(healthBarInnerLeft), s.Px(healthBarInnerHalfH)
	c.emitFillInclusive(sx-innerLeft, y-innerHalfH, sx-innerLeft+s.Px(w), y+innerHalfH, fill)
}

// drawGroupDigit stamps the one-character label '0' + group at (sx, y)
// [03 R-FX-01 §6].
func (c *Client) drawGroupDigit(sx, y int32, group uint8) {
	if c.fnt == nil {
		return
	}
	// The "default colour" of [03 R-FX-01 §6] is colour-map entry 15, and it
	// belongs to the composer rather than to this call. This previously carried an
	// open-question marker — "no section names the entry that state holds when
	// the composer reaches the label walk. Placeholder: dcb[15]" — and the
	// placeholder was right, so it is now the contract rather than a guess. The digit's own text call installs no
	// foreground — it passes the string and the pen and nothing else — while
	// the frame composer selects the local player's side font and installs
	// (foreground = entry 15, background = the skip colour) once, immediately
	// before the strip walks and so before this walk, and none of the strip
	// drawers between installs another [03 R-FX-01 §6A][03 R-FONT-01 §6].
	// Record then execute inline: classicSink.Glyphs runs the same drawText
	// rasterizer with the same pen and no foreground install of its own, exactly
	// as this call did directly [03 R-FX-01 §6A][03 §7.1].
	c.emitGlyphs(drawlist.Glyphs{
		Font:  c.fnt,
		Text:  string([]byte{'0' + group}),
		X:     sx,
		Y:     y,
		Color: c.paletteIndex(15),
	})
}

// fillRectInclusive is retail's solid rectangle fill: both boundaries are
// inclusive, and the rectangle clipper rejects on any inclusive edge test,
// clamps, and re-checks that the clamped rectangle is still non-empty
// [R-P0-19-P]. The clip is the framebuffer; the interface stage repaints the
// authored panels over any overlap afterwards [03 §1].
func (c *Client) fillRectInclusive(left, top, right, bottom int32, idx uint8) {
	if c == nil || c.width <= 0 || c.height <= 0 || len(c.indexed) != c.width*c.height {
		return
	}
	clipRight, clipBottom := int32(c.width)-1, int32(c.height)-1
	if right < 0 || left > clipRight || bottom < 0 || top > clipBottom {
		return
	}
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if right > clipRight {
		right = clipRight
	}
	if bottom > clipBottom {
		bottom = clipBottom
	}
	if left > right || top > bottom {
		return
	}
	for y := top; y <= bottom; y++ {
		row := y * int32(c.width)
		for x := left; x <= right; x++ {
			c.indexed[row+x] = idx
		}
	}
}
