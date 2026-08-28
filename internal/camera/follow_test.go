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

func TestDesiredOriginUsesSignedHeightShear(t *testing.T) {
	cam := &Camera{ViewW: 100, ViewH: 80, MapW: 1000, MapH: 1000}
	got := cam.DesiredOrigin(TargetPoint{X: 300, Y: -3, Z: 400})
	// -3/2 truncates toward zero, so the vertical shear contributes +1.
	want := Origin{X: 250, Z: 361}
	if got != want {
		t.Fatalf("desired origin: got %+v, want %+v", got, want)
	}
}

func TestDesiredOriginUsesOrderedClamp(t *testing.T) {
	cam := &Camera{ViewW: 200, ViewH: 20, MapW: 100, MapH: 100}
	if got := cam.DesiredOrigin(TargetPoint{X: 50, Z: 10}); got.X != 0 {
		t.Fatalf("negative desired origin should clamp to zero, got %d", got.X)
	}
	if got := cam.DesiredOrigin(TargetPoint{X: 200, Z: 10}); got.X != -100 {
		t.Fatalf("positive desired origin should then clamp to negative maximum, got %d", got.X)
	}
}

func TestFollowToLeavesCurrentClampForLater(t *testing.T) {
	cam := &Camera{X: 1300, ViewW: 100, MapW: 1000, ViewH: 100, MapH: 1000}
	desired := cam.FollowTo(TargetPoint{X: 950, Z: 50})
	if desired.X != 900 || cam.X != 980 {
		t.Fatalf("follow: desired=%+v current=(%d,%d), want desired X 900/current X 980", desired, cam.X, cam.Z)
	}
	cam.Clamp()
	if cam.X != 900 {
		t.Fatalf("final clamp: got X %d, want 900", cam.X)
	}
}

func TestDesiredOriginUsesEffectiveZoomedViewport(t *testing.T) {
	cam := &Camera{Scale: 2, ViewW: 200, ViewH: 100, MapW: 1000, MapH: 1000}
	got := cam.DesiredOrigin(TargetPoint{X: 300, Z: 200})
	want := Origin{X: 250, Z: 175}
	if got != want {
		t.Fatalf("zoomed desired origin: got %+v, want %+v", got, want)
	}
}
