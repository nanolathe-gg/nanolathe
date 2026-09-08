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
	// A hidden gadget is skipped before the hit test; a greyed one is not —
	// the grey bit is a press/fire test [07 R-WGT-01 §1][07 R-WGT-01 §13].
	if p.HitTest(50, 20) != -1 {
		t.Fatal("hidden gadget was hovered")
	}
	if got := p.HitTest(60, 20); got != 4 {
		t.Fatalf("greyed gadget hover = %d, want 4 so HELPTEXT can show its help", got)
	}
	if got := p.PressTest(60, 20); got != -1 {
		t.Fatalf("greyed gadget took the capture at index %d", got)
	}
}

// The pass visits gadgets in index order and every hit replaces the hovered
// gadget, so overlapping rectangles hover the last hit; the capture goes the
// other way, to the first gadget that accepts the press
// [07 R-WGT-01 §1 step 5][07 R-WGT-01 §1 "Capture"].
func TestPanelHoverIsLastHitAndCaptureIsFirstHit(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "PANEL", Active: 1},
		{Kind: gui.KindButton, Name: "UNDER", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 40, H: 20}},
		{Kind: gui.KindButton, Name: "OVER", Active: 1, Rect: gui.Rect{X: 10, Y: 5, W: 40, H: 20}},
	}}
	p := NewPanel(w)
	if got := p.HitTest(15, 10); got != 2 {
		t.Fatalf("hover over two overlapping gadgets = %d, want the last hit 2", got)
	}
	if got := p.PressTest(15, 10); got != 1 {
		t.Fatalf("capture over two overlapping gadgets = %d, want the first hit 1", got)
	}
}

// Hovering a greyed gadget yields its help text; pressing and releasing on it
// fires nothing [07 R-WGT-01 §13].
func TestPanelGreyedGadgetIsHoveredButNeverFires(t *testing.T) {
	p := NewPanel(testWindow())
	p.SetHelp("GRAY", "cannot do that yet")
	idx := p.HitTest(62, 21)
	if idx != 4 {
		t.Fatalf("greyed hover = %d, want 4", idx)
	}
	if got := p.HelpOf(p.Window.Gadgets[idx].Name); got != "cannot do that yet" {
		t.Fatalf("hover help for a greyed gadget = %q", got)
	}
	if p.Fires(idx) {
		t.Fatal("a greyed gadget reported that it fires")
	}
	if got := p.Press(62, 21); got != -1 || p.PressedIndex() != -1 {
		t.Fatalf("press on a greyed gadget captured index %d", got)
	}
	if _, ok := p.Release(62, 21); ok {
		t.Fatal("release on a greyed gadget fired")
	}
	p.SetFocus(idx)
	if act := p.DefaultKeyAction(false); act.Kind != ActionNone {
		t.Fatalf("keyboard activation of a greyed gadget produced %+v", act)
	}
}

// `colorf` is a light-table row, not a colour, and the builder zeroes it for
// every button, label and picture box at open; every other kind keeps the
// authored word [07 R-WGT-01 §1][07 R-WGT-01 §12][03 R-FONT-01 §6].
func TestPanelZeroesFlashRowForButtonsLabelsAndPictures(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "PANEL", Active: 1},
		{Kind: gui.KindButton, Name: "B", Active: 1, ColorF: 15},
		{Kind: gui.KindLabel, Name: "L", Active: 1, ColorF: 15},
		{Kind: gui.KindPicture, Name: "P", Active: 1, ColorF: 15},
		{Kind: gui.KindListBox, Name: "LB", Active: 1, ColorF: 15},
	}}
	p := NewPanel(w)
	for _, i := range []int{1, 2, 3} {
		if got := p.FlashRow(i); got != 0 {
			t.Fatalf("gadget %d kept flash row %d, want 0 at open", i, got)
		}
	}
	if got := p.FlashRow(4); got != 15 {
		t.Fatalf("listbox colorf = %d, want the authored 15 kept as a colour-table entry", got)
	}
	p.SetFlashRow(1, 5)
	p.SetFlashRow(3, 5)
	p.DecayFlash()
	if got := p.FlashRow(1); got != 3 {
		t.Fatalf("button flash decayed to %d, want 3 (2 per timer tick)", got)
	}
	if got := p.FlashRow(3); got != 4 {
		t.Fatalf("picture-box flash decayed to %d, want 4 (1 per timer tick)", got)
	}
	p.SetFlashRow(1, 1)
	p.DecayFlash()
	if got := p.FlashRow(1); got != 0 {
		t.Fatalf("button flash decay clamped to %d, want 0", got)
	}
	if got := p.FlashRow(4); got != 15 {
		t.Fatalf("listbox colorf decayed to %d; no other kind decays", got)
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
	if got := p.TextAt(1); got != "updated" || p.TextAt(2) != "second" {
		t.Fatalf("duplicate ownership first=%q second=%q", got, p.TextAt(2))
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
	if got := runtimeLabels.TextAt(1); got != "first line" {
		t.Fatalf("first label text=%q", got)
	}
	if got := runtimeLabels.TextAt(2); got != "second line" {
		t.Fatalf("second label text=%q", got)
	}
}

// A picture box, a line, a font or raw-file record and a cold surface have no
// press handler, so a plate authored in front of a button leaves the capture
// to the button; a hot surface and a linked label take it [07 R-WGT-01 §7]
// [07 R-WGT-01 §8][07 R-WGT-01 §12].
func TestPanelOnlyHandlerKindsTakeTheCapture(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "PANEL", Active: 1},
		{Kind: gui.KindPicture, Name: "PLATE", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindLine, Name: "RULE", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindFont, Name: "FONT", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindRawFile, Name: "RAW", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindSurface, Name: "COLD", Active: 1, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindLabel, Name: "CAPTION", Active: 1, Attribs: gui.AttribInert, Rect: gui.Rect{X: 0, Y: 0, W: 100, H: 100}},
		{Kind: gui.KindButton, Name: "OK", Active: 1, Rect: gui.Rect{X: 10, Y: 10, W: 20, H: 10}},
		{Kind: gui.KindSurface, Name: "HOT", Active: 1, HotOrNot: 1, Rect: gui.Rect{X: 50, Y: 10, W: 20, H: 10}},
		{Kind: gui.KindLabel, Name: "LINKED", Active: 1, Link: "OK", QuickKey: 'O', Rect: gui.Rect{X: 10, Y: 50, W: 20, H: 10}},
	}}
	p := NewPanel(w)
	if got := p.PressTest(15, 15); got != 7 {
		t.Fatalf("press over the plate and the button captured index %d, want 7 (OK)", got)
	}
	if got := p.PressTest(55, 15); got != 8 {
		t.Fatalf("press on the hot surface captured index %d, want 8", got)
	}
	if got := p.PressTest(15, 55); got != 9 {
		t.Fatalf("press on the linked label captured index %d, want 9", got)
	}
	if got := p.PressTest(90, 90); got != -1 {
		t.Fatalf("press over only handler-less gadgets captured index %d, want -1", got)
	}
}

