package ui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

func servicePanel(gadgets ...gui.Gadget) *Panel {
	w := &gui.Window{Rect: gui.Rect{W: 100, H: 100}, Gadgets: append([]gui.Gadget{{Kind: gui.KindPanel}}, gadgets...)}
	return NewPanel(w)
}

func TestServiceCaptureAndFirstFired(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindButton, Name: "FIRST", Active: 1, Rect: gui.Rect{W: 30, H: 20}},
		gui.Gadget{Kind: gui.KindButton, Name: "SECOND", Active: 1, Rect: gui.Rect{W: 30, H: 20}},
	)
	p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{})
	if got, button := p.Capture(); got != 1 || button != 1 {
		t.Fatalf("capture = %d/%d, want first left", got, button)
	}
	r := p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, PointerEvents: []input.PointerEvent{{Kind: input.LeftUp, X: 2, Y: 2}}}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 {
		t.Fatalf("fired = %+v, want first button", r)
	}
}

func TestServiceSliderTrackIsUnthrottled(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindScrollBar, Active: 1, Range: 8, KnobSize: 2, Rect: gui.Rect{W: 30, H: 8}, Attribs: 1})
	p.ServiceFrame(WidgetFrame{PointerX: 20, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 20, Y: 2}}}, WidgetHooks{})
	p.ServiceFrame(WidgetFrame{PointerX: 20, PointerY: 2, HeldButtons: 1}, WidgetHooks{})
	if got := p.SliderKnobAt(1); got != 2 {
		t.Fatalf("knob = %d, want two unthrottled track steps", got)
	}
}

func TestServiceListHeadingRejectsDoubleClick(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: 0x10 | 0x200, ItemHeight: 10, Rect: gui.Rect{W: 40, H: 30}})
	p.SetListAt(1, []string{"&G heading", "row"})
	r := p.ServiceFrame(WidgetFrame{PointerX: 3, PointerY: 3, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDoubleClick, X: 3, Y: 3}}}, WidgetHooks{})
	if r.Fired || p.ListAt(1).Selected() != 0 {
		t.Fatalf("heading result = %+v selected=%d", r, p.ListAt(1).Selected())
	}
}

func TestPanelTimerAdvancedSamplesScaledStamp(t *testing.T) {
	p := servicePanel()
	stamps := []int32{7, 7, 10, 10, 9}
	want := []bool{false, false, true, false, false}
	for i, stamp := range stamps {
		if p.TimerAdvanced(stamp) != want[i] {
			t.Fatalf("stamp %d advanced unexpectedly", stamp)
		}
	}
}

func TestServiceAssociatedTextListComputesLoadedBarGeometry(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindListBox, Active: 1, Assoc: 4, Attribs: 0x10, Rect: gui.Rect{W: 30, H: 22}},
		gui.Gadget{Kind: gui.KindScrollBar, Active: 1, Assoc: 4, Range: 99, KnobSize: 1, Rect: gui.Rect{W: 8, H: 40}},
	)
	items := make([]string, 20)
	p.SetListAt(1, items)
	hooks := WidgetHooks{Metric: func(int) int { return 4 }}
	if got := p.sliderKnobSize(2, hooks); got != 10 {
		t.Fatalf("knob length = %d, want 10", got)
	}
	if got := p.sliderTravel(2, hooks); got != 27 {
		t.Fatalf("travel = %d, want 27 from loaded dimensions", got)
	}
}

func TestServiceEdgeScrollUsesMetricDefaultRow(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: 0x10, Rect: gui.Rect{W: 30, H: 22}})
	p.SetListAt(1, make([]string, 20))
	p.listMaxTop[1] = 16
	p.capture, p.captureButton = 1, 1
	p.pointerY = 30
	hooks := WidgetHooks{Metric: func(int) int { return 4 }}
	p.scrollCapturedList(1, hooks)
	p.scrollCapturedList(1, hooks)
	if got := p.ListAt(1).Selected(); got != 4 {
		t.Fatalf("selection = %d, want final visible default-metric row 4", got)
	}
}

func TestServiceVisitsReachedSurfacesBeforeFirstFired(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindSurface, Active: 1, Rect: gui.Rect{W: 30, H: 20}},
		gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 0x10, Rect: gui.Rect{W: 30, H: 20}},
		gui.Gadget{Kind: gui.KindSurface, Active: 1, HotOrNot: 1, Rect: gui.Rect{W: 30, H: 20}},
	)
	var visited []int
	r := p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{Surface: func(index int) { visited = append(visited, index) }})
	if !r.Fired || r.FiredIndex != 2 || len(visited) != 1 || visited[0] != 1 {
		t.Fatalf("result=%+v surfaces=%v", r, visited)
	}
}

