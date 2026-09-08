package ui

import (
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

// serviceKeyboardToken applies the one-token matrix before the pointer walk.
// It deliberately reports firing through the ordinary ServiceResult path, so
// screen callbacks retain the exact indexed gadget that the key selected
// [07 R-WGT-01 §§1-2].
func (p *Panel) serviceKeyboardToken(token input.Token, frame WidgetFrame, hooks WidgetHooks, result *ServiceResult) bool {
	key := matrixKey(token)
	switch key {
	case input.KeyTab:
		if frame.ShiftHeld {
			p.MoveFocus(FocusBackward)
		} else {
			p.MoveFocus(FocusForward)
		}
		return true
	case input.KeyEnter:
		return p.keyboardDefault(true, hooks, result)
	case input.KeyEscape:
		return p.keyboardEscape(result)
	case input.KeySpace:
		return p.keyboardDefault(false, hooks, result)
	case input.KeyLeft:
		return p.keyboardHorizontal(-1, hooks)
	case input.KeyRight:
		return p.keyboardHorizontal(1, hooks)
	case input.KeyUp:
		p.keyboardList(-1, hooks)
		return true
	case input.KeyDown:
		p.keyboardList(1, hooks)
		return true
	}
	return false
}

// keyboardQuickKeyAt is called from the indexed gadget walk. It deliberately
// stays outside the navigation and token-mode matrix gates: an accepted
// accelerator can claim a token which a peek-mode battle child otherwise
// leaves for battle hotkeys [07 R-WGT-01 §§1-3,7].
func (p *Panel) keyboardQuickKeyAt(index int, token input.Token, alt bool, result *ServiceResult) bool {
	if p == nil || p.Window == nil {
		return false
	}
	capture := p.capture
	if capture < 0 {
		capture = p.PressedIndex()
	}
	if capture < 0 {
		capture = p.RightPressedIndex()
	}
	if p.EditorCaptured() {
		capture = p.EditorIndex()
	}
	if index <= 0 || index >= len(p.Window.Gadgets) {
		return false
	}
	gadget := p.Window.Gadgets[index]
	if !matrixQuickKeyMatchesToken(token, gadget.QuickKey) {
		return false
	}
	action := p.ButtonQuickKeyAction(index, capture, alt)
	if action.Kind == ActionActivate {
		p.quickKeyMutation(action.Index)
		p.SetFocus(action.Index)
		p.fire(action.Index, 0, result)
		return true
	}
	if gadget.Kind == gui.KindLabel && gadget.Link != "" {
		if capture == index || (capture >= 0 && capture < len(p.Window.Gadgets) && p.Window.Gadgets[capture].Kind == gui.KindTextBox && !alt) {
			return false
		}
		target := p.Index(gadget.Link)
		if target < 0 || !p.ActiveAt(target) {
			return false
		}
		tg := p.Window.Gadgets[target]
		if tg.Kind == gui.KindButton && tg.GrayedOut&1 != 0 {
			return false
		}
		if tg.Kind == gui.KindScrollBar && (tg.Attribs&0x10 != 0 || tg.GrayedOut != 0) {
			return false
		}
		// Label quickkeys share the pointer link target rules: buttons fire,
		// focusable non-buttons only receive focus, and locked sliders reject.
		// The token is still claimed when that link service accepts it
		// [07 R-WGT-01 §7].
		p.serviceLinkOrFire(index, 0, result)
		return true
	}
	return false
}

// quickKeyMutation is the accelerator-specific subset of a button gesture.
// It has no press/release capture, but toggle and radio controls change state
// before their ordinary fired callback observes them [07 R-WGT-01 §3].
func (p *Panel) quickKeyMutation(index int) {
	g := p.Window.Gadgets[index]
	switch {
	case g.Attribs&0x10 != 0:
		p.SetStatusAt(index, 1)
		p.clearGroup(index)
		p.markDirty()
	case g.Attribs&0x40 != 0:
		p.SetStatusAt(index, boolToInt(p.StatusAt(index) == 0))
		p.clearGroup(index)
		p.markDirty()
	case g.Attribs&8 != 0:
		p.SetStatusAt(index, flipBinary(p.StatusAt(index)))
		p.markDirty()
	}
}

func matrixKey(token input.Token) input.Key {
	if token.Kind == input.TokenEdit {
		return token.Key
	}
	if token.Kind != input.TokenText {
		return input.KeyNone
	}
	switch token.Rune {
	case '\t':
		return input.KeyTab
	case '\r', '\n':
		return input.KeyEnter
	case '\x1b':
		return input.KeyEscape
	case ' ':
		return input.KeySpace
	}
	return input.KeyNone
}

func matrixQuickKeyMatches(r rune, quick byte) bool {
	if quick == 0 || r < 0 || r > 0xff {
		return false
	}
	if quick >= 'a' && quick <= 'z' {
		quick -= 'a' - 'A'
	}
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	return byte(r) == quick
}

// matrixQuickKeyMatchesToken compares an authored byte key with the input
// token's normalized retail byte. Character tokens carry their byte directly;
// edit keys use the established control and special-key token map [07 §2].
func matrixQuickKeyMatchesToken(token input.Token, quick byte) bool {
	if token.Kind == input.TokenText {
		return matrixQuickKeyMatches(token.Rune, quick)
	}
	if token.Kind != input.TokenEdit {
		return false
	}
	value, ok := editTokenByte(token.Key)
	return ok && matrixQuickKeyMatches(rune(value), quick)
}

func editTokenByte(key input.Key) (byte, bool) {
	switch key {
	case input.KeyBackspace:
		return '\b', true
	case input.KeyTab:
		return '\t', true
	case input.KeyEnter:
		return '\r', true
	case input.KeyEscape:
		return '\x1b', true
	case input.KeyPause:
		return 0xf8, true
	case input.KeyPrior:
		return 0xf2, true
	case input.KeyNext:
		return 0xf3, true
	case input.KeyEnd:
		return 0xf1, true
	case input.KeyHome:
		return 0xf0, true
	case input.KeyLeft:
		return 0xf4, true
	case input.KeyUp:
		return 0xf5, true
	case input.KeyRight:
		return 0xf6, true
	case input.KeyDown:
		return 0xf7, true
	case input.KeyInsert:
		return 0xee, true
	case input.KeyDelete:
		return 0xef, true
	default:
		if key >= input.KeyF1 && key <= input.KeyF12 {
			return 0xe2 + byte(key-input.KeyF1), true
		}
		return 0, false
	}
}

func (p *Panel) keyboardDefault(enter bool, hooks WidgetHooks, result *ServiceResult) bool {
	action, usedDefault := p.defaultKeyAction(enter)
	if action.Kind != ActionActivate {
		return false
	}
	p.SetFocus(action.Index)
	if (!enter || !usedDefault) && action.Index >= 0 && action.Index < len(p.Window.Gadgets) {
		g := p.Window.Gadgets[action.Index]
		if g.Kind == gui.KindButton {
			if g.Attribs&0x10 != 0 {
				p.SetStatusAt(action.Index, 1)
				p.clearGroup(action.Index)
				p.markDirty()
			} else if g.Stages > 0 {
				p.cycleButton(action.Index)
				result.StageAdvanced = true
			}
		}
	}
	return p.fire(action.Index, 0, result)
}

func (p *Panel) keyboardEscape(result *ServiceResult) bool {
	if p == nil || p.Window == nil {
		return false
	}
	index := p.Window.EscapeDefaultIndex()
	if index <= 0 || !p.ActiveAt(index) {
		return false
	}
	p.SetFocus(index)
	return p.fire(index, 0, result)
}

func (p *Panel) keyboardHorizontal(delta int, hooks WidgetHooks) bool {
	if p == nil || p.Window == nil || p.focus < 0 || p.focus >= len(p.Window.Gadgets) {
		return true
	}
	g := p.Window.Gadgets[p.focus]
	if g.Kind == gui.KindTextBox {
		return false
	}
	if g.Kind == gui.KindScrollBar && g.Rect.W > g.Rect.H && p.ActiveAt(p.focus) && g.GrayedOut == 0 && g.Attribs&0x10 == 0 {
		p.setKnob(p.focus, p.knob[p.focus]+delta, hooks)
		return true
	}
	if delta < 0 {
		p.MoveFocus(FocusBackward)
	} else {
		p.MoveFocus(FocusForward)
	}
	return true
}

func (p *Panel) keyboardList(delta int, hooks WidgetHooks) bool {
	if p == nil || p.Window == nil || p.focus < 0 || p.focus >= len(p.Window.Gadgets) {
		return false
	}
	g := p.Window.Gadgets[p.focus]
	if g.Kind != gui.KindListBox || !p.ActiveAt(p.focus) {
		if delta < 0 {
			p.MoveFocus(FocusUp)
		} else {
			p.MoveFocus(FocusDown)
		}
		return false
	}
	l := p.ListAt(p.focus)
	if l == nil || l.Len() == 0 {
		return false
	}
	next := maxInt(0, minInt(l.selected+delta, l.Len()-1))
	if next == l.selected {
		return false
	}
	if p.listHeading(p.focus, next) {
		return false
	}
	metric := 0
	if hooks.Metric != nil {
		metric = hooks.Metric(p.focus)
	}
	rows := int((p.Window.PlacedRect(p.focus).H - 2) / int32(metric+1))
	if rows > 0 {
		if next < l.top {
			l.top--
		} else if next > l.top+rows-1 {
			l.top++
		}
		l.top = maxInt(0, minInt(l.top, p.listMaxTop[p.focus]))
	}
	p.setListSelection(p.focus, next, hooks)
	return false
}
