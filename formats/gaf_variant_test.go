package formats

import "testing"

// Doubled is a Nanolathe presentation rule (DESIGN_GPU_RENDERER §14.3), not
// retail behaviour: retail has one scale and no 2x variant of a GAF frame.
func TestDoubledPlainFrame(t *testing.T) {
	src := &GAFFrame{
		Width: 2, Height: 2,
		XOffset: 3, YOffset: -4,
		ColorKey:   9,
		Compressed: 1,
		Pixels:     []byte{1, 2, 9, 4},
	}
	src.Transparent = []bool{false, false, true, false}
	src.PlainPixels, src.PlainTransparent = src.Pixels, src.Transparent

	got := src.Doubled()
	if got.Width != 4 || got.Height != 4 {
		t.Fatalf("size %dx%d, want 4x4", got.Width, got.Height)
	}
	if got.XOffset != 6 || got.YOffset != -8 {
		t.Fatalf("anchor %d,%d, want 6,-8", got.XOffset, got.YOffset)
	}
	if got.ColorKey != 9 {
		t.Fatalf("colour key %d, want 9", got.ColorKey)
	}
	if got.Compressed != src.Compressed {
		t.Fatalf("doubled frame must retain source dispatch, Compressed=%d", got.Compressed)
	}
	want := []byte{
		1, 1, 2, 2,
		1, 1, 2, 2,
		9, 9, 4, 4,
		9, 9, 4, 4,
	}
	for i, w := range want {
		if got.Pixels[i] != w {
			t.Fatalf("pixel %d = %d, want %d", i, got.Pixels[i], w)
		}
	}
	for i, w := range want {
		clear := w == 9 && (i/4 >= 2 && i%4 < 2)
		if got.Transparent[i] != clear {
			t.Fatalf("transparency %d = %v, want %v", i, got.Transparent[i], clear)
		}
	}
	if &got.PlainPixels[0] != &got.Pixels[0] {
		t.Fatal("the plain raster must alias the pixels, as the decoder's does")
	}
	// Reading through At agrees with the raster: the 2x2 block of source (0,1)
	// is transparent, the block of source (1,1) opaque.
	if _, ok := got.At(0, 3); ok {
		t.Fatal("doubled key pixel became opaque")
	}
	if b, ok := got.At(3, 3); !ok || b != 4 {
		t.Fatalf("doubled opaque pixel = %d,%v", b, ok)
	}
	if src.Width != 2 || src.Pixels[0] != 1 {
		t.Fatal("Doubled mutated its source")
	}
}

// A composite doubles leaf by leaf and keeps its composite fields, so the
// classic general-GAF leaf walk still finds children and still selects each
// child's own blitter (DESIGN_GPU_RENDERER §14.3).
func TestDoubledCompositeKeepsLeaves(t *testing.T) {
	leaf := &GAFFrame{Width: 1, Height: 1, XOffset: 1, YOffset: 1, ColorKey: 9, Pixels: []byte{7}, Transparent: []bool{false}, AlternateBlitter: 2}
	leaf.PlainPixels, leaf.PlainTransparent = leaf.Pixels, leaf.Transparent
	parent := &GAFFrame{Width: 2, Height: 1, XOffset: 2, YOffset: 0, ColorKey: 9, Pixels: []byte{9, 9}, Transparent: []bool{true, true}, SubframeCount: 1, Subframes: []*GAFFrame{leaf}}
	parent.PlainPixels, parent.PlainTransparent = parent.Pixels, parent.Transparent

	got := parent.Doubled()
	if got.SubframeCount != 1 || len(got.Subframes) != 1 {
		t.Fatalf("composite fields lost: count=%d children=%d", got.SubframeCount, len(got.Subframes))
	}
	child := got.Subframes[0]
	if child.Width != 2 || child.Height != 2 || child.XOffset != 2 || child.YOffset != 2 {
		t.Fatalf("child geometry %dx%d at %d,%d", child.Width, child.Height, child.XOffset, child.YOffset)
	}
	if child.AlternateBlitter != 2 {
		t.Fatalf("child blitter selector %d, want 2", child.AlternateBlitter)
	}
	if got.XOffset != 4 {
		t.Fatalf("parent anchor %d, want 4", got.XOffset)
	}
}
