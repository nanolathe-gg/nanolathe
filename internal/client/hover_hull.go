package client

// The viewport hover hull — the shape a pointer must be inside for a unit to
// become the hovered/clicked candidate [07 R-REV-01][07 R-SEL-02B2].
//
// The hull is not a radius around the unit's projected origin and not a
// projected axis-aligned box. It is the same ground-level rectangle the
// selected-unit footprint quad draws: [07 R-REV-01] §1 and §2 specify exactly
// the geometry [03 R-WATER-01 §1] rules 1-3 specify for the drawn quad — the
// bounds helper called with its recursive walk disabled so only the selected
// root node contributes, zero-seeded extrema, nodes with two or fewer vertices
// contributing nothing, the four corners taken at the model's minimum Y in the
// order (minX,minY,minZ) → (maxX,minY,minZ) → (maxX,minY,maxZ) →
// (minX,minY,maxZ), and the same orientation transform. Because they are one
// contract, this file reuses selectionQuadCorners rather than restating the
// arithmetic; only the corner projection is written out, because the quad's
// projector is a *Client method and the picker is handed a camera.
//
// Document 07 owns hover membership; this file owns only the hull shape and
// its strict edge predicate.

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// UnitHullModels resolves a committed unit's authored model name to the
// compiled model whose root piece supplies the hull [07 R-REV-01 §1]. The
// picker takes authored model geometry from this source and every mutable unit
// value from the committed frame, so it never reads live simulation state [I6].
type UnitHullModels interface {
	HullModel(name string) *compiledmodel.Model
}

// UnitHullModelFunc adapts a plain lookup to UnitHullModels.
type UnitHullModelFunc func(name string) *compiledmodel.Model

// HullModel calls f, or returns nil when f is nil.
func (f UnitHullModelFunc) HullModel(name string) *compiledmodel.Model {
	if f == nil {
		return nil
	}
	return f(name)
}

// hullModels is the presentation model source the picker falls back to when a
// caller supplies none. PickSnapshotUnit's signature is fixed by its shell
// callers, which hold no model cache, so the cache registers itself when it is
// installed. It is presentation-only authored geometry: nothing here is
// simulation state, and the picker still reads every unit value from the
// committed frame [I6].
var hullModels UnitHullModels

// SetUnitHullModels installs the process-wide presentation model source used by
// PickSnapshotUnit when the caller passes none.
func SetUnitHullModels(src UnitHullModels) { hullModels = src }

// HullModel resolves an authored model name through the presentation model
// cache. It loads nothing that the draw path would not already load.
func (c *Client) HullModel(name string) *compiledmodel.Model {
	if c == nil {
		return nil
	}
	if c.modelTextures != nil {
		return c.modelTextures.hullByName[ckey(name)]
	}
	m := c.unitModelFor(name)
	if m == nil {
		return nil
	}
	return m.compiled
}

// hoverHullCorners projects the four hull corners into the indexed battle
// surface, the same space PickSnapshotUnit's pointer arrives in.
//
// The corner set, its minimum-Y plane and its orientation transform come from
// selectionQuadCorners [07 R-REV-01 §1-§2][03 R-WATER-01 §1]. The projection is
// [07 R-REV-01 §3]:
//
//	screenX = hi16(x + unitX - camX·65536) + 128
//	screenY = hi16((unitZ - camZ·65536) - z) - (hi16(y + unitY) >> 1) + 32
//
// with the transformed Z subtracted, then rebased off the beam origin the way
// every other committed-frame consumer rebases it. Because the camera position
// is whole world units, camera.WorldToScreen computes exactly that expression.
//
// Retail narrows each shifted term to a signed 16-bit value before the shear
// and the viewport bias; camera.WorldToScreen keeps int32 throughout, as the
// drawn quad already does. The two agree for every hull whose projected corners
// fit a signed 16-bit pixel, so no on-screen hover result differs.
func hoverHullCorners(m *compiledmodel.Model, v frame.UnitView, cam *camera.Camera) ([4][2]int32, bool) {
	var out [4][2]int32
	if m == nil || cam == nil || m.Root < 0 || m.Root >= len(m.Pieces) {
		return out, false
	}
	for i, r := range selectionQuadCorners(m, v.Heading, v.Pitch, v.Bank) {
		sx, sy := cam.WorldToScreen(v.X.Add(r[0]), v.Y.Add(r[1]), v.Z.Sub(r[2]))
		out[i] = [2]int32{sx - camera.OriginX, sy - camera.OriginY}
	}
	return out, true
}

