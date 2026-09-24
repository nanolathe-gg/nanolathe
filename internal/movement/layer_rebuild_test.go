package movement

import "testing"

// A full rebuild's cached cells must preserve the footprint/ring minimum and
// expire before another occupancy commit [04 R-SLOPE-01 §3][04 R-PATH-01 §14].
func TestFullRebuildMatchesLiveClassifierAndExpiresCellView(t *testing.T) {
	profiles := []Profile{
		Template(), kbotsSS2, tankDS2, spid3, boats4,
		{FootPrintX: 3, FootPrintZ: 1, MaxWaterDepth: 100, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16},
		{FootPrintX: 12, FootPrintZ: 1},
	}
	for _, profile := range profiles {
		terrain := layerTerrain(11, 9, 20)
		setDerived(terrain, 3, 4, 30, 47)
		setDerived(terrain, 6, 2, 0, 0)
		grid := NewOccupancyGrid()
		layer := NewClassLayer(profile, terrain, grid)
		layer.watermark = 4
		grid.Stamp(Cell{X: 5, Z: 5}, 1, 1, 1)
		for pass := 0; pass < 2; pass++ {
			layer.stampAll()
			for z := int32(0); z < layer.H; z++ {
				for x := int32(0); x < layer.W; x++ {
					if got, want := layer.Value(x, z), layer.classify(x, z); got != want {
						t.Fatalf("profile %+v pass %d cell %d,%d: got %d want %d", profile, pass, x, z, got, want)
					}
				}
			}
			grid.Clear(Cell{X: 5, Z: 5}, 1, 1, 1)
			setDerived(terrain, 3, 4, 30, 30)
		}
	}
}

// The row-major column pass must match the live classifier on irregular
// ground, for footprints wider, taller and larger than the map's structure.
func TestFullRebuildMatchesLiveClassifierOnRandomGround(t *testing.T) {
	seed := uint32(12345)
	next := func() uint32 { seed = seed*1664525 + 1013904223; return seed >> 8 }
	profiles := []Profile{Template(), kbotsSS2, tankDS2, spid3, boats4, {FootPrintX: 1, FootPrintZ: 5, MaxWaterDepth: 100, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16}}
	for trial := 0; trial < 4; trial++ {
		terrain := layerTerrain(37, 29, 20)
		for i := 0; i < 120; i++ {
			x, z := int32(next()%37), int32(next()%29)
			lo := uint8(next() % 60)
			setDerived(terrain, x, z, lo, lo+uint8(next()%40))
		}
		for _, profile := range profiles {
			layer := NewClassLayer(profile, terrain, NewOccupancyGrid())
			for z := int32(0); z < layer.H; z++ {
				for x := int32(0); x < layer.W; x++ {
					if got, want := layer.Value(x, z), layer.classify(x, z); got != want {
						t.Fatalf("trial %d profile %+v cell %d,%d: got %d want %d", trial, profile, x, z, got, want)
					}
				}
			}
		}
	}
}
