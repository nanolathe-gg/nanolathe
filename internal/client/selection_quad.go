package client

// The selected-unit footprint quad. For every bucketed unit whose selected bit
// is set the composer draws a four-line quad around the root piece's bounds, in
// the same depth slot as the unit and immediately before the model present, and
// before the fog composite so it is never fog-clipped
// [03 R-WATER-01 §1]. The gate bit is set unconditionally when settings load,
// so the quad is always on and nothing clears it (rule 5) — there is no option
// to consult here.
//
// This is not the drag rectangle of [R-SEL-02A] and not the authored selection
// primitive of [03 §2.4.1] item 1: the quad is derived from the root piece's
// vertex bounds and the authored plate stays excluded from every consumer.
// Document 07 owns selection membership; this file only draws.

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// selectionQuadLogicalColor is the logical palette entry the four lines are
// drawn in; it is resolved through the same logical-to-physical map beams use
// [03 R-WATER-01 §1] rule 4.
const selectionQuadLogicalColor byte = 10

// selectionQuadBounds returns the root piece's per-axis minimum and maximum,
// `A` and `B` [03 R-WATER-01 §1] rule 1. The walk is root-only — retail invokes
// its recursive bounds helper with recursion off — and each vertex is offset by
// the root piece's own translation. Both accumulators start at zero so the
// model origin is always inside the box, and a piece with fewer than three
// vertices contributes nothing, which is what keeps single-vertex emitter
// pieces from widening it.
func selectionQuadBounds(m *compiledmodel.Model) (a, b [3]numeric.Fixed) {
	if m == nil || m.Root < 0 || m.Root >= len(m.Pieces) {
		return
	}
	piece := m.Pieces[m.Root]
	if len(piece.Vertices) < 3 {
		return // fewer than three vertices contributes nothing [03 R-WATER-01 §1]
	}
	for _, vertex := range piece.Vertices {
		for axis := 0; axis < 3; axis++ {
			c := vertex[axis].Add(piece.Translate[axis])
			if c < a[axis] {
				a[axis] = c
			}
			if c > b[axis] {
				b[axis] = c
			}
		}
	}
	return
}

// selectionQuadRotation returns the transform that rotates a model-space point
// by the unit's orientation triple alone. Retail rotates the four corners with
// the ordinary piece rotation of [R-RAST-01 §2] and contributes no translation
// of its own: the root piece's authored offset is already inside the bounds of
// rule 1, so adding it a second time would displace the quad. The authored
// translation is therefore cancelled on this presentation copy of the piece
// state rather than duplicating the rotation arithmetic here, which keeps the
// Z-then-X-then-Y order and its round-to-nearest in one place [03 §2.4].
func selectionQuadRotation(m *compiledmodel.Model, heading, pitch, bank uint16) compiledmodel.Transform {
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	compiledmodel.FoldRootAngles(states, m.Root, heading, pitch, bank) // [03 §2.4] C24
	root := m.Pieces[m.Root]
	states[m.Root].Trans = [3]numeric.Fixed{-root.Translate[0], -root.Translate[1], -root.Translate[2]}
	return compiledmodel.Compose(m, states, m.Root)
}

// selectionQuadCorners returns the four rotated corners in model space. All
// four lie on the plane `y = A.y` and run
// `(A.x,A.y,A.z) → (B.x,A.y,A.z) → (B.x,A.y,B.z) → (A.x,A.y,B.z)`
// [03 R-WATER-01 §1] rules 2 and 3.
func selectionQuadCorners(m *compiledmodel.Model, heading, pitch, bank uint16) [4][3]numeric.Fixed {
	a, b := selectionQuadBounds(m)
	corners := [4][3]numeric.Fixed{
		{a[0], a[1], a[2]},
		{b[0], a[1], a[2]},
		{b[0], a[1], b[2]},
		{a[0], a[1], b[2]},
	}
	rotation := selectionQuadRotation(m, heading, pitch, bank)
	for i, corner := range corners {
		corners[i] = rotation.Apply(corner)
	}
	return corners
}

// selectionQuadScreen projects the four rotated corners with the world-object
// projection of [03 R-WATER-01 §1] rule 3:
//
//	sx = hi16(rx + ux − camX·65536) + 128
//	sy = hi16((uz − camZ·65536) − rz) − (hi16(ry + uy) >> 1) + 32
//
// The rotated Z is *subtracted* — the 3DO handedness flip of [R-RAST-01 §2] —
// so the world point handed to the shared projection carries `uz − rz`. Because
// the camera position is whole world units, `hi16(w − cam·65536)` is
// `hi16(w) − cam`, which is exactly what camera.WorldToScreen computes; using
// it keeps the `+128`/`+32` origin and any viewport offset in one place, and
// the result is re-based the same way every other world writer re-bases it.
func (c *Client) selectionQuadScreen(m *compiledmodel.Model, v frame.UnitView) ([4][2]int32, bool) {
	var out [4][2]int32
	if c == nil || c.cam == nil || m == nil || m.Root < 0 || m.Root >= len(m.Pieces) {
		return out, false
	}
	for i, r := range selectionQuadCorners(m, v.Heading, v.Pitch, v.Bank) {
		sx, sy := c.cam.WorldToScreen(v.X.Add(r[0]), v.Y.Add(r[1]), v.Z.Sub(r[2]))
		out[i] = [2]int32{sx - camera.OriginX, sy - camera.OriginY}
	}
	return out, true
}

// drawSelectionQuad writes the four one-pixel Bresenham lines
// corner→corner→…→corner in the physical byte held for logical entry 10,
// resolved once [03 R-WATER-01 §1] rule 4. The caller places it immediately
// before the unit's model present in the same depth slot (rule 5).
func (c *Client) drawSelectionQuad(v frame.UnitView) {
	if c == nil || v.Model == "" {
		return
	}
	m := c.modelForUnit(v)
	if m == nil || m.compiled == nil {
		return
	}
	points, ok := c.selectionQuadScreen(m.compiled, v)
	if !ok {
		return
	}
	color := c.paletteIndex(selectionQuadLogicalColor) // resolved once [03 R-WATER-01 §1]
	for i := 0; i < 4; i++ {
		a, b := points[i], points[(i+1)%4]
		// Record then execute inline: classicSink.Line runs the same Bresenham
		// primitive drawIndexedLine this used to call directly [03 R-WATER-01 §1].
		c.emitLine(drawlist.Line{X0: a[0], Y0: a[1], X1: b[0], Y1: b[1], Index: color})
	}
}