// modelTotalHeight is the compiled definition's model total-height term
// [07 R-REV-01 §7]. The catalog loader asks the model-height helper for the
// loaded 3DO node tree and stores the answer, then rewrites the definition's Y
// extent as that height minus a minimum-Y word it has just zeroed — so the Y
// extent the hover reduction reads *is* the model total height.
//
// The helper accumulates only the vertical component. Each call seeds its
// running maximum at zero, takes the maximum of `vertexY + nodeTranslateY` over
// the node's own vertices, and then, for the node's child chain, adds the
// node's own translation to whatever that recursive call returned. Because the
// seed is zero at every level rather than the first vertex, a subtree lying
// entirely below its parent's origin contributes zero and never pulls the
// height down. There is no minimum-vertex-count gate here — that gate belongs
// to the hull bounds helper of [07 R-REV-01 §1], not to this one — and no
// orientation is applied: this is the authored bind pose, resolved once per
// definition at load time.
func modelTotalHeight(m *compiledmodel.Model) numeric.Fixed { // [07 R-REV-01 §7]
	if m == nil || m.Root < 0 || m.Root >= len(m.Pieces) {
		return 0
	}
	return pieceTotalHeight(m, m.Root)
}

func pieceTotalHeight(m *compiledmodel.Model, index int) numeric.Fixed {
	best := numeric.Fixed(0) // zero-seeded at every level [07 R-REV-01 §7]
	if index < 0 || index >= len(m.Pieces) {
		return best
	}
	piece := m.Pieces[index]
	for _, vertex := range piece.Vertices {
		if h := vertex[1].Add(piece.Translate[1]); h > best {
			best = h
		}
	}
	for _, child := range piece.Children {
		if h := pieceTotalHeight(m, child).Add(piece.Translate[1]); h > best {
			best = h
		}
	}
	return best
}

// hoverScore is the hover reduction's per-candidate score [07 R-REV-01 §8].
//
//	score = ((((yExtent * 32768) >> 16) + zExtent) * xExtent) >> 16
//
// The three terms are compiled definition words, not hull-helper outputs. The
// definition compiler writes the X and Z extents from the authored footprint —
// each extent is `footprint << 20`, i.e. the footprint cell count times sixteen
// world units, expressed in 16.16 — and the catalog loader rewrites the Y
// extent as the model total height once the 3DO is loaded [07 R-REV-01 §7].
//
// Both multiplications are signed 64-bit products, each `>> 16` is an
// arithmetic shift of that 64-bit intermediate, and the result of each shift is
// taken as a signed 32-bit value before the next step — including the addition,
// which wraps in 32 bits. Retail has no overflow guard here and must not be
// silently widened.
//
// The score is a pure size measure: half the model's height plus its Z
// footprint, scaled by its X footprint. Nothing about altitude, draw order or
// mover class enters it. A small unit therefore outranks a large one whose hull
// also admits the pointer, which is why an aircraft over an aircraft plant
// picks the aircraft.
func hoverScore(m *compiledmodel.Model, footX, footZ int8) int32 { // [07 R-REV-01 §8]
	xExtent := int32(footX) << 20
	zExtent := int32(footZ) << 20
	yExtent := int32(modelTotalHeight(m))
	half := int32((int64(yExtent) * 32768) >> 16)
	return int32((int64(half+zExtent) * int64(xExtent)) >> 16)
}

// containsStrictPolygon is the four-point hull admission test [07 R-REV-01 §4].
//
// Fewer than three points reject. Every directed edge is visited once, and the
// point is admitted only when every edge satisfies
//
//	(next.y - prev.y) * (point.x - prev.x) > (next.x - prev.x) * (point.y - prev.y)
//
// Each side is the low signed 32-bit product — Go's int32 multiply wraps the
// same way, which is deliberate: retail has no overflow guard and must not be
// silently widened. The comparison is strict, so equality rejects and every
// exactly collinear point, edge interior or vertex, is outside. There is no
// tolerance, no widening and no alternate edge path; a fudge factor here would
// be invented behavior, not a nicer cursor.
//
// The operand order is the traced one. The form once printed in [R-SEL-02B2]
// had the two sides swapped, which admits the complement of the retail hull for
// the corner winding of [R-REV-01 §2].
func containsStrictPolygon(px, py int32, points [][2]int32) bool {
	if len(points) < 3 {
		return false
	}
	for i := range points {
		prev := points[i]
		next := points[(i+1)%len(points)]
		lhs := (next[1] - prev[1]) * (px - prev[0])
		rhs := (next[0] - prev[0]) * (py - prev[1])
		if lhs <= rhs {
			return false
		}
	}
	return true
}