func TestServiceCaptureRefusesSecondNonTextOwner(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 10, H: 10}},
		gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 20, W: 10, H: 10}},
	)
	p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{})
	p.ServiceFrame(WidgetFrame{PointerX: 22, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 22, Y: 2}}}, WidgetHooks{})
	if got, _ := p.Capture(); got != 1 {
		t.Fatalf("capture=%d, want first owner", got)
	}
}

func TestServiceTextListInteriorAndSingleClick(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: 0x10 | 0x40, ItemHeight: 10, Rect: gui.Rect{W: 20, H: 30}})
	p.SetListAt(1, []string{"one", "two"})
	outside := p.ServiceFrame(WidgetFrame{PointerX: 21, PointerY: 4, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 21, Y: 4}}}, WidgetHooks{})
	if outside.Fired || p.ListAt(1).Selected() != 0 {
		t.Fatalf("outside result=%+v selected=%d", outside, p.ListAt(1).Selected())
	}
	r := p.ServiceFrame(WidgetFrame{PointerX: 3, PointerY: 14, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 3, Y: 14}}}, WidgetHooks{})
	if !r.Fired || r.FiredIndex != 1 || p.ListAt(1).Selected() != 1 {
		t.Fatalf("inside result=%+v selected=%d", r, p.ListAt(1).Selected())
	}
}

func TestServiceListDoubleClickChecksPointerHeading(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: 0x10 | 0x200, ItemHeight: 10, Rect: gui.Rect{W: 30, H: 30}})
	p.SetListAt(1, []string{"row", "&G heading"})
	p.ListAt(1).selected = 0
	r := p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 14, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDoubleClick, X: 2, Y: 14}}}, WidgetHooks{})
	if r.Fired || p.ListAt(1).Selected() != 0 {
		t.Fatalf("result=%+v selected=%d", r, p.ListAt(1).Selected())
	}
}

func TestServiceListAssociationsUseRuntimeValues(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindListBox, Active: 1, Assoc: 7, Attribs: 0x10 | 8, ItemHeight: 5, Rect: gui.Rect{W: 20, H: 22}},
		gui.Gadget{Kind: gui.KindListBox, Active: 1, Assoc: 7, Attribs: 0x10, ItemHeight: 5, Rect: gui.Rect{W: 20, H: 22}},
		gui.Gadget{Kind: gui.KindScrollBar, Active: 1, Assoc: 7, Range: 9, Rect: gui.Rect{W: 8, H: 20}},
		gui.Gadget{Kind: gui.KindTextBox, Active: 1, Assoc: 7, Rect: gui.Rect{W: 20, H: 10}},
	)
	p.SetListAt(1, []string{"zero", "one", "two", "three", "four", "five"})
	p.SetListAt(2, []string{"x"})
	p.ListAt(1).top = 2
	p.listMaxTop[1] = 4
	p.setListSelection(1, 2, WidgetHooks{})
	if peer := p.ListAt(2); peer.Selected() != 0 || peer.Top() != 2 {
		t.Fatalf("peer selected/top=%d/%d", peer.Selected(), peer.Top())
	}
	if got := p.SliderKnobAt(3); got != 3 {
		t.Fatalf("knob=%d, want 3", got)
	}
	if got := p.TextAt(4); got != "two" {
		t.Fatalf("editor text=%q", got)
	}
}

func TestServiceScrollbarToListUsesItemHeightFormula(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindListBox, Active: 1, Assoc: 3, Attribs: 0x10, ItemHeight: 5, Rect: gui.Rect{W: 20, H: 20}},
		gui.Gadget{Kind: gui.KindScrollBar, Active: 1, Assoc: 3, Range: 9, Rect: gui.Rect{W: 8, H: 20}},
	)
	p.SetListAt(1, make([]string, 10))
	p.SetSliderKnobAt(2, 4)
	p.syncSlider(2, WidgetHooks{})
	if got := p.ListAt(1).Top(); got != 4 { // (10-(20/5))*4/(7-1)
		t.Fatalf("top=%d, want 4", got)
	}
}

func TestServiceListEdgeScrollAboveUsesTopBoundedSelection(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: 0x10, ItemHeight: 5, Rect: gui.Rect{W: 20, H: 22}})
	p.SetListAt(1, make([]string, 12))
	p.ListAt(1).top, p.ListAt(1).selected = 3, 5
	p.listMaxTop[1] = 8
	p.capture, p.captureButton, p.pointerY = 1, widgetLeftButton, -1
	p.scrollCapturedList(1, WidgetHooks{})
	p.scrollCapturedList(1, WidgetHooks{})
	if l := p.ListAt(1); l.Top() != 2 || l.Selected() != 2 {
		t.Fatalf("after above scroll top/selected=%d/%d, want 2/2", l.Top(), l.Selected())
	}
	p.ListAt(1).top, p.ListAt(1).selected = 0, 5
	p.scrollCapturedList(1, WidgetHooks{})
	p.scrollCapturedList(1, WidgetHooks{})
	if got := p.ListAt(1).Selected(); got != 0 {
		t.Fatalf("top-zero selection=%d, want 0", got)
	}
}

