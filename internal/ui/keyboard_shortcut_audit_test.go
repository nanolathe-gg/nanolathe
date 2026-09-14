package ui

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// A linked-label accelerator that only focuses a non-button consumes its key
// but leaves the remaining indexed visits live [07 R-WGT-01 §1][07 R-WGT-01 §7].
func TestLabelShortcutFocusContinuesIndexedService(t *testing.T) {
	for _, tokenMode := range []bool{false, true} {
		for _, pointerFire := range []bool{false, true} {
			p := NewPanel(&gui.Window{Rect: gui.Rect{W: 100, H: 30}, Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel},
				{Kind: gui.KindLabel, Name: "LABEL", Active: 1, QuickKey: 'L', Link: "EDIT"},
				{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, Attribs: 1, MaxChars: 10, Rect: gui.Rect{X: 20, W: 20, H: 10}},
				{Kind: gui.KindSurface, Name: "SURFACE", Active: 1},
				{Kind: gui.KindButton, Name: "LATER", Active: 1, QuickKey: 'L', Attribs: 0x100, Rect: gui.Rect{X: 60, W: 10, H: 10}},
			}})
			p.SetFocus(4)
			frame := WidgetFrame{TokenMode: tokenMode, KeyNavigation: true, Tokens: []input.Token{
				{Kind: input.TokenText, Rune: 'l'},
				{Kind: input.TokenText, Rune: 'x'},
			}}
			if pointerFire {
				frame.PointerX, frame.PointerY, frame.HeldButtons = 61, 1, 1
				frame.PointerEvents = []input.PointerEvent{{Kind: input.LeftDown, X: 61, Y: 1}}
			}
			surfaceVisited := false
			result := p.ServiceFrame(frame, WidgetHooks{Surface: func(index int) {
				if index == 3 {
					surfaceVisited = true
				}
			}})
			if !surfaceVisited || result.ConsumedTokens != 1 || p.TextAt(2) != "" {
				t.Fatalf("tokenMode=%t pointerFire=%t: surface=%t result=%+v text=%q", tokenMode, pointerFire, surfaceVisited, result, p.TextAt(2))
			}
			if pointerFire {
				if !result.Fired || result.FiredIndex != 4 || result.FiredButton != 1 {
					t.Fatalf("tokenMode=%t: later pointer result=%+v", tokenMode, result)
				}
			} else if result.Fired || p.Focused() != 2 || !p.EditorCaptured() {
				t.Fatalf("tokenMode=%t: focus-only result=%+v focus=%d captured=%t", tokenMode, result, p.Focused(), p.EditorCaptured())
			}
		}
	}
}
