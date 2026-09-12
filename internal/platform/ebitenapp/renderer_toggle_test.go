package ebitenapp

import (
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// F10 swaps executors between two Updates (docs/DESIGN_GPU_RENDERER.md §14.6).
// The window cannot be driven from a test, so the request-servicing half is
// exercised directly: one request is one swap, classic takes over with the
// client's blended view turned off and a presentation pending, and modern
// leaves the arming to its own next Draw.
func TestRendererRequestSwapsExecutorsOncePerRequest(t *testing.T) {
	c, err := client.New(client.Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{c: c, mode: RendererModern, interpolating: true}
	c.SetInterpolation(true)

	// No request, no swap: an Update that services nothing must leave the
	// adapter exactly as it was.
	a.serviceRendererRequest()
	if a.mode != RendererModern || !a.interpolating {
		t.Fatalf("adapter changed with no request: mode=%v interpolating=%v", a.mode, a.interpolating)
	}

	c.RequestRendererToggle()
	a.serviceRendererRequest()
	if a.mode != RendererClassic {
		t.Fatalf("mode after one request = %v, want classic", a.mode)
	}
	if a.interpolating {
		t.Error("classic took over with the blended view still armed")
	}
	if !a.presentPending {
		t.Error("classic took over without a pending presentation, so nothing would reach the screen until the next update")
	}

	// Servicing again without a new request is a no-op: the count is what the
	// adapter compares, so a swap cannot be applied twice.
	a.presentPending = false
	a.serviceRendererRequest()
	if a.mode != RendererClassic || a.presentPending {
		t.Fatalf("second service without a request swapped again: mode=%v pending=%v", a.mode, a.presentPending)
	}

	c.RequestRendererToggle()
	a.serviceRendererRequest()
	if a.mode != RendererModern {
		t.Fatalf("mode after the second request = %v, want modern", a.mode)
	}
	if a.interpolating {
		t.Error("modern armed the blended view before its first Draw")
	}
}

func TestLivePresentationPreferencesAndF10Persistence(t *testing.T) {
	c, err := client.New(client.Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	mode, fps := RendererModern, 60
	changes := 0
	a := &app{c: c, mode: RendererModern, interpolating: true}
	a.options.PresentationSettings = func() (RendererMode, int) { return mode, fps }
	a.options.RendererChanged = func(changed RendererMode) {
		if a.mode != changed {
			t.Error("renderer callback ran before applying the swap")
		}
		mode = changed
		changes++
	}
	c.SetInterpolation(true)
	c.SetEnhanced(true)
	a.pipe.armed = true
	mode, fps = RendererClassic, 30
	a.syncPresentationSettings()
	if a.mode != RendererClassic || a.interpolating || c.Enhanced() || a.pipe.armed || !a.presentPending {
		t.Fatalf("live classic selection left stale presentation: mode=%v interpolation=%t enhanced=%t pipeline=%t pending=%t",
			a.mode, a.interpolating, c.Enhanced(), a.pipe.armed, a.presentPending)
	}
	if changes != 0 || a.presentInterval != time.Second/30 {
		t.Fatalf("external selection notified %d times, interval=%v", changes, a.presentInterval)
	}

	// A shortcut applied after polling updates the owner before the next poll.
	c.RequestRendererToggle()
	a.serviceRendererRequest()
	a.syncPresentationSettings()
	a.serviceRendererRequest()
	if a.mode != RendererModern || mode != RendererModern || changes != 1 || !c.Enhanced() {
		t.Fatalf("F10 was lost or repeated: adapter=%v owner=%v changes=%d enhanced=%t", a.mode, mode, changes, c.Enhanced())
	}
	// A cap-only edit leaves the active renderer and interpolation intact.
	a.interpolating = true
	a.pipe.armed = true
	a.presentPending = false
	for _, next := range []int{60, 120, 0} {
		fps = next
		a.syncPresentationSettings()
		want := time.Duration(0)
		if fps > 0 {
			want = time.Second / time.Duration(fps)
		}
		if a.presentInterval != want || !a.interpolating || !a.pipe.armed || a.presentPending || changes != 1 {
			t.Fatalf("cap %d changed renderer state or interval: interval=%v interpolation=%t pipeline=%t pending=%t callbacks=%d",
				fps, a.presentInterval, a.interpolating, a.pipe.armed, a.presentPending, changes)
		}
	}
}

func TestPresentationPreferencesWithoutCallbackKeepRunOptions(t *testing.T) {
	a := &app{mode: RendererClassic, options: RunOptions{MaxFPS: 90}}
	a.syncPresentationSettings()
	if a.mode != RendererClassic || a.presentInterval != time.Second/90 {
		t.Fatalf("fallback changed: mode=%v interval=%v", a.mode, a.presentInterval)
	}
	a.options.MaxFPS = 0
	a.syncPresentationSettings()
	if a.presentInterval != 0 {
		t.Fatalf("display refresh interval = %v", a.presentInterval)
	}
}
