package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestReactionThrottleUsesConstructedManagerRNG verifies that the construction
// throttle's draw comes from the session stream bound at manager construction
// [08 "Strategy manager and its task graph"].
//
// Rewritten by WU-19-26: this test used to drive the throttle from the phase-2
// death sweep, because that is where this build armed it. [08 R-AI-01 §11] arms
// it from damage to a `cancapture` unit, through the damage-intake reaction
// routine of [06 §9.1] step 4, so the draw is exercised there instead. The
// deadline is tick + 30 + RNG(300).
func TestReactionThrottleUsesConstructedManagerRNG(t *testing.T) {
	cat := minimalCatalogForStrict()
	cat.Units["armcom"].CanCapture = true
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
		Combat:  &combat.Service{},
	}
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Combat.ControlByte = func(owner uint8) uint8 {
		if int(owner) >= len(s.Econ.Players) || !s.Econ.Players[owner].Exists {
			return combat.ControlByteAbsent
		}
		return s.Econ.Players[owner].ControllerState
	}
	s.bindDamageReaction()
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
	attackerH, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	probe := rng.NewSimulation(123)
	wantOffset := probe.Uint32n(300)
	before := s.SimRNG().Draws()
	// SeedSessionRNG is the battle-entry boundary and resets the authoritative
	// clock to tick zero [01 §7.1][R-CORE-02]; the reaction reads the tick it is
	// given, which is that reset clock's.
	s.Combat.ReactToDamage(w, u, w.Unit(attackerH), s.Clock.GlobalTick)
	if got := s.SimRNG().Draws(); got != before+1 {
		t.Fatalf("reaction draws=%d, want one construction-throttle draw", got-before)
	}
	wantDeadline := s.Clock.GlobalTick + 30 + wantOffset
	if got := mgr.UnitLossDeadline(); got != wantDeadline {
		t.Fatalf("unit-loss deadline=%d, want %d", got, wantDeadline)
	}
	// The same hit stops the damaged unit where it stands, through the ordinary
	// stop path [08 R-AI-01 §11].
	q := orders.QueueForUnit(u)
	if q == nil || len(q.Primary()) == 0 {
		t.Fatal("the throttle did not issue the stop the reaction site names [08 R-AI-01 §11]")
	}
	if got := orders.DescriptorFor(q.Primary()[0].ID).Name; got != "Stop" {
		t.Fatalf("front primary order after the throttle = %q, want \"Stop\"", got)
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
