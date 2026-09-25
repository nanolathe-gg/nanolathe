package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func publishTicks(t *testing.T, b *frame.Buffer, ticks ...uint32) {
	t.Helper()
	for _, tick := range ticks {
		b.BeginWrite()
		if err := b.Publish(tick); err != nil {
			t.Fatalf("publish %d: %v", tick, err)
		}
	}
}

// Under the asynchronous simulation presentation reads the pair the host names,
// holds it while the writer publishes on, and presents a named tick that is not
// published yet as the newest frame alone rather than blending toward it
// (DESIGN_GPU_RENDERER §13.13).
func TestPinPresentationFollowsTheNamedTick(t *testing.T) {
	buf := frame.NewBuffer()
	target, named := uint32(0), false
	c, err := New(Options{Buffer: buf, Width: 640, Height: 480,
		PresentationTick: func() (uint32, bool) { return target, named }})
	if err != nil {
		t.Fatal(err)
	}
	c.PinPresentation()
	if c.pin.buf != nil {
		t.Fatal("the synchronous path pinned a pair")
	}
	// Enabling widens the buffer's rotation before the writer runs beside it.
	c.SetAsyncSimulation(true)
	publishTicks(t, buf, 1, 2, 3)

	target, named = 2, true
	c.PinPresentation()
	if cur, prev := c.committedFrame(), c.committedPrevious(); cur == nil || cur.Tick != 2 || prev == nil || prev.Tick != 1 {
		t.Fatalf("named tick 2 pinned %v / %v", cur, prev)
	}
	// The writer publishes on; the pinned pair still reads ticks 1 and 2.
	publishTicks(t, buf, 4, 5, 6, 7, 8, 9, 10)
	if cur, prev := c.committedFrame(), c.committedPrevious(); cur.Tick != 2 || prev.Tick != 1 {
		t.Fatalf("pinned pair moved to %d/%d", cur.Tick, prev.Tick)
	}

	// A named tick the buffer does not hold presents the newest publication
	// alone.
	target = 20
	c.PinPresentation()
	if cur, prev := c.committedFrame(), c.committedPrevious(); cur == nil || cur.Tick != 10 || prev != nil {
		t.Fatalf("unavailable tick presented %v / %v, want tick 10 unblended", cur, prev)
	}

	// A declining producer presents the newest pair, blended.
	named = false
	c.PinPresentation()
	if cur, prev := c.committedFrame(), c.committedPrevious(); cur.Tick != 10 || prev == nil || prev.Tick != 9 {
		t.Fatalf("declined producer presented %v / %v", cur, prev)
	}

	c.SetAsyncSimulation(false)
	if c.pin.buf != nil {
		t.Fatal("leaving the asynchronous simulation kept a pin")
	}
	if cur := c.committedFrame(); cur == nil || cur.Tick != 10 {
		t.Fatalf("synchronous read = %v, want the current publication", cur)
	}
}

// A recording pass that reads an older pinned tick leaves the Enhanced history
// layers where the host's in-order observation put them.
func TestPinnedPassDoesNotRewindObservedHistory(t *testing.T) {
	buf := frame.NewBuffer()
	c, err := New(Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetAsyncSimulation(true)
	c.enhanced = true
	c.scorch.valid, c.scorch.tick = true, 12
	older := &frame.Frame{Tick: 11}
	c.observeScorchMarks(older)
	if !c.scorch.valid || c.scorch.tick != 12 {
		t.Fatalf("an older pinned tick reset the scorch history to %+v", c.scorch.tick)
	}
}
