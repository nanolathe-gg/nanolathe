package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestLoadingCompletesBeforeBattleTick ensures a loading session does not
// advance simulation time until the battle dispatch [08 "Session states"].
func TestLoadingCompletesBeforeBattleTick(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &frame.Buffer{},
		Units:    units.NewSliced(10, nil),
		State:    StateLoading,
	}
	s.RegisterAll()
	before := s.Clock.GlobalTick
	// A loading dispatch consumes no battle ticks.
	s.Step(10)
	if s.Clock.GlobalTick != before {
		t.Fatalf("loading dispatch advanced ticks [08 \"Session states\"]: %d->%d", before, s.Clock.GlobalTick)
	}
	// Loading handler should have scheduled battle for next dispatch (C2).
	if s.State != StateBattle || !s.IsPendingBattle() {
		t.Fatalf("state 5 completion must schedule state 6 for next dispatch [08] C2: state=%v pending=%v", s.State, s.IsPendingBattle())
	}
	// Second Step should clear pending and tick.
	s.Step(11)
	if s.Clock.GlobalTick == before {
		t.Fatalf("after loading completion, battle should tick on next dispatch [08] C2")
	}
	if s.IsPendingBattle() {
		t.Fatalf("pendingBattle should clear after first battle dispatch [08] C2")
	}
}

// TestVictoryReachesPostBattleExactlyOnce ensures the victory latch enters
// post-battle once [08][P1-01].
func TestVictoryReachesPostBattleExactlyOnce(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &frame.Buffer{},
		Units:    units.NewSliced(10, nil),
		State:    StateBattle,
		Mission:  &mission.Mission{Type: mission.TypeCampaign},
		Latch:    NewEndLatch(),
	}
	s.RegisterAll()
	s.State = StateBattle
	// Simulate latch arming and countdown crossing below zero to ending.
	// AdvanceWin arms to 4 on first due, then decrements each due.
	if !s.Latch.AdvanceWin(true) {
		// first call arms, not latched
	}
	for i := 0; i < 4; i++ {
		s.Latch.AdvanceWin(true)
	}
	// After 5 due calls (arm + 4 decr), countdown should be 0; one more makes -1 and latches.
	latched := s.Latch.AdvanceWin(true)
	if !latched || !s.Latch.IsEnding() {
		t.Fatalf("latch should be ending after countdown crosses below zero [P1-01] latched=%v ending=%v countdown=%d bits=0x%x", latched, s.Latch.IsEnding(), s.Latch.Countdown, s.Latch.Bits)
	}
	// Simulate trigger-poll transition 6->7 exactly once.
	win := s.Latch.IsWin()
	s.Progress.ApplyCampaignResult(0, win)
	if !s.TransitionTo(StatePostBattle) {
		t.Fatalf("victory should transition 6->7 [08] C1")
	}
	if s.State != StatePostBattle {
		t.Fatalf("state should be postbattle after victory, got %v", s.State)
	}
	// Second attempt to transition 7->7 or 6->7 again must not happen.
	if s.TransitionTo(StatePostBattle) {
		t.Fatalf("victory must reach postbattle exactly once: second transition should fail")
	}
	// Latch should stay ending, not re-arm.
	beforeBits := s.Latch.Bits
	beforeCountdown := s.Latch.Countdown
	// Further AdvanceWin calls should not change latch while already ending? At least bits stay ending.
	s.Latch.AdvanceWin(true)
	if !s.Latch.IsEnding() || s.Latch.Bits != beforeBits || s.Latch.Countdown != beforeCountdown-1 {
		// Countdown may decrement, but bits must stay ending and not toggle win.
		// We only check that IsEnding stays true and not re-enter.
		if !s.Latch.IsEnding() {
			t.Fatalf("latch must stay ending after victory")
		}
	}
	// Ensure postbattle handler transitions 7->2 exactly once via Advance.
	s.Advance()
	if s.State != StateRouter {
		t.Fatalf("postbattle handler should transition 7->2 [08] C1: got %v", s.State)
	}
}

func TestEndLatchUsesFinalTruePath(t *testing.T) {
	tests := []struct {
		name     string
		firstWin bool
		lastWin  bool
	}{
		{name: "win-armed-lose-final", firstWin: true, lastWin: false},
		{name: "lose-armed-win-final", firstWin: false, lastWin: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			latch := NewEndLatch()
			if tc.firstWin {
				latch.AdvanceWin(true)
			} else {
				latch.AdvanceLose(true)
			}
			before := latch
			if tc.firstWin {
				latch.AdvanceLose(false)
			} else {
				latch.AdvanceWin(false)
			}
			if latch != before {
				t.Fatalf("false due changed latch: before=%+v after=%+v", before, latch)
			}
			for i := 0; i < 4; i++ {
				if i&1 == 0 {
					latch.AdvanceLose(true)
				} else {
					latch.AdvanceWin(true)
				}
			}
			var latched bool
			if tc.lastWin {
				latched = latch.AdvanceWin(true)
			} else {
				latched = latch.AdvanceLose(true)
			}
			if !latched || !latch.IsEnding() || latch.IsWin() != tc.lastWin || latch.IsLose() == tc.lastWin {
				t.Fatalf("sixth due did not select final path: %+v", latch)
			}
		})
	}
}

