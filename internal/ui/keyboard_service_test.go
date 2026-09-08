package ui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

func keyboardFrame(token input.Token) WidgetFrame {
	return WidgetFrame{TokenMode: true, KeyNavigation: true, Tokens: []input.Token{token}}
}

func TestKeyboardServiceDefaultSpaceAndEscapeKeepIndexedIdentity(t *testing.T) {
	w := &gui.Window{Focus: 2, Header: gui.Header{CrDefault: "DEFAULT", EscDefault: "CANCEL"}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "DEFAULT", Active: 1},
		{Kind: gui.KindButton, Name: "FOCUS", Active: 1},
		{Kind: gui.KindButton, Name: "CANCEL", Active: 1},
	}}
	p := NewPanel(w)
	for _, tc := range []struct {
		name  string
		token input.Token
		want  int
	}{
		{"enter uses usable default", input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}, 1},
		{"space uses focus", input.Token{Kind: input.TokenEdit, Key: input.KeySpace}, 2},
		{"escape uses its own default", input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.SetFocus(2)
			r := p.ServiceFrame(keyboardFrame(tc.token), WidgetHooks{})
			if !r.Fired || r.FiredIndex != tc.want || r.ConsumedTokens != 1 || p.Focused() != tc.want {
				t.Fatalf("result=%+v focus=%d", r, p.Focused())
			}
		})
	}
}

func TestKeyboardEnterFallbackMutatesSpaceRuleOnly(t *testing.T) {
	w := &gui.Window{Focus: 2, Header: gui.Header{CrDefault: "DEFAULT"}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "DEFAULT", Active: 1, GrayedOut: 1, Stages: 3},
		{Kind: gui.KindButton, Name: "FOCUS", Active: 1, Attribs: 0x10, Stages: 3},
	}}
	p := NewPanel(w)
	p.SetFocus(2)
	r := p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}), WidgetHooks{})
	if !r.Fired || r.FiredIndex != 2 || p.StatusAt(2) != 1 {
		t.Fatalf("unusable default result=%+v radio=%d", r, p.StatusAt(2))
	}
	w.Gadgets[1].GrayedOut = 0
	p.SetStatusAt(2, 0)
	p.SetStageAt(1, 1)
	r = p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}), WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 || p.StatusAt(2) != 0 || p.StageAt(1) != 1 || r.StageAdvanced {
		t.Fatalf("usable default result=%+v radio=%d stage=%d", r, p.StatusAt(2), p.StageAt(1))
	}
}

func TestKeyboardQuickKeyMutatesRadioAndKeepsIndexedFirstFire(t *testing.T) {
	p := NewPanel(&gui.Window{Rect: gui.Rect{W: 100, H: 20}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "POINTER", Active: 1, QuickKey: 'X', Attribs: 0x100, Rect: gui.Rect{X: 0, Y: 0, W: 8, H: 8}},
		{Kind: gui.KindButton, Name: "RADIO", Active: 1, QuickKey: 'R', Attribs: 0x10},
	}})
	r := p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'r'}}}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 2 || r.ConsumedTokens != 1 || p.StatusAt(2) != 1 {
		t.Fatalf("quickkey result=%+v radio=%d", r, p.StatusAt(2))
	}
	// A lower-index pointer fire wins before the indexed walk reaches a later
	// matching accelerator [07 R-WGT-01 §§1,3].
	p.ResetPress()
	r = p.ServiceFrame(WidgetFrame{PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 1, Y: 1}}, HeldButtons: 1, Tokens: []input.Token{{Kind: input.TokenText, Rune: 'r'}}}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 || r.ConsumedTokens != 0 {
		t.Fatalf("pointer-first result=%+v", r)
	}
}

func TestKeyboardAltQuickKeyPrecedesCapturedEditor(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, MaxChars: 8},
		{Kind: gui.KindButton, Name: "ACTION", Active: 1, QuickKey: 'A'},
	}})
	p.FocusEditor(1)
	r := p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'a'}}, AltHeld: true}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 2 || r.ConsumedTokens != 1 || p.TextAt(1) != "" {
		t.Fatalf("Alt quickkey result=%+v text=%q", r, p.TextAt(1))
	}
}

func TestKeyboardAltEditorLeavesQuickKeyInIndexedPointerOrder(t *testing.T) {
	p := NewPanel(&gui.Window{Rect: gui.Rect{W: 100, H: 20}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, Attribs: 1, MaxChars: 8, Rect: gui.Rect{W: 20}},
		{Kind: gui.KindButton, Name: "POINTER", Active: 1, Attribs: 0x100, Rect: gui.Rect{X: 30, W: 8, H: 8}},
		{Kind: gui.KindButton, Name: "ACTION", Active: 1, QuickKey: 'A'},
	}})
	p.FocusEditor(1)
	r := p.ServiceFrame(WidgetFrame{AltHeld: true, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 31, Y: 1}}, Tokens: []input.Token{{Kind: input.TokenText, Rune: 'a'}}}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 2 || r.ConsumedTokens != 0 || p.TextAt(1) != "" {
		t.Fatalf("pointer ordering result=%+v text=%q", r, p.TextAt(1))
	}
}

