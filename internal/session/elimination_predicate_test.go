package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// eliminationFixtureWorld builds a world with one definition every owner can
// allocate, so a test can drive the two counters by hand.
func eliminationFixtureWorld(t *testing.T) (*units.World, *content.UnitDef) {
	t.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "elimtest"},
		UnitName:         "elimtest",
		MaxDamage:        100,
		Script:           fixtureCOBProgram(),
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	return newSessionFixtureWorld(4, cat), def
}

// TestPlayerGateHasNoEliminationTerm locks the corrected player gate of
// [04 R-MOV-03 §10]: the three clauses are the row's occupancy word being
// nonzero, its control byte being 1, 2 or 3, and its ally-group byte not being
// 10. There is NO elimination term — [04 R-MOV-03 §1]'s third clause named the
// wrong byte and invented a state — so a row whose player has lost every unit
// is still swept, and trivially owns nothing for the traversal to hand back.
//
// The control byte and the occupancy word are still read: control 0 and a row
// that does not exist are both refused.
func TestPlayerGateHasNoEliminationTerm(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	s := &Session{Units: w, Econ: &economy.Service{}}
	for i := 0; i < 3; i++ {
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	}

	// Owner 0: created and still alive. Owner 1: created, then its last unit
	// dies. Owner 2: never created a unit.
	for _, owner := range []uint8{0, 1} {
		if _, err := w.Create(def, owner, 0, 0, 0); err != nil {
			t.Fatalf("create for owner %d: %v", owner, err)
		}
	}
	if visit, work := s.sweepPlayerGate(1); !visit || !work {
		t.Fatalf("owner 1 with a live unit must be swept")
	}
	var doomed pool.Handle
	for _, u := range w.IterSliced() {
		if u != nil && u.Owner == 1 {
			doomed = u.Handle
		}
	}
	if doomed == 0 {
		t.Fatalf("owner 1 has no unit to kill")
	}
	w.Unit(doomed).Dying = true
	if res := w.FinalizeDeath(doomed, 1); !res.Freed {
		t.Fatalf("owner 1's unit was not finalized")
	}
	if !economy.PlayerEliminated(w, 1) {
		t.Fatalf("owner 1 must satisfy the derived elimination predicate")
	}

	if visit, work := s.sweepPlayerGate(0); !visit || !work {
		t.Fatalf("owner 0 with a live unit must still be swept")
	}
	// The correction: an eliminated but seated row is NOT skipped.
	if visit, work := s.sweepPlayerGate(1); !visit || !work {
		t.Fatalf("an eliminated but seated row must still be swept, got (%v,%v)", visit, work)
	}
	if visit, work := s.sweepPlayerGate(2); !visit || !work {
		t.Fatalf("owner 2 never created a unit and must be swept, got (%v,%v)", visit, work)
	}

	// Control byte 0 is refused, and so is a row whose occupancy word is zero.
	s.Econ.Players[2].ControllerState = 0
	if visit, work := s.sweepPlayerGate(2); visit || work {
		t.Fatalf("control byte 0 must be refused, got (%v,%v)", visit, work)
	}
	s.Econ.Players[0].Exists = false
	if visit, work := s.sweepPlayerGate(0); visit || work {
		t.Fatalf("a row with a zero occupancy word must be refused, got (%v,%v)", visit, work)
	}
	// The ally-group clause: slot 10 is the never-seated sentinel and is past
	// the ten records either way.
	if visit, work := s.sweepPlayerGate(neverSeatedAllyGroup); visit || work {
		t.Fatalf("the never-seated ally group must be refused, got (%v,%v)", visit, work)
	}
}

// TestVictorySweepMatchesTheEliminationPredicate drives the elimination sweep
// of [08 R-SKIR-01 §3] "Victory detection" through its two legs: a
// participating slot that has created no unit yields no victory, and the same
// slot once eliminated (live zero, ever-created nonzero) no longer blocks it.
func TestVictorySweepMatchesTheEliminationPredicate(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle}

	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create for owner 0: %v", err)
	}
	// Leg one: owner 1 has created nothing, so the sweep answers "no victory"
	// and nothing is armed.
	if s.EvaluateResult(0) {
		t.Fatalf("victory must not latch while a slot has created no unit")
	}
	if s.resultPending {
		t.Fatalf("no result may be armed while a slot has created no unit")
	}

	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create for owner 1: %v", err)
	}
	if s.EvaluateResult(1) || s.resultPending {
		t.Fatalf("two live owners must not arm a result")
	}

	// Leg two: owner 1's last unit dies. Live zero with ever-created nonzero is
	// the elimination predicate, and the sweep arms on the next evaluation.
	w.Unit(h).Dying = true
	if res := w.FinalizeDeath(h, 2); !res.Freed {
		t.Fatalf("owner 1's unit was not finalized")
	}
	if !economy.PlayerEliminated(w, 1) {
		t.Fatalf("owner 1 must satisfy the elimination predicate")
	}
	if s.EvaluateResult(2) {
		t.Fatalf("the latch is not visible on the arming tick [08 \"Evaluation\"]")
	}
	if !s.resultPending {
		t.Fatalf("the sweep must arm once the only opponent is eliminated")
	}
	if want := s.teamForOwner(0); s.resultPendingWinner != want {
		t.Fatalf("pending winner = %d, want owner 0's team %d", s.resultPendingWinner, want)
	}
}
