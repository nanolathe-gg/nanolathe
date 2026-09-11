package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Preserve ordinary scrolling while excluding every momentum-only batch from
// zoom, including a tail arriving well after its cooldown (DESIGN_GPU_RENDERER §16.6).
func TestMomentumTailCannotAdvanceZoomAfterCooldown(t *testing.T) {
	var collector scrollCollector
	collector.setActive(true)
	cam := &camera.Camera{ViewW: 1024, ViewH: 768, MapW: 16384, MapH: 16384,
		Scale: camera.ViewScaleDetail, Zoom: camera.ZoomMax}
	var zoom camera.ZoomController
	in := input.NewState()
	poll := func(now uint32) {
		batch := collector.take(0, -99) // Ebiten's parallel copy must not replay.
		applyInput(in, sampledInput{wheelX: float32(batch.x), wheelY: float32(batch.y), zoomWheelY: float32(batch.zoomY)})
		// Exercise the host sample's value-copy boundary too.
		copy := input.StateFromSample(input.SampleFromState(in, 0, 1024, 768))
		if copy.Mouse.ScrollY != float32(batch.y) {
			t.Fatal("GUI lost total scroll")
		}
		if copy.Mouse.ZoomScrollY != 0 {
			zoom.Wheel(cam, 500, 300, float64(copy.Mouse.ZoomScrollY), now)
		}
	}
	// Active scrolling and the first momentum event may share a host poll.
	collector.add(0, -1, false)
	collector.add(0, -8, true)
	poll(0)
	if zoom.Target(cam) != camera.ZoomUnit || in.Mouse.ScrollY != -9 {
		t.Fatal("initial flick did not stop at native")
	}
	for _, now := range []uint32{100, 499, 500, 900, 1600, 3000} {
		collector.add(0, -2, true)
		poll(now)
		if zoom.Target(cam) != camera.ZoomUnit {
			t.Fatalf("momentum advanced zoom at %dms", now)
		}
	}
	poll(3100)
	if in.Mouse.Scrolled() || zoom.Target(cam) != camera.ZoomUnit {
		t.Fatal("empty poll replayed wheel input")
	}
	collector.add(0, -1, false)
	poll(3200)
	if zoom.Target(cam) != camera.ZoomUnit/2 {
		t.Fatal("fresh deliberate scroll did not advance")
	}
	// A new finger gesture can oppose residual momentum in the same poll.
	// Even when total scrolling cancels, its direct zoom input survives.
	collector.add(0, 1, false)
	collector.add(0, -1, true)
	poll(4000)
	if in.Mouse.ScrollY != 0 || zoom.Target(cam) != camera.ZoomUnit {
		t.Fatal("opposing momentum hid deliberate scroll")
	}
	in.Mouse.ResetEdges()
	if in.Mouse.ZoomScrollY != 0 {
		t.Fatal("reset retained direct scrolling")
	}
}

func TestScrollCollectorFallbackAndRestart(t *testing.T) {
	var collector scrollCollector
	if got := collector.take(2, 1); got != (scrollBatch{2, 1, 1}) {
		t.Fatalf("fallback = %+v", got)
	}
	collector.setActive(true)
	collector.add(2, 3, false)
	collector.add(4, 5, true)
	if got := collector.take(99, 99); got != (scrollBatch{6, 8, 3}) {
		t.Fatalf("mixed batch = %+v", got)
	}
	collector.add(0, 20, true)
	collector.setActive(false)
	collector.setActive(true)
	if got := collector.take(99, 99); got != (scrollBatch{}) {
		t.Fatalf("restart retained events: %+v", got)
	}
}
