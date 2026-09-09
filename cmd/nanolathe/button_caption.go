package main

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// Button captions use a single-line pen. The stage-count bit moves the pen;
// pointer capture and the current down word do not [03 R-FONT-01 §6].
func (g *gameShell) drawRetailButtonCaption(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect, text string, selected *formats.FNT, measure func(string) int, metric int) {
	x, y, build, centred := retailButtonCaptionPen(gad, r, measure(text), metric)
	color, shade := g.retailTextPen(p, index, gad)
	draw := func(run string, px int, color byte) {
		g.drawRetailStringSelected(c, run, px, y, int(r.W), color, shade, selected)
	}
	key := captionQuickKeyIndex(text, gad.QuickKey)
	if key < 0 || (!build && (!centred || gad.GrayedOut&1 != 0)) {
		draw(text, x, color)
		return
	}
	// Each run retains the same pen family: the GAF path ignores the installed
	// foreground, while its FNT fallback uses it [03 R-FONT-01 §6].
	prefix, letter, suffix := text[:key], text[key:key+1], text[key+1:]
	draw(prefix, x, color)
	x += measure(prefix)
	keyColor := color
	if build {
		keyColor = g.guiColor(10)
	}
	draw(letter, x, keyColor)
	if centred {
		underline := byte(2)
		if gad.Stages != 0 {
			underline = 0
		}
		c.UIFillRect(x, y+metric-1, measure(letter), 1, g.guiColor(underline))
	}
	draw(suffix, x+measure(letter), color)
}

// Caption bytes and the key use the ordinary single-player ASCII case fold;
// the first terminated-string match wins [07 R-WGT-01 §3][07 R-WGT-02 §2].
func captionQuickKeyIndex(text string, key byte) int {
	if key == 0 {
		return -1
	}
	fold := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + ('a' - 'A')
		}
		return b
	}
	for i := 0; i < len(text) && text[i] != 0; i++ {
		if fold(text[i]) == fold(key) {
			return i
		}
	}
	return -1
}
