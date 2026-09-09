package render

import "testing"

// TestShadeRowFormulaEdgeCases locks the shaded piece renderer's real SHD
// row formula — row = trunc(dot*5.0) & 0x1F with DONT_SHADE pinning row 15
// [03 R-RAST-01 §5] — at its row-0 and row-31 boundaries, its negative-dot
// wraparound, and the DONT_SHADE pin. This replaces the deleted
// presentation-fallback row 16 (SHDMidRow/ModelShadeMidRow/SelectShadeRow):
// no production draw path could reach that constant, and the render package
// now exposes no shade-row default other than ShadeRowForNormal's own
// arithmetic and the SHDIdentityRow pin it shares with DONT_SHADE.
func TestShadeRowFormulaEdgeCases(t *testing.T) {
	light := [3]float64{1, 0, 0} // isolate the formula from DefaultModelLight's specific direction

	// dot*5 in [0,1) truncates to row 0.
	if got := ShadeRowForNormal([3]float64{0.1, 0, 0}, light, false); got != 0 {
		t.Fatalf("dot=0.1 row %d want 0 [03 R-RAST-01 §5]", got)
	}

	// dot*5 in [31,32) truncates to row 31, the top row, with no masking needed.
	if got := ShadeRowForNormal([3]float64{6.3, 0, 0}, light, false); got != 31 {
		t.Fatalf("dot=6.3 row %d want 31 [03 R-RAST-01 §5]", got)
	}

	// dot*5 == 32 truncates to 32, which wraps to row 0 under &0x1F.
	if got := ShadeRowForNormal([3]float64{6.4, 0, 0}, light, false); got != 0 {
		t.Fatalf("dot=6.4 row %d want 0 (32&31 wrap) [03 R-RAST-01 §5]", got)
	}

	// A negative dot truncates toward zero then wraps under &0x1F: trunc(-1)=-1,
	// -1&31=31 in Go's two's-complement bitwise AND, matching retail's masked row.
	if got := ShadeRowForNormal([3]float64{-0.2, 0, 0}, light, false); got != 31 {
		t.Fatalf("dot=-0.2 row %d want 31 (-1&31 wrap) [03 R-RAST-01 §5]", got)
	}

	// DONT_SHADE pins row 15 regardless of the normal/light dot [03 R-RAST-01 §5].
	if got := ShadeRowForNormal([3]float64{6.3, 0, 0}, light, true); got != 15 {
		t.Fatalf("DONT_SHADE row %d want 15 [03 R-RAST-01 §5]", got)
	}
	if got := ShadeRowForNormal([3]float64{0, 0, 0}, light, true); got != 15 {
		t.Fatalf("DONT_SHADE with zero dot row %d want 15 [03 R-RAST-01 §5]", got)
	}

	// SHDIdentityRow is the same 15 both DONT_SHADE and the piece-draw default
	// share; there is no separate mid-row constant [03 R-RAST-01 §5].
	if SHDIdentityRow != 15 {
		t.Fatalf("SHDIdentityRow %d want 15 [03 §4.3][03 R-RAST-01 §5]", SHDIdentityRow)
	}
}

// Large integers distinguish the final binary32 store from rounding the
// command argument before multiplication [03 §2.4.1].
func TestModelLightStoresScaledIntegersOnce(t *testing.T) {
	before := DefaultModelLight
	t.Cleanup(func() { DefaultModelLight = before })
	SetModelLight(16777217, -16777219, 0)
	if want := ([3]float64{167772.171875, -167772.1875, 0}); DefaultModelLight != want {
		t.Fatalf("light = %v, want %v", DefaultModelLight, want)
	}
}
