package session

import "testing"

func TestStateTransitions(t *testing.T) {
	allowed := map[State][]State{
		StateTeardownA:      {StateRouter},
		StateTeardownB:      {StateRouter},
		StateRouter:         {StateNetworkPreload, StateLocalPreload, StateLoading},
		StateNetworkPreload: {StateLoading},
		StateLocalPreload:   {StateLoading},
		StateLoading:        {StateBattle},
		StateBattle:         {StatePostBattle, StateRouter},
		StatePostBattle:     {StateRouter},
	}
	// Build expected matrix.
	var expect [8][8]bool
	for from, tos := range allowed {
		for _, to := range tos {
			expect[from][to] = true
		}
	}
	// Full 8x8 matrix incl. abort path 6->2 and 7->2.
	for from := State(0); from <= StatePostBattle; from++ {
		for to := State(0); to <= StatePostBattle; to++ {
			got := CanTransition(from, to)
			want := expect[from][to]
			if got != want {
				t.Fatalf("CanTransition(%v->%v) = %v want %v", from, to, got, want)
			}
		}
	}
	// Out-of-range states never transition.
	if CanTransition(8, 0) || CanTransition(0, 8) {
		t.Fatalf("out-of-range state should not transition")
	}
	// Single-player takes 2->5 directly; state 3 present and unreachable in SP.
	if !CanTransition(StateRouter, StateLoading) {
		t.Fatalf("single-player 2->5 must be allowed")
	}
	if !CanTransition(StateRouter, StateNetworkPreload) {
		t.Fatalf("state 3 must remain present in graph even though unreachable in single-player")
	}
	// Abort path 6->2 must be distinct from normal 6->7.
	if !CanTransition(StateBattle, StateRouter) {
		t.Fatalf("abort path 6->2 missing")
	}
	if !CanTransition(StateBattle, StatePostBattle) {
		t.Fatalf("normal 6->7 missing")
	}
	// TransitionTo validates.
	s := &Session{State: StateRouter}
	if !s.TransitionTo(StateLoading) || s.State != StateLoading {
		t.Fatalf("TransitionTo valid edge failed")
	}
	s.State = StateRouter
	if s.TransitionTo(StateBattle) {
		t.Fatalf("TransitionTo should reject invalid edge 2->6")
	}
	if s.State != StateRouter {
		t.Fatalf("rejected TransitionTo must not change State")
	}
	// 6->2 abort via TransitionTo.
	s.State = StateBattle
	if !s.TransitionTo(StateRouter) {
		t.Fatalf("TransitionTo abort 6->2 failed")
	}
	// 7->2.
	s.State = StatePostBattle
	if !s.TransitionTo(StateRouter) {
		t.Fatalf("TransitionTo 7->2 failed")
	}
}

func TestNextDispatchFirstRun(t *testing.T) {
	// C2: completing the state-5 loading thread installs state 6 whose FIRST RUN
	// happens on the NEXT dispatch, not inline [08 "Session states"].
	s := &Session{State: StateLoading}
	var loadingRuns, battleRuns int
	s.SetHandler(StateLoading, func(ss *Session) { loadingRuns++ })
	s.SetHandler(StateBattle, func(ss *Session) { battleRuns++ })

	// Loading handler runs when we dispatch in state 5.
	s.Advance()
	if loadingRuns != 1 || battleRuns != 0 {
		t.Fatalf("first dispatch in state 5: loadingRuns=%d battleRuns=%d want 1,0", loadingRuns, battleRuns)
	}
	// Simulate loading thread completion installing state 6.
	if !s.CompleteLoading() {
		t.Fatalf("CompleteLoading should succeed from state 5")
	}
	if s.State != StateBattle {
		t.Fatalf("CompleteLoading should install state 6, got %v", s.State)
	}
	if !s.IsPendingBattle() {
		t.Fatalf("pendingBattle should be true after install before next dispatch")
	}
	if battleRuns != 0 {
		t.Fatalf("battle handler must NOT run inline on CompleteLoading, battleRuns=%d", battleRuns)
	}
	// Next dispatch runs battle for first time.
	s.Advance()
	if battleRuns != 1 {
		t.Fatalf("battle handler first run should happen on next dispatch, got %d", battleRuns)
	}
	if s.IsPendingBattle() {
		t.Fatalf("pendingBattle should clear after first run")
	}
	// Subsequent dispatch runs again.
	s.Advance()
	if battleRuns != 2 {
		t.Fatalf("second battle dispatch should run again, got %d", battleRuns)
	}
	// CompleteLoading from wrong state is no-op.
	s.State = StateRouter
	if s.CompleteLoading() {
		t.Fatalf("CompleteLoading from non-loading state should return false")
	}
	// Normal inline 5->6 via handler also defers to next dispatch (single-dispatch guarantee).
	s2 := &Session{State: StateLoading}
	var s2BattleRuns int
	s2.SetHandler(StateBattle, func(ss *Session) { s2BattleRuns++ })
	s2.SetHandler(StateLoading, func(ss *Session) {
		// handler transitions 5->6 inline
		ss.TransitionTo(StateBattle)
	})
	s2.Advance()
	if s2.State != StateBattle {
		t.Fatalf("handler-initiated 5->6 should update State to 6")
	}
	if s2BattleRuns != 0 {
		t.Fatalf("handler-initiated 5->6 must not run battle handler inline, got %d", s2BattleRuns)
	}
	s2.Advance()
	if s2BattleRuns != 1 {
		t.Fatalf("battle handler should run on next dispatch after inline transition, got %d", s2BattleRuns)
	}
}

