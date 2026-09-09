package ebitenapp

import (
	"testing"

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
