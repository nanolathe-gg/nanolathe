package ui

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func editorPanel() *Panel {
	return NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindTextBox, Name: "GAMENAME", Active: 1, MaxChars: 12, Rect: gui.Rect{W: 40}},
	}})
}

func TestEditorEditsBytesAndFiresThroughCapture(t *testing.T) {
	p := editorPanel()
	if !p.FocusEditor(1) || p.Focused() != 1 || !p.EditorCaptured() {
		t.Fatalf("focus editor failed: focus=%d captured=%v", p.Focused(), p.EditorCaptured())
	}
	p.ApplyEditorTokens([]input.Token{{Kind: input.TokenText, Rune: 'a'}, {Kind: input.TokenText, Rune: 'b'}, {Kind: input.TokenEdit, Key: input.KeyLeft}, {Kind: input.TokenText, Rune: 'X'}}, func(s string) int { return len(s) })
	if got := p.TextOf("GAMENAME"); got != "aXb" || p.EditorCaret() != 2 {
		t.Fatalf("edited text/caret = %q/%d, want aXb/2 [07 R-WGT-01 §6]", got, p.EditorCaret())
	}
	p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyDelete}, {Kind: input.TokenEdit, Key: input.KeyBackspace}}, func(s string) int { return len(s) })
	if got := p.TextOf("GAMENAME"); got != "a" || p.EditorCaret() != 1 {
		t.Fatalf("delete/backspace = %q/%d, want a/1", got, p.EditorCaret())
	}
	result := p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyEnter}}, nil)
	if result.Action.Kind != ActionActivate || result.Action.Gadget != "GAMENAME" || p.EditorCaptured() {
		t.Fatalf("enter result=%+v captured=%v [07 R-WGT-01 §6]", result, p.EditorCaptured())
	}
}

func TestEditorEscapeCapacityFilterAndTranslatedBoundary(t *testing.T) {
	p := editorPanel()
	p.Window.Gadgets[1].MaxChars = 3
	p.Window.Gadgets[1].Attribs = 0x02
	p.FocusEditor(1)
	p.ApplyEditorTokens([]input.Token{{Kind: input.TokenText, Rune: 'A'}, {Kind: input.TokenText, Rune: '-'}, {Kind: input.TokenText, Rune: '_'}, {Kind: input.TokenText, Rune: 'B'}, {Kind: input.TokenText, Rune: 'C'}, {Kind: input.TokenText, Rune: 'é'}}, func(s string) int { return len(s) })
	if got := p.TextOf("GAMENAME"); got != "A_B" {
		t.Fatalf("filter/capacity/encoding boundary = %q, want A_B [07 R-WGT-01 §12]", got)
	}
	result := p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyEscape}}, nil)
	if result.Action.Kind != ActionActivate || p.TextOf("GAMENAME") != "" || p.EditorCaptured() {
		t.Fatalf("escape result/text/capture = %+v/%q/%v [07 R-WGT-01 §6]", result, p.TextOf("GAMENAME"), p.EditorCaptured())
	}
}

func TestEditorUsesTheLastConsumedTokenAndStopsAtEscape(t *testing.T) {
	p := editorPanel()
	p.FocusEditor(1)
	result := p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyEnter}, {Kind: input.TokenText, Rune: 'a'}}, nil)
	if result.Consumed != 2 || result.Action.Kind != ActionNone || !p.EditorCaptured() || p.TextOf("GAMENAME") != "a" {
		t.Fatalf("Enter followed by text = %+v capture=%t text=%q, want last-token text result", result, p.EditorCaptured(), p.TextOf("GAMENAME"))
	}
	result = p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyEscape}, {Kind: input.TokenText, Rune: 'b'}}, nil)
	if result.Consumed != 1 || result.Action.Kind != ActionActivate || p.TextOf("GAMENAME") != "" || p.EditorCaptured() {
		t.Fatalf("Escape stop result=%+v capture=%t text=%q", result, p.EditorCaptured(), p.TextOf("GAMENAME"))
	}
}

func TestSetFocusAndLabelLinkSetUpTextEditor(t *testing.T) {
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindTextBox, Name: "GAMENAME", Active: 1, MaxChars: 4, Rect: gui.Rect{W: 20}},
		{Kind: gui.KindLabel, Name: "NAME_LABEL", Link: "GAMENAME", Active: 1, Rect: gui.Rect{X: 30, W: 20, H: 10}},
	}}
	p := NewPanel(window)
	p.SetText("GAMENAME", "name")
	p.SetFocus(1)
	if !p.EditorCaptured() || p.EditorCaret() != 4 {
		t.Fatalf("focus setup capture/caret=%t/%d", p.EditorCaptured(), p.EditorCaret())
	}
	p.SetFocus(2)
	p.SetPressed(2)
	if action := p.ReleaseAction(31, 1); action.Kind != ActionNone || !p.EditorCaptured() || p.Focused() != 1 {
		t.Fatalf("label link action/capture/focus=%+v/%t/%d", action, p.EditorCaptured(), p.Focused())
	}
}