func TestKeyboardLabelQuickKeyUsesLinkAndCaptureRules(t *testing.T) {
	newPanel := func(target gui.Gadget) *Panel {
		return NewPanel(&gui.Window{Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel},
			{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, Attribs: 1, MaxChars: 8, Rect: gui.Rect{W: 20}},
			{Kind: gui.KindLabel, Name: "LABEL", Active: 1, QuickKey: 'L', Link: "TARGET"},
			target,
		}})
	}
	p := newPanel(gui.Gadget{Kind: gui.KindTextBox, Name: "TARGET", Active: 1, MaxChars: 8})
	p.FocusEditor(1)
	r := p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'l'}}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.TextAt(1) != "l" {
		t.Fatalf("editor blocked label result=%+v text=%q", r, p.TextAt(1))
	}
	p.FocusEditor(1)
	r = p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'l'}}, AltHeld: true}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.Focused() != 3 || !p.EditorCaptured() {
		t.Fatalf("Alt label focus result=%+v focus=%d capture=%t", r, p.Focused(), p.EditorCaptured())
	}
	p = newPanel(gui.Gadget{Kind: gui.KindScrollBar, Name: "TARGET", Active: 1, Attribs: 0x10})
	r = p.ServiceFrame(WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'l'}}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 0 || p.Focused() == 3 {
		t.Fatalf("locked slider result=%+v focus=%d", r, p.Focused())
	}
}

func TestMatrixQuickKeyMatchesExtendedByteWithoutUnicodeFold(t *testing.T) {
	if !matrixQuickKeyMatches(rune(0xE9), 0xE9) || matrixQuickKeyMatches(rune(0xE9), 0xC9) || matrixQuickKeyMatches(rune(0xE9), 0) || matrixQuickKeyMatches(rune(0x100), 0xE9) {
		t.Fatal("extended quickkey byte matching lost byte identity")
	}
}

func TestKeyboardServiceTraversalAndDirectionalKinds(t *testing.T) {
	p := NewPanel(&gui.Window{Focus: 1, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "LEFT", Active: 1, Rect: gui.Rect{X: 0, Y: 0}},
		{Kind: gui.KindScrollBar, Name: "SLIDER", Active: 1, Assoc: 1, Range: 3, Rect: gui.Rect{X: 20, Y: 0, W: 20, H: 4}},
		{Kind: gui.KindListBox, Name: "LIST", Active: 1, Assoc: 2, Attribs: 0x10, ItemHeight: 10, Rect: gui.Rect{X: 40, Y: 0, W: 20, H: 22}},
	}})
	p.SetListAt(3, []string{"0", "1", "2", "3", "4", "5"})
	p.SetListMaxTopAt(3, 2)
	p.SetFocus(1)

	// Tab follows strict reading order and is consumed without firing.
	r := p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyTab}), WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 1 || p.Focused() != 2 {
		t.Fatalf("Tab result=%+v focus=%d", r, p.Focused())
	}
	p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyRight}), WidgetHooks{})
	p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyRight}), WidgetHooks{})
	if got := p.SliderKnobAt(2); got != 2 {
		t.Fatalf("horizontal knob=%d, want 2", got)
	}
	p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyRight}), WidgetHooks{})
	if got := p.SliderKnobAt(2); got != 2 {
		t.Fatalf("horizontal endpoint knob=%d, want clamp 2", got)
	}

	// Keyboard list rows use metric+1 (four visible rows), not authored
	// itemheight (two pointer rows), so item 4 scrolls top to one.
	p.SetFocus(3)
	p.ListAt(3).SetSelected(3)
	p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyDown}), WidgetHooks{Metric: func(int) int { return 4 }})
	if l := p.ListAt(3); l.Selected() != 4 || l.Top() != 1 {
		t.Fatalf("keyboard list selected/top=%d/%d, want 4/1", l.Selected(), l.Top())
	}
	for range 8 {
		p.ServiceFrame(keyboardFrame(input.Token{Kind: input.TokenEdit, Key: input.KeyDown}), WidgetHooks{Metric: func(int) int { return 4 }})
	}
	if l := p.ListAt(3); l.Selected() != 5 || l.Top() != 2 {
		t.Fatalf("keyboard list endpoint selected/top=%d/%d, want 5/2", l.Selected(), l.Top())
	}
}

func TestKeyboardServiceEditorRetainsCaptureForTextAfterEnter(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindTextBox, Name: "EDIT", Active: 1, MaxChars: 8, Rect: gui.Rect{W: 20}},
	}})
	p.FocusEditor(1)
	r := p.ServiceFrame(WidgetFrame{TokenMode: true, KeyNavigation: true, Tokens: []input.Token{
		{Kind: input.TokenText, Rune: 'a'},
		{Kind: input.TokenEdit, Key: input.KeyEnter},
		{Kind: input.TokenText, Rune: 'b'},
	}}, WidgetHooks{})
	if r.Fired || r.ConsumedTokens != 3 || p.TextAt(1) != "ab" || !p.EditorCaptured() {
		t.Fatalf("editor result=%+v text=%q captured=%t", r, p.TextAt(1), p.EditorCaptured())
	}
}
