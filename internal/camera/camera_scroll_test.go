package camera

import "testing"

// The scroll magnitude is setting * rawDelta capped at 128 map pixels, where
// rawDelta counts thirtieths of a second elapsed since the previous host frame
// [07 §10 "Correction — the raw delta is thirtieths of a second"]. The cap is a
// signed comparison with no absolute value, so a negative delta (a wrapped host
// tick count) scrolls the opposite way for one frame instead of being folded
// back to a positive magnitude.
func TestScrollMagnitudeIsSignedAndCapped(t *testing.T) {
	newCam := func() *Camera {
		return &Camera{X: 2000, Z: 2000, ViewW: 640, ViewH: 480, MapW: 8192, MapH: 8192}
	}
	cases := []struct {
		name     string
		setting  byte
		rawDelta int32
		dir      Direction
		wantDX   int32
		wantDZ   int32
	}{
		// One thirtieth elapsed at the default setting byte is one setting's
		// worth of map pixels, not the cap.
		{"one unit right", 32, 1, DirectionRight, 32, 0},
		{"one unit left", 32, 1, DirectionLeft, -32, 0},
		{"one unit up", 32, 1, DirectionUp, 0, -32},
		{"one unit down", 32, 1, DirectionDown, 0, 32},
		// Three thirtieths (a 100 ms frame) is still under the cap.
		{"three units right", 32, 3, DirectionRight, 96, 0},
		// Four thirtieths reaches the cap exactly; five is clamped to it.
		{"cap reached", 32, 4, DirectionRight, 128, 0},
		{"cap clamped", 32, 5, DirectionRight, 128, 0},
		// A wrapped host counter yields a negative delta: the > 128 test is
		// signed, so the magnitude keeps its sign and reverses the direction.
		{"negative delta reverses", 32, -1, DirectionRight, -32, 0},
		{"negative delta reverses down", 32, -2, DirectionDown, 0, -64},
		// Zero skips the pass entirely.
		{"zero delta", 32, 0, DirectionRight, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCam()
			c.Scroll(tc.setting, tc.rawDelta, tc.dir)
			if gotDX, gotDZ := c.X-2000, c.Z-2000; gotDX != tc.wantDX || gotDZ != tc.wantDZ {
				t.Fatalf("Scroll(%d,%d,%v) moved (%d,%d), want (%d,%d)",
					tc.setting, tc.rawDelta, tc.dir, gotDX, gotDZ, tc.wantDX, tc.wantDZ)
			}
		})
	}
}
