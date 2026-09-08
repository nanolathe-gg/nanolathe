package ui

import "github.com/nanolathe/nanolathe/internal/gui"

// DefaultKeyAction chooses Enter's usable default before its focused fallback;
// Space (enter=false) has only that fallback [07 R-WGT-01 §2]. It preserves
// record identity and does not borrow the mouse kind or surface-hotness test.
func (p *Panel) DefaultKeyAction(enter bool) Action {
	action, _ := p.defaultKeyAction(enter)
	return action
}

// defaultKeyAction also reports whether Enter selected the authored crdefault.
// The caller needs that distinction because only the focused Space fallback
// changes radio or staged state [07 R-WGT-01 §2].
func (p *Panel) defaultKeyAction(enter bool) (Action, bool) {
	none := Action{Kind: ActionNone, Index: -1}
	if p == nil || p.Window == nil || p.EditorCaptured() {
		return none, false
	}
	usable := func(index int) bool {
		if index <= 0 || index >= len(p.Window.Gadgets) || !p.ActiveAt(index) {
			return false
		}
		g := p.Window.Gadgets[index]
		// The button grey flag is specifically the low bit [07 R-WGT-01 §13].
		return g.Kind != gui.KindButton || g.GrayedOut&1 == 0
	}
	selected := -1
	if enter {
		if index := p.Window.EnterDefaultIndex(); usable(index) {
			return Action{Kind: ActionActivate, Index: index, Gadget: p.Window.Gadgets[index].Name}, true
		}
	}
	if selected < 0 && usable(p.focus) {
		switch p.Window.Gadgets[p.focus].Kind {
		case gui.KindButton, gui.KindListBox, gui.KindSurface:
			selected = p.focus
		}
	}
	if selected < 0 {
		return none, false
	}
	return Action{Kind: ActionActivate, Index: selected, Gadget: p.Window.Gadgets[selected].Name}, false
}
