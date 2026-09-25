package camera

import "testing"

// The megamap extent and fit are the shipped ProTA 4.8 lens
// ([draw-engine-interface "Map scale", "What the view shows"]).
func TestMegamapExtentUsesTNTCells(t *testing.T) {
	w, h := MegamapExtent(128, 64)
	if w != 127*16 || h != 60*16 {
		t.Fatalf("extent = %dx%d, want (W-1)*16 x (H-4)*16", w, h)
	}
}

func TestLayoutMegamapPreservesAspectAndCentres(t *testing.T) {
	// A wide map fills the viewport width and is centred vertically.
	l := LayoutMegamap(129, 32, 1000, 600, 2000, 1000)
	if l.W != 1000 || l.H != 500 || l.X != 129 || l.Y != 32+50 {
		t.Fatalf("wide fit = %+v", l)
	}
	// A tall map fills the height and is centred horizontally.
	l = LayoutMegamap(0, 0, 1000, 600, 1000, 2000)
	if l.H != 600 || l.W != 300 || l.X != 350 || l.Y != 0 {
		t.Fatalf("tall fit = %+v", l)
	}
	// A spare margin of two pixels or less is not split.
	l = LayoutMegamap(0, 0, 1002, 1000, 1000, 1000)
	if l.W != 1000 || l.X != 0 {
		t.Fatalf("two-pixel margin was split: %+v", l)
	}
	l = LayoutMegamap(0, 0, 1003, 1000, 1000, 1000)
	if l.W != 1000 || l.X != 1 {
		t.Fatalf("three-pixel margin not split: %+v", l)
	}
}

func TestMegamapProjectionTruncatesWithHalfHeightShear(t *testing.T) {
	l := LayoutMegamap(0, 0, 300, 300, 900, 900) // one image pixel per three world units
	if x, y := l.Project(10, 4, 20); x != 3 || y != 6 {
		t.Fatalf("project = %d,%d, want trunc(10/3)=3, trunc((20-2)/3)=6", x, y)
	}
	// The reverse conversion truncates and has no shear term.
	if wx, wz := l.Unproject(3, 6); wx != 9 || wz != 18 {
		t.Fatalf("unproject = %d,%d", wx, wz)
	}
	// Pointer conversion clamps onto the image.
	if wx, wz := l.ScreenToWorld(-50, 400); wx != 0 || wz != 897 {
		t.Fatalf("clamped pointer = %d,%d", wx, wz)
	}
	if l.RowPitch() != 300 {
		t.Fatalf("pitch = %d", l.RowPitch())
	}
	if (MegamapLens{W: 301, H: 1, ExtentW: 1, ExtentH: 1}).RowPitch() != 300 {
		t.Fatal("row pitch must round down to a multiple of four")
	}
}
