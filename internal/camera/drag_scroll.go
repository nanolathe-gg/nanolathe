package camera

// DragScroll retains the quantized retail beam origin between cursor recenter
// steps [07 R-CAM-01 §11]. Pointer capture and release belong to the host caller.
type DragScroll struct {
	anchorX, anchorZ int32
}

// Begin captures the origin and clears tracked-unit/glide state once. The
// caller also clears its projectile hold and starts pointer capture [07
// R-CAM-01 §11]. Camera X/Z use the framebuffer origin, so quantization must
// follow conversion to the retail battle-view origin [03 §4.1].
func (d *DragScroll) Begin(c *Camera) {
	if d == nil || c == nil {
		return
	}
	x, z := c.BattleViewOrigin()
	d.anchorX, d.anchorZ = x/16, z/16
	c.ClearFollow()
	// Pointer dispatch precedes phase 10, unlike keyboard hotkeys. The host
	// already sampled tracking and glide state before dispatch; refresh both
	// after cancellation so this same frame cannot follow after capture
	// [07 R-CAM-01 §1].
	c.LatchTracked()
}

// Step spends this frame's displacement from the recentered cursor. Signed
// division discards sub-four-pixel movement each frame; it is not accumulated
// or rounded downward. The release frame still takes this step before the
// caller ends capture. Follow state is preserved during steps [07 R-CAM-01 §11].
func (d *DragScroll) Step(c *Camera, dx, dy int32) {
	if d == nil || c == nil {
		return
	}
	leadX, _, leadZ, _ := c.clampInsets()
	c.JumpTo((d.anchorX+dx/4)*16-leadX, (d.anchorZ+dy/4)*16-leadZ)
	x, z := c.BattleViewOrigin()
	d.anchorX, d.anchorZ = x/16, z/16
}
