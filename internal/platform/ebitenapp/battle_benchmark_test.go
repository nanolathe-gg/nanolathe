package ebitenapp

import (
	"testing"
	"time"
)

func TestBenchmarkPacerWaitsAndRebasesWithoutCatchup(t *testing.T) {
	period := time.Second / 60
	start := time.Unix(1, 0)
	p := benchmarkPacer{period: period}
	if delay := p.delay(start); delay != 0 {
		t.Fatalf("first draw waited %v", delay)
	}
	if delay := p.delay(start.Add(period / 2)); delay != period-period/2 {
		t.Fatalf("early draw wait=%v", delay)
	}
	// A long frame misses several deadlines. It draws immediately, then the
	// next frame waits for a new full interval rather than racing to catch up.
	late := start.Add(10 * period)
	if delay := p.delay(late); delay != 0 {
		t.Fatalf("late draw waited %v", delay)
	}
	if delay := p.delay(late.Add(period / 4)); delay != period-period/4 {
		t.Fatalf("catch-up burst after overrun: wait=%v", delay)
	}
}

func TestBenchmarkDrawRatiosPreserveThirtySimulationTicks(t *testing.T) {
	for _, fps := range []int{30, 60, 120} {
		g := battleBenchmark{options: BenchmarkOptions{TPS: fps}}
		ticks := 0
		for g.frame = 0; g.frame < fps; g.frame++ {
			phase, draws := g.tickPhase()
			if phase < 0 || phase >= draws || draws != fps/30 {
				t.Fatalf("fps=%d phase=%d draws=%d", fps, phase, draws)
			}
			if phase == 0 {
				ticks++
			}
		}
		if ticks != 30 {
			t.Fatalf("fps=%d produced %d ticks", fps, ticks)
		}
		g.frame = g.warmupDraws()
		phase, _ := g.tickPhase()
		if phase != 0 || g.warmupDraws()/(fps/30) != 60 {
			t.Fatalf("fps=%d warmup does not end at sixty complete ticks", fps)
		}
	}
}
