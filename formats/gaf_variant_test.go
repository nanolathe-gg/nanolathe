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
	if got.Compressed != 0 {
		t.Fatalf("doubled frame must be plain, Compressed=%d", got.Compressed)
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

// Resampled at 3/2 is the 1.5x view's variant (DESIGN_GPU_RENDERER §14.3):
// sizes are ceil(1.5x), anchors round half away from zero, and output pixel j
// reads source floor(2j/3), so a source column covers alternately two and one
// output columns. Nanolathe presentation rule, not retail behaviour.
func TestResampledThreeHalves(t *testing.T) {
	src := &GAFFrame{
		Width: 3, Height: 1,
		XOffset: 3, YOffset: -5,
		ColorKey:   9,
		Compressed: 1,
		Pixels:     []byte{1, 2, 3},
	}
	src.Transparent = []bool{false, true, false}
	src.PlainPixels, src.PlainTransparent = src.Pixels, src.Transparent

	got := src.Resampled(3, 2)
	if got.Width != 5 || got.Height != 2 {
		t.Fatalf("size %dx%d, want 5x2", got.Width, got.Height)
	}
	if got.XOffset != 5 || got.YOffset != -8 {
		t.Fatalf("anchor %d,%d, want 5,-8", got.XOffset, got.YOffset)
	}
	wantPixels := []byte{1, 1, 2, 3, 3, 1, 1, 2, 3, 3}
	if string(got.Pixels) != string(wantPixels) {
		t.Fatalf("pixels %v, want %v", got.Pixels, wantPixels)
	}
	wantClear := []bool{false, false, true, false, false, false, false, true, false, false}
	for i := range wantClear {
		if got.Transparent[i] != wantClear[i] {
			t.Fatalf("transparent[%d] = %v, want %v", i, got.Transparent[i], wantClear[i])
		}
	}
	if &got.PlainPixels[0] != &got.Pixels[0] {
		t.Fatal("a plain source keeps its plain raster aliased to Pixels")
	}
	// Doubled is Resampled(2, 1) exactly.
	d, r := src.Doubled(), src.Resampled(2, 1)
	if d.Width != r.Width || d.XOffset != r.XOffset || string(d.Pixels) != string(r.Pixels) {
		t.Fatal("Doubled and Resampled(2, 1) differ")
	}
}
