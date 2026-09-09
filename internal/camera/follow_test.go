package camera

import "testing"

func TestStepAxisBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		delta int32
		move  int32
	}{
		{name: "zero", delta: 0, move: 0},
		{name: "one", delta: 1, move: 0},
		{name: "negative one", delta: -1, move: 0},
		{name: "two", delta: 2, move: 1},
		{name: "negative two", delta: -2, move: -1},
		{name: "319", delta: 319, move: 159},
		{name: "negative 319", delta: -319, move: -159},
		{name: "320", delta: 320, move: 160},
		{name: "negative 320", delta: -320, move: -160},
		{name: "321", delta: 321, move: 320},
		{name: "negative 321", delta: -321, move: -320},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stepAxis(1000, 1000+test.delta) - 1000; got != test.move {
				t.Fatalf("stepAxis delta %d: got move %d, want %d", test.delta, got, test.move)
			}
		})
	}
}

// The desired origin centres the target in the *battle viewport* and clamps
// with the viewport's own bounds [07 R-CAM-01 §12][07 §10][03 §4.1]. At 640x480
// that is target - 128 - 512/2 on X and target - 32 - 416/2 on Z. These
// expectations changed on 2026-08-31: they previously halved the framebuffer
// and clamped to framebuffer bounds, which put a followed unit 64 map pixels
// right of centre (defect PT5-01).
func TestDesiredOriginUsesSignedHeightShear(t *testing.T) {
	cam := &Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	got := cam.DesiredOrigin(TargetPoint{X: 1000, Y: -3, Z: 900})
	// -3/2 truncates toward zero, so the vertical shear contributes +1:
	// 901 - 32 - 208 = 661. (Truncation here is a known divergence from the
	// arithmetic shift of [07 R-CRD-006 §1] and is unchanged by this test.)
	want := Origin{X: 1000 - 128 - 256, Z: 661}
	if got != want {
		t.Fatalf("desired origin: got %+v, want %+v", got, want)
	}
}

// The ordered clamp form survives the frame-of-reference conversion: the floor
// test still runs before the maximum test, so in the viewport-larger-than-map
// domain a target below the floor takes the floor and not the (smaller)
// maximum [07 §10]. That domain remains an open Unknown in [07 §10]; this test
// locks the ordering only.
func TestDesiredOriginUsesOrderedClamp(t *testing.T) {
	// A 100x100 map behind a 512x416 viewport: maximum = 100 - 512 - 128 = -540
	// on X, below the floor of -128.
	cam := &Camera{ViewW: 640, ViewH: 480, MapW: 100, MapH: 100}
	if got := cam.DesiredOrigin(TargetPoint{X: 50, Z: 10}); got.X != -OriginX || got.Z != -OriginY {
		t.Fatalf("below-floor target should take the floor, got %+v", got)
	}
	if got := cam.DesiredOrigin(TargetPoint{X: 2000, Z: 2000}); got.X != 100-512-OriginX || got.Z != 100-416-OriginY {
		t.Fatalf("above-maximum target should take the negative maximum, got %+v", got)
	}
}

func TestFollowToLeavesCurrentClampForLater(t *testing.T) {
	cam := &Camera{X: 1300, ViewW: 640, MapW: 1000, ViewH: 480, MapH: 1000}
	desired := cam.FollowTo(TargetPoint{X: 950, Z: 500})
	// 950 - 128 - 256 = 566, clamped to the maximum 1000 - 512 - 128 = 360.
	if desired.X != 360 || cam.X != 980 {
		t.Fatalf("follow: desired=%+v current=(%d,%d), want desired X 360/current X 980", desired, cam.X, cam.Z)
	}
	cam.Clamp()
	if cam.X != 360 {
		t.Fatalf("final clamp: got X %d, want 360", cam.X)
	}
}

// Presentation zoom scales the chrome insets with the view [F-P1-008]: at
// scale 2 the framebuffer shows 320x240 world pixels, the viewport 256x208 of
// them, and the leading insets are 64 and 16.
func TestDesiredOriginUsesEffectiveZoomedViewport(t *testing.T) {
	cam := &Camera{Scale: ViewScaleDetail, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	got := cam.DesiredOrigin(TargetPoint{X: 1000, Z: 800})
	want := Origin{X: 1000 - 64 - 128, Z: 800 - 16 - 104}
	if got != want {
		t.Fatalf("zoomed desired origin: got %+v, want %+v", got, want)
	}
	// The target lands on the viewport's centre pixel: the framebuffer position
	// is (world - camera) * scale, and the viewport centre is 128 + 512/2.
	if px := (1000 - got.X) * 2; px != 384 {
		t.Fatalf("zoomed target framebuffer X = %d, want 384", px)
	}
}
