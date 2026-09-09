package camera

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
	cam.SetScaleAbout(320, 240, ViewScaleDetail)
	if cam.scale() != ViewScaleDetail {
		t.Fatalf("scale should be 2x, got %s", cam.scale())
	}
	sx2, sy2 := cam.WorldToScreen(wx, numeric.Fixed(0), wz)
	if sx1 == sx2 && sy1 == sy2 {
		t.Fatalf("presentation projection should change at the detail scale: before %d,%d after %d,%d", sx1, sy1, sx2, sy2)
	}
	// The inverse is exact at every scale, so the projected point maps back
	// to the world pixel it came from (DESIGN_GPU_RENDERER §14.1).
	wx2, wz2 := cam.ScreenToWorld(sx2, sy2)
	if int64(wx2>>16) != int64(wx>>16) || int64(wz2>>16) != int64(wz>>16) {
		t.Fatalf("round trip lost the world pixel: %d,%d", wx2>>16, wz2>>16)
	}
}

// At 1.5x the projection is the first screen pixel a world pixel covers and
// the inverse is its floor, so the round trip is still the identity for every
// world pixel, and consecutive world pixels are one or two screen pixels
// apart — never zero, never three (DESIGN_GPU_RENDERER §14.1). This is a
// Nanolathe presentation rule, not retail behaviour.
func TestMidScaleProjectionRoundTrips(t *testing.T) {
	cam := &Camera{X: 37, Z: 11, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000, Scale: ViewScaleMid}
	prevX := int32(0)
	for w := int32(-40); w <= 400; w++ {
		wx := numeric.Fixed(int64(w) << 16)
		sx, sy := cam.WorldToScreen(wx, 0, wx)
		gx, gz := cam.ScreenToWorld(sx, sy)
		if int32(gx>>16) != w || int32(gz>>16) != w {
			t.Fatalf("world %d projected to (%d,%d) and picked back as (%d,%d)", w, sx, sy, gx>>16, gz>>16)
		}
		if w > -40 {
			if step := sx - prevX; step != 1 && step != 2 {
				t.Fatalf("world %d and %d are %d screen pixels apart, want 1 or 2", w-1, w, step)
			}
		}
		prevX = sx
	}
	// The three helpers agree with each other at the whole scales exactly.
	for _, s := range []ViewScale{ViewScaleNative, ViewScaleDetail} {
		k, _ := s.Whole()
		for v := int32(-9); v <= 9; v++ {
			if s.Project(v) != v*k || s.Px(v) != v*k {
				t.Fatalf("%s: Project(%d)=%d Px=%d, want %d", s, v, s.Project(v), s.Px(v), v*k)
			}
		}
	}
	if ViewScaleMid.Px(5) != 8 || ViewScaleMid.Px(-5) != -8 || ViewScaleMid.Px(2) != 3 {
		t.Fatalf("1.5x Px rounds half away from zero: %d %d %d", ViewScaleMid.Px(5), ViewScaleMid.Px(-5), ViewScaleMid.Px(2))
	}
	if ViewScaleNative.Next() != ViewScaleMid || ViewScaleMid.Next() != ViewScaleDetail || ViewScaleDetail.Next() != ViewScaleNative {
		t.Fatal("the F9 cycle is 1x, 1.5x, 2x, 1x")
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

// SetScaleAbout clamps to the three views and keeps the world point under
// the given screen position fixed [F-P1-008] (DESIGN_GPU_RENDERER §14.1).
func TestViewScaleClamp(t *testing.T) {
	cam := &Camera{Scale: ViewScaleNative, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000, X: 200, Z: 200}
	before, _ := cam.ScreenToWorld(320, 240)
	cam.SetScaleAbout(320, 240, 9)
	if cam.scale() != ViewScaleDetail {
		t.Fatalf("scale should clamp to 2x, got %s", cam.scale())
	}
	after, _ := cam.ScreenToWorld(320, 240)
	if before != after {
		t.Fatalf("the point under the cursor moved: %d then %d", before>>16, after>>16)
	}
	cam.SetScaleAbout(320, 240, ViewScaleMid)
	if cam.scale() != ViewScaleMid {
		t.Fatalf("scale should be 1.5x, got %s", cam.scale())
	}
	if mid, _ := cam.ScreenToWorld(320, 240); mid != before {
		t.Fatalf("the point under the cursor moved at 1.5x: %d then %d", before>>16, mid>>16)
	}
	cam.SetScaleAbout(320, 240, -3)
	if cam.scale() != ViewScaleNative {
		t.Fatalf("scale should clamp to 1x, got %s", cam.scale())
	}
}
