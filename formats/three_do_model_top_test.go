package formats

import "testing"

// modelTopFixture is an authored four-piece model built so that every clause of
// the model-top walk is load-bearing: the deepest vertex is not the highest, the
// winner sits two levels down and only wins once both ancestors' translations
// have been added, and it is reached through a sibling link rather than a first
// child. Authored here, never copied retail bytes.
//
//	base   translation +2   vertices up to +2
//	  mid    translation +5   vertices up to +1
//	    tip    translation +4   vertices up to +3
//	  arm    translation +1   vertices up to +20   (sibling of mid)
func modelTopFixture() *ThreeDO {
	f := func(v int32) int32 { return v * 65536 }
	return &ThreeDO{Root: 0, Objects: []ThreeDOObject{
		{Version: 1, Name: "base", Selection: -1, Translation: [3]int32{0, f(2), 0},
			FirstChild: 1, NextSibling: -1, Parent: -1,
			Vertices: []ThreeDOVertex{{0, 0, 0}, {f(1), f(2), 0}}},
		{Version: 1, Name: "mid", Selection: -1, Translation: [3]int32{0, f(5), 0},
			FirstChild: 2, NextSibling: 3, Parent: 0,
			Vertices: []ThreeDOVertex{{0, 0, 0}, {f(1), f(1), 0}}},
		{Version: 1, Name: "tip", Selection: -1, Translation: [3]int32{0, f(4), 0},
			FirstChild: -1, NextSibling: -1, Parent: 1,
			Vertices: []ThreeDOVertex{{0, 0, 0}, {f(1), f(3), 0}}},
		{Version: 1, Name: "arm", Selection: -1, Translation: [3]int32{0, f(1), 0},
			FirstChild: -1, NextSibling: -1, Parent: 0,
			Vertices: []ThreeDOVertex{{0, 0, 0}, {f(1), f(20), 0}}},
	}}
}

// TestModelTopWalkAccumulatesAncestorTranslations locks the arithmetic of the
// model-top walk [02 R-CAT-01 §7][07 R-REV-01 §7]: the maximum over the
// hierarchy of `vertexY` plus every ancestor translation on the path to it,
// with the child subtree's result raised by the parent's own translation and
// the sibling chain walked at each level.
//
// For the fixture the candidates are base 2+2 = 4, mid 1+5+2 = 8,
// tip 3+4+5+2 = 14 and arm 20+1+2 = 23, so the walk must return 23 world units:
// a walk that forgot an ancestor translation, stopped at the first child, or
// took the deepest piece rather than the highest would return 14 or less.
func TestModelTopWalkAccumulatesAncestorTranslations(t *testing.T) {
	const want = 23 << 16
	if got := modelTopFixture().ModelTop(); got != want {
		t.Fatalf("ModelTop = %d (%d world units), want %d (23 world units)", got, got>>16, want)
	}

	// The same answer must survive the byte layout, since retail measures the
	// model after loading it, not before writing it [fmt 3do].
	data, err := EncodeThreeDO(modelTopFixture())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadThreeDO(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.ModelTop(); got != want {
		t.Fatalf("ModelTop after a round trip = %d, want %d", got, want)
	}
}

// TestModelTopWalkIsFlooredAtZero locks the zero seed, which is the only reason
// the definition's Y extent can be read as a height: the accumulator starts at
// zero rather than at the first vertex, so a model hanging entirely below its
// origin reports 0 and never a negative [02 R-CAT-01 §7].
//
// This is also the observable half of the finding that retail has no min-Y
// walk: the loader zeroes the definition's minimum-Y word and measures only the
// maximum, so a submerged-origin model contributes nothing downward anywhere.
func TestModelTopWalkIsFlooredAtZero(t *testing.T) {
	f := func(v int32) int32 { return v * 65536 }
	below := &ThreeDO{Root: 0, Objects: []ThreeDOObject{
		{Version: 1, Name: "sunk", Selection: -1, Translation: [3]int32{0, f(-3), 0},
			FirstChild: 1, NextSibling: -1, Parent: -1,
			Vertices: []ThreeDOVertex{{0, f(-1), 0}, {f(1), f(-8), 0}}},
		{Version: 1, Name: "deeper", Selection: -1, Translation: [3]int32{0, f(-4), 0},
			FirstChild: -1, NextSibling: -1, Parent: 0,
			Vertices: []ThreeDOVertex{{0, f(-2), 0}}},
	}}
	if got := below.ModelTop(); got != 0 {
		t.Fatalf("ModelTop of an entirely-below-origin model = %d, want 0", got)
	}
}
