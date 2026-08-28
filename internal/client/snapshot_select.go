package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SnapshotVisible reports whether a published unit may be interacted with by
// viewer. Friendly units bypass fog; foreign units require the current visible
// mask cell. A cloaked flag remains hidden, as in the live picker.
func SnapshotVisible(f *frame.Frame, v frame.UnitView, viewer uint8) bool {
	if f == nil || viewer >= 10 {
		return false
	}
	if v.Owner == viewer {
		return true
	}
	if v.Flags&0x4 != 0 {
		return false
	}
	m := f.Visibility
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
func PickSnapshotUnit(f *frame.Frame, sx, sy int32, cam *camera.Camera, viewer uint8) (pool.Handle, frame.UnitView, bool) {
	if f == nil || cam == nil {
		return 0, frame.UnitView{}, false
	}
	const radiusSq int64 = 16 * 16
	best := pool.Handle(0)
	var bestView frame.UnitView
	bestDist := int64(1 << 62)
	for i := 0; i < len(f.Units); i++ {
		v := f.Units[i]
		if v.Slot == 0 || !SnapshotVisible(f, v, viewer) {
			continue
		}
		p := NewViewportTransform(cam, nil, 0, 0).WorldToSurface(v.X, v.Y, v.Z)
		dx := int64(p.X) - int64(sx)
		dy := int64(p.Y) - int64(sy)
		d := dx*dx + dy*dy
		if d <= radiusSq && (d < bestDist || (d == bestDist && (best == 0 || v.Slot < best))) {
			bestDist, best, bestView = d, v.Slot, v
		}
	}
	return best, bestView, best != 0
}

// SnapshotUnitHandlesInRect returns visible handles in stable frame order.
// It does not mutate the frame or any authoritative selection flags.
func SnapshotUnitHandlesInRect(f *frame.Frame, cam *camera.Camera, rect Rect, viewer uint8) []pool.Handle {
	if f == nil || cam == nil || rectEmpty(rect) {
		return nil
	}
	out := make([]pool.Handle, 0)
	for i := 0; i < len(f.Units); i++ {
		v := f.Units[i]
		if v.Slot == 0 || !SnapshotVisible(f, v, viewer) {
			continue
		}
		p := NewViewportTransform(cam, nil, 0, 0).WorldToSurface(v.X, v.Y, v.Z)
		if rect.Contains(p.X, p.Y) {
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
