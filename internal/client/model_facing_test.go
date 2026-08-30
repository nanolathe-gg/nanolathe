package client

import (
	"testing"

	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestModelNoseFacesTravelDirection locks the one relationship that ties the
// three layers together: a unit's rendered nose points where the simulation is
// carrying it.
//
// The chain under test is
//
//	root fold      heading into Y, unchanged            [03 §2.4] C24
//	rotation       Ry: x' = c*x - s*z ; z' = s*x + c*z  [03 §2.4]
//	projection     sx = hi16(vx), sy = hi16(-vz) - ...  [R-RAST-01 §2]
//
// against the position step `vx = -sin[h]*speed, vz = -cos[h]*speed`
// [04 R-MOV-01 §4], which sends heading 0 toward -Z, 16384 toward -X, 32768
// toward +Z and 49152 toward +X. Screen X grows with world X and screen Y grows
// with world Z at the unit blit, so a travel direction and a rendered direction
// are directly comparable in screen offsets.
//
// The fold sign and the projection's Z negation are only correct TOGETHER:
// dropping either one leaves the model turning the wrong way, and dropping both
// leaves it exactly a half circle from its travel direction — the "walks
// backwards" defect this test exists to catch a second time.
//
// One input rests on an inference, and it is the model's nose direction. That a
// post-load model faces +Z follows from muzzle locators being authored at
// negative Z ([fmt 3do] "Model facing is -Z", 135 pieces to 13) plus the
// load-time half-turn negating X and Z ([03 §2.4]); measured over this install's
// 278 stock unit models, 133 flare pieces sit at positive post-load local Z
// against 12 negative. [03 §2.4] still records the heading-zero nose mapping
// itself as a supported inference with a probe pending, so if that probe ever
// inverts the mapping, this test's expectations invert with it — the arithmetic
// it locks does not change.
func TestModelNoseFacesTravelDirection(t *testing.T) {
	const reach = 4 // whole world units from the unit origin to the nose vertex
	m := &compiledmodel.Model{
		Root: 0,
		Name: "synthetic-facing",
		Pieces: []compiledmodel.Piece{{
			Name:   "body",
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{0, 0, 0},
				{0, 0, numeric.Fixed(reach * 65536)}, // the nose: post-load +Z
			},
		}},
	}

	// Travel directions of [04 R-MOV-01 §4], as screen offsets of the same reach.
	cases := []struct {
		heading      uint16
		wantX, wantY int32
		what         string
	}{
		{0, 0, -reach, "-Z, up-screen"},
		{16384, -reach, 0, "-X, left"},
		{32768, 0, reach, "+Z, down-screen"},
		{49152, reach, 0, "+X, right"},
	}
	for _, tc := range cases {
		states := presentationrender.BuildUnitPieceStates(m, nil, tc.heading, 0, 0)
		nose := compiledmodel.Compose(m, states, m.Root).Apply(m.Pieces[0].Vertices[1])
		gotX, gotY, _ := modelLocalVertex(nose, [3]numeric.Fixed{})
		if gotX != tc.wantX || gotY != tc.wantY {
			t.Fatalf("heading %d travels %s: rendered nose at screen offset (%d,%d), want (%d,%d)",
				tc.heading, tc.what, gotX, gotY, tc.wantX, tc.wantY)
		}
	}
}
