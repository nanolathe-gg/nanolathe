package render

import (
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"slices"
)

// DrawScratch retains presentation-only arrays for one borrowed UnitDraw.
// Use a different instance for every simultaneously live draw, including children.
type DrawScratch struct {
	draw       UnitDraw
	states     []model.PieceState
	transforms []model.Transform
	pieces     []PieceDraw
	storage    []pieceScratch
	compose    model.ComposeScratch
	visited    []bool
}
type pieceScratch struct {
	world               [][3]numeric.Fixed
	normals             [][3]float64
	count, rows, shades []int
	prims               []PrimitiveDraw
}

func reuseDrawSlice[T any](v []T, n int) []T {
	if cap(v) < n {
		return slices.Grow(v, n-len(v))[:n]
	}
	return v[:n]
}
