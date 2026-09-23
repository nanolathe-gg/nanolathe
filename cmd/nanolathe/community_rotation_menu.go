package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// communityRotationMenuState is the presentation-only click feedback for the
// CP-CON-5 build-menu overlay. The window pointer is part of the identity:
// authored and generated pages can reuse the same gadget index.
type communityRotationMenuState struct {
	feedbackWindow *gui.Window
	feedbackIndex  int
	feedbackFacing units.StructureFacing
	feedbackAt     uint32
}

const (
	communityRotationEdgeBand     = int32(13)
	communityRotationFeedback     = uint32(200)
	communityRotationFill         = uint8(251)
	communityRotationOutline      = uint8(0)
	communityRotationChevronArm   = 3
	communityRotationChevronDepth = 4
	communityRotationChevronGap   = 4
	communityRotationChevronInset = 5
)

// communityRotationCardinal maps a pointer inside a build button to the
// nearest edge. Equal distances keep the source's N, S, E, W precedence; the
// central area has no rotation meaning (community patch engine CP-CON-5).
func communityRotationCardinal(rect gui.Rect, x, y int32) (units.StructureFacing, bool) {
	if rect.W <= 0 || rect.H <= 0 || !guiRectContains(rect, x, y) {
		return units.FacingSouth, false
	}
	rx, ry := x-rect.X, y-rect.Y
	distN := ry
	distS := rect.H - 1 - ry
	distE := rect.W - 1 - rx
	distW := rx
	minDistance := min(distN, distS, distE, distW)
	if minDistance >= communityRotationEdgeBand {
		return units.FacingSouth, false
	}
	switch minDistance {
	case distN:
		return units.FacingNorth, true
	case distS:
		return units.FacingSouth, true
	case distE:
		return units.FacingEast, true
	default:
		return units.FacingWest, true
	}
}

func communityRotationFacingCount(mask content.FacingMask) int {
	count := 0
	for facing := units.FacingSouth; facing <= units.FacingWest; facing++ {
		if mask&units.FacingMask(facing) != 0 {
			count++
		}
	}
	return count
}

// selectCommunityRotationMenuFacing applies the edge choice immediately
// before the ordinary product callback arms placement. Turning the overlay off
// also turns this hit band off, matching the extension's single preference.
func (h *retailBattleHUD) selectCommunityRotationMenuFacing(b *battleSession, window *gui.Window, index int, product *content.UnitDef, x, y int32) bool {
	if h == nil || b == nil || window == nil || index <= 0 || index >= len(window.Gadgets) || product == nil || product.BMCode != 0 || b.hostPreferences().BuildRotationOverlay == 0 || b.sess == nil || b.sess.Build == nil {
		return false
	}
	allowed := b.sess.Build.AllowedFacings(product)
	if communityRotationFacingCount(allowed) < 2 {
		return false
	}
	facing, ok := communityRotationCardinal(window.PlacedRect(index), x, y)
	if !ok || allowed&units.FacingMask(facing) == 0 {
		return false
	}
	b.communityPlacement.facing = facing
	b.communityRotationMenu = communityRotationMenuState{
		feedbackWindow: window,
		feedbackIndex:  index,
		feedbackFacing: facing,
		feedbackAt:     b.communityRotationMenuMillis(),
	}
	return true
}

func (b *battleSession) communityRotationMenuMillis() uint32 {
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	return b.millisSource.Millis32()
}

// drawCommunityRotationMenu overlays every allowed facing on visible,
// rotatable structure buttons. The caller places this directly after the
// existing side-page painter; the established later modal and popup painters
// then provide the same occlusion as the rest of the palette.
func (h *retailBattleHUD) drawCommunityRotationMenu(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || b == nil || f == nil || b.sess == nil || b.sess.Build == nil || b.hostPreferences().BuildRotationOverlay == 0 {
		return
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return
	}
	if window == nil || b.cat == nil {
		return
	}
	idle := communityRotationMenuArt(h, "buildrotate")
	click := communityRotationMenuArt(h, "buildrotateclick")
	now := b.communityRotationMenuMillis()
	feedback := &b.communityRotationMenu
	feedbackOn := feedback.feedbackWindow == window && feedback.feedbackIndex > 0 && now-feedback.feedbackAt < communityRotationFeedback

	for index, gad := range window.Gadgets {
		if index == 0 || gad.Active == 0 || gad.Kind != gui.KindButton {
			continue
		}
		product, found := b.cat.Unit(gad.Name)
		if !found || product == nil || product.BMCode != 0 {
			continue
		}
		allowed := b.sess.Build.AllowedFacings(product)
		if communityRotationFacingCount(allowed) < 2 {
			continue
		}
		rect := window.PlacedRect(index)
		for facing := units.FacingSouth; facing <= units.FacingWest; facing++ {
			if allowed&units.FacingMask(facing) == 0 {
				continue
			}
			highlight := feedbackOn && feedback.feedbackIndex == index && feedback.feedbackFacing == facing
			if idle != nil {
				entry := idle
				if highlight && click != nil {
					entry = click
				}
				drawCommunityRotationGAF(c, rect, facing, entry.Frames[int(facing)].Frame)
				continue
			}
			fill, outline := communityRotationFill, communityRotationOutline
			if highlight {
				fill = h.guiColor(15)
				outline = fill
			}
			drawCommunityRotationChevrons(c, rect, facing, fill, outline)
		}
	}
}