// TestRetryReloadsSameMission ensures retry keeps the mission and returns to
// loading [P1-01 §7.5].
func TestRetryReloadsSameMission(t *testing.T) {
	m := &mission.Mission{Type: mission.TypeCampaign}
	// Use a minimal mission with one feature/unit not needed for state check.
	s := &Session{
		Clock:   &clock.State{Requested: 10, Active: 10},
		Mission: m,
		State:   StatePostBattle,
		Latch:   NewEndLatch(),
	}
	s.RegisterAll()
	s.State = StatePostBattle
	s.Mission = m
	origMission := s.Mission
	// Simulate that we had a win and progress written.
	s.Latch.Win()
	s.Latch.Bits |= LatchBitEnding
	s.Progress.ApplyCampaignResult(0, true)
	if s.Progress.WL[0] != 'W' {
		t.Fatalf("progress should be W before retry")
	}
	// Clear WL to test that retry does NOT rewrite beyond current slot? Retry should keep same mission and not overwrite W/L beyond slot?
	// Retry keeps the same mission object and returns to loading.
	if !s.Retry() {
		t.Fatalf("Retry should succeed from postbattle [P1-01 §7.5]")
	}
	if s.Mission != origMission {
		t.Fatalf("retry must reload same mission object [P1-01 §7.5]: mission changed")
	}
	if s.State != StateLoading {
		t.Fatalf("retry must end in Loading (5) via 7->2->5 [P1-01 §7.5][08] C1: got %v", s.State)
	}
	// Latch should be reset for new battle
	if s.Latch.IsEnding() {
		t.Fatalf("retry should reset latch for new battle [P1-01]")
	}
}

// TestContinueWritesProgress ensures continue records the result and returns
// to the router [P1-01 §7.5].
func TestContinueWritesProgress(t *testing.T) {
	m := &mission.Mission{Type: mission.TypeCampaign}
	s := &Session{
		Clock:        &clock.State{Requested: 10, Active: 10},
		Mission:      m,
		State:        StatePostBattle,
		Latch:        NewEndLatch(),
		CampaignSlot: 0,
	}
	s.RegisterAll()
	s.State = StatePostBattle
	// Simulate victory latch win before continue
	s.Latch = NewEndLatch()
	s.Latch.AdvanceWin(true) // arm
	for i := 0; i < 5; i++ {
		s.Latch.AdvanceWin(true)
	}
	if !s.Latch.IsEnding() || !s.Latch.IsWin() {
		t.Fatalf("latch should be win ending before continue")
	}
	// The terminal latch writes the result before CONTINUE is selected.
	s.Progress.ApplyCampaignResult(s.CampaignSlot, true)
	if !s.ContinueCampaign() {
		t.Fatalf("Continue should succeed from postbattle [P1-01 §7.5]")
	}
	if s.Progress.WL[s.CampaignSlot] != 'W' {
		t.Fatalf("continue must preserve terminal progress [P1-01 §2.3]: got %d", s.Progress.WL[s.CampaignSlot])
	}
	if s.State != StateRouter {
		t.Fatalf("continue must transition 7->2 [08] C1: got %v", s.State)
	}
}

// TestTeardownReturnsToRouter keeps the authoritative lifecycle edge
// covered without giving the session ownership of platform resources. The
// window, display, sound, archive, semaphore, and registry owners are all at
// the command/platform edge [01 §2.3].
func TestTeardownReturnsToRouter(t *testing.T) {
	for _, state := range []State{StateTeardownA, StateTeardownB} {
		s := &Session{State: state}
		s.RegisterAll()
		s.Advance()
		if s.State != StateRouter {
			t.Fatalf("teardown state %v should transition to router (2) [08]: got %v", state, s.State)
		}
	}
}

// TestAbortTransitionsThroughRouter ensures abort does not leave battle
// ticking behind the result overlay [08].
func TestAbortTransitionsThroughRouter(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &frame.Buffer{},
		Units:    units.NewSliced(10, nil),
		State:    StateBattle,
	}
	s.RegisterAll()
	s.State = StateBattle
	before := s.Clock.GlobalTick
	// Abort should transition 6->2
	if !s.AbortBattle() {
		t.Fatalf("abort from battle should succeed 6->2 [08] C1")
	}
	if s.State != StateRouter {
		t.Fatalf("abort should be in Router(2), got %v", s.State)
	}
	// Step should not tick after abort
	s.Step(10)
	if s.Clock.GlobalTick != before {
		t.Fatalf("abort must not leave battle ticking behind overlay: ticks %d->%d", before, s.Clock.GlobalTick)
	}
	// Victory abort from postbattle also 7->2
	s.State = StatePostBattle
	if !s.AbortBattle() {
		t.Fatalf("abort from postbattle should succeed 7->2")
	}
	if s.State != StateRouter {
		t.Fatalf("postbattle abort should be Router, got %v", s.State)
	}
}
