package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// SnapshotVisible reports whether a published unit may be interacted with by
// viewer. Friendly units bypass fog; foreign units require the current visible
// mask cell. A cloaked flag remains hidden, as in the live picker.
func SnapshotVisible(frame *snapshot.Frame, v snapshot.UnitView, viewer uint8) bool {
	if frame == nil || viewer >= 10 {
		return false
	}
	if v.Owner == viewer {
		return true
	}
	if v.Flags&0x4 != 0 {
		return false
	}
	m := frame.Visibility
	if !m.Valid || m.W <= 0 || m.H <= 0 || len(m.Visible) != int(m.W*m.H) {
		return false
	}
	cx := int32(int64(v.X) >> 20)
	cz := int32(int64(v.Z) >> 20)
	if cx < 0 || cz < 0 || cx >= m.W || cz >= m.H {
		return false
	}
	return m.Visible[int(cz*m.W+cx)] != 0
}

// PickSnapshotUnit is the immutable production picker. It returns a copied
// UnitView value and stable pool handle, never a pointer into the live world.
// The 16px radius is inclusive; strict '<' winner comparison preserves the
// lower-slot winner on equal squared distance [07 §9].
func PickSnapshotUnit(frame *snapshot.Frame, sx, sy int32, cam *camera.Camera, viewer uint8) (pool.Handle, snapshot.UnitView, bool) {
	if frame == nil || cam == nil {
		return 0, snapshot.UnitView{}, false
	}
	const radiusSq int64 = 16 * 16
	best := pool.Handle(0)
	var bestView snapshot.UnitView
	bestDist := int64(1 << 62)
	for i := 0; i < len(frame.Units); i++ {
		v := frame.Units[i]
		if v.Slot == 0 || !SnapshotVisible(frame, v, viewer) {
			continue
		}
		px, py := cam.WorldToScreen(v.X, v.Y, v.Z)
		dx := int64(px-camera.OriginX) - int64(sx)
		dy := int64(py-camera.OriginY) - int64(sy)
		d := dx*dx + dy*dy
		if d <= radiusSq && (d < bestDist || (d == bestDist && (best == 0 || v.Slot < best))) {
			bestDist, best, bestView = d, v.Slot, v
		}
	}
	return best, bestView, best != 0
}

// SnapshotUnitHandlesInRect returns visible handles in stable frame order.
// It does not mutate the frame or any authoritative selection flags.
func SnapshotUnitHandlesInRect(frame *snapshot.Frame, cam *camera.Camera, rect Rect, viewer uint8) []pool.Handle {
	if frame == nil || cam == nil || rect.IsEmpty() {
		return nil
	}
	out := make([]pool.Handle, 0)
	for i := 0; i < len(frame.Units); i++ {
		v := frame.Units[i]
		if v.Slot == 0 || !SnapshotVisible(frame, v, viewer) {
			continue
		}
		px, py := cam.WorldToScreen(v.X, v.Y, v.Z)
		if rect.Contains(px-camera.OriginX, py-camera.OriginY) {
			out = append(out, v.Slot)
		}
	}
	return out
}

// SnapshotGroundPosition converts a shell cursor to the fixed world ground
// point used by typed orders. The authoritative terrain-height refinement is
// intentionally left to the session's order/construction consumers.
func SnapshotGroundPosition(cam *camera.Camera, sx, sy int32) (numeric.Fixed, numeric.Fixed) {
	if cam == nil {
		return 0, 0
	}
	return cam.ScreenToWorld(sx+camera.OriginX, sy+camera.OriginY)
}
