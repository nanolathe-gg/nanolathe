package ebitenapp

import "testing"

// The confinement rectangle must admit exactly the device pixels Ebitengine
// reports as logical 0..w-1 (truncating toward zero), so both exact camera
// edges stay reachable and no pixel reports a coordinate off the canvas.
func TestPresentedCursorRectAdmitsExactlyTheCanvas(t *testing.T) {
	for _, tc := range []struct{ cw, ch, w, h int }{
		{3440, 1440, 1024, 768}, // the reported ultrawide pillarbox
		{3440, 1440, 640, 480},
		{1920, 1080, 800, 600},
		{1280, 1024, 1024, 768}, // letterbox top and bottom
		{1024, 768, 1024, 768},  // exact fit
		{2560, 1600, 1600, 1200},
	} {
		left, top, right, bottom, ok := presentedCursorRect(tc.cw, tc.ch, tc.w, tc.h)
		if !ok {
			t.Fatalf("%v: no rectangle", tc)
		}
		s := min(float64(tc.cw)/float64(tc.w), float64(tc.ch)/float64(tc.h))
		ox := (float64(tc.cw) - float64(tc.w)*s) / 2
		oy := (float64(tc.ch) - float64(tc.h)*s) / 2
		logical := func(p int32, o float64) int { return int((float64(p) - o) / s) }
		inside := func(p int32, o float64, n int) bool {
			v := (float64(p) - o) / s
			return v > -1 && int(v) < n
		}
		if logical(left, ox) != 0 || logical(right-1, ox) != tc.w-1 ||
			logical(top, oy) != 0 || logical(bottom-1, oy) != tc.h-1 {
			t.Fatalf("%v: rect [%d,%d)x[%d,%d) misses a canvas edge", tc, left, right, top, bottom)
		}
		if left > 0 && inside(left-1, ox, tc.w) || right < int32(tc.cw) && inside(right, ox, tc.w) ||
			top > 0 && inside(top-1, oy, tc.h) || bottom < int32(tc.ch) && inside(bottom, oy, tc.h) {
			t.Fatalf("%v: rect [%d,%d)x[%d,%d) excludes a canvas pixel", tc, left, right, top, bottom)
		}
		if left > 0 && !inside(left, ox, tc.w) || right < int32(tc.cw) && inside(right, ox, tc.w) {
			t.Fatalf("%v: rect admits a bar pixel", tc)
		}
	}
	if _, _, _, _, ok := presentedCursorRect(0, 1440, 1024, 768); ok {
		t.Fatal("empty client produced a rectangle")
	}
}
