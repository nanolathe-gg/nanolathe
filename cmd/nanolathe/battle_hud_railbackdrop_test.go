package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// Stock rail pages are authored at (0,128), 128×352, so they end exactly at
// PANELSIDE's 480th row and keep the retail black band [07 R-HUD-05]; only a
// page whose drawn control ends below the art stretches the backdrop
// (DESIGN_INTERFACE_HUD_INPUT §3.3 "Rail backdrop"). Inactive records and the
// header do not count.
func TestRailWindowReachesBelowArt(t *testing.T) {
	page := func(g gui.Gadget) *gui.Window {
		return &gui.Window{OriginY: 128, Rect: gui.Rect{W: 128, H: 352}, Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel, Active: 1, Rect: gui.Rect{Y: 128, W: 128, H: 900}}, g,
		}}
	}
	stock := page(gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 64, Y: 321, W: 55, H: 31}})
	if railWindowReachesBelow(stock, 480) {
		t.Fatal("a control ending on row 480 must keep the retail band")
	}
	fitted := page(gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 64, Y: 322, W: 55, H: 31}})
	if !railWindowReachesBelow(fitted, 480) {
		t.Fatal("a control ending below row 480 must stretch the backdrop")
	}
	inactive := page(gui.Gadget{Kind: gui.KindButton, Rect: gui.Rect{Y: 600, W: 55, H: 31}})
	if railWindowReachesBelow(inactive, 480) {
		t.Fatal("an inactive record draws nothing and must not stretch the backdrop")
	}
}
