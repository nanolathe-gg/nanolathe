package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Established: radar compares the full hull top with sea level, inclusively,
// after a signed 32-bit sum [03 R-VIS-01 §5]. Hidden targets skip the final LOS
// probe and the emitter has no sonar, so neither can mask a radar rejection.
func TestRadarFullHeightBothCandidateWalks(t *testing.T) {
	const sea = 100 << 16
	const fractionalTop = 10<<16 | 1<<15
	cases := []struct {
		name   string
		y, top int32
		seen   bool
	}{
		{"fractional crossing", sea - (10<<16 | 1<<14), fractionalTop, true},
		{"equality", sea - fractionalTop, fractionalTop, true},
		{"one raw unit below", sea - fractionalTop - 1, fractionalTop, false},
		{"above byte range", sea - (266<<16 | 1<<14), 266<<16 | 1<<15, true},
		{"positive sum wraps negative", 32767 << 16, 1 << 16, false},
	}
	for _, exhaustive := range []bool{false, true} {
		name := "indexed"
		if exhaustive {
			name = "exhaustive"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s := newTestService(&world.Terrain{CellW: 64, CellH: 64, SeaLevel: 100}, ModeHistoryEnabled|ModeCurrentEnabled)
					s.SetLocal(0)
					s.sensorIndex.forceExhaustive = exhaustive
					var sourceStatus, targetStatus uint32
					units := []SensorUnit{
						{ID: 1, Owner: 0, Status: &sourceStatus, Alive: true, Active: true, RadarDistance: 100},
						{ID: 2, Owner: 1, Status: &targetStatus, Alive: true, Hidden: true, X: 32 << 16, Y: numeric.Fixed(tc.y), ModelTopFixed: tc.top},
					}
					// Sparse distant entries force a proper indexed subset instead of
					// the compact-formation fallback, without adding sensor emitters.
					for i := 0; i < 16; i++ {
						units = append(units, SensorUnit{X: numeric.Fixed(512+i*128) << 16})
					}
					s.SensorTick(1, 2, units)
					candidates := s.sensorCandidates(&units[0], 100)
					if exhaustive {
						if candidates != nil {
							t.Fatal("fixture did not use exhaustive walk")
						}
					} else if len(candidates) != 2 {
						t.Fatalf("indexed fixture candidates = %v, want source and target only", candidates)
					}
					want := uint32(0)
					if tc.seen {
						want = SeenBit
					}
					if targetStatus != want {
						t.Fatalf("target status = %#x, want radar-only %#x", targetStatus, want)
					}
				})
			}
		})
	}
}