func TestGametypeRouting(t *testing.T) {
	// C3: Gametype-2 save selects state 5 directly; Gametype-1 selects state 4
	// first which configures two campaign players then same state-5 path [08 "Session states"].
	st2, ok2 := StateForGametype(GametypeMultiplayer)
	if !ok2 || st2 != StateLoading {
		t.Fatalf("StateForGametype(2) = %v,%v want %v,true", st2, ok2, StateLoading)
	}
	st1, ok1 := StateForGametype(GametypeCampaign)
	if !ok1 || st1 != StateLocalPreload {
		t.Fatalf("StateForGametype(1) = %v,%v want %v,true", st1, ok1, StateLocalPreload)
	}
	if _, ok := StateForGametype(0); ok {
		t.Fatalf("StateForGametype(0) should be invalid")
	}
	if _, ok := StateForGametype(3); ok {
		t.Fatalf("StateForGametype(3) should be invalid")
	}
	s := New()
	if err := s.SelectForGametype(GametypeMultiplayer); err != nil {
		t.Fatalf("SelectForGametype(2) error: %v", err)
	}
	if s.State != StateLoading {
		t.Fatalf("SelectForGametype(2) state = %v want %v", s.State, StateLoading)
	}
	s2 := New()
	if err := s2.SelectForGametype(GametypeCampaign); err != nil {
		t.Fatalf("SelectForGametype(1) error: %v", err)
	}
	if s2.State != StateLocalPreload {
		t.Fatalf("SelectForGametype(1) state = %v want %v", s2.State, StateLocalPreload)
	}
	// After state 4, path is same state-5 path: 4->5 must be allowed.
	if !CanTransition(s2.State, StateLoading) {
		t.Fatalf("Gametype-1 state4 must transition 4->5")
	}
	// Simulate campaign two-player setup: state4 handler configures two
	// campaign players then transitions to 5.
	s2.TransitionTo(StateLoading)
	if s2.State != StateLoading {
		t.Fatalf("after 4->5 transition, state should be 5, got %v", s2.State)
	}
	// Invalid gametype via Session.
	s3 := New()
	if err := s3.SelectForGametype(99); err == nil {
		t.Fatalf("SelectForGametype invalid should error")
	}
}

func TestAdmissionMasks(t *testing.T) {
	// C4: admission masks as written even with no network: bit0 admits states
	// other than 5 and 6; bit1 admits 5; bit2 admits 6 [08 "Admission masks"].
	// Mask matrix over all states × bits.
	for s := State(0); s <= StatePostBattle; s++ {
		mask := AdmissionMaskForState(s)
		var want uint8
		switch s {
		case StateLoading:
			want = MaskLoading
		case StateBattle:
			want = MaskBattle
		default:
			want = MaskOther
		}
		if mask != want {
			t.Fatalf("AdmissionMaskForState(%v) = %d want %d", s, mask, want)
		}
	}
	// bit0 alone
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := IsAdmitted(s, MaskOther)
		shouldAdmit := s != StateLoading && s != StateBattle
		if admitted != shouldAdmit {
			t.Fatalf("IsAdmitted(%v, MaskOther=1) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// bit1 alone admits only state 5
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := IsAdmitted(s, MaskLoading)
		shouldAdmit := s == StateLoading
		if admitted != shouldAdmit {
			t.Fatalf("IsAdmitted(%v, MaskLoading=2) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// bit2 alone admits only state 6
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := IsAdmitted(s, MaskBattle)
		shouldAdmit := s == StateBattle
		if admitted != shouldAdmit {
			t.Fatalf("IsAdmitted(%v, MaskBattle=4) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// Combinations
	if !IsAdmitted(StateLoading, MaskOther|MaskLoading) {
		t.Fatalf("state5 should be admitted by mask 3")
	}
	if IsAdmitted(StateBattle, MaskOther|MaskLoading) {
		t.Fatalf("state6 should NOT be admitted by mask 3")
	}
	if !IsAdmitted(StateBattle, MaskOther|MaskBattle) {
		t.Fatalf("state6 should be admitted by mask 5")
	}
	if IsAdmitted(StateLoading, MaskOther|MaskBattle) {
		t.Fatalf("state5 should NOT be admitted by mask 5")
	}
	// mask 7 admits all
	for s := State(0); s <= StatePostBattle; s++ {
		if !IsAdmitted(s, 7) {
			t.Fatalf("mask 7 should admit %v", s)
		}
	}
	// mask 0 admits none
	for s := State(0); s <= StatePostBattle; s++ {
		if IsAdmitted(s, 0) {
			t.Fatalf("mask 0 should admit none, but admitted %v", s)
		}
	}
	// State 3 present and unreachable still respects mask: it is MaskOther.
	if AdmissionMaskForState(StateNetworkPreload) != MaskOther {
		t.Fatalf("state3 should be MaskOther")
	}
	if !IsAdmitted(StateNetworkPreload, MaskOther) {
		t.Fatalf("state3 should be admitted by bit0")
	}
	if IsAdmitted(StateNetworkPreload, MaskLoading) || IsAdmitted(StateNetworkPreload, MaskBattle) {
		t.Fatalf("state3 should NOT be admitted by bit1 or bit2 alone")
	}
}
