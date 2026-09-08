package ui

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/gui"
)

func focusWindow(gadgets ...gui.Gadget) *gui.Window {
	return &gui.Window{Gadgets: append([]gui.Gadget{{Kind: gui.KindPanel, Active: 1}}, gadgets...)}
}

func TestMoveFocusReadingOrderWrapAndFirstIndexTie(t *testing.T) {
	w := focusWindow(
		gui.Gadget{Kind: gui.KindButton, Name: "CURRENT", Active: 1, Rect: gui.Rect{X: 10}},
		gui.Gadget{Kind: gui.KindButton, Name: "FIRST", Active: 1, Rect: gui.Rect{X: 20}},
		gui.Gadget{Kind: gui.KindButton, Name: "SECOND", Active: 1, Rect: gui.Rect{X: 20}},
	)
	p := NewPanel(w)
	p.SetFocus(1)
	if !p.MoveFocus(FocusForward) || p.Focused() != 2 {
		t.Fatalf("forward focus=%d, want first tied successor 2", p.Focused())
	}
	p.SetFocus(1)
	if !p.MoveFocus(FocusBackward) || p.Focused() != 2 {
		t.Fatalf("backward wrap focus=%d, want first tied predecessor 2", p.Focused())
	}
}

func TestMoveFocusUsesOneSigned32Wrap(t *testing.T) {
	w := focusWindow(
		gui.Gadget{Kind: gui.KindButton, Name: "CURRENT", Active: 1, Rect: gui.Rect{X: 0}},
		gui.Gadget{Kind: gui.KindButton, Name: "TOO_FAR", Active: 1, Rect: gui.Rect{X: 50000000}},
	)
	p := NewPanel(w)
	p.SetFocus(1)
	p.SetPressed(1)
	p.SetRightPressed(1)
	if !p.MoveFocus(FocusForward) || p.Focused() != 1 {
		t.Fatalf("one-wrap forward focus=%d, want unchanged 1", p.Focused())
	}
	if p.PressedIndex() != -1 || p.RightPressedIndex() != -1 {
		t.Fatalf("unchanged traversal retained capture left=%d right=%d", p.PressedIndex(), p.RightPressedIndex())
	}
	if got := focusKey(gui.Gadget{Rect: gui.Rect{X: 1, Y: 429497}}, 0, FocusForward); got != -2147482295 {
		t.Fatalf("reading key=%d, want signed-32 overflow result", got)
	}
	if got := focusKey(gui.Gadget{Rect: gui.Rect{X: math.MaxInt32, Y: 1}}, 0, FocusForward); got != -2147478649 {
		t.Fatalf("reading key=%d, want signed-32 addition result", got)
	}
}

func TestMoveFocusCanonicalColumnsRespectStrictDonorAndZeroSentinel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		firstX int32
		nearX  int32
		want   int
	}{
		{name: "nine pixels donates", firstX: 100, nearX: 109, want: 3},
		{name: "ten pixels does not donate", firstX: 100, nearX: 110, want: 4},
		{name: "zero stops donor scan", firstX: 0, nearX: 109, want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := focusWindow(
				// Canonical-X donors include controls that focus later rejects.
				gui.Gadget{Kind: gui.KindLabel, Name: "FIRST", Active: 1, Rect: gui.Rect{X: tc.firstX, Y: -10000}},
				gui.Gadget{Kind: gui.KindButton, Name: "CURRENT", Active: 1, Rect: gui.Rect{X: 100, Y: 0}},
				gui.Gadget{Kind: gui.KindButton, Name: "NEAR", Active: 1, Rect: gui.Rect{X: tc.nearX, Y: 5000}},
				gui.Gadget{Kind: gui.KindButton, Name: "FAR", Active: 1, Rect: gui.Rect{X: 100, Y: 10000}},
			)
			p := NewPanel(w)
			p.SetFocus(2)
			if !p.MoveFocus(FocusDown) || p.Focused() != tc.want {
				t.Fatalf("down focus=%d, want %d", p.Focused(), tc.want)
			}
		})
	}
}

func TestMoveFocusKeepsHeaderCanonicalXAtZero(t *testing.T) {
	newPanel := func() *Panel {
		w := focusWindow(
			gui.Gadget{Kind: gui.KindButton, Name: "FIRST", Rect: gui.Rect{X: 1, Y: 1}},
			gui.Gadget{Kind: gui.KindButton, Name: "SECOND", Rect: gui.Rect{X: 200, Y: 1}},
		)
		w.Gadgets[0].Rect = gui.Rect{X: 100, Y: 0}
		return NewPanel(w)
	}

	down := newPanel()
	if down.Focused() != 0 {
		t.Fatalf("inactive opening focus=%d, want header 0", down.Focused())
	}
	down.SetActiveAt(1, true)
	down.SetActiveAt(2, true)
	if !down.MoveFocus(FocusDown) || down.Focused() != 1 {
		t.Fatalf("down from header focus=%d, want first control 1", down.Focused())
	}

	up := newPanel()
	up.SetActiveAt(1, true)
	up.SetActiveAt(2, true)
	if !up.MoveFocus(FocusUp) || up.Focused() != 2 {
		t.Fatalf("up from header focus=%d, want second control 2", up.Focused())
	}
}

