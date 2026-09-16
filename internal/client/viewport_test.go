package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestViewportLogicalCursorCoordinates(t *testing.T) {
	tr := NewViewportTransform(nil, 0, 0)
	if tr.LogicalWidth != 640 || tr.LogicalHeight != 480 {
		t.Fatalf("defaults=(%d,%d), want (640,480)", tr.LogicalWidth, tr.LogicalHeight)
	}
	// Battle viewport is the drawn-chrome region [129,32]..[639,447] [07 §6][07 §8] C-3.
	if tr.Viewport != (ViewportRect{Left: 129, Top: 32, Right: 639, Bottom: 447}) {
		t.Fatalf("battle viewport=%+v, want drawn-chrome 511x416 rectangle at 129,32", tr.Viewport)
	}
	// Viewport.Contains is the production region predicate: the battle cursor
	// admits world input with it and treats everything else as HUD [07 §8].
	if !tr.Viewport.Contains(639, 447) {
		t.Fatal("last battle pixel was classified as HUD")
	}
	if tr.Viewport.Contains(640, 447) || tr.Viewport.Contains(129, 448) {
		t.Fatal("HUD boundary reached the world region")
	}
	for _, p := range []Point{{X: 128, Y: 100}, {X: 10, Y: 31}, {X: 640, Y: 100}, {X: 639, Y: 448}, {X: -1, Y: 10}} {
		if tr.Viewport.Contains(p.X, p.Y) {
			t.Fatalf("HUD point %+v reached the world region", p)
		}
	}
}

func TestViewportNegativeProjectionUsesArithmeticShift(t *testing.T) {
	tr := NewViewportTransform(&camera.Camera{}, 640, 480)
	beam := tr.WorldToBeam(numeric.Fixed(-1), 0, numeric.Fixed(-1))
	if beam != (Point{X: 127, Y: 31}) {
		t.Fatalf("negative beam point=%+v, want (127,31)", beam)
	}
}

func TestViewportSurfaceProjectionSharesBeamOrigin(t *testing.T) {
	cam := &camera.Camera{X: 40, Z: 24, ViewW: 512, ViewH: 416, MapW: 1024, MapH: 1024}
	v := NewViewportTransform(cam, 640, 480)
	beam := v.WorldToBeam(numeric.Fixed(40<<16), 0, numeric.Fixed(24<<16))
	surface := v.WorldToSurface(numeric.Fixed(40<<16), 0, numeric.Fixed(24<<16))
	if beam != (Point{X: camera.OriginX, Y: camera.OriginY}) {
		t.Fatalf("beam origin=%+v", beam)
	}
	// The surface origin is the beam origin with the beam offset taken off,
	// which is the whole of WorldToSurface.
	if surface != (Point{}) {
		t.Fatalf("surface origin=%+v", surface)
	}
}
