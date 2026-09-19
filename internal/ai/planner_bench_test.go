package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// BenchmarkManagerDispatch measures the per-player think step the session
// dispatches once per tick, on the smallest fixture in which both gates open
// and every task slot is due. It exists so the seam's cost is a number rather
// than a cost model (docs/DESIGN_GAMEPLAY_RULES.md "Granularity"): it uses
// only the dispatch entry, so the same benchmark measures a build with the
// seam and one without it.
func BenchmarkManagerDispatch(b *testing.B) {
	const tick = uint32(100)
	r := rng.NewSimulation(7)
	m := &Manager{Player: 1, RNG: &r}
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = tick
	}
	econ := runtimeEconomy(1, 2)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Tick(tick, nil, econ)
	}
}
