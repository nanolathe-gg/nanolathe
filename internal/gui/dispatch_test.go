package gui

import "testing"

func TestWindowHitTestInclusiveMatrix(t *testing.T) {
	// [07 §3][07 §4] inclusive bounds after runtime placement; hidden, grayed,
	// and the non-interactive panel header do not receive hits.
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
		{40, 10, -1}, // grayed
		{10, 30, -1}, // hidden
		{0, 0, -1},   // panel header
	} {
		if got := w.HitTest(tc.x, tc.y); got != tc.want {
			t.Errorf("HitTest(%d,%d) = %d, want %d [07 §3][07 §4]", tc.x, tc.y, got, tc.want)
		}
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
