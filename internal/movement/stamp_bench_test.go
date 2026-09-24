package movement

import "testing"

// BenchmarkStampAll540 times one full class-layer stamp on a 540x540 map, the
// size of the simulation benchmark's Town & Country.
func BenchmarkStampAll540(b *testing.B) {
	terrain := flatTerrain(&testing.T{}, 540, 540, 100)
	for i := range terrain.Plot {
		if i%37 == 0 {
			terrain.Plot[i].SetHeight(160)
			terrain.Plot[i].SetMaxHeight(160)
		}
	}
	l := NewClassLayer(Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 255}, terrain, NewOccupancyGrid())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.stampAll()
	}
}
