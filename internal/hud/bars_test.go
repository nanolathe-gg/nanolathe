package hud

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestBarFillHorizontalVectors(t *testing.T) {
	anchor := Rect{X1: 10, Y1: 20, X2: 110, Y2: 30} // w=100 h=10
	tests := []struct {
		frac float32
		want Rect
	}{
		{0, Rect{X1: 10, Y1: 20, X2: 10, Y2: 30}},
		{0.5, Rect{X1: 10, Y1: 20, X2: 60, Y2: 30}},
		{1, Rect{X1: 10, Y1: 20, X2: 110, Y2: 30}},
		{0.25, Rect{X1: 10, Y1: 20, X2: 35, Y2: 30}},
		{0.33, Rect{X1: 10, Y1: 20, X2: 43, Y2: 30}}, // 0.33*100=33 trunc
		{0.75, Rect{X1: 10, Y1: 20, X2: 85, Y2: 30}},
	}
	for _, tc := range tests {
		got := BarFillHorizontal(anchor, tc.frac)
		if got != tc.want {
			t.Errorf("BarFillHorizontal frac %v: got %+v want %+v", tc.frac, got, tc.want)
		}
		// Alias functions should match
		if got2 := EnergyBarFill(anchor, tc.frac); got2 != got {
			t.Errorf("EnergyBarFill mismatch at %v: %v vs %v", tc.frac, got2, got)
		}
		if got2 := MetalBarFill(anchor, tc.frac); got2 != got {
			t.Errorf("MetalBarFill mismatch")
		}
		if got2 := HealthBarFill(anchor, tc.frac); got2 != got {
			t.Errorf("HealthBarFill mismatch")
		}
	}
}

func TestBarFillVerticalVectors(t *testing.T) {
	anchor := Rect{X1: 10, Y1: 20, X2: 30, Y2: 120} // w=20 h=100
	tests := []struct {
		frac float32
		want Rect
	}{
		{0, Rect{X1: 10, Y1: 20, X2: 30, Y2: 20}},
		{0.5, Rect{X1: 10, Y1: 20, X2: 30, Y2: 70}},
		{1, Rect{X1: 10, Y1: 20, X2: 30, Y2: 120}},
		{0.25, Rect{X1: 10, Y1: 20, X2: 30, Y2: 45}},
	}
	for _, tc := range tests {
		got := BarFillVertical(anchor, tc.frac)
		if got != tc.want {
			t.Errorf("BarFillVertical frac %v: got %+v want %+v", tc.frac, got, tc.want)
		}
	}
}

func TestBarClamping(t *testing.T) {
	anchor := Rect{X1: 0, Y1: 0, X2: 100, Y2: 10}
	// Below 0 clamped to 0
	if got := BarFillHorizontal(anchor, -0.5); got.X2 != 0 {
		t.Errorf("clamp -0.5: got %+v", got)
	}
	if got := BarFillHorizontal(anchor, -100); got.X2 != 0 {
		t.Errorf("clamp -100: got %+v", got)
	}
	// Above 1 clamped to 1
	if got := BarFillHorizontal(anchor, 1.5); got.X2 != 100 {
		t.Errorf("clamp 1.5: got %+v", got)
	}
	if got := BarFillHorizontal(anchor, 10); got != (Rect{X1: 0, Y1: 0, X2: 100, Y2: 10}) {
		t.Errorf("clamp 10: got %+v", got)
	}
	// Vertical clamping
	if got := BarFillVertical(anchor, -1); got.Y2 != 0 {
		t.Errorf("vertical clamp -1: got %+v", got)
	}
	if got := BarFillVertical(anchor, 2); got.Y2 != 10 {
		t.Errorf("vertical clamp 2: got %+v", got)
	}
}

func TestBarInvertedAnchor(t *testing.T) {
	// Anchor stored verbatim inverted; bar geometry normalizes for presentation.
	// Storage remains verbatim but drawing uses ordered bounds.
	anchor := Rect{X1: 110, Y1: 30, X2: 10, Y2: 20} // inverted both
	if anchor.Width() != -100 {
		t.Fatalf("verbatim width expected -100")
	}
	got := BarFillHorizontal(anchor, 0.5)
	want := Rect{X1: 10, Y1: 20, X2: 60, Y2: 30}
	if got != want {
		t.Errorf("BarFillHorizontal inverted: got %+v want %+v", got, want)
	}
	gotV := BarFillVertical(anchor, 0.5)
	wantV := Rect{X1: 10, Y1: 20, X2: 110, Y2: 25}
	if gotV != wantV {
		t.Errorf("BarFillVertical inverted: got %+v want %+v", gotV, wantV)
	}
}

