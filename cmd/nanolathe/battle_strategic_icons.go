package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Selection, footer hover and contextual orders share the modern icon picker;
// the client's normal-zoom branch retains the retail hull contract (§18.4).
func (b *battleSession) pickPresentedUnit(f *frame.Frame, x, y int32, viewer uint8) (pool.Handle, frame.UnitView, bool) {
	if b.cl != nil && b.cl.StrategicIconsActive() {
		return b.cl.PickPresentedUnit(f, x, y, viewer)
	}
	return client.PickSnapshotUnit(f, x, y, b.cam, viewer)
}
