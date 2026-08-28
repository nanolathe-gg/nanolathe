package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SnapshotVisible reports whether a published unit may be interacted with by
// viewer. Friendly units bypass fog; foreign units require the mode-selected
// committed coverage cell. A cloaked flag remains hidden unless the published
// decloak status is present, as in the gameplay predicate.
func SnapshotVisible(f *frame.Frame, v frame.UnitView, viewer uint8) bool {
	if f == nil || viewer >= 10 {
		return false
	}
	if v.Owner == viewer {
		return true
	}
	if v.Flags&0x4 != 0 && v.Flags&0x1000 == 0 {
		return false
	}
	m := f.Visibility
	// UnitView carries the committed anchor and footprint, but not the
	// definition hull deltas or terrain sea level used by the full four-point
	// gameplay predicate. Keep the exact projected one-point gate at this
	// boundary; extending it with footprint-derived guesses would change the
	// established hull semantics [03 §3.2].
	// TODO(question): publish unit X/Y/Z hull extents and the sea-level value in
	// frame data so presentation can reproduce the full four-point gate without
	// reading live definitions or terrain.
	return SnapshotPointVisible(m, v.X, v.Y, v.Z, viewer)
}

// snapshotPointVisible applies the committed visibility representation to one
// world point. Byte coverage is preferred when it is valid and selected;
// otherwise the published word grid is used at the local player's bit. The
// projection (including Y shear, signed 16-bit pixel narrowing, and 32-pixel
// tile conversion) is shared with projectile presentation [03 §3.2].
func SnapshotPointVisible(m frame.VisibilityView, x, y, z numeric.Fixed, viewer uint8) bool {
	if !m.Valid || viewer >= 10 {
		return false
	}
	if m.CoverageBytes {
		if _, ok := visibilityGridSize(m.W, m.H, len(m.Visible)); ok {
			return PointVisible(m, x, y, z, ProjectileVisibilityModeBytes, viewer)
		}
		// A malformed byte publication must not expose a point. A valid word
		// publication remains an explicit compatibility fallback.
	}
	if _, ok := visibilityGridSize(m.W, m.H, len(m.WordVisible)); ok {
		return PointVisible(m, x, y, z, 0, viewer)
	}
	if !m.CoverageBytes {
		// Older committed fixtures may carry only byte coverage and leave the
		// mode bit unset; accept that representation when no word grid exists.
		if _, ok := visibilityGridSize(m.W, m.H, len(m.Visible)); ok {
			return PointVisible(m, x, y, z, ProjectileVisibilityModeBytes, viewer)
		}
	}
	return false
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
