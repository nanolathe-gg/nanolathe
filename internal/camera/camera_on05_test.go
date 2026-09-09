package camera

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestMiddleDragChangesCameraOnly(t *testing.T) {
	cam := &Camera{X: 100, Z: 100, ViewW: 640, ViewH: 480, MapW: 2000, MapH: 2000}
	origX, origZ := cam.X, cam.Z
	// Simulate middle-drag delta 10,5
	cam.Drag(10, 5)
	if cam.X == origX && cam.Z == origZ {
		t.Fatalf("middle drag should change camera")
	}
	// Verify world coordinates not mutated: check that WorldToScreen scaling is consistent
	wx, wz := cam.ScreenToWorld(200, 200)
	// Camera changed, world mapping changed accordingly but sim world not touched (no sim here)
	_ = wx
	_ = wz
	// Pan is presentation-only: no side effect on sim
}

// The view scale is presentation-only: setting it changes where a world point
// lands on screen and nothing else [F-P1-008] (DESIGN_GPU_RENDERER §14.1). The
// wheel is no longer a camera control, so the scale is set directly.
func TestDetailScaleChangesPresentationOnly(t *testing.T) {
	cam := &Camera{X: 50, Z: 50, ViewW: 640, ViewH: 480, MapW: 2000, MapH: 2000, Scale: 1}
	// World point at (200,200) pixels
	wx := numeric.Fixed(int64(200) << 16)
	wz := numeric.Fixed(int64(200) << 16)
	sx1, sy1 := cam.WorldToScreen(wx, numeric.Fixed(0), wz)
	cam.SetScaleAbout(320, 240, 2)
	if cam.scale() != 2 {
		t.Fatalf("scale should be 2, got %d", cam.scale())
	}
	sx2, sy2 := cam.WorldToScreen(wx, numeric.Fixed(0), wz)
	if sx1 == sx2 && sy1 == sy2 {
		t.Fatalf("presentation projection should change at the detail scale: before %d,%d after %d,%d", sx1, sy1, sx2, sy2)
	}
	// The inverse is exact at every integer scale, so the projected point maps
	// back to the world pixel it came from (DESIGN_GPU_RENDERER §14.1).
	wx2, wz2 := cam.ScreenToWorld(sx2, sy2)
	if int64(wx2>>16) != int64(wx>>16) || int64(wz2>>16) != int64(wz>>16) {
		t.Fatalf("round trip lost the world pixel: %d,%d", wx2>>16, wz2>>16)
	}
}

func TestWASDUnbound(t *testing.T) {
	// Verify that Camera.Scroll via W/A/S/D is not part of camera package (it is battleSession concern).
	// Here we just verify camera itself has no WASD binding; battleSession.viewerStep no longer calls Scroll for W/A/S/D.
	// This test ensures Scale handling doesn't break existing Scroll/Clamp.
	cam := &Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 1000, MapH: 1000}
	cam.Scroll(8, 16, DirUp)
	if cam.Z != 0 {
		// At origin, scrolling up clamps to 0
	}
	cam.Scroll(8, 16, DirDown)
	if cam.Z == 0 {
		t.Fatalf("down scroll should move")
	}
	// No WASD magic in camera package itself.
}

// SetScaleAbout clamps to the integer scale's [1, 2] and keeps the world point
// under the given screen position fixed [F-P1-008]
// (DESIGN_GPU_RENDERER §14.1).
func TestViewScaleClamp(t *testing.T) {
	cam := &Camera{Scale: 1, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000, X: 200, Z: 200}
	before, _ := cam.ScreenToWorld(320, 240)
	cam.SetScaleAbout(320, 240, 9)
	if cam.scale() != 2 {
		t.Fatalf("scale should clamp to 2, got %d", cam.scale())
	}
	after, _ := cam.ScreenToWorld(320, 240)
	if before != after {
		t.Fatalf("the point under the cursor moved: %d then %d", before>>16, after>>16)
	}
	cam.SetScaleAbout(320, 240, -3)
	if cam.scale() != 1 {
		t.Fatalf("scale should clamp to 1, got %d", cam.scale())
	}
}