func TestServiceStopsPerIndexTimerAndHoverAtFirstFire(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 0x10, Rect: gui.Rect{W: 20, H: 20}, Help: "first"},
		gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 20, H: 20}, Help: "later"},
	)
	p.SetFlashRow(1, 4)
	p.SetFlashRow(2, 4)
	r := p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, TimerAdvanced: true, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{})
	if !r.Fired || p.FlashRow(1) != 2 || p.FlashRow(2) != 4 || p.Hovered() != 1 {
		t.Fatalf("result=%+v flash=%d/%d hover=%d", r, p.FlashRow(1), p.FlashRow(2), p.Hovered())
	}
}

func TestServiceSliderKnobIncludesTrailingEndpoint(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindScrollBar, Active: 1, Range: 8, KnobSize: 2, Rect: gui.Rect{W: 30, H: 8}, Attribs: 1})
	p.SetSliderKnobAt(1, 3)
	p.pointerX = p.sliderKnobStart(1) + int32(p.sliderKnobSize(1, WidgetHooks{}))
	if !p.inSliderKnob(1, WidgetHooks{}) {
		t.Fatal("knob trailing endpoint was excluded")
	}
}

func TestServiceButtonStateContracts(t *testing.T) {
	p := servicePanel(
		gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 0x100, Stages: 3, Rect: gui.Rect{W: 20, H: 20}},
		gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 8, Rect: gui.Rect{X: 30, W: 20, H: 20}},
	)
	p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{ArtFrames: func(int) int { return 4 }})
	if p.StatusAt(1) != 1 || p.StageAt(1) != 0 {
		t.Fatalf("cycle status/stage=%d/%d", p.StatusAt(1), p.StageAt(1))
	}
	p.SetStatusAt(2, 7)
	p.ServiceFrame(WidgetFrame{PointerX: 32, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 32, Y: 2}}}, WidgetHooks{})
	p.ServiceFrame(WidgetFrame{PointerX: 32, PointerY: 2, PointerEvents: []input.PointerEvent{{Kind: input.LeftUp, X: 32, Y: 2}}}, WidgetHooks{})
	if p.StatusAt(2) != 7 {
		t.Fatalf("attribute-8 changed nonbinary status to %d", p.StatusAt(2))
	}
}

func TestServiceUsesLowGreyBitAndRawSurfacePress(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindButton, Active: 1, GrayedOut: 2, Rect: gui.Rect{W: 20, H: 20}})
	p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{})
	if got, _ := p.Capture(); got != 1 {
		t.Fatalf("upper grey bits rejected capture %d", got)
	}
	w := &gui.Window{Rect: gui.Rect{X: 50, W: 100, H: 30}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindSurface, Active: 1, HotOrNot: 1, Rect: gui.Rect{X: 10, W: 10, H: 10}}}}
	p = NewPanel(w)
	p.pointerX, p.pointerY = 11, 1
	p.ServiceFrame(WidgetFrame{PointerX: 11, PointerY: 1, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 11, Y: 1}}}, WidgetHooks{})
	if got, _ := p.Capture(); got != 1 {
		t.Fatalf("raw surface capture=%d, want 1", got)
	}
}

func TestServicePointerEventsKeepTheirCoordinates(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{W: 10, H: 10}}, gui.Gadget{Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 20, W: 10, H: 10}})
	result := p.ServiceFrame(WidgetFrame{PointerX: 22, PointerY: 2, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}, {Kind: input.LeftUp, X: 22, Y: 2}}}, WidgetHooks{})
	if result.Fired || p.CaptureIndex() != -1 {
		t.Fatalf("cross-button release fired or retained capture: %+v", result)
	}
}
func TestServiceCycleUsesResolvedFrames(t *testing.T) {
	p := servicePanel(gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 0x100, Stages: 2, Rect: gui.Rect{W: 10, H: 10}})
	p.SetStatusAt(1, 2)
	p.ServiceFrame(WidgetFrame{PointerX: 2, PointerY: 2, HeldButtons: 1, PointerEvents: []input.PointerEvent{{Kind: input.LeftDown, X: 2, Y: 2}}}, WidgetHooks{ArtFrames: func(int) int { return 4 }})
	if p.StatusAt(1) != 3 {
		t.Fatalf("cycle=%d, want third resolved frame", p.StatusAt(1))
	}
}
