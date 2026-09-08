package ui

import "github.com/nanolathe/nanolathe/internal/gui"

// ButtonQuickKeyAction checks one matching button accelerator without taking
// pointer capture [07 R-WGT-01 §3]. The caller supplies its current capture
// index (-1 when free); the frontend and battle options have separate pumps.
// Key matching and window quickkey-enable policy remain with the caller.
func (p *Panel) ButtonQuickKeyAction(index, capture int, alt bool) Action {
	none := Action{Kind: ActionNone, Index: -1}
	if p == nil || p.Window == nil || index <= 0 || index >= len(p.Window.Gadgets) || !p.ActiveAt(index) {
		return none
	}
	g := p.Window.Gadgets[index]
	if g.Kind != gui.KindButton || g.QuickKey == 0 || g.GrayedOut&1 != 0 || capture == index {
		return none
	}
	// A different non-text capture does not block the accelerator. Only a
	// captured editor requires Alt; the captured button itself stays on its
	// pointer path rather than also firing from this key [07 R-WGT-01 §3].
	if capture >= 0 && capture < len(p.Window.Gadgets) && p.Window.Gadgets[capture].Kind == gui.KindTextBox && !alt {
		return none
	}
	return Action{Kind: ActionActivate, Index: index, Gadget: g.Name}
}
