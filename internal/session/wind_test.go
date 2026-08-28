package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestWindBattleEntryConsumesNoDraws locks the corrected battle-entry contract
// [R-CORE-02]: battle entry performs NO wind draws — the briefing-screen
// speed/direction values are front-end display state with no battle-side
// reader — and only zeroes the deadline. The earlier assertion that battle
// entry consumes exactly three CRT draws is superseded; the two briefing
// display draws and the interval draw belong to the front-end briefing screen
// (world.Wind.SeedBriefing), which nanolathe does not build yet.
func TestWindBattleEntryConsumesNoDraws(t *testing.T) {
	crt := rng.NewCRT(0x1234)
	sim := rng.NewSimulation(0x1234)
	simBefore := sim.Draws()

	bounds := mission.WindBounds{Min: 100, Max: 2000}
	w := InitBattleWind(bounds)
	if crt.Draws() != 0 {
		t.Fatalf("battle entry consumed %d CRT draws, want 0 [R-CORE-02]", crt.Draws())
	}
	if sim.Draws() != simBefore {
		t.Fatalf("battle entry consumed sim draws %d->%d [R-CORE-02]", simBefore, sim.Draws())
	}
	if w.NextChange != 0 {
		t.Fatalf("battle entry must zero the wind deadline, got %d [R-CORE-02]", w.NextChange)
	}

	// Collapsed bounds behave identically: no draws either way.
	w2 := InitBattleWind(mission.WindBounds{Min: 500, Max: 500})
	if w2.NextChange != 0 {
		t.Fatalf("collapsed bounds deadline = %d want 0 [R-CORE-02]", w2.NextChange)
	}
}

// TestWindTick1Chain locks the first sub-tick wind chain [R-CORE-02 census]:
// with the deadline zeroed at entry and the strict gate (not due while
// tick < deadline), phase 8 at tick 1 consumes exactly 1 CRT interval draw,
// then 1 sim strength draw, then 1 sim heading draw only when the strength is
// nonzero. A not-due tick consumes nothing.
func TestWindTick1Chain(t *testing.T) {
	// Nonzero-strength bounds: full chain = 1 CRT + 2 sim.
	w := world.NewWind(100, 2000)
	crt := rng.NewCRT(7)
	sim := rng.NewSimulation(7)
	if !w.Jitter(1, &crt, &sim) {
		t.Fatal("tick 1 must be due (deadline zeroed at entry, tick 1 > 0) [R-CORE-02]")
	}
	if crt.Draws() != 1 {
		t.Fatalf("tick-1 chain CRT draws = %d want 1 [R-CORE-02]", crt.Draws())
	}
	if got := sim.Draws(); got != 2 {
		t.Fatalf("tick-1 chain sim draws = %d want 2 (strength + heading) [R-CORE-02]", got)
	}
	if w.Strength < 100 || w.Strength > 2000 {
		t.Fatalf("strength %d out of bounds", w.Strength)
	}

	// Zero-only bounds: the strength draw's span is 0, so Uint32n returns 0
	// WITHOUT advancing [01 §7.1], and the zero strength skips the heading.
	w0 := world.NewWind(0, 0)
	crt0 := rng.NewCRT(7)
	sim0 := rng.NewSimulation(7)
	if !w0.Jitter(1, &crt0, &sim0) {
		t.Fatal("tick 1 must be due for collapsed bounds too")
	}
	if crt0.Draws() != 1 {
		t.Fatalf("collapsed-bounds CRT draws = %d want 1", crt0.Draws())
	}
	if got := sim0.Draws(); got != 0 {
		t.Fatalf("collapsed-bounds sim draws = %d want 0 (bound<2 returns 0 without advancing [01 §7.1]; heading drawn only when strength != 0 [01 §7.3])", got)
	}
	if w0.Strength != 0 || w0.Heading != 0 {
		t.Fatalf("collapsed bounds produced strength %d heading %d, want 0/0", w0.Strength, w0.Heading)
	}

	// A later not-due tick consumes nothing and clears the change flag.
	crtDraws, simDraws := crt.Draws(), sim.Draws()
	w.Changed = true
	if w.Jitter(2, &crt, &sim) {
		t.Fatal("tick 2 must not be due (interval > 0)")
	}
	if crt.Draws() != crtDraws || sim.Draws() != simDraws {
		t.Fatal("not-due tick consumed draws")
	}
	if w.Changed {
		t.Fatal("not-due tick must clear the one-tick change flag")
	}
}
