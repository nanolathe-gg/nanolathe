package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/units"
)

// endConditionDueSession is the smallest session the end-condition block will
// run in: a one-slot skirmish whose local record is due at tick 0. With one
// participating player the kind-2 victory sweep finds no unskipped slot, so
// every due is a true due and the countdown steps on every one of them.
func endConditionDueSession(t *testing.T, seedDue uint32) *Session {
	t.Helper()
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: frame.NewBuffer(),
		Units:    units.NewSliced(1, &content.Catalog{}),
		Mission:  &mission.Mission{Type: mission.TypeSkirmish},
		Skirmish: SkirmishConfig{NumPlayers: 1},
		Econ:     &economy.Service{},
		Latch:    NewEndLatch(),
	}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].UpdateTime = seedDue
	s.Econ.Players[0].EndGameCountdown = -1
	return s
}

// TestEndConditionRunsOncePerSettlementDue is the cadence contract of
// [08 R-TRIG-01 §6] "The due tick is the settlement deadline": the end-condition
// block runs inside the LOCAL slot's settlement deadline block, after that
// slot's UpdateTime has advanced by 30, so the predicates are evaluated exactly
// once per settlement due and never between dues.
//
// The observable is the shared countdown. A true predicate arms it to 4 on the
// first due and steps it once per later due; if the block ran per sub-tick it
// would reach -1 in six ticks instead of six dues.
func TestEndConditionRunsOncePerSettlementDue(t *testing.T) {
	s := endConditionDueSession(t, 0)
	// The first sub-tick is global tick 1, and a deadline of 0 is already past
	// it: the compare is inclusive and the advance is a single +30, so the
	// deadline phases to 30 and the later dues fall on 30, 60, 90, 120, 150
	// [05 R-ECO-01 §1].
	s.Step(1)
	if s.Latch.Countdown != LatchInitial {
		t.Fatalf("countdown after the first due = %d, want the armed %d [08 R-TRIG-01 §6]", s.Latch.Countdown, LatchInitial)
	}
	// Twenty-nine sub-ticks with no due: the countdown must not move, and the
	// end must not arrive.
	for tick := int32(2); tick <= 30; tick++ {
		s.Step(tick)
	}
	if s.Latch.Countdown != LatchInitial-1 {
		t.Fatalf("countdown at tick 30 = %d, want exactly one step for the tick-30 due", s.Latch.Countdown)
	}
	if s.GetResult().Ended {
		t.Fatalf("the result latched before the sixth due: %+v", s.GetResult())
	}
	for tick := int32(31); tick <= 200 && !s.GetResult().Ended; tick++ {
		s.Step(tick)
	}
	res := s.GetResult()
	if !res.Ended {
		t.Fatalf("the result never latched: %+v latch %+v", res, s.Latch)
	}
	// The sixth consecutive true due writes the latch [08 R-TRIG-01 §6]
	// "Countdown and latch": the arming due at tick 1, then 30, 60, 90, 120 and
	// the terminal 150.
	if res.ArmedTick != 1 || res.Tick != 150 {
		t.Fatalf("armed at %d, latched at %d; want the arming due 1 and the sixth due 150", res.ArmedTick, res.Tick)
	}
}

// TestEndConditionResumesOnTheSavedDeadlinePhase is the half of the same
// finding an implementation with a private deadline gets wrong: the block has
// no deadline of its own, so it inherits whatever phase the slot's UpdateTime
// carries — which is what a retail save restores [08 R-TRIG-01 §6][05
// R-ECO-01 §1].
func TestEndConditionResumesOnTheSavedDeadlinePhase(t *testing.T) {
	// A deadline restored at 17 puts every due at 17, 47, 77, ...
	s := endConditionDueSession(t, 17)
	for tick := int32(1); tick <= 16; tick++ {
		s.Step(tick)
	}
	if s.Latch.Countdown >= 0 {
		t.Fatalf("countdown %d before the restored deadline; nothing is due until tick 17", s.Latch.Countdown)
	}
	s.Step(17)
	if s.Latch.Countdown != LatchInitial {
		t.Fatalf("countdown at the restored deadline = %d, want the armed %d", s.Latch.Countdown, LatchInitial)
	}
	for tick := int32(18); tick <= 300 && !s.GetResult().Ended; tick++ {
		s.Step(tick)
	}
	if res := s.GetResult(); !res.Ended || res.ArmedTick != 17 || res.Tick != 17+150 {
		t.Fatalf("result %+v; want the poll resumed on the saved phase, armed 17 and latched 167", res)
	}
}

// TestFalseDueNeitherResetsNorAdvancesTheCountdown pins the last sentence of
// [08 R-TRIG-01 §6] "Countdown and latch". The local slot's live count decides
// the defeat predicate; a due on which neither predicate holds leaves the
// countdown exactly where it was.
func TestFalseDueNeitherResetsNorAdvancesTheCountdown(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle, Latch: NewEndLatch()}
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create for owner 1: %v", err)
	}
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create for owner 0: %v", err)
	}

	// Owner 1 is wiped: the victory sweep is true and the countdown arms.
	w.Unit(h).Dying = true
	if res := w.FinalizeDeath(h, 1); !res.Freed {
		t.Fatalf("owner 1's unit was not finalized")
	}
	s.EvaluateResult(30)
	s.EvaluateResult(60)
	if s.Latch.Countdown != LatchInitial-1 {
		t.Fatalf("countdown after two true dues = %d, want %d", s.Latch.Countdown, LatchInitial-1)
	}

	// Owner 1 is back: neither predicate holds, and the countdown is untouched.
	if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatalf("recreate for owner 1: %v", err)
	}
	for tick := uint32(90); tick <= 300; tick += 30 {
		if s.EvaluateResult(tick) {
			t.Fatalf("a false due latched the result at tick %d", tick)
		}
	}
	if s.Latch.Countdown != LatchInitial-1 {
		t.Fatalf("countdown after seven false dues = %d, want it left at %d [08 R-TRIG-01 §6]", s.Latch.Countdown, LatchInitial-1)
	}
	if s.Latch.IsEnding() {
		t.Fatalf("a false due wrote the end latch: %+v", s.Latch)
	}
}
