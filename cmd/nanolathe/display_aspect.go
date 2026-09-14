package main

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// Monitor aspect labels are Nanolathe host UI (DESIGN_PRESENTATION_CLIENT §2.1).
// Keep the authored resolution read-out intact and reuse its label style in
// the free column beside it.
func addDisplayAspectLabels(window *gui.Window) {
	index := window.GadgetIndex("VIDVAL")
	if index < 0 {
		return
	}
	label := window.Gadgets[index]
	label.Rect.X += label.Rect.W + 12
	label.Rect.W = window.Rect.W - label.Rect.X
	label.Link, label.Text = "", ""
	label.Name, label.SourceName = "NASPECT", "NASPECT"
	window.Gadgets = append(window.Gadgets, label)
	label.Name, label.SourceName = "NMONITOR", "NMONITOR"
	label.Rect.Y += label.Rect.H + 4
	window.Gadgets = append(window.Gadgets, label)
}

func displayAspect(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	a, b := width, height
	for b != 0 {
		a, b = b, a%b
	}
	w, h := width/a, height/a
	if w == 8 && h == 5 {
		w, h = 16, 10
	}
	return fmt.Sprintf("%d:%d", w, h)
}
