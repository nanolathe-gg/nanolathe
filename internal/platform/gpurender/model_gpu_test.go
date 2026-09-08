package gpurender

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
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
	// At row 3, the left D-C lanes remain fractional after the centre-sample
	// shift. A premature integer ModelVertex conversion would lose this lane.
	if got := strips[0].Vertices[0]; got.Key != 310.125 || got.U != 2.875 || got.Shade != 8.375 {
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

func TestTexturedQuadStripsKeepTheTwoChainMappingAcrossEveryRow(t *testing.T) {
	// This authored skewed quad has a texture boundary that would bend at the
	// 0→2 triangle diagonal. The textured path must instead use exactly the
	// two chains the scanline mapper uses, so each output row has one continuous
	// U/V interval [03 R-REN-03A §5][03 R-RAST-01 §1].
	f := drawlist.ModelFace{Texture: &formats.GAFFrame{Width: 8, Height: 8}, Vertices: []drawlist.ModelVertex{
		{X: 2, Y: 1, U: 0, V: 0},
		{X: 10, Y: 3, U: 7, V: 0},
		{X: 7, Y: 9, U: 7, V: 7},
		{X: 0, Y: 7, U: 0, V: 7},
	}}
	strips := modelTextureStrips(f)
	if len(strips) != 7 {
		t.Fatalf("textured strips = %d, want 7 positive rows", len(strips))
	}
	for i, s := range strips {
		y := int32(i + 2) // The top row has coincident chain endpoints.
		left, leftOK := chainAt(f.Vertices, 0, 2, -1, y)
		right, rightOK := chainAt(f.Vertices, 0, 2, 1, y)
		if !leftOK || !rightOK || right.X <= left.X {
			t.Fatalf("row %d did not produce the expected positive two-chain span", y)
		}
		left.Key, right.Key = centerSampledSpan(left.Key, right.Key, right.X-left.X)
		left.U, right.U = centerSampledSpan(left.U, right.U, right.X-left.X)
		left.V, right.V = centerSampledSpan(left.V, right.V, right.X-left.X)
		left.Shade, right.Shade = centerSampledSpan(left.Shade, right.Shade, right.X-left.X)
		got := s.Vertices
		if got[0] != left || got[1] != right || got[3].X != left.X || got[3].Y != left.Y+1 || got[2].X != right.X || got[2].Y != right.Y+1 {
			t.Fatalf("row %d strip = %#v, want scanline endpoints %#v %#v", y, got, left, right)
		}
		if got[0].U != got[3].U || got[1].U != got[2].U || got[0].V != got[3].V || got[1].V != got[2].V {
			t.Fatalf("row %d changed texture coordinates through its one-pixel height: %#v", y, got)
		}
	}

	// Rotating the first authored corner changes no edge or span. This guards
	// against accidentally choosing an internal triangulation diagonal.
	rotated := f
	rotated.Vertices = append(append([]drawlist.ModelVertex(nil), f.Vertices[1:]...), f.Vertices[0])
	other := modelTextureStrips(rotated)
	if len(other) != len(strips) {
		t.Fatalf("rotated textured strips = %d, want %d", len(other), len(strips))
	}
	for i := range strips {
		if len(other[i].Vertices) != len(strips[i].Vertices) {
			t.Fatalf("row %d rotated vertex count differs", i)
		}
		for j := range strips[i].Vertices {
			if other[i].Vertices[j] != strips[i].Vertices[j] {
				t.Fatalf("row %d vertex %d depends on an internal diagonal: got %#v, want %#v", i, j, other[i].Vertices[j], strips[i].Vertices[j])
			}
		}
	}
}

func TestCenterSampledSpanStartsAtTheCPUFirstColumn(t *testing.T) {
	left, right := centerSampledSpan(2, 10, 4)
	if left != 1 || right != 9 {
		t.Fatalf("center-adjusted endpoints = (%v,%v), want (1,9)", left, right)
	}
	if first := left + 0.5*(right-left)/4; first != 2 {
		t.Fatalf("first device sample = %v, want CPU left lane 2", first)
	}
	if last := left + 3.5*(right-left)/4; last != 8 {
		t.Fatalf("last device sample = %v, want CPU left+3*step 8", last)
	}
}

// A ring touching itself at the middle has no strict edge crossing and cannot
// be ear-triangulated. Its positive two-chain rows remain defined by
// [03 R-RAST-01 §1]; failure to find an ear must not discard the entire subject.
func TestUntriangulatedRingRetainsPositiveRows(t *testing.T) {
	f := fixturePinchedRing(0, 0)
	if polygonCrosses(f.Vertices) {
		t.Fatal("fixture must exercise a touching, not crossing ring")
	}
	if _, _, supported := modelFaceTriangles(f); supported {
		t.Fatal("fixture no longer exercises the non-triangle path")
	}
	rows := modelSpanStrips(f)
	want := [][3]float32{{0, 0, 4}, {1, 1, 3}, {3, 1, 3}}
	if len(rows) != len(want) {
		t.Fatalf("positive rows=%d, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		if row.Vertices[0].Y != want[i][0] || row.Vertices[0].X != want[i][1] || row.Vertices[1].X != want[i][2] {
			t.Fatalf("row %d=%+v, want %v", i, row.Vertices, want[i])
		}
	}
}

func fixturePinchedRing(x, y int32) drawlist.ModelFace {
	v := []drawlist.ModelVertex{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 2}, {X: 4, Y: 4}, {X: 0, Y: 4}, {X: 2, Y: 2}}
	for i := range v {
		v[i].X += x
		v[i].Y += y
		v[i].Key = 30
	}
	return drawlist.ModelFace{Vertices: v, Color: 19}
}
