package features

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// restingForestService builds a side×side map carrying a resting sprite on
// every cell and `active` burning records whose countdowns never reach zero,
// so a lifecycle tick's only work is the active walk.
func restingForestService(t testing.TB, side, active int) *Service {
	t.Helper()
	terrain := newTestTerrainP1(side, side)
	tree := defP1("tree", 1, 1, "", "trees")
	tree.Flamable = true
	tree.SeqNameBurn = "burn"
	terrain.FeatureDefs = []*content.FeatureDef{tree}
	sim := rng.SimulationFromState(1)
	crt := rng.CRTFromState(1)
	svc := NewService(terrain, &sim, &crt, nil)
	stubSequences(svc, longBurn(), nil, nil)
	for cz := 0; cz < side; cz++ {
		for cx := 0; cx < side; cx++ {
			if svc.spawnFeatureAt(cx, cz, tree) == nil {
				t.Fatalf("spawn at (%d,%d) rejected", cx, cz)
			}
		}
	}
	for i := 0; i < active; i++ {
		// A burn whose one frame outlasts any run, and a countdown that never
		// reaches its event.
		startBurning(svc, svc.InstanceAt(i%side, i/side), []int32{1 << 30}, 1<<30)
	}
	return svc
}

// TestLifecycleWorkIsProportionalToActiveRecords is the proportionality check
// of [05 R-FEAT-01 §10] pass 3: with the number of active records held
// constant, growing the resting forest sixty-four-fold must not grow the
// lifecycle tick's allocations at all, and its time by no more than a small
// factor. It reports the numbers to the file named by
// NANOLATHE_FEATLIFE_REPORT when set. The publication walk and the grid
// resync still scale with the map; they are not this pass and are measured
// separately by the benchmark below.
func TestLifecycleWorkIsProportionalToActiveRecords(t *testing.T) {
	const active = 8
	type sample struct {
		side           int
		allocs         float64
		nsPerTick      float64
		activeAtSample int
	}
	var samples []sample
	for _, side := range []int{8, 64} {
		svc := restingForestService(t, side, active)
		tick := uint32(1)
		allocs := testing.AllocsPerRun(50, func() {
			svc.TickLifecycle(tick)
			tick++
		})
		const runs = 2000
		start := time.Now()
		for i := 0; i < runs; i++ {
			svc.TickLifecycle(tick)
			tick++
		}
		elapsed := time.Since(start)
		samples = append(samples, sample{side: side, allocs: allocs, nsPerTick: float64(elapsed.Nanoseconds()) / runs, activeAtSample: len(activeOrder(svc))})
	}
	report := ""
	for _, s := range samples {
		report += fmt.Sprintf("resting=%d active=%d allocs/tick=%.1f ns/tick=%.0f\n", s.side*s.side, s.activeAtSample, s.allocs, s.nsPerTick)
	}
	t.Log(report)
	if path := os.Getenv("NANOLATHE_FEATLIFE_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}
	small, large := samples[0], samples[1]
	if small.activeAtSample != active || large.activeAtSample != active {
		t.Fatalf("active records drifted: %d and %d, want %d", small.activeAtSample, large.activeAtSample, active)
	}
	if large.allocs > small.allocs {
		t.Fatalf("a 64x larger resting forest raised lifecycle allocations from %.1f to %.1f per tick", small.allocs, large.allocs)
	}
	// Time is noisy on a shared machine; the bound is loose but a cell-sorted
	// walk over 4096 records against 64 would exceed it by an order of
	// magnitude.
	if large.nsPerTick > 4*small.nsPerTick+20000 {
		t.Fatalf("a 64x larger resting forest raised lifecycle time from %.0f to %.0f ns/tick", small.nsPerTick, large.nsPerTick)
	}
}

// BenchmarkTickLifecycleRestingForest measures the lifecycle tick alone with
// eight active records over a 4096-sprite forest.
func BenchmarkTickLifecycleRestingForest(b *testing.B) {
	svc := restingForestService(b, 64, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.TickLifecycle(uint32(i + 1))
	}
}

// BenchmarkInstancesWalkRestingForest measures the publication walk over the
// same forest, which still scales with the map: it is the remaining
// map-sized cost and is attributed here, not to the lifecycle.
func BenchmarkInstancesWalkRestingForest(b *testing.B) {
	svc := restingForestService(b, 64, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.Instances()
	}
}