// communityRotationMenuArt uses the battle HUD's existing lazy GAF cache and
// accepts the extension artwork only when its first sequence has exactly the
// four S, E, N, W frames required by CP-CON-5.
func communityRotationMenuArt(h *retailBattleHUD, name string) *formats.GAFEntry {
	if h == nil {
		return nil
	}
	gaf := h.resolvePageArt(name)
	if gaf == nil || len(gaf.Entries) == 0 || len(gaf.Entries[0].Frames) != 4 {
		return nil
	}
	for _, ref := range gaf.Entries[0].Frames {
		if ref.Frame == nil {
			return nil
		}
	}
	return &gaf.Entries[0]
}

func drawCommunityRotationGAF(c *client.Client, rect gui.Rect, facing units.StructureFacing, art *formats.GAFFrame) {
	if c == nil || art == nil {
		return
	}
	halfBand := communityRotationEdgeBand/2 + 1
	anchorX, anchorY := rect.X+rect.W/2, rect.Y+rect.H/2
	switch facing {
	case units.FacingSouth:
		anchorY = rect.Y + rect.H - halfBand
	case units.FacingEast:
		anchorX = rect.X + rect.W - halfBand
	case units.FacingNorth:
		anchorY = rect.Y + halfBand
	case units.FacingWest:
		anchorX = rect.X + halfBand
	}
	c.UIBlitAnchor(art, int(anchorX), int(anchorY))
}

func drawCommunityRotationChevrons(c *client.Client, rect gui.Rect, facing units.StructureFacing, fill, outline uint8) {
	if c == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	outX := [...]int{0, 1, 0, -1}
	outY := [...]int{1, 0, -1, 0}
	ox, oy := outX[int(facing)&3], outY[int(facing)&3]
	px, py := oy, -ox
	edgeX, edgeY := int(rect.X+rect.W/2), int(rect.Y+rect.H/2)
	switch facing {
	case units.FacingSouth:
		edgeY = int(rect.Y + rect.H)
	case units.FacingEast:
		edgeX = int(rect.X + rect.W)
	case units.FacingNorth:
		edgeY = int(rect.Y)
	case units.FacingWest:
		edgeX = int(rect.X)
	}
	a1x, a1y := edgeX-communityRotationChevronInset*ox, edgeY-communityRotationChevronInset*oy
	a2x, a2y := a1x-communityRotationChevronGap*ox, a1y-communityRotationChevronGap*oy
	draw := func(ax, ay int, color uint8, thickness int) {
		leftX := ax - communityRotationChevronDepth*ox - communityRotationChevronArm*px
		leftY := ay - communityRotationChevronDepth*oy - communityRotationChevronArm*py
		rightX := ax - communityRotationChevronDepth*ox + communityRotationChevronArm*px
		rightY := ay - communityRotationChevronDepth*oy + communityRotationChevronArm*py
		drawCommunityRotationLine(c, ax, ay, leftX, leftY, color, thickness)
		drawCommunityRotationLine(c, ax, ay, rightX, rightY, color, thickness)
	}
	for _, apex := range [][2]int{{a1x, a1y}, {a2x, a2y}} {
		draw(apex[0], apex[1], outline, 3)
	}
	for _, apex := range [][2]int{{a1x, a1y}, {a2x, a2y}} {
		draw(apex[0], apex[1], fill, 1)
	}
}

func drawCommunityRotationLine(c *client.Client, x0, y0, x1, y1 int, color uint8, thickness int) {
	dx := communityRotationAbs(x1 - x0)
	dy := -communityRotationAbs(y1 - y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	half := thickness / 2
	for {
		c.UIFillRect(x0-half, y0-half, thickness, thickness, color)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func communityRotationAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
