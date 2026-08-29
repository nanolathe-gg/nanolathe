package client

import (
	"testing"

	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestShadowProjectionShearsByAQuarterHeight locks retail's shadow shear: a
// quarter of the vertex height added in X and subtracted in screen Y, with no
// half-height term at all [R-REN-03D §2].
func TestShadowProjectionShearsByAQuarterHeight(t *testing.T) {
	f := func(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
	origin := [3]numeric.Fixed{0, 0, 0}

	sx, sy, ry := shadowLocalVertex([3]numeric.Fixed{f(10), f(8), f(20)}, origin)
	if sx != 12 || sy != 18 || ry != 8 {
		t.Fatalf("shadow projection = (%d,%d) ry=%d, want (12,18) ry=8", sx, sy, ry)
	}
	// The quarter is an arithmetic shift, so a negative height floors.
	sx, sy, _ = shadowLocalVertex([3]numeric.Fixed{0, f(-3), 0}, origin)
	if sx != -1 || sy != 1 {
		t.Fatalf("negative height shear = (%d,%d), want (-1,1)", sx, sy)
	}
	// A ground-level vertex is not sheared: the shadow lies flat.
	sx, sy, _ = shadowLocalVertex([3]numeric.Fixed{f(7), 0, f(9)}, origin)
	if sx != 7 || sy != 9 {
		t.Fatalf("ground vertex = (%d,%d), want (7,9)", sx, sy)
	}
}

// TestShadowGateReadsOptionsAndAuthoredKeys locks the three option bits and the
// three authored keys [R-REN-03D §1].
func TestShadowGateReadsOptionsAndAuthoredKeys(t *testing.T) {
	c := compositionClient(t)
	c.shadows, c.vehicleShadows, c.shading = true, true, true
	if !c.castsModelShadow(false, false, false) {
		t.Fatal("an ordinary unit with all options on must cast a shadow")
	}
	for _, tc := range []struct {
		name                        string
		noShadow, canHover, floater bool
	}{
		{"noshadow", true, false, false},
		{"canhover", false, true, false},
		{"floater", false, false, true},
	} {
		if c.castsModelShadow(tc.noShadow, tc.canHover, tc.floater) {
			t.Fatalf("%s must suppress the model shadow", tc.name)
		}
	}
	for _, tc := range []struct {
		name                     string
		master, vehicle, shading bool
	}{
		{"master off", false, true, true},
		{"vehicle off", true, false, true},
		// Every tinted blit is gated on shading, so the shadow goes with it.
		{"shading off", true, true, false},
	} {
		c.shadows, c.vehicleShadows, c.shading = tc.master, tc.vehicle, tc.shading
		if c.castsModelShadow(false, false, false) {
			t.Fatalf("%s must suppress the model shadow", tc.name)
		}
	}
}

// TestTintedCommitBlendsThroughALP locks the translucent blit: the destination
// resolves to ALP[src*256 + dst], which for a shadow's index-0 silhouette
// darkens the ground rather than replacing it [R-REN-03D §4].
func TestTintedCommitBlendsThroughALP(t *testing.T) {
	c := compositionClient(t)
	for i := range c.indexed {
		c.indexed[i] = 200 // ground
	}
	img := newModelImage(2, 1, 0, 0, 10, 10, false, 1)
	img.write(0, shadowColorIndex, true)
	// The second pixel stays background and must not touch the ground.
	img.tintedCommit(c.indexed, c.width, c.height, &c.pal.Alpha)

	want := c.pal.Alpha[int(shadowColorIndex)*256+200]
	if got := c.indexed[10*c.width+10]; got != want {
		t.Fatalf("shadow pixel = %d, want ALP[0,200] = %d", got, want)
	}
	if got := c.indexed[10*c.width+11]; got != 200 {
		t.Fatalf("background pixel darkened the ground to %d, want 200 untouched", got)
	}
}

// TestShadowIsFilledWithPaletteIndexZero locks the fill colour every shadow
// face carries [R-REN-03D §2].
func TestShadowIsFilledWithPaletteIndexZero(t *testing.T) {
	if shadowColorIndex != 0 {
		t.Fatalf("shadow fill index = %d, want 0", shadowColorIndex)
	}
	if shadowKeyBias != 25 {
		t.Fatalf("shadow key bias = %d, want 25", shadowKeyBias)
	}
	if shadowXOffset != 5 {
		t.Fatalf("shadow X offset = %d, want 5", shadowXOffset)
	}
}

// TestCollectShadowTrisIgnoresTexturesAndTheSelectionPlate locks that the
// shadow pass fills every face flat, at any arity, and skips the load-time
// selection primitive exactly as the body pass does [R-REN-03D §2].
func TestCollectShadowTrisIgnoresTexturesAndTheSelectionPlate(t *testing.T) {
	c := compositionClient(t)
	f := func(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
	verts := [][3]numeric.Fixed{{0, 0, 0}, {f(4), 0, 0}, {f(4), 0, f(4)}, {0, 0, f(4)}, {f(2), f(4), f(2)}}
	draw := &presentationrender.UnitDraw{
		Model: &compiledmodel.Model{Pieces: []compiledmodel.Piece{{Selection: true}}},
		Pieces: []presentationrender.PieceDraw{{
			WorldVertices: verts,
			Primitives: []presentationrender.PrimitiveDraw{
				{VertexIndices: []uint16{0, 1, 2, 3}},                       // the selection plate, skipped
				{TextureName: "anything", VertexIndices: []uint16{0, 1, 4}}, // textured: still a flat shadow face
				{IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3, 4}},      // a flat 5-gon
			},
		}},
	}
	tris := c.collectShadowTris(draw)
	// Primitive 0 is skipped; primitive 1 fans to 1 triangle; primitive 2 to 3.
	if len(tris) != 4 {
		t.Fatalf("shadow tris = %d, want 4 (plate skipped, 1 + 3 fan triangles)", len(tris))
	}
	for i := range tris {
		if tris[i].color != shadowColorIndex {
			t.Fatalf("shadow triangle %d filled with %d, want %d", i, tris[i].color, shadowColorIndex)
		}
		if tris[i].frame != nil {
			t.Fatalf("shadow triangle %d carries a texture; the shadow pass never samples one", i)
		}
	}
}

// TestEraseAtOrBelowClipsTheBuriedHalf locks the digger clip [R-REN-03A §8].
func TestEraseAtOrBelowClipsTheBuriedHalf(t *testing.T) {
	img := newModelImage(3, 1, 0, 0, 0, 0, true, 1)
	img.write(0, 40, true)
	img.height[0] = 100 // below the origin for a digger: 25 units down
	img.write(1, 41, true)
	img.height[1] = 125 // exactly at the origin: clipped, the test is inclusive
	img.write(2, 42, true)
	img.height[2] = 126 // above the origin: kept

	img.eraseAtOrBelow(125)

	if img.covered[0] || img.covered[1] {
		t.Fatal("digger clip must erase at and below the threshold")
	}
	if !img.covered[2] || img.color[2] != 42 {
		t.Fatalf("pixel above the threshold was erased: covered=%v colour=%d", img.covered[2], img.color[2])
	}
}
