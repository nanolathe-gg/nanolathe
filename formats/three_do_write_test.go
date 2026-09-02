package formats

import (
	"bytes"
	"reflect"
	"testing"
)

// authoredThreeDO is a hand-built model: a root with a ground plate and a
// textured quad, one child holding a flat triangle, and a sibling emit point.
func authoredThreeDO() *ThreeDO {
	f := func(v float64) int32 { return int32(v * 65536) }
	return &ThreeDO{Root: 0, Objects: []ThreeDOObject{
		{Version: 1, Name: "base", Selection: 0, FirstChild: 1, NextSibling: -1, Parent: -1,
			Vertices: []ThreeDOVertex{{f(-4), 0, f(-4)}, {f(4), 0, f(-4)}, {f(4), 0, f(4)}, {f(-4), 0, f(4)},
				{f(-2), f(1), f(-2)}, {f(2), f(1), f(-2)}, {f(2), f(3), f(-2)}, {f(-2), f(3), f(-2)}},
			Primitives: []ThreeDOPrimitive{
				{ColorIndex: 0, VertexIndices: []uint16{0, 3, 2, 1}, IsColored: 1},
				{ColorIndex: 0x0344E9FF, VertexIndices: []uint16{4, 5, 6, 7}, TextureName: "rm_plate", Unknown1: 7},
			}},
		{Version: 1, Name: "turret", Selection: -1, Translation: [3]int32{0, f(3), 0}, FirstChild: -1, NextSibling: 2, Parent: 0,
			Vertices:   []ThreeDOVertex{{0, 0, 0}, {f(1), 0, 0}, {0, f(1), 0}},
			Primitives: []ThreeDOPrimitive{{ColorIndex: 69, VertexIndices: []uint16{0, 1, 2}, IsColored: 1}}},
		{Version: 1, Name: "flare", Selection: -1, Translation: [3]int32{0, f(4), f(-1)}, FirstChild: -1, NextSibling: -1, Parent: 0,
			Vertices: []ThreeDOVertex{{0, 0, 0}}},
	}}
}

func TestEncodeThreeDORoundTrip(t *testing.T) {
	want := authoredThreeDO()
	data, err := EncodeThreeDO(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadThreeDO(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != 0 || len(got.Objects) != len(want.Objects) {
		t.Fatalf("root %d objects %d", got.Root, len(got.Objects))
	}
	for i := range want.Objects {
		w, g := want.Objects[i], got.Objects[i]
		if g.Name != w.Name || g.Translation != w.Translation || g.Selection != w.Selection || g.Parent != w.Parent || g.FirstChild != w.FirstChild || g.NextSibling != w.NextSibling {
			t.Fatalf("object %d: got %+v want %+v", i, g, w)
		}
		if !reflect.DeepEqual(g.Vertices, w.Vertices) {
			t.Fatalf("object %s vertices differ", w.Name)
		}
		if len(g.Primitives) != len(w.Primitives) {
			t.Fatalf("object %s primitives %d want %d", w.Name, len(g.Primitives), len(w.Primitives))
		}
		for p := range w.Primitives {
			wp, gp := w.Primitives[p], g.Primitives[p]
			if gp.ColorIndex != wp.ColorIndex || gp.TextureName != wp.TextureName || gp.IsColored != wp.IsColored || gp.Unknown1 != wp.Unknown1 || !reflect.DeepEqual(gp.VertexIndices, wp.VertexIndices) {
				t.Fatalf("object %s primitive %d: got %+v want %+v", w.Name, p, gp, wp)
			}
		}
	}
	// The loader's reordering is a fixed point for already-ordered data, so a
	// second encode reproduces the bytes exactly.
	again, err := EncodeThreeDO(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Fatal("re-encoding a loaded model changed the bytes")
	}
}

func TestEncodeThreeDORejectsTexturedTriangle(t *testing.T) {
	m := authoredThreeDO()
	m.Objects[1].Primitives[0].TextureName = "rm_plate"
	if _, err := EncodeThreeDO(m); err == nil {
		t.Fatal("textured triangle must be rejected: retail's quad mapper has no textured n-gon path")
	}
}
