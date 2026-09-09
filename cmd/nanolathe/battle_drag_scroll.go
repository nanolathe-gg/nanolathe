package main

import "github.com/nanolathe/nanolathe/internal/client"

func (b *battleSession) beginDragScroll(x, y int32, cl *client.Client) {
	if b == nil || b.cam == nil {
		return
	}
	b.dragScroll.Begin(b.cam)
	b.dragScrollLastX, b.dragScrollLastY = x, y
	b.dragScrollActive = true
	cl.SetPointerCaptured(true)
	// TODO(question): the camera has no projectile-hold count or followed
	// projectile owner yet; bind their entry-time clear here when that owner
	// exists [07 R-CAM-01 §11][06 §7.3].
}

// Captured host positions are cumulative virtual coordinates. Spending their
// difference implements the per-frame recenter without accumulating movement
// discarded by signed division [07 R-CAM-01 §11].
func (b *battleSession) serviceDragScroll(x, y int32, rightHeld bool, cl *client.Client) bool {
	if b == nil || !b.dragScrollActive {
		return false
	}
	if cl != nil && !cl.PointerCaptured() {
		b.endDragScroll(cl)
		return true
	}
	b.dragScroll.Step(b.cam, x-b.dragScrollLastX, y-b.dragScrollLastY)
	b.dragScrollLastX, b.dragScrollLastY = x, y
	b.dragScrollStepped = true
	if !rightHeld {
		b.endDragScroll(cl)
	}
	return true
}

func (b *battleSession) endDragScroll(cl *client.Client) {
	if b == nil {
		return
	}
	b.dragScrollActive = false
	cl.SetPointerCaptured(false)
}
