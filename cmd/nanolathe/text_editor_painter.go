package main

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// drawTextEditorState is the shared kind-3 geometry used by frontend and
// battle child windows. Each caller supplies its own clipped text and fill
// sinks, while focus, text and caret remain retained Panel state
// [07 R-WGT-01 §6].
func drawTextEditorState(c *client.Client, p *ui.Panel, index int, gad gui.Gadget, r gui.Rect, text string, measure func(string) int, lineStep int, background, caret byte, fill func(x, y, w, h int, color byte), draw func(text string, x, y, width int)) {
	if c == nil || p == nil || measure == nil || fill == nil || draw == nil {
		return
	}
	if gad.Attribs&1 != 0 {
		fill(int(r.X), int(r.Y), int(r.W), int(r.H), background)
	}
	x, y := int(r.X), int(r.Y)+3
	draw(text, x, y, int(r.W))
	if p.EditorCaptured() && p.EditorIndex() == index {
		at := p.EditorCaret()
		if at > len(text) {
			at = len(text)
		}
		fill(x+measure(text[:at]), y, 1, lineStep, caret)
	}
}
