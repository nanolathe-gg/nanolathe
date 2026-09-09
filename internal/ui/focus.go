package ui

import "github.com/nanolathe-gg/nanolathe/internal/gui"

// FocusDirection is the supported reading-order traversal direction.
type FocusDirection uint8

const (
	FocusForward FocusDirection = iota
	FocusBackward
	FocusUp
	FocusDown
)

// focusWrap is the single traversal adjustment [07 R-WGT-01 §2].
const focusWrap int32 = 25000000

// MoveFocus applies the window focus ordering. It reports a supported attempt,
// including one that retains the current focus [07 R-WGT-01 §2].
func (p *Panel) MoveFocus(direction FocusDirection) bool {
	if p == nil || p.Window == nil || p.focus < 0 || p.focus >= len(p.Window.Gadgets) {
		return false
	}
	if direction != FocusForward && direction != FocusBackward && direction != FocusUp && direction != FocusDown {
		return false
	}
	// TODO(question): windows with more than 49 controls depend on temporary
	// canonical-coordinate storage; a bounded initialization/lifetime trace or
	// manual custom-window observation would settle the remaining entries.
	if len(p.Window.Gadgets)-1 > 49 {
		return false
	}

	canonicalX := focusCanonicalX(p.Window)
	current := focusKey(p.Window.Gadgets[p.focus], canonicalX[p.focus], direction)
	winner := p.focus

	switch direction {
	case FocusBackward, FocusUp:
		best := current - focusWrap
		for i, g := range p.Window.Gadgets {
			if !p.focusCandidate(i, g) {
				continue
			}
			key := focusKey(g, canonicalX[i], direction)
			if key >= current {
				key -= focusWrap
			}
			if key > best {
				best, winner = key, i
			}
		}
	case FocusForward, FocusDown:
		best := current + focusWrap
		for i, g := range p.Window.Gadgets {
			if !p.focusCandidate(i, g) {
				continue
			}
			key := focusKey(g, canonicalX[i], direction)
			if key <= current {
				key += focusWrap
			}
			if key < best {
				best, winner = key, i
			}
		}
	}

	// Traversal frees any current capture before it installs its winner, even
	// when the strict boundary leaves the focus unchanged [07 R-WGT-01 §2].
	p.ResetPress()
	p.drag = scrollDrag{}
	p.editor.captured = -1
	p.SetFocus(winner)
	return true
}

func focusCanonicalX(window *gui.Window) []int32 {
	canonical := make([]int32, len(window.Gadgets))
	// Slot zero stays at its initialized sentinel; canonical coordinates belong
	// only to controls 1..N [07 R-WGT-01 §2].
	for i := 1; i < len(window.Gadgets); i++ {
		g := window.Gadgets[i]
		canonical[i] = g.Rect.X
		for prior := 1; prior < i; prior++ {
			if canonical[prior] == 0 {
				break
			}
			delta := g.Rect.X - canonical[prior]
			if delta > -10 && delta < 10 {
				canonical[i] = canonical[prior]
				break
			}
		}
	}
	return canonical
}

func focusKey(g gui.Gadget, canonicalX int32, direction FocusDirection) int32 {
	if direction == FocusUp || direction == FocusDown {
		return g.Rect.Y + canonicalX*5000
	}
	return g.Rect.X + g.Rect.Y*5000
}

func (p *Panel) focusCandidate(index int, g gui.Gadget) bool {
	if index == 0 || !p.ActiveAt(index) || g.Attribs&0x400 != 0 {
		return false
	}
	switch g.Kind {
	case gui.KindButton:
		return g.GrayedOut&1 == 0
	case gui.KindListBox:
		return g.Attribs&0x100 == 0
	case gui.KindTextBox:
		return true
	case gui.KindScrollBar:
		return g.GrayedOut == 0 && g.Rect.W >= g.Rect.H
	case gui.KindSurface:
		return true
	}
	return false
}
