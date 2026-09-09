package render

import (
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// DrawScratch retains presentation-only arrays for one borrowed UnitDraw.
// Use a different instance for every simultaneously live draw, including children.
type DrawScratch struct {
	draw        UnitDraw
	states      []model.PieceState
	transforms  []model.Transform
	pieces      []PieceDraw
	storage     []pieceScratch
	compose     model.ComposeScratch
	hidden      []bool
	hiddenState []uint8
	hiddenStack []int
}
type pieceScratch struct {
	world        [][3]numeric.Fixed
	normals      []vertexNormal
	rows, shades []int
	prims        []PrimitiveDraw
}

// vertexNormal accumulates one vertex's unnormalized face-normal sum and the
// number of faces that contributed to it [03 §2.4.1]. Sum and count share one
// record so the accumulator is a single buffer with a single erase per piece.
type vertexNormal struct {
	sum   [3]float64
	count int
}

// reuseDrawSlice rewinds a retained buffer to exactly n elements, overshooting
// when it has to grow. A slot is reused by whichever subject lands in it, so
// growing to exactly the current need reallocated on every subject one element
// larger than the last; the returned length is still exactly n, so no caller
// can observe the extra capacity.
func reuseDrawSlice[T any](v []T, n int) []T {
	if cap(v) < n {
		// Retained inner buffers live in these elements, and a slot that a
		// smaller subject shortened still owns the buffers past its length, so
		// the whole capacity moves with the slice, not just the live prefix.
		grown := make([]T, cap(v), n+n/2)
		copy(grown, v[:cap(v)])
		v = grown
	}
	return v[:n]
}
