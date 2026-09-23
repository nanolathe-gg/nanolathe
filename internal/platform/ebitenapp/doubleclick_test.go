package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// leftAt polls one press or release at a point and returns the record the host
// service published for it.
func leftAt(t *testing.T, in *input.State, clicks *doubleClickRecognizer, down bool, x, y int32, at uint32) input.PointerEvent {
	t.Helper()
	applyInputWith(in, sampledInput{x: x, y: y, timestamp: at, buttons: input.MouseButtons{Left: down}}, clicks)
	event, ok := in.CurrentPointer()
	if !ok {
		t.Fatalf("no published record for the transition at %d,%d t=%d", x, y, at)
	}
	return event
}

func rightAt(t *testing.T, in *input.State, clicks *doubleClickRecognizer, down bool, x, y int32, at uint32) input.PointerEvent {
	t.Helper()
	applyInputWith(in, sampledInput{x: x, y: y, timestamp: at, buttons: input.MouseButtons{Right: down}}, clicks)
	event, ok := in.CurrentPointer()
	if !ok {
		t.Fatalf("no published right-button record for the transition at %d,%d t=%d", x, y, at)
	}
	return event
}

// The second press of a pair replaces its plain press identity, and the pair is
// consumed so a third press is a press again [07 R-WGT-01 §4].
func TestLeftPressPairBecomesDoubleClickAndDoesNotTriple(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	if got := leftAt(t, in, &clicks, true, 40, 50, 100); got.Kind != input.LeftDown {
		t.Fatalf("first press = %v, want LeftDown", got.Kind)
	}
	if got := leftAt(t, in, &clicks, false, 40, 50, 101); got.Kind != input.LeftUp {
		t.Fatalf("first release = %v, want LeftUp", got.Kind)
	}
	second := leftAt(t, in, &clicks, true, 40, 50, 100+doubleClickInterval)
	if second.Kind != input.LeftDoubleClick || second.X != 40 || second.Y != 50 {
		t.Fatalf("second press = %+v, want a double-click record at the point", second)
	}
	if got := leftAt(t, in, &clicks, false, 40, 50, 100+doubleClickInterval); got.Kind != input.LeftUp {
		t.Fatalf("second release = %v, want LeftUp", got.Kind)
	}
	third := leftAt(t, in, &clicks, true, 40, 50, 100+doubleClickInterval)
	if third.Kind != input.LeftDown {
		t.Fatalf("third press = %v, want a new pair's first press, never a triple", third.Kind)
	}
}

// The interval is inclusive at its last scaled unit and rejects the next one;
// the rectangle is inclusive at the tolerance and rejects one pixel beyond.
// Host policy, not a retail measurement (docs/DESIGN_INTERFACE_HUD_INPUT.md §5).
func TestDoubleClickWindowAndRectangleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dx, dy   int32
		dt       uint32
		wantPair bool
	}{
		{"same point at the last unit", 0, 0, doubleClickInterval, true},
		{"one unit past the interval", 0, 0, doubleClickInterval + 1, false},
		{"corner of the rectangle", doubleClickTolerance, -doubleClickTolerance, 1, true},
		{"one pixel wider", doubleClickTolerance + 1, 0, 1, false},
		{"one pixel taller", 0, doubleClickTolerance + 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input.NewState()
			var clicks doubleClickRecognizer
			leftAt(t, in, &clicks, true, 40, 50, 100)
			leftAt(t, in, &clicks, false, 40, 50, 100)
			got := leftAt(t, in, &clicks, true, 40+tc.dx, 50+tc.dy, 100+tc.dt)
			want := input.LeftDown
			if tc.wantPair {
				want = input.LeftDoubleClick
			}
			if got.Kind != want {
				t.Fatalf("second press = %v, want %v", got.Kind, want)
			}
		})
	}
}

// A press rejected for distance becomes the new candidate, so the click that
// follows it pairs with it rather than with the abandoned one.
func TestRejectedPressArmsTheNextPair(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	leftAt(t, in, &clicks, true, 40, 50, 100)
	leftAt(t, in, &clicks, false, 40, 50, 100)
	if got := leftAt(t, in, &clicks, true, 200, 50, 101); got.Kind != input.LeftDown {
		t.Fatalf("moved press = %v, want LeftDown", got.Kind)
	}
	leftAt(t, in, &clicks, false, 200, 50, 101)
	if got := leftAt(t, in, &clicks, true, 200, 50, 102); got.Kind != input.LeftDoubleClick {
		t.Fatalf("press pairing with the moved one = %v, want LeftDoubleClick", got.Kind)
	}
}

