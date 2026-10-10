package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Two empty publications enable the actual Enhanced camera blend without
// retail assets or a simulation (DESIGN_GPU_RENDERER §13.5).
func chromeCameraFixture(t *testing.T, width, height int) *battleSession {
	t.Helper()
	buf := frame.NewBuffer()
	cam := &camera.Camera{X: 1000, Z: 1000, ViewW: int32(width), ViewH: int32(height), MapW: 8192, MapH: 8192}
	cl, err := client.New(client.Options{Buffer: buf, Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	cl.SetCamera(cam)
	cl.SetEnhanced(true)
	cl.SetInterpolation(true)
	cl.SetTickFraction(0.5)
	cl.SetCameraFraction(0.5)
	for tick := uint32(1); tick <= 2; tick++ {
		buf.BeginWrite()
		if err := buf.Publish(tick); err != nil {
			t.Fatal(err)
		}
		cl.Step(1.0 / 30)
	}
	prefs := settings.DefaultPresentation()
	prefs.UIScale = 1
	b := &battleSession{cl: cl, cam: cam, hostPresentation: &prefs}
	cl.SetUIStage(battleHUDUIStage{battle: b})
	return b
}

func chromeWorldCenter(cam *camera.Camera) (int32, int32) {
	x, z := cam.BattleViewOrigin()
	w, h := cam.BattleView()
	return x + w/2, z + h/2
}

func assertChromeWorldMarker(t *testing.T, list *drawlist.List, width, height int, scale int32) {
	t.Helper()
	spaces := list.WorldSpaces()
	left, top := railInset(scale), camera.OriginY*scale
	want := drawlist.Rect{X: left, Y: top, W: int32(width) - left, H: int32(height) - 2*top}
	if len(spaces) == 0 || !spaces[0].Begin || spaces[0].Viewport != want {
		t.Fatalf("first world marker = %+v, want viewport %+v", spaces, want)
	}
}

// A layout change must persist on the real host camera and take effect in the
// first world marker, before DrawUI runs. Snapping both samples also keeps the
// old viewport's world centre at every camera fraction (Modern UI scale).
func TestChromeScaleChangesBeforeBlendedWorldRecording(t *testing.T) {
	b := chromeCameraFixture(t, 640, 480)
	cx, cz := chromeWorldCenter(b.cam)
	for _, k := range []int{1, 2, 1} {
		b.hostPresentation.UIScale = k
		for _, fraction := range []float32{0.25, 0.5, 0.75} {
			b.cl.SetCameraFraction(fraction)
			b.cl.BeginPresentationFrame()
			left := railInset(int32(k))
			wantChrome := camera.ChromeInsets{}
			if k > 1 {
				wantChrome = camera.ChromeInsets{Left: left, Top: camera.OriginY * int32(k), Bottom: camera.OriginY * int32(k)}
			}
			if b.cam.Chrome != wantChrome {
				t.Fatalf("%dx: retained Chrome = %+v, want %+v", k, b.cam.Chrome, wantChrome)
			}
			x, z := chromeWorldCenter(b.cam)
			if x != cx || z != cz {
				t.Fatalf("%dx: world centre = (%d,%d), want (%d,%d)", k, x, z, cx, cz)
			}
			d := b.cl.PresentationDigest()
			view := b.cam.PresentationView()
			if d.CamSamples != 2 || d.CamPrevView != view || d.CamCurView != view {
				t.Fatalf("%dx: camera samples = %+v / %+v, want installed view %+v", k, d.CamPrevView, d.CamCurView, view)
			}
			before := *b.cam
			assertChromeWorldMarker(t, b.cl.RecordModernFrame(), 640, 480, int32(k))
			if *b.cam != before {
				t.Fatalf("%dx: recording changed the retained camera", k)
			}
		}
	}
}

// The capture boundary and a resize that crosses Auto's threshold both prepare
// camera insets and modal layout before their first world recording.
func TestChromeScalePreparesFirstFrameAndAutoResize(t *testing.T) {
	b := chromeCameraFixture(t, 2560, 1440)
	b.hostPresentation.UIScale = 0
	h := &retailBattleHUD{}
	b.cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
	for _, size := range [][2]int{{2560, 1440}, {2560, 720}, {1280, 1440}, {1281, 1441}, {2560, 1440}} {
		width, height := size[0], size[1]
		b.cl.Resize(width, height)
		b.cam.ViewW, b.cam.ViewH = int32(width), int32(height)
		cx, cz := chromeWorldCenter(b.cam)
		b.cl.BeginPresentationFrame()
		k := int32(1)
		if height >= 1440 && width >= 1281 {
			k = 2
		}
		if h.screenW != int32(width) || h.screenH != int32(height) || h.chromeScale != k || h.placedScale != k {
			t.Fatalf("height %d: HUD size/scale = %dx%d at %d/%d", height, h.screenW, h.screenH, h.chromeScale, h.placedScale)
		}
		if b.stripRegion().OffsetY != int32(height)%k {
			t.Fatal("bottom strip did not retain the framebuffer's final row")
		}
		if x, z := chromeWorldCenter(b.cam); x != cx || z != cz {
			t.Fatalf("height %d: inset change moved the world centre", height)
		}
		// The HUD needs no art for preparation. Leave it out of pure recording.
		b.cl.SetUIStage(battleHUDUIStage{battle: b})
		assertChromeWorldMarker(t, b.cl.RecordModernFrame(), width, height, k)
		b.cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
	}
	// A fixed-inset capture and Classic both return to retail's viewport.
	b.chromeFixed = true
	b.cl.BeginPresentationFrame()
	if b.cam.Chrome != (camera.ChromeInsets{}) || h.chromeScale != 1 {
		t.Fatal("fixed capture retained magnified chrome")
	}
	b.chromeFixed = false
	b.cl.SetEnhanced(false)
	b.cl.BeginPresentationFrame()
	if b.cam.Chrome != (camera.ChromeInsets{}) || h.chromeScale != 1 {
		t.Fatal("Classic retained magnified chrome")
	}
}
