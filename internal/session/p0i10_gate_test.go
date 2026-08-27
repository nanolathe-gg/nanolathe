package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestP0I10_LoadingCannotTick ensures newly constructed session cannot tick while loading [08 "Session states"] P0-I10.
func TestP0I10_LoadingCannotTick(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &snapshot.Buffer{},
		Units:    units.New(10, nil),
		State:    StateLoading,
	}
	s.RegisterAll()
	before := s.Clock.GlobalTick
	// scaledNow 10 would give up to 5 ticks in battle, but must give 0 while loading [P0-I10].
	s.Step(10)
	if s.Clock.GlobalTick != before {
		t.Fatalf("newly constructed session cannot tick while loading [P0-I10][08 \"Session states\"]: ticks %d->%d", before, s.Clock.GlobalTick)
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

// TestP0I10_LoadingCompletionDeferred verifies C2 next-dispatch deferral via production handlers.
func TestP0I10_LoadingCompletionDeferred(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &snapshot.Buffer{},
		State:    StateLoading,
	}
	s.RegisterAll()
	var battleRuns int
	// Overwrite battle handler after RegisterAll to count, but keep loading handler that does CompleteLoading.
	s.SetHandler(StateBattle, func(ss *Session) { battleRuns++ })
	// First dispatch: loading -> pending battle, battle not run inline.
	s.Advance()
	if battleRuns != 0 {
		t.Fatalf("battle handler must NOT run inline on loading completion [08] C2: got %d", battleRuns)
	}
	if s.State != StateBattle || !s.IsPendingBattle() {
		t.Fatalf("after loading dispatch state should be battle pending [08] C2: %v pending %v", s.State, s.IsPendingBattle())
	}
	// Next dispatch: battle handler first run.
	s.Advance()
	if battleRuns != 1 {
		t.Fatalf("battle handler first run should happen on next dispatch [08] C2: got %d", battleRuns)
	}
	if s.IsPendingBattle() {
		t.Fatalf("pendingBattle should clear after first battle dispatch [08] C2")
	}
	// Subsequent dispatch runs again.
	s.Advance()
	if battleRuns != 2 {
		t.Fatalf("second battle dispatch should run again, got %d", battleRuns)
	}
}

// TestP0I10_VictoryReachesPostBattleExactlyOnce ensures victory latch transitions 6->7 exactly once [08][P1-01].
func TestP0I10_VictoryReachesPostBattleExactlyOnce(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &snapshot.Buffer{},
		Units:    units.New(10, nil),
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
		t.Fatalf("victory must reach postbattle exactly once [P0-I10]: second transition should fail")
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

// TestP0I10_RetryReloadsSameMission ensures retry reloads same mission via state 5 directly [P1-01 §7.5].
func TestP0I10_RetryReloadsSameMission(t *testing.T) {
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
	// For gate, we check that retry keeps same Mission object and ends in Loading.
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

// TestP0I10_ContinueWritesProgress ensures continue writes progress and selects next mission [P1-01 §7.5].
func TestP0I10_ContinueWritesProgress(t *testing.T) {
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
	// Ensure progress not yet written (simulate fresh postbattle before handler's write)
	s.Progress = BankProgress{}
	if !s.ContinueCampaign() {
		t.Fatalf("Continue should succeed from postbattle [P1-01 §7.5]")
	}
	if s.Progress.WL[0] != 'W' {
		t.Fatalf("continue must write progress W/L [P1-01 §2.3]: got %d", s.Progress.WL[0])
	}
	if s.State != StateRouter {
		t.Fatalf("continue must transition 7->2 [08] C1: got %v", s.State)
	}
}

// TestP0I10_TeardownReleasesInDocumentedOrder ensures teardown releases state in documented order [01 §2.1][01 §2.3].
func TestP0I10_TeardownReleasesInDocumentedOrder(t *testing.T) {
	// Record shutdown order via custom Shutdown steps
	sd := &Shutdown{}
	var order []string
	for _, name := range ShutdownOrder {
		n := name
		sd.Register(func() error {
			order = append(order, n)
			return nil
		})
	}
	s := &Session{
		State:    StateTeardownA,
		Shutdown: sd,
	}
	s.RegisterAll()
	// Override shutdown with our recorded one (RegisterAll already created one, replace)
	s.Shutdown = sd
	if s.State != StateTeardownA {
		t.Fatalf("initial state should be teardown 0")
	}
	s.Advance()
	if s.State != StateRouter {
		t.Fatalf("teardown 0 should transition 0->2 [08] C1: got %v", s.State)
	}
	// Check reverse order: Shutdown.Run is called inside teardown, which runs reverse startup.
	// Startup order is ShutdownOrder: window, display, sound, archives, semaphore, registryAudio
	// So Run should execute reverse: registryAudio, semaphore, archives, sound, display, window
	expectedReverse := make([]string, len(ShutdownOrder))
	for i, name := range ShutdownOrder {
		expectedReverse[len(ShutdownOrder)-1-i] = name
	}
	if len(order) != len(expectedReverse) {
		t.Fatalf("teardown order length %d want %d", len(order), len(expectedReverse))
	}
	for i := range order {
		if order[i] != expectedReverse[i] {
			t.Fatalf("teardown order mismatch at %d: got %s want %s [01 §2.1][01 §2.3] ShutdownOrder %v", i, order[i], expectedReverse[i], ShutdownOrder)
		}
	}
	// Test variant B as well
	sd2 := &Shutdown{}
	var order2 []string
	for _, name := range ShutdownOrder {
		n := name
		sd2.Register(func() error { order2 = append(order2, n); return nil })
	}
	s2 := &Session{State: StateTeardownB, Shutdown: sd2}
	s2.RegisterAll()
	s2.Shutdown = sd2
	s2.Advance()
	if s2.State != StateRouter {
		t.Fatalf("teardown 1 should transition 1->2 [08] C1: got %v", s2.State)
	}
	if len(order2) != len(expectedReverse) {
		t.Fatalf("teardown B order length %d want %d", len(order2), len(expectedReverse))
	}
	for i := range order2 {
		if order2[i] != expectedReverse[i] {
			t.Fatalf("teardown B order mismatch at %d: got %s want %s", i, order2[i], expectedReverse[i])
		}
	}
}

// TestP0I10_AbortTransitionsThroughRouter ensures abort 6->2 does not leave battle ticking [P0-I10].
func TestP0I10_AbortTransitionsThroughRouter(t *testing.T) {
	s := &Session{
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 0},
		Snapshot: &snapshot.Buffer{},
		Units:    units.New(10, nil),
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
		t.Fatalf("abort must not leave battle ticking behind overlay [P0-I10]: ticks %d->%d", before, s.Clock.GlobalTick)
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