func TestEditorRejectsTextThatDoesNotFitTheControl(t *testing.T) {
	p := editorPanel()
	p.Window.Gadgets[1].Rect.W = 6
	p.FocusEditor(1)
	p.ApplyEditorTokens([]input.Token{{Kind: input.TokenText, Rune: 'a'}, {Kind: input.TokenText, Rune: 'b'}, {Kind: input.TokenText, Rune: 'c'}}, func(s string) int { return len(s) })
	if got := p.TextOf("GAMENAME"); got != "ab" {
		t.Fatalf("width-limited text = %q, want ab [07 R-WGT-01 §12]", got)
	}
}

// Captured editing addresses the record; a named mutation still finds the
// first duplicate and must not move another record's caret [07 R-FE-02 §5].
func TestEditorDuplicateNamesKeepTextAndCaretIndependent(t *testing.T) {
	w := editorPanel().Window
	w.Gadgets = append(w.Gadgets, w.Gadgets[1])
	p := NewPanel(w)
	p.SetTextAt(2, "a")
	if !p.FocusEditor(2) {
		t.Fatal("duplicate editor did not capture")
	}
	p.SetText("GAMENAME", "first")
	if p.EditorCaret() != 1 {
		t.Fatal("named first-record write moved duplicate caret")
	}
	p.ApplyEditorTokens([]input.Token{{Kind: input.TokenText, Rune: 'b'}}, nil)
	if p.TextAt(1) != "first" || p.TextAt(2) != "ab" || p.EditorCaret() != 2 {
		t.Fatal("captured editor shared duplicate text")
	}
}

func TestEditorPasteReplacementBoundsAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		clipboard input.ClipboardText
		maximum   int16
		width     int32
		want      string
	}{
		{"replace-filter-bypass", input.ClipboardText{Text: "a-+\t", Available: true}, 12, 40, "a-+\t"},
		{"empty-clears", input.ClipboardText{Available: true}, 12, 40, ""},
		{"failed-preserves", input.ClipboardText{}, 12, 40, "old"},
		{"capacity-reserves-terminator", input.ClipboardText{Text: "abcdef", Available: true}, 4, 40, "abc"},
		{"full-width-and-strict-comparison", input.ClipboardText{Text: "abcde", Available: true}, 12, 8, "abcd"},
		{"nul-terminates", input.ClipboardText{Text: "ab\x00cd", Available: true}, 12, 40, "ab"},
		{"one-too-wide-glyph", input.ClipboardText{Text: "abc", Available: true}, 12, 1, "a"},
		{"zero-capacity", input.ClipboardText{Text: "abc", Available: true}, 0, 40, ""},
		{"buffer-limit", input.ClipboardText{Text: strings.Repeat("x", 140), Available: true}, 200, 400, strings.Repeat("x", 127)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := editorPanel()
			p.Window.Gadgets[1].Attribs = 0x02
			p.Window.Gadgets[1].MaxChars = tc.maximum
			p.Window.Gadgets[1].Rect.W = tc.width
			p.SetTextAt(1, "old")
			p.FocusEditor(1)
			p.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyHome}, {Kind: input.TokenEdit, Key: input.KeyV, Ctrl: true, Clipboard: tc.clipboard}}, func(s string) int { return len(s) * 2 })
			wantCaret := 0 // Home precedes paste; replacement retains its index.
			if p.TextAt(1) != tc.want || p.EditorCaret() != wantCaret || !p.EditorCaptured() {
				t.Fatalf("paste text=%q caret=%d captured=%t; want %q/%d/true [07 §2]", p.TextAt(1), p.EditorCaret(), p.EditorCaptured(), tc.want, wantCaret)
			}
		})
	}
}

func TestEditorPasteRetainsCaretUntilFollowingEditBoundsIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		text      string
		available bool
		tail      []input.Token
		want      string
		caret     int
	}{
		{"longer-keeps-middle", "replacement", true, nil, "replacement", 3},
		{"shorter-keeps-stale", "x", true, nil, "x", 3},
		{"empty-keeps-stale", "", true, nil, "", 3},
		{"failed-keeps-original", "", false, nil, "old", 3},
		{"shorter-then-type", "x", true, []input.Token{{Kind: input.TokenText, Rune: '!'}}, "x!", 2},
		{"shorter-then-backspace", "x", true, []input.Token{{Kind: input.TokenEdit, Key: input.KeyBackspace}}, "", 0},
		{"empty-then-type", "", true, []input.Token{{Kind: input.TokenText, Rune: '!'}}, "!", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := editorPanel()
			p.SetTextAt(1, "old")
			p.FocusEditor(1)
			tokens := []input.Token{{Kind: input.TokenEdit, Key: input.KeyInsert, Clipboard: input.ClipboardText{Text: tc.text, Available: tc.available}}}
			tokens = append(tokens, tc.tail...)
			p.ApplyEditorTokens(tokens, nil)
			if p.TextAt(1) != tc.want || p.EditorCaret() != tc.caret {
				t.Fatalf("paste/edit = %q/%d, want %q/%d", p.TextAt(1), p.EditorCaret(), tc.want, tc.caret)
			}
		})
	}
}
