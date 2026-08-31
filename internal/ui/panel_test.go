package ui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/gui"
)

func testWindow() *gui.Window {
	return &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "PANEL", Active: 1},
		{Kind: gui.KindButton, Name: "OK", Active: 1, Rect: gui.Rect{X: 10, Y: 20, W: 5, H: 4}, SourceName: "OK"},
		{Kind: gui.KindListBox, Name: "LIST", Active: 1, Rect: gui.Rect{X: 30, Y: 20, W: 10, H: 10}, SourceName: "LIST"},
		{Kind: gui.KindButton, Name: "HIDDEN", Active: 0, Rect: gui.Rect{X: 50, Y: 20, W: 5, H: 4}},
		{Kind: gui.KindButton, Name: "GRAY", Active: 1, GrayedOut: 1, Rect: gui.Rect{X: 60, Y: 20, W: 5, H: 4}},
	}}
}

func TestPanelUsesAuthoredInclusiveHitAndRuntimeRejection(t *testing.T) {
	p := NewPanel(testWindow())
	if got := p.HitTest(14, 23); got != 1 {
		t.Fatalf("inclusive right/bottom hit = %d, want 1", got)
	}
	if p.HitTest(50, 20) != -1 || p.HitTest(60, 20) != -1 {
		t.Fatal("hidden or grayed gadget was accepted")
	}
}

func TestPanelStartsAtAuthoredFocusForKeyboardActivation(t *testing.T) {
	w := testWindow()
	w.Focus = 1
	if got := NewPanel(w).Focused(); got != 1 {
		t.Fatalf("authored focus=%d, want 1 for Enter/Space activation before mouse input", got)
	}
	w.Focus = len(w.Gadgets)
	if got := NewPanel(w).Focused(); got != -1 {
		t.Fatalf("invalid authored focus=%d, want -1", got)
	}
}

func TestPanelPressReleaseFocusAndListWheelClamp(t *testing.T) {
	p := NewPanel(testWindow())
	p.SetList("LIST", []string{"a", "b", "c", "d", "e"})
	if got := p.Press(14, 23); got != 1 || p.Focused() != 1 || p.PressedIndex() != 1 {
		t.Fatalf("press got=%d focus=%d pressed=%d", got, p.Focused(), p.PressedIndex())
	}
	if got, ok := p.Release(14, 23); !ok || got != 1 || p.PressedIndex() != -1 {
		t.Fatalf("release got=%d ok=%t pressed=%d", got, ok, p.PressedIndex())
	}
	if p.Press(11, 21) != 1 {
		t.Fatal("second press was not accepted")
	}
	if _, ok := p.Release(100, 100); ok {
		t.Fatal("release outside activated the button")
	}
	if top, ok := p.ScrollList("LIST", 99, 2); !ok || top != 3 {
		t.Fatalf("maxTop clamp top=%d ok=%t, want 3", top, ok)
	}
	if top, _ := p.ScrollList("LIST", -99, 2); top != 0 {
		t.Fatalf("minimum clamp top=%d, want 0", top)
	}
	p.SetListSelection("LIST", 4, 2)
	_, selected, top, _ := p.ListValues("LIST")
	if selected != 4 || top != 3 {
		t.Fatalf("selection visibility selected=%d top=%d", selected, top)
	}
}

func TestPanelSetListTopUsesExplicitMaxTop(t *testing.T) {
	p := NewPanel(testWindow())
	p.SetList("LIST", []string{"a", "b", "c", "d", "e", "f", "g", "h"})
	// With two visible rows, eight items have maxTop six. Supplying that maximum directly
	// keeps scrollbar geometry separate from row-count calculations.
	if !p.SetListTop("LIST", 99, 6) {
		t.Fatal("SetListTop did not find LIST")
	}
	_, _, top, _ := p.ListValues("LIST")
	if top != 6 {
		t.Fatalf("explicit maxTop clamp top=%d, want 6", top)
	}
}

