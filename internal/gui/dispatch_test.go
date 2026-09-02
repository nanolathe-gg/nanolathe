package gui

import "testing"

func TestWindowHitTestInclusiveMatrix(t *testing.T) {
	// [07 §3][07 R-WGT-01 §1] inclusive bounds after runtime placement. The
	// pass's hit test skips only hidden gadgets and never visits the header
	// record; a greyed gadget is still hovered, and only Fires tests the grey
	// bit [07 R-WGT-01 §13].
	w := &Window{
		Gadgets: []Gadget{
			{Kind: KindPanel, Rect: Rect{X: 0, Y: 0, W: 100, H: 100}, Active: 1},
			{Kind: KindButton, Rect: Rect{X: 10, Y: 10, W: 20, H: 10}, Active: 1},
			{Kind: KindButton, Rect: Rect{X: 40, Y: 10, W: 20, H: 10}, Active: 1, GrayedOut: 1},
			{Kind: KindButton, Rect: Rect{X: 10, Y: 30, W: 20, H: 10}, Active: 0},
			{Kind: KindButton, Rect: Rect{X: 50, Y: 50, W: 1, H: 1}, Active: 1},
		},
	}
	for _, tc := range []struct {
		x, y int32
		want int
	}{
		{10, 10, 1}, // top-left inclusive
		{29, 19, 1}, // bottom-right inclusive
		{50, 50, 4}, // one-pixel control
		{30, 10, -1},
		{10, 20, -1},
		{9, 10, -1},
		{10, 9, -1},
		{51, 50, -1},
		{40, 10, 2},  // greyed: still hovered, so it still feeds HELPTEXT
		{10, 30, -1}, // hidden
		{0, 0, -1},   // panel header
	} {
		if got := w.HitTest(tc.x, tc.y); got != tc.want {
			t.Errorf("HitTest(%d,%d) = %d, want %d [07 §3][07 R-WGT-01 §1]", tc.x, tc.y, got, tc.want)
		}
	}
	if w.Fires(2) {
		t.Errorf("Fires(2) = true; a greyed button neither captures nor fires [07 R-WGT-01 §13]")
	}
	if !w.Fires(1) {
		t.Errorf("Fires(1) = false; an active, un-greyed button fires [07 R-WGT-01 §13]")
	}
	if w.Fires(3) || w.Fires(0) {
		t.Errorf("a hidden gadget and the header record never fire [07 R-WGT-01 §1]")
	}
}

func TestWindowHitTestUsesPlacedCoordinates(t *testing.T) {
	// Window-local gadget coordinates are translated by the authored origin;
	// the production UI uses Window.HitTest through ui.Panel.
	w := &Window{
		OriginX: 100,
		OriginY: 50,
		Gadgets: []Gadget{
			{Kind: KindPanel, Active: 1},
			{Kind: KindButton, Rect: Rect{X: 5, Y: 7, W: 4, H: 3}, Active: 1},
		},
	}
	if got := w.HitTest(105, 57); got != 1 {
		t.Fatalf("HitTest placed top-left = %d, want 1", got)
	}
	if got := w.HitTest(108, 59); got != 1 {
		t.Fatalf("HitTest placed bottom-right = %d, want 1", got)
	}
	if got := w.HitTest(109, 59); got != -1 {
		t.Fatalf("HitTest placed outside = %d, want -1", got)
	}
}
