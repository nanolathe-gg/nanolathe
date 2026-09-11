package camera

import "testing"

func testCamera() *Camera {
	return &Camera{ViewW: 640, ViewH: 480, MapW: 2000, MapH: 2000}
}

// TestBookmarkStoreRecall locks [07 R-CAM-01 §12]: store copies the current
// origin and sets the valid byte; recall jumps unconditionally — an unwritten
// slot recalls its zero origin — clamps, and clears the follow triple.
func TestBookmarkStoreRecall(t *testing.T) {
	c := testCamera()
	c.JumpTo(400, 300)
	if !c.StoreBookmark(2) {
		t.Fatalf("StoreBookmark(2) refused")
	}
	if got := c.Follow.Bookmarks[2]; got.Origin.X != 400 || got.Origin.Z != 300 || !got.Valid {
		t.Fatalf("slot 2 = %+v, want the current origin with its valid byte", got)
	}
	c.SetTracked(9)
	c.JumpTo(0, 0)
	if !c.RecallBookmark(2) {
		t.Fatalf("RecallBookmark(2) refused")
	}
	if c.X != 400 || c.Z != 300 {
		t.Fatalf("recall left (%d,%d), want (400,300)", c.X, c.Z)
	}
	if c.Tracked() != 0 || c.Follow.Gliding {
		t.Fatalf("recall did not clear the follow triple")
	}
	// An unwritten slot still recalls, so its zero origin is what loads.
	if !c.RecallBookmark(0) {
		t.Fatalf("RecallBookmark(0) refused an unwritten slot")
	}
	zero := *testCamera()
	zero.JumpTo(0, 0)
	if c.X != zero.X || c.Z != zero.Z {
		t.Fatalf("unwritten recall left (%d,%d), want the clamped zero origin (%d,%d)", c.X, c.Z, zero.X, zero.Z)
	}
	if c.StoreBookmark(4) || c.RecallBookmark(-1) {
		t.Fatalf("an out-of-range slot was accepted")
	}
}

// TestGlideStepsAtPhaseTenRate locks the glide half of [07 R-CAM-01 §12]: a
// glide writes only the desired origin and the follow step closes the gap at
// the 320-per-tick / half-remaining rate, then stops.
func TestGlideStepsAtPhaseTenRate(t *testing.T) {
	c := testCamera()
	c.JumpTo(0, 0)
	start := c.X
	c.GlideTo(1000, 0)
	if c.X != start {
		t.Fatalf("GlideTo moved the current origin to %d, want it left at %d", c.X, start)
	}
	if !c.Follow.Gliding {
		t.Fatalf("GlideTo did not arm the glide")
	}
	if !c.StepGlide() {
		t.Fatalf("the first step already ended the glide")
	}
	if c.X != start+320 {
		t.Fatalf("first step moved to %d, want the 320 cap at %d", c.X, start+320)
	}
	for i := 0; i < 64 && c.StepGlide(); i++ {
	}
	if c.Follow.Gliding {
		t.Fatalf("the glide never converged")
	}
	// The truncating half-step of [07 §10] leaves at most one pixel.
	if d := c.X - c.Follow.Desired.X; d > 1 || d < -1 {
		t.Fatalf("converged at %d, want within one pixel of the desired origin %d", c.X, c.Follow.Desired.X)
	}
	// ClearFollow cancels an in-flight glide.
	c.GlideTo(0, 0)
	c.ClearFollow()
	if c.Follow.Gliding || c.StepGlide() {
		t.Fatalf("ClearFollow left the glide running")
	}
}

// Established: n/F3 change only the desired origin; they do not cancel the
// tracked object [07 R-CAM-01 §12].
func TestGlidePreservesTrackedObject(t *testing.T) {
	c := testCamera()
	c.JumpTo(100, 200)
	c.SetTracked(7)
	c.LatchTracked()
	c.GlideTo(800, 900)
	if c.Tracked() != 7 || c.LatchedTracked() != 7 {
		t.Fatalf("glide cancelled tracking: current=%d latched=%d", c.Tracked(), c.LatchedTracked())
	}
	if c.X != 100 || c.Z != 200 || c.Follow.Desired != (Origin{800, 900}) {
		t.Fatalf("glide changed more than desired origin: current=(%d,%d) desired=%+v", c.X, c.Z, c.Follow.Desired)
	}
	if c.LatchTracked() != 7 {
		t.Fatal("next host batch lost the tracked object")
	}
}
