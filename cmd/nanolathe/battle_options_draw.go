package main

// The in-battle options window's painter [07 R-FE-01 §6][07 R-FE-01 §7].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
)

// drawBattleOptionsWindow composes the in-battle options root and its merged
// page over the battle.
//
// It is not the front end's own window painter for one reason: the in-battle
// family's plates — the root's `IGOPT` and each page's own full-column art —
// are picture boxes (kind 12), whose art resolves through the gadget's own GAF
// and then the **common** GAF [07 R-WGT-01 §12]. The front-end painter resolves
// a picture only against the open screen's resource set, which in a battle is
// not mounted, so those plates would not draw at all. Every other control goes
// through the shared front-end routines.
//
// Draw order is the window's gadget order, which puts each plate behind the
// controls authored over it, exactly as the merge leaves the array.
func (h *retailBattleHUD) drawBattleOptionsWindow(c *client.Client, b *battleSession) {
	if h == nil || c == nil || !b.battlePrefsActive() {
		return
	}
	g := b.shell
	p := optionsPanel
	if p == nil || p.Window == nil {
		return
	}
	// The in-battle root installs no background bitmap at any step, so the
	// window takes the nine-slice tile fill of its authored `panel` entry,
	// clipped to the window rectangle [07 §4][07 R-WGT-02][07 R-FE-01 §6]. The
	// plates below cover it wherever they reach; what shows through is the
	// column past the page art's own width.
	h.drawWindowBackground(c, p.Window, nil)
	for i, gad := range p.Window.Gadgets {
		if i == 0 || !p.ActiveOf(gad.Name) {
			continue
		}
		r := p.Window.PlacedRect(i)
		switch gad.Kind {
		case gui.KindPanel, gui.KindPanelAlias, gui.KindFont, gui.KindRawFile:
			// The synthesised `PANEL` and the two file kinds paint nothing
			// [07 R-WGT-01 §12].
		case gui.KindButton:
			g.drawRetailButton(c, p, i, gad, r)
		case gui.KindScrollBar:
			g.drawRetailScrollbar(c, p, gad, r)
		case gui.KindPicture:
			if f := battleOptionsPictureFrame(g, gad); f != nil {
				blitRetailFrame(c, f, int(r.X), int(r.Y))
			}
		default:
			g.drawRetailArt(c, p, gad, r)
			g.drawRetailText(c, p, i, gad, r)
		}
	}
}

// battleOptionsPictureFrame resolves a picture box's frame 0 through the
// gadget's own GAF and then the common GAF [07 R-WGT-01 §12]. The in-battle
// family's plates — `IGOPT`, `SOUNDSRT`, `MUSICRT`, `SPEEDSRT`, `VISUALSRT` —
// all live in the common GAF, which is the hop the front-end picture path does
// not walk.
func battleOptionsPictureFrame(g *gameShell, gad gui.Gadget) *formats.GAFFrame {
	if f := g.gadgetArt(gad, 0); f != nil {
		return f
	}
	if g == nil || g.assets == nil || g.assets.common == nil {
		return nil
	}
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	entry, ok := g.assets.common.Find(name)
	if !ok || len(entry.Frames) == 0 {
		return nil
	}
	return entry.Frames[0].Frame
}
