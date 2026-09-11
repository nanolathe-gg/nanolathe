package camera

import "testing"

func TestDragScrollQuantizesRetailOriginAndSignedDelta(t *testing.T) {
	c := &Camera{X: 1, Z: 1, ViewW: 640, ViewH: 480, MapW: 3000, MapH: 3000}
	c.SetTracked(7)
	var d DragScroll
	d.Begin(c)
	if c.Tracked() != 0 {
		t.Fatal("drag entry retained tracked unit")
	}
	// The framebuffer origin (1,1) corresponds to beam origin (129,33).
	// -3 truncates to zero; +7 contributes one quantized step [07 R-CAM-01 §11].
	d.Step(c, -3, 7)
	if x, z := c.BattleViewOrigin(); x != 128 || z != 48 {
		t.Fatalf("beam origin = %d,%d, want 128,48", x, z)
	}
	c.SetTracked(9)
	d.Step(c, -3, -7)
	if x, z := c.BattleViewOrigin(); x != 128 || z != 32 {
		t.Fatalf("second beam origin = %d,%d, want 128,32", x, z)
	}
	if c.Tracked() != 9 || c.Follow.Desired != (Origin{c.X, c.Z}) {
		t.Fatal("step cleared follow or failed to synchronize desired origin")
	}
}

func TestDragScrollReanchorsAfterClamp(t *testing.T) {
	c := &Camera{ViewW: 640, ViewH: 480, MapW: 1001, MapH: 1003}
	var d DragScroll
	d.Begin(c)
	d.Step(c, 10000, 10000)
	if x, z := c.BattleViewOrigin(); x != 489 || z != 587 {
		t.Fatalf("clamped beam origin = %d,%d, want 489,587", x, z)
	}
	// Quantize the clamped origin, not the attempted displacement. A zero
	// displacement on the next (including release) frame still snaps it down.
	d.Step(c, 0, 0)
	if x, z := c.BattleViewOrigin(); x != 480 || z != 576 {
		t.Fatalf("reanchored beam origin = %d,%d, want 480,576", x, z)
	}
}

// Established: pointer drag entry cancels following before the sub-tick
// phase-10 writer [07 R-CAM-01 §1][07 R-CAM-01 §11]. A host snapshot must
// not revive the glide that the pointer handler just cancelled.
func TestDragEntryCancelsPendingGlide(t *testing.T) {
	c := testCamera()
	c.JumpTo(100, 200)
	c.GlideTo(800, 900)
	c.LatchTracked()
	var drag DragScroll
	drag.Begin(c)
	for tick := 0; tick < 2; tick++ {
		c.StepLatchedGlide()
	}
	if c.X != 100 || c.Z != 200 || c.Follow.Gliding {
		t.Fatalf("cancelled glide moved during capture: current=(%d,%d) gliding=%v", c.X, c.Z, c.Follow.Gliding)
	}
	// Entry cancellation does not suppress a later glide.
	c.GlideTo(800, 900)
	c.LatchTracked()
	c.StepLatchedGlide()
	if c.X == 100 && c.Z == 200 {
		t.Fatal("later glide did not run")
	}
}
