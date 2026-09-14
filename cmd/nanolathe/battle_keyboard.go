package main

import "github.com/nanolathe-gg/nanolathe/internal/input"

// battleTokenInput selects at most one residual keyboard event after the GUI's
// indexed peek. A claimed GUI token owns this pass; its unconsumed tail remains
// queued for the next pass [07 §2][07 R-WGT-01 §1]. Held state and pointer input
// are independent of that event and remain untouched.
func battleTokenInput(in *input.State, claimed bool) *input.State {
	if in == nil {
		return nil
	}
	if !in.ShortcutTokenMode && in.PendingTokens() == 0 && !claimed {
		return in // Legacy hand-authored frame; the device producer always enables token mode.
	}
	residual := *in
	residual.ShortcutTokenMode = true
	residual.ShortcutToken = input.Token{}
	if !claimed {
		if tokens := in.PeekTokens(); len(tokens) != 0 {
			residual.ShortcutToken = tokens[0]
			in.DiscardTokens(1)
		}
	}
	return &residual
}

// battleShortcutKeyboard decodes only the selected token into the existing
// battle shortcut vocabulary. The returned modifier state belongs to shortcut
// classification, never to pointer work or held-key services. A translated
// character retains its case even if held Shift has since changed; composed
// Ctrl is part of the event [07 §2][07 R-CAM-01 §14].
func battleShortcutKeyboard(in *input.State) *input.KeyboardState {
	if in == nil {
		return nil
	}
	if !in.ShortcutTokenMode {
		return in.Kbd
	}
	token := in.ShortcutToken
	modifiers := input.Modifiers{Ctrl: token.Ctrl}
	if in.Kbd != nil {
		modifiers.Shift = in.Kbd.HasShift()
		modifiers.Alt = in.Kbd.KeyHeld(input.KeyAlt)
	}
	key := input.KeyNone
	switch token.Kind {
	case input.TokenEdit:
		key = token.Key
	case input.TokenText:
		switch r := token.Rune; {
		case r >= '0' && r <= '9':
			key = input.Key0 + input.Key(r-'0')
		case r == 'n':
			key, modifiers.Shift = input.KeyN, false
		case r == 't' || r == 'T':
			key, modifiers.Shift = input.KeyT, r == 'T'
		case r == '!' || r == '#' || r == '*' || r == '`' || r == '~':
			key = input.KeyBackquote
		case r == '+' || r == '=':
			key = input.KeyEqual
		case r == '-' || r == '_':
			key = input.KeyMinus
		case r == ',':
			key = input.KeyComma
		case r == '.':
			key = input.KeyPeriod
		case r == '\t':
			key = input.KeyTab
		case r == '\r':
			key = input.KeyEnter
		case r == '\x1b':
			key = input.KeyEscape
		}
	}
	kbd := &input.KeyboardState{}
	kbd.SetKey(input.KeyShift, modifiers.Shift)
	kbd.SetKey(input.KeyCtrl, modifiers.Ctrl)
	kbd.SetKey(input.KeyAlt, modifiers.Alt)
	kbd.ResetEdges()
	if key != input.KeyNone {
		kbd.SetKey(key, true)
	}
	return kbd
}
