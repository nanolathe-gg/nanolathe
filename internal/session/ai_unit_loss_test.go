package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestPhase2ComputerDeathUsesConstructedManagerRNG verifies that a computer
// unit finalized by the phase-2 sweep can arm the manager's unit-loss retry
// throttle before phase-5 dispatch. The draw is from the session stream that
// was bound at manager construction [08 "Strategy manager and its task graph"].
func TestPhase2ComputerDeathUsesConstructedManagerRNG(t *testing.T) {
	cat := minimalCatalogForStrict()
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Catalog: cat,
		World:   minimalTerrain(),
		Units:   w,
		Econ:    &economy.Service{},
		Clock:   &clock.State{GlobalTick: 1},
	}
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.SeedSessionRNG(123, 456)
	mgr := &ai.Manager{Player: 1, RNG: s.SimRNG()}
	s.AI[1] = mgr
	s.RegisterAll()

	def := cat.Units["armcom"]
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatal("created computer unit is missing")
	}
	// The phase-2 sweep receives this already-latched death from the preceding
	// unit/combat work. Setting the latch directly avoids firing the hook before
	// the phase under test.
	u.Dying = true
	u.DeathCause = 1

	probe := rng.NewSimulation(123)
	wantOffset := probe.Uint32n(300)
	before := s.SimRNG().Draws()
	s.phaseUnits(1)
	if got := s.SimRNG().Draws(); got != before+1 {
		t.Fatalf("phase-2 death draws=%d, want one unit-loss draw", got-before)
	}
	// SeedSessionRNG is the battle-entry boundary and resets the authoritative
	// clock to tick zero [01 §7.1][R-CORE-02]. The phase receives tick one,
	// but the death hook reads the reset session clock, so the throttle's
	// deadline is based on zero rather than the caller's phase argument. The
	// previous expectation incorrectly used one here.
	wantDeadline := uint32(30 + wantOffset)
	if got := mgr.UnitLossDeadline(); got != wantDeadline {
		t.Fatalf("unit-loss deadline=%d, want %d", got, wantDeadline)
	}
}

// TestPhase2DeathRemovesTheDyingUnitFromItsAIGroupRecord locks WU-19-21: the
// FinalizeDeath boundary (Units.OnDeath in session.go) calls the owning
// manager's direct-writer remove-sentinel immediately, so a wave member that
// dies mid-battle is gone from its stored group record before the next
// 30-entry classification sweep, not just eventually via the reconciliation
// sweep [08 R-P0-04 §3 "The direct manager-group writer"].
func TestPhase2DeathRemovesTheDyingUnitFromItsAIGroupRecord(t *testing.T) {
	cat := minimalCatalogForStrict()
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Catalog: cat,
		World:   minimalTerrain(),
		Units:   w,
		Econ:    &economy.Service{},
		Clock:   &clock.State{GlobalTick: 1},
	}
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.SeedSessionRNG(123, 456)
	mgr := &ai.Manager{Player: 1, RNG: s.SimRNG()}
	s.AI[1] = mgr
	s.RegisterAll()

	def := cat.Units["armcom"]
	dyingH, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	siblingH, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	dying := w.Unit(dyingH)
	sibling := w.Unit(siblingH)
	if dying == nil || sibling == nil {
		t.Fatal("created computer units are missing")
	}
	// Simulate both units already assigned to wave A by a prior classification
	// sweep [08 R-P0-04 §2].
	dying.Group = 2
	sibling.Group = 2
	mgr.GroupWaveA = []pool.Handle{dyingH, siblingH}

	dying.Dying = true
	dying.DeathCause = 1
	s.phaseUnits(1)

	if got, want := mgr.GroupWaveA, []pool.Handle{siblingH}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("wave A after phase-2 death = %v, want only the surviving member %v", got, want)
	}
}
