package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func fixedUnits(v float64) numeric.Fixed { return numeric.Fixed(int64(v * 65536)) }

// TestSelectionQuadProjectsRootBounds locks the footprint quad's geometry for a
// synthetic root piece at heading 0 [03 R-WATER-01 §1] rules 1-3.
//
// The root piece's vertices span x -8..16, y 0..20, z -12..24; with both
// accumulators seeded at 0 the bounds are A = (-8, 0, -12), B = (16, 20, 24),
// so the four corners sit on y = A.y = 0. The `wake` piece carries a single
// vertex far outside that box and must contribute nothing, and it is not the
// root piece either. With every orientation axis zero the corners are
// unrotated, so with the camera at the origin the projection reduces to
// sx = unitX + corner.x and sy = unitZ - corner.z: the rotated Z is
// subtracted, which is the 3DO handedness flip of [R-RAST-01 §2].
func TestSelectionQuadProjectsRootBounds(t *testing.T) {
	c := newTestClient(t)
	m := &compiledmodel.Model{
		Root: 0,
		Name: "synthetic-quad",
		Pieces: []compiledmodel.Piece{
			{
				Name:   "base",
				Parent: -1,
				Vertices: [][3]numeric.Fixed{
					{fixedUnits(-8), fixedUnits(0), fixedUnits(-12)},
					{fixedUnits(16), fixedUnits(20), fixedUnits(24)},
					{fixedUnits(4), fixedUnits(5), fixedUnits(6)},
				},
			},
			{
				Name:     "wake",
				Parent:   0,
				Vertices: [][3]numeric.Fixed{{fixedUnits(400), fixedUnits(400), fixedUnits(400)}},
			},
		},
	}
	m.Pieces[0].Children = []int{1}

	view := frame.UnitView{
		Model: "synthetic-quad",
		X:     fixedUnits(100),
		Y:     fixedUnits(0),
		Z:     fixedUnits(200),
	}
	got, ok := c.selectionQuadScreen(m, view)
	if !ok {
		t.Fatal("selectionQuadScreen reported no quad for a three-vertex root piece")
	}
	want := [4][2]int32{{92, 212}, {116, 212}, {116, 176}, {92, 176}}
	if got != want {
		t.Fatalf("footprint quad corners = %v, want %v", got, want)
	}
}
