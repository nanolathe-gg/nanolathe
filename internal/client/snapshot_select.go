package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SnapshotVisible is the gameplay visibility gate evaluated over the committed
// frame [03 §3.2]. Its five steps run in the researched order: owner-identity
// bypass, cloak early-out, below-sea-level rejection with the runtime
// exemption, then the four accumulating hull samples against the mode-selected
// committed coverage.
//
// Every input is published. The hull extent triple and the exemption bit ride
// the unit view and the scaled sea level rides the visibility view, so nothing
// here reads a live definition or the mutable terrain [I6].
func SnapshotVisible(f *frame.Frame, v frame.UnitView, viewer uint8) bool {
	if f == nil || viewer >= 10 {
		return false
	}
	if v.Owner == viewer {
		return true
	}
	// Step 2 of the gate [03 §3.2]: a cloaked unit is hidden from a non-owner
	// unless its decloak timer is running [03 §3.4].
	//
	// This used to read the instance flag word directly, as `Flags&0x4` with
	// `Flags&0x1000` for the timer. Neither bit means that there: 0x4 is the
	// construction layer's start-building edge and 0x1000 is the order pump's
	// active-record marker. So every enemy builder vanished the moment it began
	// building — a play-test report of aircraft plants disappearing behind
	// their own nanoframe and spray — and a genuinely cloaked unit was never
	// hidden at all. The committed frame carries the two real inputs; nothing
	// is reconstructed from presentation bits [03 §3.2][R-VIS-01 §4].
	if v.Cloaked && !v.Decloaking {
		return false
	}
	m := f.Visibility
	// Step 3 of the gate [03 §3.2]: a base height below sea level is not
	// visible unless the runtime underwater-exemption bit is set. Sea level is
	// the map header byte scaled to world units — the comparison is against
	// that scaled byte and never against zero [03 §2.2] — and the sensor phase
	// sets the exemption on owned and allied units [03 §3.4], which is why
	// those are never rejected for depth.
	if !v.UnderwaterExempt && v.Y < m.SeaLevel {
		return false
	}
	// Steps 4-5, the four hull samples [03 §3.2]. They ACCUMULATE: one
	// coordinate triple is carried through all four tests and each step mutates
	// it, which is why the last step subtracts the X extent "again". The figure
	// is a rectangle in projected space, not a diamond about the base point.
	// Any admitted sample returns visible.
	//
	// The north step subtracts the WHOLE published height word, not half of it.
	// [03 §3.2]'s numbered list says "half-height subtracted from the height",
	// but its own sample table writes `Y - ey` and the paragraph under that
	// table settles what `ey` is: "its own definition field, distinct from the
	// Z extent `ez`; the two are not the same value and neither is half of the
	// unit's height". The list's phrase is the loose one — the projection's own
	// half-height shear leaking into a sentence about sample offsets — and the
	// table with its correction is the later, specific text. Halving here would
	// admit a strictly smaller rectangle than the gate does.
	x, y, z := v.X, v.Y, v.Z
	if SnapshotPointVisible(m, x, y, z, viewer) { // 0: centre
		return true
	}
	x += v.HullXExtent
	if SnapshotPointVisible(m, x, y, z, viewer) { // 1: east
		return true
	}
	y -= v.HullYExtent
	z += v.HullZExtent
	if SnapshotPointVisible(m, x, y, z, viewer) { // 2: north, still east-shifted
		return true
	}
	x -= v.HullXExtent
	return SnapshotPointVisible(m, x, y, z, viewer) // 3: west, still north-shifted
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

// PickSnapshotUnit is the immutable presentation picker. It returns a copied
// UnitView value and stable pool handle, never a pointer into the live world.
//
// Retail's viewport hover is a hull test, not a proximity test
// [07 R-REV-01][07 R-SEL-02B2]: each candidate's root-piece bounds become a
// ground-level rectangle at the model's minimum Y, the four corners are
// oriented and projected, and the pointer is admitted only by the strict
// polygon predicate of [07 R-REV-01 §4]. That hull is the same quad the
// selected-unit footprint outline draws, so the clickable area is exactly the
// drawn rectangle. The 16-pixel radius this function used before had no
// research behind it and made large units clickable only near their centre.
//
// Candidate order is the ascending unit-pool walk of [07 R-REV-01 §5], which is
// reproduced here by walking the published slice and breaking ties on the
// lowest stable slot. The producer's three admission tests map as follows: test
// 1 is the non-empty model reference; test 3 is SnapshotVisible, which applies
// ownership bypass, the cloak gate and the committed coverage cell. Test 2 —
// the projected definition-extent box against the viewport bounds — is not
// reproduced: those six compiled extent words are not published, and skipping
// it can only admit candidates whose hull is off-screen, which no on-screen
// pointer can be inside.
//
// Among the candidates whose hull admits the pointer, the winner is the one
// with the smallest hoverScore, replaced only on a strictly smaller score, so
// an equal score retains the earlier (lower-slot) pool member
// [07 R-SEL-02B2][07 R-REV-01 §5][07 R-REV-01 §8]. This is the whole of retail's
// overlap rule: there is no draw-order, depth, altitude or mover-class
// precedence anywhere in the pick. An aircraft flying over a plant wins because
// its definition is smaller, not because it is in the air [07 R-REV-01 §9].
//
// The score's three compiled definition words were recorded Unknown in
// [07 R-REV-01 §6]. Their writers are now traced in [07 R-REV-01 §7]: the X and
// Z extents are the authored footprint scaled by sixteen world units and the Y
// extent is the model total height, both already available at this boundary
// from the committed footprint and the authored model. No new committed pick
// record is needed to reproduce the ordering.
//
// models supplies the authored hull geometry. When no source is passed the
// process-wide presentation cache installed by SetUnitHullModels is used; the
// parameter is variadic so shell callers that hold no model cache keep working.
// A unit whose model cannot be resolved has no hull and is never picked.
func PickSnapshotUnit(f *frame.Frame, sx, sy int32, cam *camera.Camera, viewer uint8, models ...UnitHullModels) (pool.Handle, frame.UnitView, bool) {
	if f == nil || cam == nil {
		return 0, frame.UnitView{}, false
	}
	src := hullModels
	if len(models) > 0 {
		src = models[0]
	}
	if src == nil {
		return 0, frame.UnitView{}, false
	}
	best := pool.Handle(0)
	var bestView frame.UnitView
	// The reduction seeds its running best above every reachable score and
	// replaces it only on a strictly smaller one [07 R-REV-01 §5].
	bestScore := int32(0x7fff0000)
	for i := 0; i < len(f.Units); i++ {
		v := f.Units[i]
		// Test 1: a candidate without a model reference never reaches the hull
		// helper [07 R-REV-01 §5].
		if v.Slot == 0 || v.Model == "" {
			continue
		}
		// Test 3: ownership, or the mode-selected committed coverage cell.
		if !SnapshotVisible(f, v, viewer) {
			continue
		}
		m := src.HullModel(v.Model)
		corners, ok := hoverHullCorners(m, v, cam)
		if !ok || !containsStrictPolygon(sx, sy, corners[:]) {
			continue
		}
		score := hoverScore(m, v.FootX, v.FootZ)
		// Strictly smaller replaces; an equal score keeps the standing winner,
		// which the ascending-slot tie-break below makes the lower slot.
		if score > bestScore || (score == bestScore && best != 0 && v.Slot >= best) {
			continue
		}
		best, bestView, bestScore = v.Slot, v, score
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
