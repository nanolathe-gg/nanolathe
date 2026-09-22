package main

// The footer: the selected unit's name, its health bar and the hovered
// gadget's caption [07 R-HUD-03 §1].

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// drawFooter paints the ordinary footer [07 R-HUD-03 §1–§3]. It replaces the
// former drawSelectedUnit, which drew the SELECTED unit's name, description
// and bar at the wrong anchors: the footer never reads the selection. Its
// three sources are the hovered gadget, the hovered world unit and the
// hovered feature, in that fixed priority.
//
// Placement is shared by every field: each anchor's y is offset by
// dy = screenHeight − baseheight (the side's [GENERAL] baseheight, default
// 480 [02 §6]) and x is never shifted. Text uses the side's console face and,
// unless a rule names a colour-map entry, the raw palette index 83. There is
// no maximum width, so no footer field wraps, ellipsises or truncates.
func (h *retailBattleHUD) drawFooter(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || f == nil {
		return
	}
	if b != nil && b.developer.film && b.developer.information {
		b.drawDeveloperFooter(c, f, h.primaryFont)
		return
	}
	footer := hud.BuildFooter(f, h.cat, f.ViewingPlayer, b.footerHover(f), false,
		communityFooterOptions(c))
	if footer.Empty() {
		return
	}
	dy := h.footerDY(c)
	for _, bar := range footer.Bars {
		r, ok := h.anchors.ByIndex(bar.Anchor)
		if !ok {
			continue
		}
		h.drawFooterBar(c, r, dy, bar.HP, bar.Max)
	}
	// The owner's logo at LOGO2: the frame of the side-logo GAF indexed by the
	// owner's lobby colour byte, at the frame's full size, at the anchor
	// shifted by dy [07 R-HUD-03 §2]. The entry is `32xlogos` of
	// textures/logos.gaf — one handle bound at battle-data initialization that
	// this draw and the score panel's row logo both read
	// [07 R-HUD-04 §4]. footer.Logos already carried the resolved frame index;
	// only the entry name was missing.
	for _, logo := range footer.Logos {
		r, ok := h.anchors.ByIndex(logo.Anchor)
		if !ok {
			continue
		}
		art := h.sideLogoFrame(uint8(logo.Frame))
		if art == nil {
			continue
		}
		c.UIBlit(art, int(r.X1), int(r.Y1+dy))
	}
	for _, text := range footer.Texts {
		r, ok := h.anchors.ByIndex(text.Anchor)
		if !ok || text.Text == "" {
			continue
		}
		x, y := r.X1, r.Y1
		if text.FromY2 {
			y = r.Y2
		}
		y += text.OffsetY + dy
		if text.Centered {
			// "Centred at A" is x = A.x1 − trunc(textWidth/2), a signed divide
			// truncating toward zero [07 R-HUD-03 §1].
			x -= int32(client.MeasureText(h.console, text.Text)) / 2
		}
		color := text.Color.Value
		if text.Color.Logical {
			color = h.guiColor(color)
		}
		c.UIText(h.console, text.Text, int(x), int(y), color)
	}
}

// footerDY is the shared vertical offset dy = screenHeight − baseheight
// [07 R-HUD-03 §1][02 §6]. x is never shifted.
func (h *retailBattleHUD) footerDY(c *client.Client) int32 {
	base := int32(480)
	if h.side != nil && h.side.BaseHeight > 0 {
		base = h.side.BaseHeight
	}
	_, height := c.Size()
	return int32(height) - base
}

// drawFooterBar is the footer's two-part inclusive fill [07 R-HUD-03 §2]: the
// filled span [x1..fill] takes dcb[10] and, when fill is not already x2, the
// remainder [fill+1..x2] takes dcb[4]. A dead-level hp still paints the one
// pixel column at x1; there is no threshold colouring here — that belongs to
// the world health bar [03 R-FX-01 §6].
func (h *retailBattleHUD) drawFooterBar(c *client.Client, r hud.Rect, dy int32, health, max int32) {
	left, top, right, bottom := r.Ordered()
	if right < left || bottom < top || max <= 0 {
		return
	}
	fill := hud.FooterBarFill(hud.Rect{X1: left, Y1: top, X2: right, Y2: bottom}, health, max)
	top += dy
	bottom += dy
	c.UIFillRect(int(left), int(top), int(fill-left+1), int(bottom-top+1), h.guiColor(hud.PaletteProduction))
	if fill != right {
		c.UIFillRect(int(fill+1), int(top), int(right-fill), int(bottom-top+1), h.guiColor(hud.FooterBarRemainder))
	}
}

func (h *retailBattleHUD) defFor(u *frame.UnitView) (*content.UnitDef, bool) {
	if h == nil || h.cat == nil || u == nil {
		return nil, false
	}
	if u.DefName != "" {
		if def, ok := h.cat.Unit(u.DefName); ok {
			return def, true
		}
	}
	return h.cat.UnitDefByIndex(uint32(u.DefID))
}
