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

func TestAdvanceDispatchesEachConcreteState(t *testing.T) {
	// Advance selects the retail operation directly for every state. These
	// assertions cover the state graph without installing test callbacks
	// [08 "Session states"].
	tests := []struct {
		name State
		want State
	}{
		{StateTeardownA, StateRouter},
		{StateTeardownB, StateRouter},
		{StateRouter, StateLoading},
		{StateNetworkPreload, StateLoading},
		{StateLocalPreload, StateLoading},
		{StateLoading, StateBattle},
		{StateBattle, StateBattle},
		{StatePostBattle, StateRouter},
	}
	for _, test := range tests {
		s := &Session{State: test.name}
		if test.name == StateBattle {
			s.pendingBattle = true
		}
		s.Advance()
		if s.State != test.want {
			t.Errorf("Advance(%v) state = %v want %v", test.name, s.State, test.want)
		}
		if test.name == StateBattle && s.IsPendingBattle() {
			t.Errorf("Advance(%v) should clear deferred first-run marker", test.name)
		}
	}

	// An unknown state is unsupported and idle; it must not call any state
	// operation or alter the value [08 "Session states"].
	s := &Session{State: State(8)}
	s.Advance()
	if s.State != State(8) {
		t.Fatalf("Advance(8) changed unsupported state to %v", s.State)
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
	s := newBareSession()
	if err := s.SelectForGametype(GametypeMultiplayer); err != nil {
		t.Fatalf("SelectForGametype(2) error: %v", err)
	}
	if s.State != StateLoading {
		t.Fatalf("SelectForGametype(2) state = %v want %v", s.State, StateLoading)
	}
	s2 := newBareSession()
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
	s3 := newBareSession()
	if err := s3.SelectForGametype(99); err == nil {
		t.Fatalf("SelectForGametype invalid should error")
	}
}

func TestAdmissionMasks(t *testing.T) {
	// C4: admission masks as written even with no network: bit0 admits states
	// other than 5 and 6; bit1 admits 5; bit2 admits 6 [08 "Admission masks"].
	// Mask matrix over all states × bits.
	for s := State(0); s <= StatePostBattle; s++ {
		mask := admissionMaskForState(s)
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
			t.Fatalf("admissionMaskForState(%v) = %d want %d", s, mask, want)
		}
	}
	// bit0 alone
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := isAdmitted(s, MaskOther)
		shouldAdmit := s != StateLoading && s != StateBattle
		if admitted != shouldAdmit {
			t.Fatalf("isAdmitted(%v, MaskOther=1) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// bit1 alone admits only state 5
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := isAdmitted(s, MaskLoading)
		shouldAdmit := s == StateLoading
		if admitted != shouldAdmit {
			t.Fatalf("isAdmitted(%v, MaskLoading=2) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// bit2 alone admits only state 6
	for s := State(0); s <= StatePostBattle; s++ {
		admitted := isAdmitted(s, MaskBattle)
		shouldAdmit := s == StateBattle
		if admitted != shouldAdmit {
			t.Fatalf("isAdmitted(%v, MaskBattle=4) = %v want %v", s, admitted, shouldAdmit)
		}
	}
	// Combinations
	if !isAdmitted(StateLoading, MaskOther|MaskLoading) {
		t.Fatalf("state5 should be admitted by mask 3")
	}
	if isAdmitted(StateBattle, MaskOther|MaskLoading) {
		t.Fatalf("state6 should NOT be admitted by mask 3")
	}
	if !isAdmitted(StateBattle, MaskOther|MaskBattle) {
		t.Fatalf("state6 should be admitted by mask 5")
	}
	if isAdmitted(StateLoading, MaskOther|MaskBattle) {
		t.Fatalf("state5 should NOT be admitted by mask 5")
	}
	// mask 7 admits all
	for s := State(0); s <= StatePostBattle; s++ {
		if !isAdmitted(s, 7) {
			t.Fatalf("mask 7 should admit %v", s)
		}
	}
	// mask 0 admits none
	for s := State(0); s <= StatePostBattle; s++ {
		if isAdmitted(s, 0) {
			t.Fatalf("mask 0 should admit none, but admitted %v", s)
		}
	}
	// State 3 present and unreachable still respects mask: it is MaskOther.
	if admissionMaskForState(StateNetworkPreload) != MaskOther {
		t.Fatalf("state3 should be MaskOther")
	}
	if !isAdmitted(StateNetworkPreload, MaskOther) {
		t.Fatalf("state3 should be admitted by bit0")
	}
	if isAdmitted(StateNetworkPreload, MaskLoading) || isAdmitted(StateNetworkPreload, MaskBattle) {
		t.Fatalf("state3 should NOT be admitted by bit1 or bit2 alone")
	}
}

// The packet admission rule and the bare constructor below have no shipped
// caller: no packet path consumes the three-bit mask [08 "Admission masks"],
// and production builds sessions through the skirmish and mission
// constructors. They live here with the state-machine test that pins them.

// admissionMaskForState returns the single bit that admits the given state
// per [08 "Admission masks"] C4: bit0 admits states other than 5 and 6, bit1
// admits state 5, bit2 admits state 6.
func admissionMaskForState(s State) uint8 {
	switch s {
	case StateLoading:
		return MaskLoading
	case StateBattle:
		return MaskBattle
	default:
		return MaskOther
	}
}

// isAdmitted reports whether the given packet admission mask admits the
// session state [08 "Admission masks"] C4.
func isAdmitted(s State, mask uint8) bool {
	return mask&admissionMaskForState(s) != 0
}

// newBareSession creates a session in teardown state 0. Production composes a
// session through NewSkirmishWithProgress or NewMissionWithEntryOptions; only
// the state-machine tests need a bare one.
func newBareSession() *Session {
	s := &Session{State: StateTeardownA}
	s.ensurePublicationState()
	return s
}
