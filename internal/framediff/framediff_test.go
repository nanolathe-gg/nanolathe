package framediff

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

// solid builds a w×h opaque RGBA filled with one colour.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestIdenticalImagesNoDifference locks the parity-gate zero: two identical
// captures report no differing pixels and no clusters (§6 gate 2).
func TestIdenticalImagesNoDifference(t *testing.T) {
	a := solid(16, 12, color.RGBA{10, 20, 30, 255})
	b := solid(16, 12, color.RGBA{10, 20, 30, 255})
	res, err := Compare(a, b, 8)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
	if res.Total != 16*12 {
		t.Errorf("Total = %d, want %d", res.Total, 16*12)
	}
	if len(res.Clusters) != 0 {
		t.Errorf("Clusters = %d, want 0", len(res.Clusters))
	}
}

// TestSinglePixelDifference locks the count and clustering for one changed
// pixel: exactly one differing pixel, one cluster whose bounds are that pixel.
func TestSinglePixelDifference(t *testing.T) {
	a := solid(16, 12, color.RGBA{0, 0, 0, 255})
	b := solid(16, 12, color.RGBA{0, 0, 0, 255})
	b.SetRGBA(5, 7, color.RGBA{1, 0, 0, 255})
	res, err := Compare(a, b, 8)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("Count = %d, want 1", res.Count)
	}
	if !res.Differing[7*16+5] {
		t.Errorf("Differing mask does not mark (5,7)")
	}
	if len(res.Clusters) != 1 {
		t.Fatalf("Clusters = %d, want 1", len(res.Clusters))
	}
	c := res.Clusters[0]
	if c.Count != 1 || c.X0 != 5 || c.Y0 != 7 || c.X1 != 5 || c.Y1 != 7 {
		t.Errorf("cluster = %+v, want count 1 bounds (5,7)-(5,7)", c)
	}
}

// TestAlphaCounts locks that alpha differences count: a transparent pixel where
// the other is opaque is a difference even when RGB matches.
func TestAlphaCounts(t *testing.T) {
	a := solid(4, 4, color.RGBA{9, 9, 9, 255})
	b := solid(4, 4, color.RGBA{9, 9, 9, 255})
	b.SetRGBA(1, 1, color.RGBA{9, 9, 9, 0})
	res, err := Compare(a, b, 8)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1 (alpha-only difference)", res.Count)
	}
}

// TestTwoClusters locks that two separated differences form two clusters at a
// small block size, largest first.
func TestTwoClusters(t *testing.T) {
	a := solid(40, 8, color.RGBA{0, 0, 0, 255})
	b := solid(40, 8, color.RGBA{0, 0, 0, 255})
	// Left blob: two pixels. Right blob: one pixel, far away.
	b.SetRGBA(1, 1, color.RGBA{255, 0, 0, 255})
	b.SetRGBA(2, 1, color.RGBA{255, 0, 0, 255})
	b.SetRGBA(38, 6, color.RGBA{255, 0, 0, 255})
	res, err := Compare(a, b, 2)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.Count != 3 {
		t.Fatalf("Count = %d, want 3", res.Count)
	}
	if len(res.Clusters) != 2 {
		t.Fatalf("Clusters = %d, want 2", len(res.Clusters))
	}
	if res.Clusters[0].Count < res.Clusters[1].Count {
		t.Errorf("clusters not sorted largest first: %+v", res.Clusters)
	}
	if res.Clusters[0].Count != 2 || res.Clusters[1].Count != 1 {
		t.Errorf("cluster counts = %d,%d, want 2,1", res.Clusters[0].Count, res.Clusters[1].Count)
	}
}

// TestSizeMismatch locks the mismatch signal callers turn into exit code 2.
func TestSizeMismatch(t *testing.T) {
	a := solid(16, 12, color.RGBA{0, 0, 0, 255})
	b := solid(8, 12, color.RGBA{0, 0, 0, 255})
	_, err := Compare(a, b, 8)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
}

// TestDiffImage locks the diff render: differing pixels are magenta, unchanged
// pixels are the first image dimmed to a third.
func TestDiffImage(t *testing.T) {
	a := solid(4, 4, color.RGBA{99, 99, 99, 255})
	b := solid(4, 4, color.RGBA{99, 99, 99, 255})
	b.SetRGBA(2, 2, color.RGBA{0, 0, 0, 255})
	res, err := Compare(a, b, 8)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	diff := DiffImage(a, res.Differing, res.Width, res.Height)
	if got := diff.RGBAAt(2, 2); got != (color.RGBA{255, 0, 255, 255}) {
		t.Errorf("diff at (2,2) = %+v, want magenta", got)
	}
	if got := diff.RGBAAt(0, 0); got != (color.RGBA{33, 33, 33, 255}) {
		t.Errorf("diff at (0,0) = %+v, want dimmed 33,33,33", got)
	}
}
