package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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

// referenceSelectionQuadCorners retains the general composition path to check
// the allocation-free rotation against [03 §2.4] C21/C24 and [03 R-WATER-01 §1].
func referenceSelectionQuadCorners(m *compiledmodel.Model, heading, pitch, bank uint16) [4][3]numeric.Fixed {
	a, b := selectionQuadBounds(m)
	corners := [4][3]numeric.Fixed{{a[0], a[1], a[2]}, {b[0], a[1], a[2]}, {b[0], a[1], b[2]}, {a[0], a[1], b[2]}}
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	compiledmodel.FoldRootAngles(states, m.Root, heading, pitch, bank)
	root := m.Pieces[m.Root]
	states[m.Root].Trans = [3]numeric.Fixed{-root.Translate[0], -root.Translate[1], -root.Translate[2]}
	rotation := compiledmodel.Compose(m, states, m.Root)
	for i := range corners {
		corners[i] = rotation.Apply(corners[i])
	}
	return corners
}

func selectionRotationModel() *compiledmodel.Model {
	// Nonzero root index and fractional translations exercise offset cancellation;
	// the other pieces must not affect a root-only footprint.
	m := &compiledmodel.Model{Root: 2, Pieces: make([]compiledmodel.Piece, 32)}
	m.Pieces[2] = compiledmodel.Piece{
		Parent:    -1,
		Translate: [3]numeric.Fixed{123456, -654321, 987654},
		Vertices:  [][3]numeric.Fixed{{-876543, -123456, -2345678}, {1234567, 2345678, 3456789}, {456789, -567890, 678901}},
	}
	return m
}

func TestSelectionQuadRotationMatchesComposition(t *testing.T) {
	m := selectionRotationModel()
	angles := []uint16{0, 1, 8191, 16383, 16384, 16385, 32767, 32768, 49152, 65535}
	for _, heading := range angles {
		for _, pitch := range angles {
			for _, bank := range angles {
				got := selectionQuadCorners(m, heading, pitch, bank)
				want := referenceSelectionQuadCorners(m, heading, pitch, bank)
				if got != want {
					t.Fatalf("orientation %d/%d/%d: got %v, want %v", heading, pitch, bank, got, want)
				}
			}
		}
	}
	for _, angles := range [][3]uint16{{}, {12345, 45678, 56789}} {
		if n := testing.AllocsPerRun(100, func() {
			selectionQuadCorners(m, angles[0], angles[1], angles[2])
		}); n != 0 {
			t.Fatalf("orientation %v allocated %g times, want zero", angles, n)
		}
	}
}

var selectionCornersResult [4][3]numeric.Fixed

func BenchmarkSelectionQuadCorners(b *testing.B) {
	m := selectionRotationModel()
	for _, tc := range []struct {
		name    string
		corners func(*compiledmodel.Model, uint16, uint16, uint16) [4][3]numeric.Fixed
	}{{"composition", referenceSelectionQuadCorners}, {"in-place", selectionQuadCorners}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				selectionCornersResult = tc.corners(m, uint16(i), 12345, 45678)
			}
		})
	}
}
