package visibility

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// BenchmarkSensorTickCandidateIndex includes rebuilding the position index on
// every due pass and reports warm scratch allocation. The compact population
// exercises dense candidates; the wide population exercises spatial culling.
func BenchmarkSensorTickCandidateIndex(b *testing.B) {
	benchmarkSensorTick(b, false)
}

// BenchmarkSensorTickExhaustive uses the identical fixture with the index
// forced to its production fallback. Both benchmarks include rebuild work and
// report warm scratch allocation, so their timing compares only enumeration.
func BenchmarkSensorTickExhaustive(b *testing.B) {
	benchmarkSensorTick(b, true)
}

func benchmarkSensorTick(b *testing.B, forceExhaustive bool) {
	for _, n := range []int{500, 1000} {
		for _, layout := range []struct {
			name    string
			spacing int
		}{{"dense", 16}, {"sparse", 256}} {
			b.Run(fmt.Sprintf("%s-%d", layout.name, n), func(b *testing.B) {
				s := newTestService(&world.Terrain{CellW: 512, CellH: 512}, ModeHistoryEnabled|ModeCurrentEnabled)
				s.sensorIndex.forceExhaustive = forceExhaustive
				status := make([]uint32, n)
				deadline := make([]uint32, n)
				units := make([]SensorUnit, n)
				for i := range units {
					units[i] = SensorUnit{ID: uint16(i + 1), Owner: PlayerID(i % 2), Status: &status[i], DecloakDeadline: &deadline[i], X: numeric.Fixed(int64((i%32)*layout.spacing)<<16) + 12345, Z: numeric.Fixed(int64((i/32)*layout.spacing)<<16) + 32767, Alive: true, Active: true, ModelTop: 16, OwnerLocallySimulated: true, PrimaryCandidateOf: uint16(1) << uint(1-i%2)}
					if i%10 == 0 {
						units[i].RadarDistance = 600
						units[i].SonarDistance = 450
					}
					if i%25 == 1 {
						units[i].RadarJam = 300
						units[i].SonarJam = 200
					}
					if i%7 == 0 {
						units[i].CanCloak = true
						units[i].MinCloakDistance = 100
					}
				}
				s.SensorTick(0, 2, units)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					s.SensorTick(uint32(i), 2, units)
				}
			})
		}
	}
}
