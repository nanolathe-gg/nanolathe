package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestBriefingConsumesThreeCRTDrawsRegardless is the R2 regression. Retail's
// briefing draws are raw inline rand()%n expressions [01 §7.3], so a map whose
// wind bounds are equal must still consume the speed draw. Skipping it shifts
// every later CRT consumer — interval jitter, meteor geometry [06 §6.5], screen
// shake [03 §5.6], audio variants [03 §8.3] — by one.
func TestBriefingConsumesThreeCRTDrawsRegardless(t *testing.T) {
	for _, bounds := range [][2]int32{{100, 2000}, {500, 500}, {0, 0}} {
		crt := rng.NewCRT(1)
		w := NewWind(bounds[0], bounds[1])
		w.SeedBriefing(&crt, 0)
		if crt.Draws() != 3 {
			t.Fatalf("bounds %v: briefing consumed %d CRT draws, want 3", bounds, crt.Draws())
		}
		if w.Strength < bounds[0] || w.Strength > bounds[1] {
			t.Fatalf("bounds %v: strength %d out of range", bounds, w.Strength)
		}
		if w.NextChange < 150 || w.NextChange > 420 {
			t.Fatalf("bounds %v: first deadline %d outside 150..420 [01 §7.3]", bounds, w.NextChange)
		}
	}
}

// TestWindChangeDrawCounts locks the per-change stream budget of [01 §7.3]:
// one CRT draw for the interval in phase 8, then one simulation draw for the
// strength and one for the heading in phase 9. The heading is simRand(0x10000);
// if the simulation helper ever regains a chunk-concatenation path it becomes
// two draws and this fails (R1).
func TestWindChangeDrawCounts(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(100, 2000)
	w.SeedBriefing(&crt, 0)

	crtAfterBriefing := crt.Draws()
	due := w.NextChange

	// A tick before the deadline draws nothing at all.
	w.Jitter(due-1, &crt)
	if w.Field(due-1, &sim) {
		t.Fatal("wind changed before its deadline")
	}
	if crt.Draws() != crtAfterBriefing || sim.Draws() != 0 {
		t.Fatalf("undue tick drew: crt %d sim %d", crt.Draws()-crtAfterBriefing, sim.Draws())
	}

	// The due tick draws exactly one CRT and two simulation values.
	w.Jitter(due, &crt)
	if !w.Field(due, &sim) {
		t.Fatal("wind did not change on its deadline")
	}
	if got := crt.Draws() - crtAfterBriefing; got != 1 {
		t.Fatalf("change consumed %d CRT draws, want 1 (interval)", got)
	}
	if sim.Draws() != 2 {
		t.Fatalf("change consumed %d simulation draws, want 2 (strength, heading)", sim.Draws())
	}
	if !w.Changed || w.LastChange != due {
		t.Fatalf("change not published at tick %d", due)
	}
}

// TestZeroStrengthSkipsHeadingDraw: the heading draw is taken only when the new
// strength is nonzero [01 §7.3].
func TestZeroStrengthSkipsHeadingDraw(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(0, 0) // span 0: strength draw returns 0 without advancing
	w.SeedBriefing(&crt, 0)
	due := w.NextChange
	w.Jitter(due, &crt)
	w.Field(due, &sim)
	if sim.Draws() != 0 {
		t.Fatalf("zero-span change consumed %d simulation draws, want 0", sim.Draws())
	}
}

// TestWindScalarClampsAtOne locks the published ratio: speed/5000 as float32,
// clamped from above at exactly 1.0 [01 §7.3], [05 "Wind generation"].
func TestWindScalarClampsAtOne(t *testing.T) {
	if got := windScalar(2500); got != 0.5 {
		t.Fatalf("windScalar(2500) = %v, want 0.5", got)
	}
	if got := windScalar(5000); got != 1.0 {
		t.Fatalf("windScalar(5000) = %v, want 1", got)
	}
	if got := windScalar(90000); got != 1.0 {
		t.Fatalf("windScalar(90000) = %v, want 1 (clamped)", got)
	}
}

// TestRegisteredPhaseOrder locks the stream ordering across the kernel: the
// phase-8 CRT draw happens strictly before the phase-9 simulation draws
// [01 §4.4], [01 §7.3].
func TestRegisteredPhaseOrder(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(100, 2000)
	w.SeedBriefing(&crt, 0)

	var k kernel.Kernel
	w.Register(&k, &crt, &sim)

	var state clock.State
	changes := 0
	for state.GlobalTick < 900 {
		before := w.LastChange
		k.SubTick(&state)
		if w.LastChange != before {
			changes++
		}
	}
	if changes < 2 {
		t.Fatalf("only %d wind changes in 900 ticks, want at least 2", changes)
	}
	// Every change is one CRT interval draw and at most two simulation draws.
	if want := uint64(3 + changes); crt.Draws() != want {
		t.Fatalf("crt draws = %d, want %d (3 briefing + 1 per change)", crt.Draws(), want)
	}
	if sim.Draws() != uint64(2*changes) {
		t.Fatalf("sim draws = %d, want %d (2 per change)", sim.Draws(), 2*changes)
	}
}