func TestPanelPreservesFirstOwnerForDuplicateNames(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindLabel, Name: "TEXT", Text: "first", SourceName: "A", Active: 1},
		{Kind: gui.KindLabel, Name: "TEXT", Text: "second", SourceName: "B", Active: 1},
	}}
	p := NewPanel(w)
	p.SetText("TEXT", "updated")
	if got := p.TextFor(w.Gadgets[1]); got != "updated" || p.TextFor(w.Gadgets[2]) != "second" {
		t.Fatalf("duplicate ownership first=%q second=%q", got, p.TextFor(w.Gadgets[2]))
	}
}

func TestPanelStackOwnsSaveUnderAndModalClose(t *testing.T) {
	var stack PanelStack
	base := NewPanel(testWindow())
	under := NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	stack.Replace(base)
	stack.Push(under)
	if stack.Top() != under || stack.SaveUnder() != base {
		t.Fatalf("stack top/save-under mismatch: top=%p under=%p", stack.Top(), stack.SaveUnder())
	}
	modal := NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	stack.PushModal(modal)
	if stack.Modal() != modal || stack.SaveUnder() != under {
		t.Fatal("modal was not placed over the saved-under panel")
	}
	if stack.CloseModal() != modal || stack.Top() != under {
		t.Fatal("closing modal did not restore predecessor")
	}
}

func TestPanelSemanticActivationAndThumbDrag(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindListBox, Name: "LIST", Assoc: 7, Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 10, H: 10}},
		{Kind: gui.KindScrollBar, Name: "SCROLL", Assoc: 7, Active: 1, Rect: gui.Rect{X: 10, Y: 0, W: 10, H: 10}},
	}})
	p.SetList("LIST", []string{"a", "b", "c", "d"})
	if got := p.Press(1, 1); got != 1 {
		t.Fatalf("press index=%d, want 1", got)
	}
	action := p.ReleaseAction(1, 1)
	if action.Kind != ActionActivate || action.Gadget != "LIST" {
		t.Fatalf("semantic release=%+v", action)
	}
	if !p.BeginScrollDrag(2, false, 10, 2, 10) || !p.ScrollDragging() {
		t.Fatal("thumb drag was not captured")
	}
	if !p.UpdateScrollDrag(20, 0, true) {
		t.Fatal("thumb drag update was not accepted")
	}
	_, _, top, _ := p.ListValues("LIST")
	if top != 2 {
		t.Fatalf("drag top=%d, want 2", top)
	}
	p.UpdateScrollDrag(20, 0, false)
	if p.ScrollDragging() {
		t.Fatal("thumb drag remained captured after release")
	}
}

// TestPanelMessageDoesNotNeedAnAuthoredTextControl locks the corrected
// contract: the message box's labels are runtime gadgets carrying their own
// text, so a window with no authored label still retains the message
// [07 R-FE-01 §9]. The superseded test asserted the opposite — that SetMessage
// refused such a window — and `MSGBOX.GUI` is exactly such a window, which is
// what made every shell diagnostic unreachable.
func TestPanelMessageDoesNotNeedAnAuthoredTextControl(t *testing.T) {
	withoutText := NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}})
	withoutText.SetMessage("diagnostic")
	if withoutText.Message() != "diagnostic" {
		t.Fatalf("message=%q, want diagnostic", withoutText.Message())
	}
	// A runtime label's own text is what the painter draws; SetMessage rewrites
	// no gadget.
	runtimeLabels := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindLabel, Name: "TEXT", SourceName: "GADGET1", Active: 1, Text: "first line"},
		{Kind: gui.KindLabel, Name: "TEXT", SourceName: "GADGET2", Active: 1, Text: "second line"},
	}})
	runtimeLabels.SetMessage("first line\nsecond line")
	if got := runtimeLabels.TextFor(runtimeLabels.Window.Gadgets[1]); got != "first line" {
		t.Fatalf("first label text=%q", got)
	}
	if got := runtimeLabels.TextFor(runtimeLabels.Window.Gadgets[2]); got != "second line" {
		t.Fatalf("second label text=%q", got)
	}
}
