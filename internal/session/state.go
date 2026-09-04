package session

import "fmt"

// State is the eight-state index used by the session state machine
// [08 "Session states"].
type State uint8

// Eight states per [08 "Session states"].
// Semantic names for 0..4 are supported inference; behavior and transitions
// are established.
const (
	StateTeardownA      State = 0 // cleanup variant A, then state 2 [08 "Session states"]
	StateTeardownB      State = 1 // alternate cleanup, then state 2 [08 "Session states"]
	StateRouter         State = 2 // front-end/session router, selects 3|4|5 [08 "Session states"]
	StateNetworkPreload State = 3 // network startup polling, then state 5 [08 "Session states"]
	StateLocalPreload   State = 4 // initializes local two-player records, then state 5 [08 "Session states"]
	StateLoading        State = 5 // loading UI/thread and readiness barrier [08 "Session states"]
	StateBattle         State = 6 // live battle loop [08 "Session states"]
	StatePostBattle     State = 7 // post-battle handling [08 "Session states"]

	// Numeric aliases for direct indexing.
	State0 = StateTeardownA
	State1 = StateTeardownB
	State2 = StateRouter
	State3 = StateNetworkPreload
	State4 = StateLocalPreload
	State5 = StateLoading
	State6 = StateBattle
	State7 = StatePostBattle
)

// Gametype wire values used by save/load routing [08 "Session states"].
// Gametype 1 is campaign, Gametype 2 is skirmish/multiplayer.
const (
	GametypeCampaign    = 1
	GametypeMultiplayer = 2
)

// Admission mask bits per [08 "Admission masks"].
const (
	MaskOther   uint8 = 1 // bit0 admits states other than 5 and 6 [08 "Admission masks"]
	MaskLoading uint8 = 2 // bit1 admits state 5 (battle loading/setup) [08 "Admission masks"]
	MaskBattle  uint8 = 4 // bit2 admits state 6 (live battle) [08 "Admission masks"]
)

// transitionMatrix encodes the observed transition graph [08 "Session states"]:
//
//	0 -> 2
//	1 -> 2
//	2 -> 3 | 4 | 5
//	3 -> 5
//	4 -> 5
//	5 -> 6
//	6 -> 7 (normal battle completion)
//	6 -> 2 (abort/return path)
//	7 -> 2 (front-end return paths)
//
// Single-player takes 2->5 directly; state 3 (network pre-load) is present
// and unreachable in single-player [08 "Session states"] C1.
var transitionMatrix = [8][8]bool{
	0: {2: true},
	1: {2: true},
	2: {3: true, 4: true, 5: true},
	3: {5: true},
	4: {5: true},
	5: {6: true},
	6: {2: true, 7: true},
	7: {2: true},
}

// CanTransition reports whether from->to is an allowed edge in the
// eight-state graph [08 "Session states"] C1.
func CanTransition(from, to State) bool {
	if from > StatePostBattle || to > StatePostBattle {
		return false
	}
	return transitionMatrix[from][to]
}

// ValidState reports whether s is one of the eight defined states [08 "Session states"].
func ValidState(s State) bool { return s <= StatePostBattle }

// AdmissionMaskForState returns the single bit that admits the given state
// per [08 "Admission masks"] C4: bit0 admits states other than 5 and 6, bit1
// admits state 5, bit2 admits state 6.
func AdmissionMaskForState(s State) uint8 {
	switch s {
	case StateLoading:
		return MaskLoading
	case StateBattle:
		return MaskBattle
	default:
		return MaskOther
	}
}

// IsAdmitted reports whether the given packet admission mask admits the
// session state [08 "Admission masks"] C4.
func IsAdmitted(s State, mask uint8) bool {
	return mask&AdmissionMaskForState(s) != 0
}

// StateForGametype maps a save Gametype to the initial state that save/load
// rides unchanged [08 "Session states"] C3. Gametype-2 selects state 5
// directly; Gametype-1 selects state 4 first which configures two campaign
// players and then takes the same state-5 path.
func StateForGametype(gametype int) (State, bool) {
	switch gametype {
	case GametypeCampaign:
		return StateLocalPreload, true
	case GametypeMultiplayer:
		return StateLoading, true
	default:
		return 0, false
	}
}

// Session is defined in session.go (canonical full struct per PLAN_14 Public
// API). This file implements the eight-state machine [08 "Session states"] and
// transition helpers C1-C4; state-machine methods remain here while the
// complete field set is kept with the type definition.