func TestMoveFocusAdmissionAndCaptureRelease(t *testing.T) {
	w := focusWindow(
		gui.Gadget{Kind: gui.KindButton, Name: "CURRENT", Active: 1, Rect: gui.Rect{X: 0, W: 4, H: 4}},
		gui.Gadget{Kind: gui.KindButton, Name: "GREY", Active: 1, GrayedOut: 1, Rect: gui.Rect{X: 10}},
		gui.Gadget{Kind: gui.KindButton, Name: "NONLOW", Active: 1, GrayedOut: 2, Rect: gui.Rect{X: 20}},
		gui.Gadget{Kind: gui.KindButton, Name: "ATTR", Active: 1, Attribs: 0x400, Rect: gui.Rect{X: 30}},
		gui.Gadget{Kind: gui.KindListBox, Name: "LOCKEDLIST", Active: 1, Attribs: 0x100, Rect: gui.Rect{X: 40}},
		gui.Gadget{Kind: gui.KindScrollBar, Name: "LOCKED", Active: 1, GrayedOut: 1, Rect: gui.Rect{X: 50, W: 2, H: 2}},
		gui.Gadget{Kind: gui.KindScrollBar, Name: "VERTICAL", Active: 1, Rect: gui.Rect{X: 60, W: 1, H: 2}},
		gui.Gadget{Kind: gui.KindScrollBar, Name: "SQUARE", Active: 1, Rect: gui.Rect{X: 70, W: 2, H: 2}},
		gui.Gadget{Kind: gui.KindSurface, Name: "COLD", Active: 1, HotOrNot: 0, Rect: gui.Rect{X: 80}},
	)
	p := NewPanel(w)
	p.SetFocus(1)
	p.SetPressed(1)
	p.SetRightPressed(1)
	if !p.MoveFocus(FocusForward) || p.Focused() != 3 {
		t.Fatalf("admission focus=%d, want non-low-grey button 3", p.Focused())
	}
	if p.PressedIndex() != -1 || p.RightPressedIndex() != -1 {
		t.Fatalf("traversal retained pointer capture left=%d right=%d", p.PressedIndex(), p.RightPressedIndex())
	}
	p.SetFocus(3)
	p.SetActiveAt(3, false)
	if p.Focused() != 8 {
		t.Fatalf("disabled focus moved to %d, want square slider 8", p.Focused())
	}
	p.SetActiveAt(8, false)
	if p.Focused() != 9 {
		t.Fatalf("disabled square slider moved to cold surface %d", p.Focused())
	}
}

func TestSetActiveAtRetriesTraversalForAnAlreadyInactiveFocus(t *testing.T) {
	p := NewPanel(focusWindow(gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 2, H: 2}}))
	p.SetFocus(1)
	p.SetActiveAt(1, false)
	if p.Focused() != 1 {
		t.Fatalf("first disable focus=%d, want unchanged inactive index 1", p.Focused())
	}
	p.SetPressed(1)
	p.SetRightPressed(1)
	p.SetActiveAt(1, false)
	if p.PressedIndex() != -1 || p.RightPressedIndex() != -1 {
		t.Fatalf("repeated inactive disable retained capture left=%d right=%d", p.PressedIndex(), p.RightPressedIndex())
	}
}

func TestFocusLifecycleOpeningDisablingAndTextSetup(t *testing.T) {
	w := focusWindow(
		gui.Gadget{Kind: gui.KindLabel, Name: "CAPTION", Active: 1},
		gui.Gadget{Kind: gui.KindButton, Name: "BUTTON", Active: 1, Rect: gui.Rect{X: 10, W: 4, H: 4}},
		gui.Gadget{Kind: gui.KindTextBox, Name: "TEXT", Active: 1, MaxChars: 8, Rect: gui.Rect{X: 20}},
	)
	p := NewPanel(w)
	if p.Focused() != 2 {
		t.Fatalf("empty default opening focus=%d, want first eligible 2", p.Focused())
	}
	p.SetTextAt(3, "text")
	p.SetPressed(2)
	p.SetActiveAt(2, false)
	if p.Focused() != 3 || !p.EditorCaptured() || p.EditorCaret() != 4 {
		t.Fatalf("disable lifecycle focus/capture/caret=%d/%t/%d, want 3/true/4", p.Focused(), p.EditorCaptured(), p.EditorCaret())
	}
	if p.PressedIndex() != -1 {
		t.Fatalf("disable lifecycle retained capture=%d", p.PressedIndex())
	}

	missing := focusWindow(gui.Gadget{Kind: gui.KindButton, Name: "BUTTON", Active: 1})
	missing.Header.DefaultFocus = "MISSING"
	missing.Focus = -1
	if got := NewPanel(missing).Focused(); got != -1 {
		t.Fatalf("nonempty missing default focus=%d, want -1", got)
	}
}

func TestMoveFocusRejectsUnsupportedAndLargeWindowsWithoutMutation(t *testing.T) {
	p := NewPanel(focusWindow(gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 2, H: 2}}))
	p.SetFocus(1)
	p.SetPressed(1)
	if p.MoveFocus(FocusDirection(99)) || p.Focused() != 1 || p.PressedIndex() != 1 {
		t.Fatal("invalid direction mutated focus or capture")
	}
	p.SetFocus(-1)
	if p.MoveFocus(FocusForward) {
		t.Fatal("absent focus accepted traversal")
	}

	controls := make([]gui.Gadget, 51)
	controls[0] = gui.Gadget{Kind: gui.KindPanel, Active: 1}
	for i := 1; i < len(controls); i++ {
		controls[i] = gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: int32(i)}}
	}
	large := NewPanel(&gui.Window{Header: gui.Header{DefaultFocus: "set"}, Focus: 1, Gadgets: controls})
	large.SetPressed(1)
	if large.MoveFocus(FocusForward) || large.Focused() != 1 || large.PressedIndex() != 1 {
		t.Fatal("more-than-49 controls mutated focus or capture")
	}
}
