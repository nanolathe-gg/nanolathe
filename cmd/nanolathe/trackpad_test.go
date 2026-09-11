package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// These lock Nanolathe's presentation choices, not retail behavior (§16.6).
func TestPinchStopsAtNativeUntilANewGesture(t *testing.T) {
	b := zoomTestBattle()
	b.cam.Zoom = camera.ZoomMax
	feed := func(events ...input.PinchEvent) {
		b.applyTrackpadGestures(&input.MouseState{Pinches: events}, true, 400, 250)
		for i := 0; i < 100; i++ {
			b.zoom.Step(b.cam)
		}
	}
	feed(input.PinchEvent{Began: true, Delta: -0.05})
	if b.zoom.Target(b.cam) != camera.ZoomMax {
		t.Fatal("small pinch crossed threshold")
	}
	feed(input.PinchEvent{Delta: -0.08})
	if b.cam.EffectiveZoom() != camera.ZoomUnit {
		t.Fatal("pinch did not settle at native")
	}
	for i := 0; i < 30; i++ {
		feed(input.PinchEvent{Delta: -0.4})
	}
	feed(input.PinchEvent{Delta: 1}, input.PinchEvent{Ended: true})
	if b.zoom.Target(b.cam) != camera.ZoomUnit {
		t.Fatal("continued or reversed pinch skipped native")
	}
	feed(input.PinchEvent{Began: true, Delta: -0.2, Ended: true})
	if b.cam.EffectiveZoom() != camera.ZoomUnit/4 {
		t.Fatal("fresh pinch did not reach overview")
	}
	// Multiple complete gestures in one poll retain their own boundaries.
	feed(input.PinchEvent{Began: true, Delta: 0.2, Ended: true}, input.PinchEvent{Began: true, Delta: 0.2, Ended: true})
	if b.cam.EffectiveZoom() != camera.ZoomMax {
		t.Fatal("batched gestures merged")
	}
	// A gesture spent against the limit cannot reverse into a zoom later.
	feed(input.PinchEvent{Began: true, Delta: 0.3}, input.PinchEvent{Delta: -1})
	if b.cam.EffectiveZoom() != camera.ZoomMax {
		t.Fatal("limit did not spend gesture")
	}
}

func TestPinchCancellationAndViewportOwnership(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		b := zoomTestBattle()
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: 0.05}}}, true, 400, 250)
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Cancelled: cancel, Delta: 0.5}}, PanX: 200}, cancel, 400, 250)
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Delta: 0.5}}}, true, 400, 250)
		if b.zoom.Target(b.cam) != camera.ZoomUnit {
			t.Fatal("cancelled/blocked pinch reactivated")
		}
		if !cancel && b.cam.X != 400 {
			t.Fatal("blocked scroll panned")
		}
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: 0.2, Ended: true}}}, true, 400, 250)
		if b.zoom.Target(b.cam) != camera.ZoomMax {
			t.Fatal("new gesture stayed blocked")
		}
	}
}

func TestPinchAnchorsAtGestureStart(t *testing.T) {
	b := zoomTestBattle()
	b.cam.X, b.cam.Z = 2000, 1500
	px, py := int32(400), int32(250)
	wx, wz := b.cam.X+px, b.cam.Z+py
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true}}}, true, px, py)
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Delta: 0.2, Ended: true}}}, true, px+100, py+50)
	for i := 0; i < 100; i++ {
		b.zoom.Step(b.cam)
	}
	z := b.cam.EffectiveZoom()
	if gx, gy := z.Project(wx-b.cam.X), z.Project(wz-b.cam.Z); gx < px-2 || gx > px || gy < py-2 || gy > py {
		t.Fatalf("pinch start anchor moved to (%d,%d)", gx, gy)
	}
}

func TestTrackpadPanPreservesSmallDeltasAcrossZoomLevels(t *testing.T) {
	for _, zoom := range camera.ZoomSteps {
		b := zoomTestBattle()
		b.cam.Zoom = zoom
		for i := 0; i < 8; i++ {
			b.applyTrackpadGestures(&input.MouseState{PanX: 0.5, PanY: -0.25}, true, 400, 250)
		}
		if b.cam.X != 400-int32(4*camera.ZoomUnit/zoom) || b.cam.Z != 300+int32(2*camera.ZoomUnit/zoom) {
			t.Fatalf("at %v small deltas lost: (%d,%d)", zoom, b.cam.X, b.cam.Z)
		}
	}
}

func TestTrackpadCameraPassOwnership(t *testing.T) {
	for _, gate := range []string{"viewport", "classic", "unfocused", "chrome", "modal"} {
		t.Run(gate, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(300, 300))
			b.millisSource = &fakeMillisSource{}
			cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetCamera(b.cam)
			cl.SetFocused(true)
			cl.SetEnhanced(true)
			m := cl.Input().Mouse
			m.SetPosition(320, 240)
			b.viewerStep(0, cl)
			b.cam.X, b.cam.Z, b.cam.Zoom = 1000, 1000, camera.ZoomMax
			switch gate {
			case "classic":
				cl.SetEnhanced(false)
			case "unfocused":
				cl.SetFocused(false)
			case "chrome":
				m.SetPosition(50, 200)
			case "modal":
				b.openBattleMenu()
			}
			m.PanX, m.PanY = 20, 10
			m.Pinches = []input.PinchEvent{{Began: true, Delta: -0.2}}
			b.viewerStep(0, cl)
			if gate == "viewport" {
				if b.zoom.Target(b.cam) != camera.ZoomUnit {
					t.Fatal("viewport pinch was not dispatched")
				}
			} else if b.cam.X != 1000 || b.cam.Z != 1000 || b.zoom.Target(b.cam) != camera.ZoomMax {
				t.Fatalf("blocked camera input leaked: %+v", b.cam)
			}
		})
	}
}
