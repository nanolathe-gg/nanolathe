package world

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// pickFixture builds a flat terrain of the given height, 16x16 cells.
func pickFixture(height uint8) *Terrain {
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: PlotFeatureNone}
	}
	return &Terrain{CellW: 16, CellH: 16, Plot: ExpandPlot(attrs, 16, 16), SeaLevel: 0}
}

// TestCursorToWorldCompensatesHeight locks the reason the search exists: the
// picked point, projected back through the half-height shear world objects are
// drawn with, must land on the row that was clicked [07 §8][03 §2.5]. On flat
// ground of height h that means the result sits h/2 pixels south of the click.
func TestCursorToWorldCompensatesHeight(t *testing.T) {
	for _, h := range []uint8{0, 10, 40, 100} {
		ter := pickFixture(h)
		const clickX, clickZ = 100, 100
		x, y, z := ter.CursorToWorld(clickX, clickZ)
		if got := int32(x >> 16); got != clickX {
			t.Fatalf("h=%d: X moved to %d, want %d", h, got, clickX)
		}
		if got := int32(y >> 16); got != int32(h) {
			t.Fatalf("h=%d: Y = %d, want the ground height", h, got)
		}
		// Project the answer the way a unit standing there is drawn.
		row := int32(z>>16) - int32(h)/2
		if row != clickZ {
			t.Fatalf("h=%d: picked z=%d projects to row %d, want %d", h, z>>16, row, clickZ)
		}
	}
}

// TestCursorToWorldClampsToMap locks the first thing retail does: both axes are
// clamped into 0 .. pixels-1 before anything else [07 §8].
func TestCursorToWorldClampsToMap(t *testing.T) {
	ter := pickFixture(0)
	pixW := ter.CellW * 16
	x, _, _ := ter.CursorToWorld(-50, 10)
	if x != 0 {
		t.Fatalf("negative X clamped to %d, want 0", x>>16)
	}
	x, _, _ = ter.CursorToWorld(pixW+50, 10)
	if got := int32(x >> 16); got != pixW-1 {
		t.Fatalf("overshooting X clamped to %d, want %d", got, pixW-1)
	}
}

// TestCursorToWorldFloorsAtSeaLevel locks that the water surface picks like
// ground: the height query is max(terrain, sea level) [07 §8].
func TestCursorToWorldFloorsAtSeaLevel(t *testing.T) {
	ter := pickFixture(0)
	ter.SeaLevel = 60
	_, y, z := ter.CursorToWorld(100, 100)
	if got := int32(y >> 16); got != 60 {
		t.Fatalf("Y = %d under water, want the sea level 60", got)
	}
	if row := int32(z>>16) - 30; row != 100 {
		t.Fatalf("picked z=%d projects to row %d, want 100", z>>16, row)
	}
}

// TestCursorToWorldRetainsNorthCandidatePastFarBracket crafts the int16
// projection wrap that reaches the second retail guard. [07 §8] narrows both
// candidate rows to int16 before comparing them; at map row 32816, the north
// candidate projects as -32720 while its unrefined south bracket is -32704.
// The clicked row (32700) is therefore south of that far bracket, so the
// unrefined north candidate must be retained rather than interpolated.
func TestCursorToWorldRetainsNorthCandidatePastFarBracket(t *testing.T) {
	const cellW, cellH = 4, 4096
	attrs := make([]formats.TNTAttribute, cellW*cellH)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Feature: PlotFeatureNone}
	}
	ter := &Terrain{CellW: cellW, CellH: cellH, Plot: ExpandPlot(attrs, cellW, cellH), SeaLevel: 0}
	const clickedX, clickedRow = 16, 32700
	x, y, z := ter.CursorToWorld(clickedX, clickedRow)
	const expectedNorth = 32816 // (32700 &^ 15) + 128: initial probe, before walk north
	if x != numeric.Fixed(clickedX<<16) || y != 0 || z != numeric.Fixed(expectedNorth<<16) {
		t.Fatalf("far-bracket guard result=(%d,%d,%d), want (%d,0,%d)", x>>16, y>>16, z>>16, clickedX, expectedNorth)
	}
}
