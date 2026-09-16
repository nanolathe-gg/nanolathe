package ui

import (
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"testing"
)

func TestDisabledQuickkeysPreserveTokenAndReenable(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1, Rect: gui.Rect{W: 100, H: 80}},
		{Kind: gui.KindButton, Name: "BUTTON", Active: 1, QuickKey: 'M', Rect: gui.Rect{X: 10, Y: 10, W: 30, H: 20}},
	}})
	r := p.ServiceFrame(WidgetFrame{DisableQuickKeys: true, Tokens: []input.Token{{Kind: input.TokenText, Rune: 'm'}}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 0 {
		t.Fatal("disabled accelerator claimed token")
	}
	r = p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'm'}}}, WidgetHooks{})
	if !r.Fired || r.ConsumedTokens != 1 {
		t.Fatal("reenabled accelerator lost token")
	}
}
