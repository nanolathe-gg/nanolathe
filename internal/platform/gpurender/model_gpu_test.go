package gpurender

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/drawlist"
)

func TestModelFaceSupportHonorsSpanWalkWindingAndConvexity(t *testing.T) {
	face := func(vertices ...drawlist.ModelVertex) drawlist.ModelFace {
		return drawlist.ModelFace{Vertices: vertices}
	}
	cw := face(
		drawlist.ModelVertex{X: 0, Y: 0}, drawlist.ModelVertex{X: 4, Y: 0},
		drawlist.ModelVertex{X: 4, Y: 4}, drawlist.ModelVertex{X: 0, Y: 4},
	)
	if triangles, paints, ok := modelFaceTriangles(cw); !ok || !paints || len(triangles) != 6 {
		t.Fatal("clockwise screen ring rejected; CPU right-chain span would paint")
	}
	ccw := face(cw.Vertices[3], cw.Vertices[2], cw.Vertices[1], cw.Vertices[0])
	if _, paints, ok := modelFaceTriangles(ccw); !ok || paints {
		t.Fatal("counter-clockwise screen ring was not retained as culled input")
	}
	concave := face(
		drawlist.ModelVertex{X: 0, Y: 0}, drawlist.ModelVertex{X: 4, Y: 0},
		drawlist.ModelVertex{X: 2, Y: 2}, drawlist.ModelVertex{X: 4, Y: 4}, drawlist.ModelVertex{X: 0, Y: 4},
	)
	if triangles, paints, ok := modelFaceTriangles(concave); !ok || !paints || len(triangles) != 9 {
		t.Fatal("simple concave ring was not ear-triangulated")
	}
}

func TestModelFaceTrianglesAcceptsProjectedConvexQuad(t *testing.T) {
	f := drawlist.ModelFace{Vertices: []drawlist.ModelVertex{{X: 2, Y: 1}, {X: 9, Y: 3}, {X: 7, Y: 8}, {X: 1, Y: 6}}}
	triangles, paints, ok := modelFaceTriangles(f)
	if !ok || !paints || len(triangles) != 6 {
		t.Fatalf("convex projected quad = triangles %v paints=%v ok=%v crosses=%v", triangles, paints, ok, polygonCrosses(f.Vertices))
	}
}

func TestModelFaceTrianglesAcceptsProjectedConcaveQuad(t *testing.T) {
	f := drawlist.ModelFace{Vertices: []drawlist.ModelVertex{{X: 1, Y: 1}, {X: 8, Y: 2}, {X: 4, Y: 4}, {X: 1, Y: 7}}}
	triangles, paints, ok := modelFaceTriangles(f)
	if !ok || !paints || len(triangles) != 6 {
		t.Fatalf("concave projected quad = %v paints=%v ok=%v", triangles, paints, ok)
	}
}

func TestModelFaceTrianglesRejectsStrictlyCrossedRing(t *testing.T) {
	f := drawlist.ModelFace{Vertices: []drawlist.ModelVertex{{X: 1, Y: 1}, {X: 9, Y: 8}, {X: 1, Y: 8}, {X: 7, Y: 1}}}
	if _, paints, ok := modelFaceTriangles(f); !paints || ok {
		t.Fatal("strictly crossed ring was accepted")
	}
	if got := cross(f.Vertices[0], f.Vertices[1], f.Vertices[2]); got != 56 {
		t.Fatalf("cross = %d, want 56", got)
	}
}

func TestFoldedStripsKeepPositiveSpansAndFractionalAttributes(t *testing.T) {
	// Authored projection fixture: the two top corners fold back across the
	// origin. The CPU two-chain walk has only the three positive rows below;
	// the narrow negative lobe above them must not become GPU coverage.
	f := drawlist.ModelFace{Vertices: []drawlist.ModelVertex{
		{X: 0, Y: 0, Key: 250, U: 0, V: 0, Shade: 1},
		{X: 8, Y: 4, Key: 266, U: 8, V: 4, Shade: 5},
		{X: 6, Y: 6, Key: 500, U: 6, V: 6, Shade: 12},
		{X: 2, Y: 0, Key: 101, U: 1, V: 2, Shade: 3},
	}}
	strips := foldedStrips(f)
	if len(strips) != 3 {
		t.Fatalf("folded strips = %d, want three positive rows", len(strips))
	}
	want := [][3]float32{{3, 4, 6}, {4, 5, 8}, {5, 6, 7}}
	for i, s := range strips {
		v := s.Vertices
		if v[0].Y != want[i][0] || v[0].X != want[i][1] || v[1].X != want[i][2] {
			t.Fatalf("strip %d = left (%v,%v), right %v; want row %v [%v,%v)", i, v[0].Y, v[0].X, v[1].X, want[i][0], want[i][1], want[i][2])
		}
		if v[2].Y != v[1].Y+1 || v[3].Y != v[0].Y+1 {
			t.Fatalf("strip %d does not span exactly one row: %#v", i, v)
		}
		if v[0].Key != v[3].Key || v[1].Key != v[2].Key || v[0].U != v[3].U || v[1].U != v[2].U || v[0].Shade != v[3].Shade || v[1].Shade != v[2].Shade {
			t.Fatalf("strip %d vertical attributes changed: %#v", i, v)
		}
	}
	// At row 3, the left D-C key/U/shade interpolation is fractional. A
	// premature integer ModelVertex conversion would discard the .5 lanes.
	if got := strips[0].Vertices[0]; got.Key != 300.5 || got.U != 3.5 || got.Shade != 7.5 {
		t.Fatalf("fractional row attributes narrowed early: key=%v u=%v shade=%v", got.Key, got.U, got.Shade)
	}
}

func TestBiasedEdgeXUsesOneTruncatedSignedSlope(t *testing.T) {
	a := drawlist.ModelVertex{X: 3, Y: 0}
	b := drawlist.ModelVertex{X: -2, Y: 4}
	for y, want := range []int32{3, 2, 1, 0, -2} {
		if got := biasedEdgeX(a, b, int32(y)); got != want {
			t.Fatalf("biasedEdgeX row %d = %d, want %d", y, got, want)
		}
	}
}