func TestRightPressPairBecomesDoubleClickAndDoesNotTriple(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	if got := rightAt(t, in, &clicks, true, 40, 50, 100); got.Kind != input.RightDown {
		t.Fatalf("first press = %v, want RightDown", got.Kind)
	}
	rightAt(t, in, &clicks, false, 40, 50, 101)
	if got := rightAt(t, in, &clicks, true, 40, 50, 100+doubleClickInterval); got.Kind != input.RightDoubleClick {
		t.Fatalf("second press = %v, want RightDoubleClick", got.Kind)
	}
	rightAt(t, in, &clicks, false, 40, 50, 100+doubleClickInterval)
	if got := rightAt(t, in, &clicks, true, 40, 50, 100+doubleClickInterval); got.Kind != input.RightDown {
		t.Fatalf("third press = %v, want RightDown", got.Kind)
	}
}

func TestInterleavedButtonsKeepIndependentPairCandidates(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	leftAt(t, in, &clicks, true, 40, 50, 100)
	leftAt(t, in, &clicks, false, 40, 50, 100)
	if got := rightAt(t, in, &clicks, true, 40, 50, 101); got.Kind != input.RightDown {
		t.Fatalf("first interleaved right press = %v, want RightDown", got.Kind)
	}
	rightAt(t, in, &clicks, false, 40, 50, 101)
	if got := leftAt(t, in, &clicks, true, 40, 50, 102); got.Kind != input.LeftDoubleClick {
		t.Fatalf("second left press = %v, want LeftDoubleClick", got.Kind)
	}
	leftAt(t, in, &clicks, false, 40, 50, 102)
	if got := rightAt(t, in, &clicks, true, 40, 50, 103); got.Kind != input.RightDoubleClick {
		t.Fatalf("second right press = %v, want RightDoubleClick", got.Kind)
	}
}

// End to end at the seam the defect was reported at: a pair of polled presses
// on a list row reaches the widget pass as the open action, where a single
// click on a plain text list only selects [07 R-WGT-01 §4].
func TestPolledDoubleClickOpensAListRow(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	window := &gui.Window{Rect: gui.Rect{W: 100, H: 100}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindListBox, Name: "LOADLIST", Active: 1, Attribs: 0x10, ItemHeight: 10, Rect: gui.Rect{W: 40, H: 30}},
	}}
	p := ui.NewPanel(window)
	p.SetListAt(1, []string{"first", "second", "third"})

	service := func(down bool, x, y int32, at uint32) ui.ServiceResult {
		event := leftAt(t, in, &clicks, down, x, y, at)
		frame := ui.WidgetFrame{PointerX: event.X, PointerY: event.Y, PointerEvents: []input.PointerEvent{event}}
		if in.Mouse.Held(input.MouseButtonLeft) {
			frame.HeldButtons = 1
		}
		return p.ServiceFrame(frame, ui.WidgetHooks{})
	}

	if r := service(true, 3, 14, 100); r.Fired {
		t.Fatalf("single click fired %+v; a plain text list only selects", r)
	}
	if got := p.ListAt(1).Selected(); got != 1 {
		t.Fatalf("selected = %d, want the clicked row", got)
	}
	service(false, 3, 14, 100)
	opened := service(true, 3, 14, 101)
	if !opened.Fired || opened.FiredIndex != 1 {
		t.Fatalf("double click result = %+v, want the list fired", opened)
	}
	if got := p.ListAt(1).Selected(); got != 1 {
		t.Fatalf("selected after the open = %d, want the same row", got)
	}
}

func TestPolledRightDoubleClickDoesNotOpenAListRow(t *testing.T) {
	in := input.NewState()
	var clicks doubleClickRecognizer
	window := &gui.Window{Rect: gui.Rect{W: 100, H: 100}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindListBox, Name: "LOADLIST", Active: 1, Attribs: 0x10, ItemHeight: 10, Rect: gui.Rect{W: 40, H: 30}},
	}}
	p := ui.NewPanel(window)
	p.SetListAt(1, []string{"first", "second", "third"})

	service := func(down bool, at uint32) ui.ServiceResult {
		event := rightAt(t, in, &clicks, down, 3, 14, at)
		held := uint8(0)
		if in.Mouse.Held(input.MouseButtonRight) {
			held = 2
		}
		return p.ServiceFrame(ui.WidgetFrame{PointerX: event.X, PointerY: event.Y, HeldButtons: held, PointerEvents: []input.PointerEvent{event}}, ui.WidgetHooks{})
	}

	service(true, 100)
	service(false, 100)
	if got := service(true, 101); got.Fired {
		t.Fatalf("right double-click opened a left-only list: %+v", got)
	}
}
