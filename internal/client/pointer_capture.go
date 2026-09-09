package client

// SetPointerCaptured requests relative host pointer input and hides the
// software cursor during drag-scroll [07 R-CAM-01 §11]. The platform adapter
// owns native capture; simulation state never reads this request [I6].
func (c *Client) SetPointerCaptured(captured bool) {
	if c == nil || c.pointerCaptured == captured {
		return
	}
	if captured {
		pointer, _ := c.in.PointerSample()
		c.cursorRestoreX, c.cursorRestoreY = pointer.X, pointer.Y
		c.cursorRestorePending = false
	} else {
		// Draw the release frame at the original event position even though
		// its input record still carries the final relative movement. The next
		// host poll resumes ordinary positioning [07 R-CAM-01 §11].
		c.cursorRestorePending = true
	}
	c.pointerCaptured = captured
}

// PointerCaptured reports the current presentation capture request.
func (c *Client) PointerCaptured() bool {
	return c != nil && c.pointerCaptured
}
