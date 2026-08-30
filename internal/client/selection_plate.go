package client

// Authored selection-plate projection is a diagnostic geometry API only
// [03 §2.4][03 §2.5][07 §8][07 §9]. Retail excludes the authored primitive from
// body, wireframe, click, drag, and cursor consumers [R-SEL-02A][03 §2.4.1]
// item 1, and that exclusion still holds. What retail does draw under a
// selected unit is the footprint quad of [03 R-WATER-01 §1] — derived from the
// root piece's vertex bounds, not from this authored plate — and it is owned
// by selection_quad.go. This file computes geometry only; it does not mutate
// selection membership or authoritative state (I6).

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SelectionPlateEdge joins two entries in ScreenVertices. Edges retain the
// authored polygon order, including n-gons; consumers must not reduce a plate
// to a fixed four-corner footprint [03 §2.4][07 §8].
type SelectionPlateEdge struct {
	From int
	To   int
}

// SelectionPlateGeometry is the projected diagnostic representation for one
// authored selection primitive. It is not a retail selection consumer.
// Visible and Selectable are caller-supplied
// committed-frame gates, further restricted when the declaring piece (or an
// ancestor) is hidden. Invalid/fallback results carry no fabricated geometry
// or radius [03 §2.4.1][07 §8][07 §9].
type SelectionPlateGeometry struct {
	Handle pool.Handle

	ScreenVertices [][2]int32
	ScreenEdges    []SelectionPlateEdge
	Bounds         Rect
	FrontKey       int32 // projected unit-origin Y, the established screen-Y bucket key [03 §1]

	Visible    bool
	Selectable bool

	SourcePiece     int
	SourcePrimitive int

	Valid      bool
	Fallback   bool
	Diagnostic string
}

// BuildSelectionPlateGeometry projects every valid authored selection
// primitive from one committed unit pose for diagnostics. It deliberately calls the same
// BuildUnitDraw hierarchy/root-angle path used by body rendering, then applies
// the same camera projection to the resulting world vertices [03 §2.4]
// [03 §2.5][03 §5.2]. The returned slice is stable in model piece order.
func BuildSelectionPlateGeometry(m *compiledmodel.Model, pose frame.UnitView, cam *camera.Camera, visible, selectable bool) []SelectionPlateGeometry {
	if m == nil {
		return []SelectionPlateGeometry{selectionPlateFallback(pose.Slot, visible, "model is nil")}
	}
	if cam == nil {
		return []SelectionPlateGeometry{selectionPlateFallback(pose.Slot, visible, "camera is nil")}
	}
	primitives := m.SelectionPrimitives()
	if len(primitives) == 0 {
		return []SelectionPlateGeometry{selectionPlateFallback(pose.Slot, visible, "model has no valid authored selection primitive")}
	}

	states := modelStatesForCompiled(m, pose.Pieces)
	draw := render.BuildUnitDraw(m, states, pose.Heading, pose.Pitch, pose.Bank, pose, nil)
	if draw == nil {
		return []SelectionPlateGeometry{selectionPlateFallback(pose.Slot, visible, "model draw could not be built")}
	}
	_, unitScreenY := render.ModelProjectToScreen(cam, [3]numeric.Fixed{pose.X, pose.Y, pose.Z})

	out := make([]SelectionPlateGeometry, 0, len(primitives))
	for _, selection := range primitives {
		geometry := SelectionPlateGeometry{
			Handle:          pose.Slot,
			SourcePiece:     selection.PieceIndex,
			SourcePrimitive: selection.PrimitiveIndex,
			Visible:         visible,
			Selectable:      selectable && visible,
			Valid:           true,
		}
		if selection.PieceIndex < 0 || selection.PieceIndex >= len(draw.Pieces) {
			geometry.Valid = false
			geometry.Visible = false
			geometry.Selectable = false
			geometry.Bounds = emptySelectionPlateBounds()
			geometry.Diagnostic = "selection primitive piece is absent from model draw"
			out = append(out, geometry)
			continue
		}
		piece := draw.Pieces[selection.PieceIndex]
		if len(piece.WorldVertices) == 0 {
			// BuildUnitDraw applies the same hidden-piece/hidden-ancestor gate as
			// body rendering; do not reimplement a second visibility walk here.
			geometry.Visible = false
			geometry.Selectable = false
			geometry.Bounds = emptySelectionPlateBounds()
			geometry.Diagnostic = "selection primitive is hidden by piece state"
			out = append(out, geometry)
			continue
		}

		vertices := make([][2]int32, len(selection.Primitive.VertexIndices))
		valid := true
		for i, vertexIndex := range selection.Primitive.VertexIndices {
			if int(vertexIndex) >= len(piece.WorldVertices) {
				valid = false
				break
			}
			sx, sy := render.ModelProjectToScreen(cam, piece.WorldVertices[vertexIndex])
			vertices[i] = [2]int32{sx, sy}
		}
		if !valid {
			geometry.Valid = false
			geometry.Visible = false
			geometry.Selectable = false
			geometry.Bounds = emptySelectionPlateBounds()
			geometry.Diagnostic = "selection primitive vertex is absent from model draw"
			out = append(out, geometry)
			continue
		}
		geometry.ScreenVertices = vertices
		geometry.ScreenEdges = make([]SelectionPlateEdge, len(vertices))
		for i := range vertices {
			geometry.ScreenEdges[i] = SelectionPlateEdge{From: i, To: (i + 1) % len(vertices)}
		}
		geometry.Bounds = selectionPlateBounds(vertices)
		geometry.FrontKey = unitScreenY
		out = append(out, geometry)
	}
	return out
}

func selectionPlateFallback(handle pool.Handle, visible bool, diagnostic string) SelectionPlateGeometry {
	return SelectionPlateGeometry{
		Handle:          handle,
		Visible:         visible,
		Bounds:          emptySelectionPlateBounds(),
		SourcePiece:     -1,
		SourcePrimitive: -1,
		Fallback:        true,
		Diagnostic:      diagnostic,
	}
}

func emptySelectionPlateBounds() Rect {
	return Rect{MinX: 1, MinY: 1, MaxX: 0, MaxY: 0}
}

func selectionPlateBounds(vertices [][2]int32) Rect {
	if len(vertices) == 0 {
		return emptySelectionPlateBounds()
	}
	minX, maxX := vertices[0][0], vertices[0][0]
	minY, maxY := vertices[0][1], vertices[0][1]
	for _, vertex := range vertices[1:] {
		if vertex[0] < minX {
			minX = vertex[0]
		}
		if vertex[0] > maxX {
			maxX = vertex[0]
		}
		if vertex[1] < minY {
			minY = vertex[1]
		}
		if vertex[1] > maxY {
			maxY = vertex[1]
		}
	}
	return Rect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}
