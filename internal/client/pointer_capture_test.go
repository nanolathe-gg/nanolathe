package client

import "testing"

func TestCaptureHidesCursorAndRestoresReleaseFrame(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	c.cursors = snapshotCursor(37)
	c.in.Mouse.SetPosition(10, 12)
	c.SetPointerCaptured(true)
	c.in.Mouse.SetPosition(30, 32)
	shot := c.ComposeFrameSnapshot()
	for _, pixel := range shot.Indexed {
		if pixel == 37 {
			t.Fatal("captured cursor was painted")
		}
	}
	c.SetPointerCaptured(false)
	shot = c.ComposeFrameSnapshot()
	if shot.Indexed[12*64+10] != 37 || shot.Indexed[32*64+30] == 37 {
		t.Fatal("release frame did not restore the original pointer")
	}
	c.Step(0)
	c.in.Mouse.SetPosition(15, 17)
	shot = c.ComposeFrameSnapshot()
	if shot.Indexed[17*64+15] != 37 {
		t.Fatal("next host frame did not resume normal pointer positioning")
	}
}