// Names are bounded byte comparisons, while state belongs to each record
// [07 R-FE-02 §5]. Header records never participate in named lookup.
func TestPanelExactNamesKeepIndependentRecordState(t *testing.T) {
	names := []string{"OK", "OK", "ok", " OK", "OK ", "OK", "1234567890abcdefA", "1234567890abcdefB", "NUL\x00ignored"}
	w := &gui.Window{}
	for i, name := range names {
		w.Gadgets = append(w.Gadgets, gui.Gadget{Name: name, Kind: gui.KindButton, Active: 1, Text: name, Rect: gui.Rect{X: int32(i * 20), W: 10, H: 10}})
	}
	p := NewPanel(w)
	for _, tc := range []struct {
		name  string
		index int
	}{
		{"OK", 1}, {"ok", 2}, {" OK", 3}, {"OK ", 4}, {"Ok", -1}, {"OK  ", -1},
		{"1234567890abcdef", 6}, {"1234567890abcdefZ", 6}, {"1234567890abcde", -1}, {"NUL", 8},
	} {
		if got := p.Index(tc.name); got != tc.index {
			t.Fatalf("Index(%q)=%d want %d", tc.name, got, tc.index)
		}
	}
	p.SetActive("OK", false)
	p.SetStatus("OK", 7)
	p.SetText("OK", "first")
	p.SetHelp("OK", "first help")
	p.SetStatusAt(5, 9)
	p.SetTextAt(5, "duplicate")
	p.SetHelpAt(5, "duplicate help")
	if p.ActiveAt(1) || !p.ActiveAt(5) || p.StatusAt(1) != 7 || p.StatusAt(5) != 9 || p.TextAt(1) != "first" || p.TextAt(5) != "duplicate" || p.HelpAt(5) != "duplicate help" {
		t.Fatal("duplicate records shared state")
	}
	if !p.ActiveAt(2) || p.TextAt(2) != "ok" || p.StatusAt(2) != 0 || p.TextAt(3) != " OK" || p.TextAt(4) != "OK " {
		t.Fatal("case or whitespace names shared state")
	}
	if p.HitTest(21, 1) != -1 || p.Press(101, 1) != 5 {
		t.Fatal("input did not use per-record activity")
	}
	if action := p.ReleaseAction(101, 1); action.Index != 5 || action.Gadget != "OK" || action.Kind != ActionActivate {
		t.Fatalf("duplicate activation=%+v", action)
	}
}

func TestPanelDuplicateListsKeepAssociationAndCapture(t *testing.T) {
	p := NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindListBox, Name: "LIST", Assoc: 1},
		{Kind: gui.KindListBox, Name: "LIST", Assoc: 2},
		{Kind: gui.KindScrollBar, Name: "SCROLL", Assoc: 2},
	}})
	p.SetList("LIST", []string{"first"})
	p.SetListAt(2, []string{"a", "b", "c", "d"})
	if !p.BeginScrollDrag(3, false, 10, 2, 10) || !p.UpdateScrollDrag(20, 0, true) {
		t.Fatal("second list did not capture scrollbar")
	}
	if p.ListAt(1).Len() != 1 || p.ListAt(1).Top() != 0 || p.ListAt(2).Top() != 2 {
		t.Fatal("association selected the wrong duplicate")
	}
	p.SetListSelectionAt(2, 3, 2)
	if p.ListFor("LIST").Selected() != 0 || p.ListAt(2).Selected() != 3 {
		t.Fatal("duplicate lists shared selection")
	}
}
