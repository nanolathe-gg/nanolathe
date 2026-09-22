package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// SnapshotVisible evaluates the shared gameplay gate using only committed
// inputs. The published hull offsets locate the first probe independently of
// the unit's draw base; its top height controls the sea-level test [06 §3.1].
// The sample projection and mode-selected coverage remain committed [03 §3.2][I6].
func SnapshotVisible(f *frame.Frame, v frame.UnitView, viewer uint8) bool {
	if f == nil {
		return false
	}
	if v.DirectVisibilityKnown && viewer == f.ViewingPlayer {
		return v.DirectlyVisible
	}
	var status uint32
	if v.UnderwaterExempt {
		status = visibility.SonarBit
	}
	target := visibility.Target{
		Owner: visibility.PlayerID(v.Owner),
		X:     v.X + v.HullOffsetX, Y: v.Y + v.HullOffsetY, Z: v.Z + v.HullOffsetZ,
		XExtent: v.HullXExtent, YExtent: v.HullYExtent, ZExtent: v.HullZExtent,
		Hidden: v.Cloaked, Status: status,
	}
	return target.IsVisible(visibility.PlayerID(viewer), f.Visibility.SeaLevel, func(x, y, z numeric.Fixed) bool {
		return SnapshotPointVisible(f.Visibility, x, y, z, viewer)
	})
}

// SnapshotPointVisible applies the committed visibility representation to one
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
		// A malformed byte publication must not expose a point; the word grid
		// the same publication always carries is tried next [03 §3.1-§3.2].
	}
	if _, ok := visibilityGridSize(m.W, m.H, len(m.WordVisible)); ok {
		return PointVisible(m, x, y, z, 0, viewer)
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
	// The hull corners come from WorldToScreen, which projects at the RECORD
	// step; the pointer arrives in the PRESENTED picture's own surface pixels.
	// The two are the same space at every rest factor and differ only while the
	// modern executor's free zoom is in flight, which is what the bridge is for
	// (DESIGN_GPU_RENDERER §16.4). Surface pixels carry no beam origin, so the
	// bridge is taken about it: passing the surface point straight to
	// ScreenToRecord, which
	// expects beam pixels, shifted the pick by the view origin scaled through
	// the factor, and a click off a rest step selected the wrong unit.
	sx, sy = recordPoint(cam, sx, sy)
	best := pool.Handle(0)
	var bestView frame.UnitView
	// The reduction seeds its running best above every reachable score and
	// replaces it only on a strictly smaller one [07 R-REV-01 §5].
	bestScore := int32(0x7fff0000)
	for i := 0; i < len(f.Units); i++ {
		v := f.Units[i]
		// Slot zero is not a candidate. The geometry lookup below tests the
		// model reference, including an empty resource basename [07 R-REV-01 §5].
		if v.Slot == 0 {
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

// SelectionBand is the drag-selection rectangle of [07 §9] "Drag-rectangle
// conversion is closed", carried in the two spaces its consumers need.
//
// Retail records both drag endpoints as whole three-component world points and
// converts each one by the ordinary projection at the moment the rectangle is
// tested, so the band and the unit points compared against it are built by the
// same formula, from the same camera, each carrying its own half-height shear.
// A band derived instead from the pointer pixels the gesture passed through
// stays in the press-time screen frame, and any camera movement during the drag
// — an edge scroll, a held arrow key, a wheel zoom — slides it off the terrain
// it was drawn over.
//
// Record is the band in the space unit positions are projected into
// (WorldToSurface at the record step); Surface is the same band in the surface
// pixels of the presented picture, which is where the overlay draws it
// (DESIGN_GPU_RENDERER §16.3, §16.4). At a rest factor the two are equal.
type SelectionBand struct {
	Record  Rect
	Surface Rect
}

// WorldSelectionBand converts the two recorded drag endpoints into the band
// [07 §9]. Each endpoint goes through the ordinary world→surface projection
// with its own height, exactly as the unit points it will be tested against do,
// and the two projected corners are then sorted independently per axis.
func WorldSelectionBand(cam *camera.Camera, ax, ay, az, bx, by, bz numeric.Fixed) SelectionBand {
	t := NewViewportTransform(cam, 0, 0)
	pa := t.WorldToSurface(ax, ay, az)
	pb := t.WorldToSurface(bx, by, bz)
	sax, say := surfacePoint(cam, pa.X, pa.Y)
	sbx, sby := surfacePoint(cam, pb.X, pb.Y)
	return SelectionBand{
		Record:  NormalizeRect(pa.X, pa.Y, pb.X, pb.Y),
		Surface: NormalizeRect(sax, say, sbx, sby),
	}
}

// SnapshotUnitHandlesInBand walks the committed frame against a band whose
// corners were already projected from the recorded world endpoints, so no
// surface→record bridge is applied to them.
func SnapshotUnitHandlesInBand(f *frame.Frame, cam *camera.Camera, band SelectionBand, viewer uint8) []pool.Handle {
	return snapshotUnitHandlesInRecordRect(f, cam, band.Record, viewer)
}

// snapshotUnitHandlesInRecordRect returns visible handles in stable frame
// order. The rectangle is already in the record space unit positions project
// into, which is the space WorldSelectionBand converts the drag endpoints to;
// there is no surface-pixel entry point, because the only rectangle a shipped
// path tests is that band. It does not mutate the frame or any authoritative
// selection flags.
func snapshotUnitHandlesInRecordRect(f *frame.Frame, cam *camera.Camera, rect Rect, viewer uint8) []pool.Handle {
	if f == nil || cam == nil || rectEmpty(rect) {
		return nil
	}
	out := make([]pool.Handle, 0)
	for i := 0; i < len(f.Units); i++ {
		v := f.Units[i]
		if v.Slot == 0 || !SnapshotVisible(f, v, viewer) {
			continue
		}
		p := NewViewportTransform(cam, 0, 0).WorldToSurface(v.X, v.Y, v.Z)
		if rect.Contains(p.X, p.Y) {
			out = append(out, v.Slot)
		}
	}
	return out
}

// recordPoint converts a surface-space point of the presented picture into the
// record space unit positions are projected in (§16.4). Surface coordinates
// carry no beam origin and ScreenToRecord takes beam pixels, so the bridge is
// applied about the origin and taken off again. It is the identity at every
// rest factor.
func recordPoint(cam *camera.Camera, x, y int32) (int32, int32) {
	if cam == nil || cam.AtRestStep() {
		return x, y
	}
	rx, ry := cam.ScreenToRecord(x+camera.OriginX, y+camera.OriginY)
	return rx - camera.OriginX, ry - camera.OriginY
}

// surfacePoint is recordPoint's inverse: it carries a point of the record space
// unit positions project into back to the surface pixels of the presented
// picture (§16.4). ViewScale.Inverse is exact on a coordinate ViewScale.Project
// produced, so the result equals projecting the same world offset at the live
// factor. It is the identity at every rest factor.
func surfacePoint(cam *camera.Camera, x, y int32) (int32, int32) {
	if cam == nil || cam.AtRestStep() {
		return x, y
	}
	z, s := cam.EffectiveZoom(), cam.EffectiveScale()
	return z.Project(s.Inverse(x)), z.Project(s.Inverse(y))
}