// New creates a session in teardown state 0. Callers normally transition to
// StateRouter (2) before use.
func New() *Session {
	s := &Session{State: StateTeardownA}
	s.ensurePublicationState()
	return s
}

// CanTransitionTo reports whether s.State can transition to next state per C1.
func (s *Session) CanTransitionTo(next State) bool { return CanTransition(s.State, next) }

// TransitionTo attempts from s.State to next. It returns true and updates
// s.State on a valid edge, false otherwise. No subsequent state operation runs
// inline.
func (s *Session) TransitionTo(next State) bool {
	if !CanTransition(s.State, next) {
		return false
	}
	s.State = next
	// Clear deferred flag unless we are transitioning into battle via the
	// dedicated CompleteLoading path which sets it explicitly. Normal
	// TransitionTo(5->6) is still deferred by Advance's single-dispatch
	// guarantee rather than this flag.
	if next != StateBattle {
		s.pendingBattle = false
	}
	return true
}

// CompleteLoading installs StateBattle as the result of the state-5 loading
// thread. Its first run happens on the NEXT dispatch, not inline
// [08 "Session states"] C2. It is a no-op if the current state is not
// StateLoading.
func (s *Session) CompleteLoading() bool {
	if s.State != StateLoading {
		return false
	}
	s.State = StateBattle
	s.pendingBattle = true
	return true
}

// IsPendingBattle reports whether StateBattle has been installed by
// CompleteLoading but its first run has not yet been dispatched. Useful for
// the next-dispatch-first-run assertion in tests [08 "Session states"] C2.
func (s *Session) IsPendingBattle() bool { return s.pendingBattle && s.State == StateBattle }

// SelectForGametype selects the initial state for a save based on gametype
// per [08 "Session states"] C3.
func (s *Session) SelectForGametype(gametype int) error {
	st, ok := StateForGametype(gametype)
	if !ok {
		return fmt.Errorf("session: invalid gametype %d", gametype)
	}
	s.State = st
	s.pendingBattle = false
	return nil
}

// Advance performs one state-machine dispatch for the current state exactly
// once [08 "Session states"]. The concrete state operation is selected here;
// if it changes State, the new state's operation does not run until the next
// Advance call.
// Completion of the state-5 loading thread installs state 6 whose FIRST RUN
// happens on the NEXT dispatch, not inline [08 "Session states"] C2.
func (s *Session) Advance() {
	if s == nil || s.State > StatePostBattle {
		return
	}
	switch s.State {
	case StateTeardownA:
		handleTeardownA(s)
	case StateTeardownB:
		handleTeardownB(s)
	case StateRouter:
		handleRouter(s)
	case StateNetworkPreload:
		handleNetworkPreload(s)
	case StateLocalPreload:
		handleLocalPreload(s)
	case StateLoading:
		handleLoading(s)
	case StateBattle:
		// C2 defers the first state-6 operation until this dispatch after
		// loading completion. The battle operation itself is intentionally
		// idle; authoritative ticks are driven by Step.
		if s.pendingBattle {
			s.pendingBattle = false
		}
		handleBattle(s)
	case StatePostBattle:
		handlePostBattle(s)
	}
}

// String returns a human-readable name. The transitions and side effects of
// all eight states are Established, but the *semantic labels* for states 0–4
// are Supported inference [08 "Session states"], so these five names are the
// document's inferred ones, not retail's.
//
// TODO(question): whether the image carries any name for states 0–4 at all.
// Doc 08 keeps "UI-level names for states 0–4" as its own residual; the
// decider is a static trace of the session callback table for a per-state
// diagnostic or resource string. Nothing in the simulation reads this
// method — it is diagnostic text — so no behavior waits on the answer.
func (s State) String() string {
	switch s {
	case StateTeardownA:
		return "TeardownA(0)"
	case StateTeardownB:
		return "TeardownB(1)"
	case StateRouter:
		return "Router(2)"
	case StateNetworkPreload:
		return "NetworkPreload(3)"
	case StateLocalPreload:
		return "LocalPreload(4)"
	case StateLoading:
		return "Loading(5)"
	case StateBattle:
		return "Battle(6)"
	case StatePostBattle:
		return "PostBattle(7)"
	default:
		return fmt.Sprintf("State(%d)", uint8(s))
	}
}
