package client

// SetOutsideSize records the host extent supplied by the window layout. It
// includes letterboxing and never changes the logical canvas or HUD geometry
// (DESIGN_PRESENTATION_CLIENT §2.1).
func (c *Client) SetOutsideSize(width, height int) {
	c.outsideWidth, c.outsideHeight = width, height
}

// EdgeScrollPosition maps the live pointer onto the logical canvas edges for
// camera scrolling only. Picking and widget dispatch keep the original sample.
// The host policy extends retail's 100-pixel trailing strip [07 §10] to all
// four sides and includes the host's letterbox bars; see
// DESIGN_INTERFACE_HUD_INPUT §3.1. Both axis bounds and focus gate the clamp.
func (c *Client) EdgeScrollPosition() (int32, int32) {
	x, y := float64(c.in.Mouse.X), float64(c.in.Mouse.Y)
	w, h := float64(c.width), float64(c.height)
	var padX, padY float64
	if c.outsideWidth > 0 && c.outsideHeight > 0 {
		scale := max(w/float64(c.outsideWidth), h/float64(c.outsideHeight))
		padX = (float64(c.outsideWidth)*scale - w) / 2
		padY = (float64(c.outsideHeight)*scale - h) / 2
	}
	if c.focused && x > -padX-100 && x < w+padX+100 && y > -padY-100 && y < h+padY+100 {
		x = max(0, min(x, w-1))
		y = max(0, min(y, h-1))
	}
	return int32(x), int32(y)
}
