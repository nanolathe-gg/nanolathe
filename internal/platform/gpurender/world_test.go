package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// worldRegion is the boundary marker a test opens its world region with.
func worldRegion(z camera.Zoom, step camera.ViewScale, recW, recH int32) drawlist.WorldSpace {
	return drawlist.WorldSpace{
		Begin: true, Zoom: z, Step: step, RecordW: recW, RecordH: recH,
		Viewport: drawlist.Rect{X: camera.OriginX, Y: camera.OriginY, W: 64, H: 64},
	}
}

// A world region on a rest step is the identity: the transform is disarmed and
// the compiled geometry is exactly what the same command compiled to before the
// region existed. That is what keeps the §6 parity gate exact at 1x and 2x
// (DESIGN_GPU_RENDERER §16.3).
func TestWorldRegionIsTheIdentityOnARestStep(t *testing.T) {
	r, _ := schedulerFixture(t)
	for _, tc := range []struct {
		z    camera.Zoom
		step camera.ViewScale
	}{
		{camera.ZoomUnit, camera.ViewScaleNative},
		{camera.ZoomMax, camera.ViewScaleDetail},
		{camera.ZoomOf(camera.ViewScaleMid), camera.ViewScaleMid},
	} {
		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 4, Y: 6, W: 10, H: 8}, Index: 3})
		plain := append([]float32(nil), quadCorners(r)...)

		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.World(worldRegion(tc.z, tc.step, 64, 64))
		if r.sched.worldOn {
			t.Fatalf("%s on step %s armed the transform", tc.z, tc.step)
		}
		if r.clipW() != 64 || r.clipH() != 64 {
			t.Fatalf("%s: clip extent %dx%d, want the framebuffer", tc.z, r.clipW(), r.clipH())
		}
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 4, Y: 6, W: 10, H: 8}, Index: 3})
		got := quadCorners(r)
		if len(got) != len(plain) {
			t.Fatalf("%s: %d compiled corners, want %d", tc.z, len(got), len(plain))
		}
		for i := range got {
			if got[i] != plain[i] {
				t.Fatalf("%s: corner %d = %v, want %v (the region must be the identity)", tc.z, i, got[i], plain[i])
			}
		}
	}
}

// Off a rest step the region scales every world command by the live factor over
// the record step, about the surface origin — one affine transform, applied at
// the scheduler's intake so the placed rectangle and the appended vertices agree
// (§16.3).
func TestWorldRegionScalesAWorldQuad(t *testing.T) {
	r, frame := schedulerFixture(t)
	// 1.5x recorded at the 2x step: the recording shrinks by three quarters.
	r.sched.resetFrame(64, 64)
	r.worldW, r.worldH = 64, 64
	r.World(worldRegion(camera.ZoomOf(camera.ViewScaleMid), camera.ViewScaleDetail, 86, 86))
	if !r.sched.worldOn {
		t.Fatal("1.5x recorded at the 2x step left the transform disarmed")
	}
	if r.clipW() != 86 || r.clipH() != 86 {
		t.Fatalf("clip extent %dx%d, want the record extent 86x86", r.clipW(), r.clipH())
	}
	frame.Width, frame.Height = 8, 8
	frame.Pixels = make([]byte, 64)
	frame.Transparent = make([]bool, 64)
	r.Sprite(drawlist.Sprite{Frame: frame, X: 16, Y: 24, Kind: drawlist.BlitKeyed})
	got := quadCorners(r)
	if len(got) != 4 {
		t.Fatalf("compiled %d corners, want one quad", len(got))
	}
	// The recorded rectangle is (16,24)..(24,32); three quarters of it is
	// (12,18)..(18,24).
	want := []float32{12, 18, 18, 24}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("scaled quad = %v, want %v", got, want)
		}
	}

	// Closing the region disarms the transform, so the chrome that follows keeps
	// its authored pixel size.
	r.World(drawlist.WorldSpace{Begin: false})
	if r.sched.worldOn || r.clipW() != 64 || r.clipH() != 64 {
		t.Fatalf("closing the region left worldOn=%v clip=%dx%d", r.sched.worldOn, r.clipW(), r.clipH())
	}
}

// A world primitive that shrinks below one screen pixel keeps one, so the
// selection quad's lines and a dotted path's dots cannot fall between two pixel
// centres and vanish at an arbitrary subset of factors (§16.3).
func TestSubPixelWorldPrimitivesKeepOnePixel(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	r.worldW, r.worldH = 64, 64
	r.World(worldRegion(camera.ZoomUnit/4, camera.ViewScaleNative, 256, 256))
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 8, Y: 8, W: 1, H: 1}, Index: 5})
	got := quadCorners(r)
	if len(got) != 4 {
		t.Fatalf("compiled %d corners, want one quad", len(got))
	}
	if got[2]-got[0] < 1 || got[3]-got[1] < 1 {
		t.Fatalf("a one-pixel world fill compiled to %v, thinner than a screen pixel", got)
	}
}

// quadCorners returns the opaque batch's single quad as x0,y0,x1,y1.
func quadCorners(r *Renderer) []float32 {
	verts := r.sched.classVerts(schedOpaque)
	if len(verts) < 4 {
		return nil
	}
	return []float32{verts[0].DstX, verts[0].DstY, verts[3].DstX, verts[3].DstY}
}
