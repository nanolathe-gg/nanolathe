package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// This popup is Nanolathe presentation, not a retail loading stage. It reuses
// MSGBOX panel art and LIGHTBAR without adding an interactive modal to the
// frontend stack (DESIGN_GPU_RENDERER §14.4).
const remasterPopupDelay = 0.35

type remasterProgress struct {
	label   string
	percent int
}

// reportRemaster consumes the two phase families, leaving the overall family
// to feed the existing Terrain row. Publish the label and percent together so
// the renderer never combines a new phase with the previous phase's percent.
func (l *loadingState) reportRemaster(family string, percent int) bool {
	p := remasterProgress{percent: max(0, min(percent, 100))}
	switch family {
	case familyDetailArt:
		if percent >= 100 {
			l.remaster.Store(nil)
		} else if l.remaster.Load() == nil {
			l.remaster.Store(&remasterProgress{label: "Preparing remastered art"})
		}
		return false
	case familyDetailTiles:
		p.label = "Remastering map tiles"
	case familyDetailSprites:
		p.label = "Remastering sprites"
	default:
		return false
	}
	if previous := l.remaster.Load(); previous == nil || *previous != p {
		l.remaster.Store(&p)
	}
	return true
}

func (g *gameShell) drawRemasterProgress(c *client.Client) {
	l := g.loading
	p := l.remaster.Load()
	if p == nil || l.remasterElapsed < remasterPopupDelay {
		return
	}
	// Size the new layout around the original, unscaled loading grille and
	// the installed font. The message window supplies its panel art only.
	barW, barH := 351, retailLoadBarBottom+1
	bar := g.retailLightBarFrame()
	if bar != nil {
		barW, barH = int(bar.Width), int(bar.Height)
	}
	lineH := g.retailTextHeight() + 6
	const margin = 24
	w, h := barW+2*margin, 2*margin+4*lineH+barH
	screenW, screenH := c.Size()
	x, y := (screenW-w)/2, (screenH-h)/2
	window := gui.Window{Rect: gui.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)}}
	var page *formats.GAF
	if message := g.assets.message; message != nil && message.window != nil {
		window.Header = message.window.Header
		page = message.art
	}
	drawWindowPanel(c, &window, page, g.assets.common, g.guiColor)
	text := func(value string, rowY int, color byte) {
		g.drawRetailString(c, value, x+(w-g.retailTextWidth(value))/2, rowY, -1, g.guiColor(color))
	}
	text("Preparing enhanced graphics", y+margin, retailLoadTitleColor)
	// Dots and elapsed seconds keep moving during example preparation and
	// cache writes, which have no completed tile/frame count to report.
	dots := strings.Repeat(".", 1+int(l.remasterElapsed*2)%3)
	text(p.label+dots, y+margin+lineH, retailLoadTitleColor)
	barY := y + margin + 2*lineH
	c.UIFillRect(x+margin, barY, barW, barH, g.guiColor(0))
	fill := g.guiColor(retailLoadWorkingColor)
	if p.percent == 100 {
		fill = g.guiColor(retailLoadDoneColor)
	}
	c.UIFillRect(x+margin, barY, p.percent*(barW-1)/100+1, barH, fill)
	if bar != nil {
		blitRetailFrame(c, bar, x+margin, barY)
	}
	text(fmt.Sprintf("%d%%   %ds elapsed", p.percent, int(l.remasterElapsed)), barY+barH+6, retailLoadTitleColor)
	text("First-time preparation may take a moment.", barY+barH+6+lineH, retailLoadTitleColor)
}
