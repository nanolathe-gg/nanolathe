package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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

// TestKind2VictorySweepSkipsZeroLiveCountSlots drives the elimination sweep of
// [08 R-TRIG-01 §6] "The kind-2 victory sweep": walk slots 0-9, skip the local
// slot, skip allies of the local player, skip any slot with a zero live-unit
// count; any survivor means no victory, otherwise victory.
//
// **Correction.** This test was TestVictorySweepMatchesTheEliminationPredicate
// and asserted that a participating slot which had created no unit blocks
// victory — the "ever created is zero" term of the elimination predicate. That
// term belongs to the kind-3 sweep of [08 R-SKIR-01 §3] "Victory detection";
// [08 R-TRIG-01 §6] states the kind-2 sweep has "no shared-victory bit, no
// controller or elimination test, and no rule-word test". A slot with nothing
// alive is skipped whether or not it ever held a unit.
func TestKind2VictorySweepSkipsZeroLiveCountSlots(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	// The latch starts from its retail initial state: countdown -1, "unarmed"
	// [P1-01 §2.2]. A zero-valued EndLatch would read as already armed at 0, so
	// the first true due would latch instead of arming [08 R-TRIG-01 §6].
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle, Latch: NewEndLatch()}

	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create for owner 0: %v", err)
	}
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create for owner 1: %v", err)
	}
	// A live opponent blocks the sweep and arms nothing.
	if s.EvaluateResult(1) || s.resultPending {
		t.Fatalf("two live owners must not arm a result")
	}

	// Owner 1's last unit dies. A zero live count is the whole skip, so the
	// sweep passes on the next evaluation.
	w.Unit(h).Dying = true
	if res := w.FinalizeDeath(h, 2); !res.Freed {
		t.Fatalf("owner 1's unit was not finalized")
	}
	if !economy.PlayerEliminated(w, 1) {
		t.Fatalf("owner 1 must satisfy the elimination predicate")
	}
	if s.EvaluateResult(2) {
		t.Fatalf("the latch is not visible on the arming tick [08 R-TRIG-01 §6]")
	}
	if !s.resultPending {
		t.Fatalf("the sweep must arm once the only opponent has nothing alive")
	}
	if want := s.teamForOwner(0); s.resultPendingWinner != want {
		t.Fatalf("pending winner = %d, want the local player's team %d", s.resultPendingWinner, want)
	}
}

// TestKind2VictorySweepIgnoresTheCommanderDeathRuleWord locks the missing
// rule-word test of [08 R-TRIG-01 §6] "The kind-2 victory sweep": the sweep
// has "no rule-word test", so a deathmatch (commander-death rule 2) skirmish
// that eliminates every opponent wins. The early-out that returns false when
// the rule word is 2 is the first step of the kind-3 alliance-aware sweep
// [08 R-SKIR-01 §3] "Victory detection" and has no kind-2 site.
//
// The terminal due is the second half of the contract: rule 2 selects the
// commander respawn only on the LOST path, so the won path latches the win
// [08 R-TRIG-01 §6] "Countdown and latch".
func TestKind2VictorySweepIgnoresTheCommanderDeathRuleWord(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.CommanderDeath = int(CommanderDeathDeathmatch)
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle, Latch: NewEndLatch()}

	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create for owner 0: %v", err)
	}
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create for owner 1: %v", err)
	}
	if s.EvaluateResult(1) || s.resultPending {
		t.Fatalf("two live owners must not arm a result under rule 2")
	}

	w.Unit(h).Dying = true
	if res := w.FinalizeDeath(h, 2); !res.Freed {
		t.Fatalf("owner 1's unit was not finalized")
	}
	if s.EvaluateResult(2) {
		t.Fatalf("the latch is not visible on the arming tick [08 R-TRIG-01 §6]")
	}
	if !s.resultPending {
		t.Fatalf("rule 2 must not suppress the kind-2 elimination sweep")
	}
	if s.Latch.Pending != 1 {
		t.Fatalf("latch pending = %d, want the won path [08 R-TRIG-01 §6]", s.Latch.Pending)
	}
	// Five more true dues take the shared countdown below zero; the won latch
	// is written there and the rule word does not divert it to a respawn.
	var latched bool
	for tick := uint32(3); tick <= 2+150; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("rule 2 victory did not latch within five dues, latch %+v", s.Latch)
	}
	if !s.Latch.IsWin() || s.GetResult().Kind != "victory" {
		t.Fatalf("latched result = %+v, latch %+v, want a victory", s.GetResult(), s.Latch)
	}
}

// TestLocalDefeatDoesNotWaitForTheLastOpponent is the defeat predicate of
// [08 R-SKIR-01 §3] "Defeat detection" in the shape the old single-count fold
// could not express: the local player is eliminated while two other players
// are still fighting each other. Defeat is the local live count reaching zero,
// not "one side left standing", so it arms on the spot.
func TestLocalDefeatDoesNotWaitForTheLastOpponent(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 3}
	cfg.ApplyDefaults()
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle, LocalOwner: 0, Latch: NewEndLatch()}

	var local pool.Handle
	for owner := uint8(0); owner < 3; owner++ {
		h, err := w.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatalf("create for owner %d: %v", owner, err)
		}
		if owner == 0 {
			local = h
		}
	}
	if s.EvaluateResult(1) || s.resultPending {
		t.Fatalf("three live owners must not arm a result")
	}

	w.Unit(local).Dying = true
	if res := w.FinalizeDeath(local, 2); !res.Freed {
		t.Fatalf("the local unit was not finalized")
	}
	if s.EvaluateResult(3) {
		t.Fatalf("the latch is not visible on the arming tick")
	}
	if !s.resultPending {
		t.Fatalf("local elimination must arm the defeat immediately, with two opponents still alive")
	}
	if s.Latch.Pending != 2 {
		t.Fatalf("latch pending = %d, want the lost path [08 R-TRIG-01 §6]", s.Latch.Pending)
	}
	if s.resultPendingWinner != s.teamForOwner(1) {
		t.Fatalf("pending winner = %d, want the lowest surviving opponent's team %d", s.resultPendingWinner, s.teamForOwner(1))
	}
	// Five 30-tick dues later the latch is visible and it carries the lost bit.
	var latched bool
	for tick := uint32(4); tick <= 3+150; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("defeat did not latch within five dues, latch %+v", s.Latch)
	}
	if !s.Latch.IsLose() || s.GetResult().Kind != "defeat" {
		t.Fatalf("latched result = %+v, latch %+v, want a local defeat", s.GetResult(), s.Latch)
	}
	// Both opponents are still alive: the defeat did not depend on one of them
	// winning first.
	if w.LiveCountForPlayer(1) == 0 || w.LiveCountForPlayer(2) == 0 {
		t.Fatalf("both opponents must still be alive at the defeat latch")
	}
}
