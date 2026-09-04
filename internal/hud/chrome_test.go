package hud

import "testing"

// The strip loop is retail's "advance by the frame width while x < width":
// the stock frames (ARM PANELTOP 513, CORE PANELTOP 511, both bottoms about
// 512) reach the 640 edge in one stamp, and a wider surface earns extension
// stamps whose first origin is the rail boundary plus the first frame's width
// [07 R-HUD-03 §4][07 R-HUD-05].
func TestStripStamps(t *testing.T) {
	cases := []struct {
		screenW, firstW, restW int32
		want                   []int32
	}{
		{640, 513, 513, []int32{129}},
		{640, 511, 512, []int32{129}},
		{800, 513, 513, []int32{129, 642}},
		{800, 511, 512, []int32{129, 640}},
		{1024, 513, 513, []int32{129, 642}},
		{1600, 511, 512, []int32{129, 640, 1152}},
		{640, 513, 0, []int32{129}},
	}
	for _, c := range cases {
		got := StripStamps(c.screenW, c.firstW, c.restW)
		if len(got) != len(c.want) {
			t.Fatalf("StripStamps(%d,%d,%d) = %v, want %v", c.screenW, c.firstW, c.restW, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("StripStamps(%d,%d,%d) = %v, want %v", c.screenW, c.firstW, c.restW, got, c.want)
			}
		}
	}
}

func TestBottomStripAndRailGap(t *testing.T) {
	if got := BottomStripY(480); got != 448 {
		t.Fatalf("BottomStripY(480) = %d, want 448", got)
	}
	if got := BottomStripY(600); got != 568 {
		t.Fatalf("BottomStripY(600) = %d, want 568", got)
	}
	if _, ok := RailGap(480, 129, 480); ok {
		t.Fatalf("RailGap at 640x480 must be empty")
	}
	r, ok := RailGap(600, 129, 480)
	if !ok || r != (Rect{X1: 0, Y1: 480, X2: 128, Y2: 599}) {
		t.Fatalf("RailGap(600,129,480) = %+v ok=%v", r, ok)
	}
}

// The battle modal centring at the authored size is the documented pair —
// the 150×155 exit window at (309,162) and the 400×100 confirmation at
// (184,190) [07 "Tab options menu and manual exit"] — and at 800×600 the same
// arithmetic runs on the live surface.
func TestModalPlacement(t *testing.T) {
	if x, y := ModalPlacement(640, 480, 150, 155); x != 309 || y != 162 {
		t.Fatalf("exit window at 640x480 = (%d,%d), want (309,162)", x, y)
	}
	if x, y := ModalPlacement(640, 480, 400, 100); x != 184 || y != 190 {
		t.Fatalf("confirm window at 640x480 = (%d,%d), want (184,190)", x, y)
	}
	if x, y := ModalPlacement(800, 600, 400, 100); x != 264 || y != 250 {
		t.Fatalf("confirm window at 800x600 = (%d,%d), want (264,250)", x, y)
	}
}
