package ui

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestKeyboardMatrixPrecedesCapturedEditor(t *testing.T) {
	w := &gui.Window{Focus: 2, Header: gui.Header{EscDefault: "CANCEL"}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "CANCEL", Active: 1, Rect: gui.Rect{X: 0}},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, MaxChars: 8, Rect: gui.Rect{X: 20, W: 20}},
		{Kind: gui.KindButton, Name: "NEXT", Active: 1, Rect: gui.Rect{X: 40}},
	}}
	p := NewPanel(w)
	p.FocusEditor(2)

	r := p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}), WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 || r.ConsumedTokens != 1 || p.EditorCaptured() {
		t.Fatalf("Escape result=%+v editor=%t", r, p.EditorCaptured())
	}

	p.FocusEditor(2)
	r = p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyTab}), WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.Focused() != 3 || p.EditorCaptured() {
		t.Fatalf("Tab result=%+v focus=%d editor=%t", r, p.Focused(), p.EditorCaptured())
	}

	p.FocusEditor(2)
	r = p.ServiceFrame(WidgetFrame{TokenMode: true, KeyNavigation: true, AltHeld: true, Tokens: []input.Token{{Kind: input.TokenEdit, Key: input.KeyTab}}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.Focused() != 3 || p.EditorCaptured() {
		t.Fatalf("Alt+Tab result=%+v focus=%d editor=%t", r, p.Focused(), p.EditorCaptured())
	}

	p.FocusEditor(2)
	r = p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyDown}), WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.Focused() != 3 || p.EditorCaptured() {
		t.Fatalf("Down result=%+v focus=%d editor=%t", r, p.Focused(), p.EditorCaptured())
	}
}

func TestKeyboardEditorIsServicedAtItsGadgetIndex(t *testing.T) {
	p := NewPanel(&gui.Window{Rect: gui.Rect{W: 80, H: 20}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "POINTER", Active: 1, Attribs: 0x100, Rect: gui.Rect{X: 0, Y: 0, W: 8, H: 8}},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, MaxChars: 8, Rect: gui.Rect{X: 20, W: 20}},
	}})
	p.FocusEditor(2)
	r := p.ServiceFrame(WidgetFrame{
		TokenMode: true, KeyNavigation: true,
		HeldButtons:   1,
		PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 1, Y: 1}},
		Tokens:        []input.Token{{Kind: input.TokenEdit, Key: input.KeyEnter}},
	}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 || r.ConsumedTokens != 1 || p.TextAt(2) != "" {
		t.Fatalf("earlier pointer result=%+v text=%q", r, p.TextAt(2))
	}
}

func TestKeyboardLeftRightLeaveFocusedEditorForItsOwnService(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, MaxChars: 8, Rect: gui.Rect{W: 20}},
	}})
	p.SetTextAt(1, "ab")
	p.FocusEditor(1)
	p.editor.caret = 1
	r := p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyLeft}), WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.EditorCaret() != 0 || !p.EditorCaptured() {
		t.Fatalf("left result=%+v caret=%d editor=%t", r, p.EditorCaret(), p.EditorCaptured())
	}
	r = p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyRight}), WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.EditorCaret() != 1 || !p.EditorCaptured() {
		t.Fatalf("right result=%+v caret=%d editor=%t", r, p.EditorCaret(), p.EditorCaptured())
	}
}

func TestZeroTokenModeDoesNotApplyMatrixDefaults(t *testing.T) {
	p := NewPanel(&gui.Window{Header: gui.Header{EscDefault: "CANCEL"}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "CANCEL", Active: 1},
	}})
	r := p.ServiceFrame(WidgetFrame{KeyNavigation: true, Tokens: []input.Token{{Kind: input.TokenEdit, Key: input.KeyEscape}}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 0 {
		t.Fatalf("zero-token result=%+v", r)
	}
}
