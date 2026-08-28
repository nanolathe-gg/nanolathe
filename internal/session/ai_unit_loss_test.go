package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
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
	wantDeadline := uint32(1 + 30 + wantOffset)
	if got := mgr.UnitLossDeadline(); got != wantDeadline {
		t.Fatalf("unit-loss deadline=%d, want %d", got, wantDeadline)
	}
}