func TestBarZeroSize(t *testing.T) {
	anchor := Rect{X1: 50, Y1: 50, X2: 50, Y2: 60} // zero width
	got := BarFillHorizontal(anchor, 0.5)
	if got.X1 != 50 || got.X2 != 50 {
		t.Errorf("zero width: got %+v", got)
	}
	anchor2 := Rect{X1: 0, Y1: 0, X2: 100, Y2: 0} // zero height for vertical
	got2 := BarFillVertical(anchor2, 0.5)
	if got2.Y1 != 0 || got2.Y2 != 0 {
		t.Errorf("zero height vertical: got %+v", got2)
	}
}

func TestHealthFraction(t *testing.T) {
	tests := []struct {
		cur, max int32
		want     float32
	}{
		{50, 100, 0.5},
		{0, 100, 0},
		{100, 100, 1},
		{150, 100, 1}, // clamped
		{-10, 100, 0}, // clamped
		{10, 0, 0},    // zero max
		{10, -5, 0},   // negative max
	}
	for _, tc := range tests {
		got := HealthFraction(tc.cur, tc.max)
		if got != tc.want {
			t.Errorf("HealthFraction %d/%d: got %v want %v", tc.cur, tc.max, got, tc.want)
		}
	}
}

func TestResourceFraction(t *testing.T) {
	tests := []struct {
		cur, max float32
		want     float32
	}{
		{50, 100, 0.5},
		{0, 100, 0},
		{100, 100, 1},
		{150, 100, 1},
		{-10, 100, 0},
		{10, 0, 0},
	}
	for _, tc := range tests {
		got := ResourceFraction(tc.cur, tc.max)
		if got != tc.want {
			t.Errorf("ResourceFraction %v/%v: got %v want %v", tc.cur, tc.max, got, tc.want)
		}
	}
}

func TestAnchorsBarIntegration(t *testing.T) {
	side := makeFullSide("ARM")
	// Make specific anchors for bars with known geometry.
	side.Anchors[content.CanonicalKey("ENERGYBAR")] = content.Rect{X1: 0, Y1: 0, X2: 200, Y2: 10}
	side.Anchors[content.CanonicalKey("METALBAR")] = content.Rect{X1: 0, Y1: 12, X2: 200, Y2: 22}
	side.Anchors[content.CanonicalKey("DAMAGEBAR")] = content.Rect{X1: 0, Y1: 30, X2: 100, Y2: 40}
	anchors, err := AnchorsFromSide(side)
	if err != nil {
		t.Fatalf("AnchorsFromSide: %v", err)
	}
	// Energy at 25%
	eb := EnergyBarFromAnchors(anchors, 0.25)
	if eb.X2 != 50 {
		t.Errorf("EnergyBarFromAnchors 0.25: got %v", eb)
	}
	mb := MetalBarFromAnchors(anchors, 0.5)
	if mb.X2 != 100 {
		t.Errorf("MetalBarFromAnchors 0.5: got %v", mb)
	}
	hb := HealthBarFromAnchors(anchors, 1.0)
	if hb.X2 != 100 {
		t.Errorf("HealthBarFromAnchors 1.0: got %v", hb)
	}
	// Zero health
	hb0 := HealthBarFromAnchors(anchors, 0)
	if hb0.X2 != 0 {
		t.Errorf("HealthBarFromAnchors 0: got %v", hb0)
	}
}

// Unordered fractions reach the retail integer conversion, whose indefinite
// result has a zero low word [01 R-DET-01 §1].
func TestBarUnorderedFractionUsesLowWord(t *testing.T) {
	anchor := Rect{X1: 10, Y1: 20, X2: 110, Y2: 120}
	fraction := float32(math.NaN())
	if got := BarFillHorizontal(anchor, fraction); got.X2 != anchor.X1 {
		t.Fatalf("horizontal unordered fill = %+v", got)
	}
	if got := BarFillVertical(anchor, fraction); got.Y2 != anchor.Y1 {
		t.Fatalf("vertical unordered fill = %+v", got)
	}
}
