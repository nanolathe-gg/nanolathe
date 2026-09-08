package ui

import (
	"unicode"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

// editorState is the mutable state of the one captured kind-3 gadget in a
// panel. Text remains in the captured gadget state so screen bindings retain
// one source of truth [07 R-WGT-01 §6].
type editorState struct {
	captured int
	caret    int
}

// EditorResult identifies the token prefix a captured editor consumed and the
// action derived from its final token [07 R-WGT-01 §12].
type EditorResult struct {
	Action   Action
	Consumed int
}

// FocusEditor gives an active kind-3 gadget the panel's sole focus and capture.
// Its re-lay puts the caret at the end of text within the authored byte bound,
// or empties an overlong value [07 R-WGT-01 §6].
func (p *Panel) FocusEditor(index int) bool {
	if p == nil || p.Window == nil || index < 0 || index >= len(p.Window.Gadgets) {
		return false
	}
	gadget := p.Window.Gadgets[index]
	if gadget.Kind != gui.KindTextBox || !p.ActiveAt(index) || gadget.GrayedOut != 0 {
		return false
	}
	text := p.TextAt(index)
	if len(text) > editorCapacity(gadget) {
		text = ""
		p.states[index].text = text
	}
	p.focus = index
	p.editor.captured = index
	p.editor.caret = len(text)
	return true
}

// EditorCaptured reports whether a kind-3 editor owns the panel capture.
func (p *Panel) EditorCaptured() bool {
	return p != nil && p.editor.captured >= 0
}

// EditorCaret reports the captured editor's byte insertion index, or zero when
// no editor is captured.
func (p *Panel) EditorCaret() int {
	if p == nil || p.editor.captured < 0 {
		return 0
	}
	return p.editor.caret
}

// EditorIndex reports the captured kind-3 gadget index, or -1 when none.
func (p *Panel) EditorIndex() int {
	if p == nil {
		return -1
	}
	return p.editor.captured
}

// ApplyEditorTokens consumes a supplied ordered token prefix into the captured
// kind-3 editor. measure is the selected font's byte-string width. Escape
// stops the loop; the final consumed token determines whether Enter or Escape
// frees capture and fires the input [07 R-WGT-01 §6][07 R-WGT-01 §12].
func (p *Panel) ApplyEditorTokens(tokens []input.Token, measure func(string) int) EditorResult {
	if p == nil || p.Window == nil || p.editor.captured < 0 || p.editor.captured >= len(p.Window.Gadgets) {
		return EditorResult{Action: Action{Kind: ActionNone, Index: -1}}
	}
	index := p.editor.captured
	gadget := p.Window.Gadgets[index]
	if gadget.Kind != gui.KindTextBox {
		p.editor.captured = -1
		return EditorResult{Action: Action{Kind: ActionNone, Index: -1}}
	}
	if measure == nil {
		measure = func(s string) int { return len(s) }
	}
	text := p.TextAt(index)
	if p.editor.caret < 0 {
		p.editor.caret = 0
	}
	if p.editor.caret > len(text) {
		p.editor.caret = len(text)
	}
	result := EditorResult{Action: Action{Kind: ActionNone, Index: -1}}
	for i, token := range tokens {
		result.Consumed = i + 1
		if token.Kind == input.TokenEdit {
			switch token.Key {
			case input.KeyEscape:
				text = ""
				p.editor.caret = 0
				p.states[index].text = text
			case input.KeyBackspace:
				if p.editor.caret > 0 {
					text = text[:p.editor.caret-1] + text[p.editor.caret:]
					p.editor.caret--
				}
			case input.KeyDelete:
				if p.editor.caret < len(text) {
					text = text[:p.editor.caret] + text[p.editor.caret+1:]
				}
			case input.KeyHome:
				p.editor.caret = 0
			case input.KeyEnd:
				p.editor.caret = len(text)
			case input.KeyLeft:
				if p.editor.caret > 0 {
					p.editor.caret--
				}
			case input.KeyRight:
				if p.editor.caret < len(text) {
					p.editor.caret++
				}
			}
		} else if token.Kind == input.TokenText {
			// Ebiten gives locale-translated Unicode. Retail admits one byte. No
			// Unicode-to-codepage conversion is established, so only the traced
			// ASCII byte domain crosses this boundary [07 §2][07 R-WGT-01 §12].
			if token.Rune < 0x20 || token.Rune > 0x7f {
				continue // TODO(question): establish the retail codepage/IME mapping.
			}
			ch := byte(token.Rune)
			if len(text) >= editorCapacity(gadget) || !editorAdmits(gadget, ch) {
				continue
			}
			next := text[:p.editor.caret] + string(ch) + text[p.editor.caret:]
			if measure(next) > int(gadget.Rect.W)-4 {
				continue
			}
			text = next
			p.editor.caret++
		}
		p.states[index].text = text
		if token.Kind == input.TokenEdit && token.Key == input.KeyEscape {
			break
		}
	}
	if result.Consumed != 0 {
		last := tokens[result.Consumed-1]
		if last.Kind == input.TokenEdit && (last.Key == input.KeyEnter || last.Key == input.KeyEscape) {
			p.editor.captured = -1
			result.Action = Action{Kind: ActionActivate, Gadget: gadget.Name, Index: index}
		}
	}
	return result
}

func editorCapacity(gadget gui.Gadget) int {
	if gadget.MaxChars < 0 {
		return 0
	}
	return int(gadget.MaxChars)
}

func editorAdmits(gadget gui.Gadget, ch byte) bool {
	if gadget.Attribs&0x02 == 0 {
		return true
	}
	return unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == ' ' || ch == '_' || ch == '\''
}
