package world

import "testing"

func TestLOSHeightWordReadDoesNotBuild(t *testing.T) {
	terrain := &Terrain{CellW: 4, CellH: 4, Plot: ExpandPlot(flat(4, 4, 12), 4, 4)}
	before := append([]PlotCell(nil), terrain.Plot...)
	if low, high := terrain.LOSHeightWord(0, 0); low != 0 || high != 0 {
		t.Fatalf("unbootstrapped LOS word = (%d,%d), want safe zero", low, high)
	}
	if terrain.LOSHeightBuildCount() != 0 {
		t.Fatalf("LOS read built table %d times", terrain.LOSHeightBuildCount())
	}
	if string(flattenPlot(terrain.Plot)) != string(flattenPlot(before)) {
		t.Fatal("LOS read mutated plot data")
	}

	terrain.BuildLOSHeightWordsForTest()
	if terrain.LOSHeightBuildCount() != 1 {
		t.Fatalf("explicit bootstrap count = %d, want 1", terrain.LOSHeightBuildCount())
	}
	low, high := terrain.LOSHeightWord(0, 0)
	wordsBefore := [2]uint8{low, high}
	// Height changes are authoritative plot deformation, but the LOS table is
	// a load-time product and remains unchanged.
	terrain.PlotAt(0, 0).SetHeight(200)
	terrain.BuildLOSHeightWordsForTest()
	low, high = terrain.LOSHeightWord(0, 0)
	if [2]uint8{low, high} != wordsBefore {
		t.Fatalf("LOS word changed after deformation: before %v after (%d,%d)", wordsBefore, low, high)
	}
}

func flattenPlot(plot []PlotCell) []byte {
	out := make([]byte, 0, len(plot)*len(plot[0]))
	for _, cell := range plot {
		out = append(out, cell[:]...)
	}
	return out
}
