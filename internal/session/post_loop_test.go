package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestStepSeparatesPhasesPublicationAndPostLoopTail(t *testing.T) {
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: frame.NewBuffer(),
	}
	s.EnablePhaseTrace()
	var events []string
	var callbackTicks []uint32
	s.SetPostLoopHooks(PostLoopHooks{
		Barrier: func(index int, lastTick uint32) {
			events = append(events, "barrier")
			callbackTicks = append(callbackTicks, lastTick)
			if index != len(events) {
				t.Fatalf("barrier index=%d after %d callbacks", index, len(events))
			}
		},
		SlideDeadlineRing: func(lastTick uint32) {
			events = append(events, "ring")
			callbackTicks = append(callbackTicks, lastTick)
		},
		CompactPending: func(lastTick uint32) {
			events = append(events, "pending")
			callbackTicks = append(callbackTicks, lastTick)
		},
	})

	s.Step(5)
	if got := len(s.PhaseTrace()); got != 12*5 {
		t.Fatalf("phase trace length=%d, want 60; outer tail/publication must stay outside the registry", got)
	}
	if got := s.PublicationCount(); got != 5 {
		t.Fatalf("publication count=%d, want 5 completed-subtick boundaries", got)
	}
	if got, want := s.PostLoopTrace(), []string{"barrier-1", "barrier-2", "barrier-3", "deadline-ring-slide", "pending-compact"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-loop trace=%v, want %v", got, want)
	}
	if got, want := events, []string{"barrier", "barrier", "barrier", "ring", "pending"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-loop callbacks=%v, want %v", got, want)
	}
	for i, got := range callbackTicks {
		if got != 5 {
			t.Fatalf("callback %d received lastTick=%d, want 5", i, got)
		}
	}
	if tick, ok := s.Snapshot.PublishedTick(); !ok || tick != 5 {
		t.Fatalf("published tick=%d (%v), want 5", tick, ok)
	}
}

func TestStepPublishesCompletedSubTickBeforeCatchUpExit(t *testing.T) {
	s := &Session{
		State:               StateBattle,
		Clock:               &clock.State{Requested: 10, Active: 10},
		Snapshot:            frame.NewBuffer(),
		Units:               units.NewSliced(1, &content.Catalog{}),
		Mission:             &mission.Mission{Type: mission.TypeSkirmish},
		Skirmish:            SkirmishConfig{NumPlayers: 1},
		resultPending:       true,
		resultPendingDraw:   true,
		resultPendingWinner: -1,
		resultNextDue:       0,
		resultPendingReason: "test",
	}
	s.Latch.Countdown = 0
	s.EnablePhaseTrace()

	s.Step(5)
	if s.State == StateBattle {
		t.Fatal("catch-up batch did not observe the terminal result")
	}
	if got := s.PublicationCount(); got != 1 {
		t.Fatalf("publication count=%d, want one completed sub-tick before exit", got)
	}
	if got := len(s.PhaseTrace()); got != 12 {
		t.Fatalf("phase trace length=%d, want one completed sub-tick", got)
	}
	if got := len(s.PostLoopTrace()); got != 5 {
		t.Fatalf("post-loop trace length=%d, want one tail after early exit", got)
	}
}

func TestFiveSubTicksMatchOneCatchUpPumpExceptTailCadence(t *testing.T) {
	makeSession := func() (*Session, *int) {
		s := visibilityFixture(t, false)
		s.State = StateBattle
		s.Clock = &clock.State{Requested: 10, Active: 10}
		tailCalls := 0
		s.SetPostLoopHooks(PostLoopHooks{Barrier: func(index int, _ uint32) {
			if index == 1 {
				tailCalls++
			}
		}})
		return s, &tailCalls
	}
	one, oneTailCalls := makeSession()
	five, fiveTailCalls := makeSession()
	one.EnablePhaseTrace()
	five.EnablePhaseTrace()
	one.Step(5)
	for now := int32(1); now <= 5; now++ {
		five.Step(now)
	}

	if one.Clock.GlobalTick != five.Clock.GlobalTick {
		t.Fatalf("global ticks differ: one-pump=%d five-pumps=%d", one.Clock.GlobalTick, five.Clock.GlobalTick)
	}
	if one.SimRNG().State != five.SimRNG().State || one.SimRNG().Draws() != five.SimRNG().Draws() || one.CrtRNG().State != five.CrtRNG().State || one.CrtRNG().Draws() != five.CrtRNG().Draws() {
		t.Fatalf("RNG state/draws differ after equivalent authoritative ticks")
	}
	if !reflect.DeepEqual(one.PhaseTrace(), five.PhaseTrace()) {
		t.Fatalf("authoritative phase traces differ between 1x5 and 5x1")
	}
	if !reflect.DeepEqual(one.Snapshot.Current(), five.Snapshot.Current()) {
		t.Fatalf("published result differs between 1x5 and 5x1")
	}
	if !reflect.DeepEqual(one.strips, five.strips) {
		t.Fatalf("authoritative strip state differs between 1x5 and 5x1")
	}
	if *oneTailCalls != 1 || *fiveTailCalls != 5 {
		t.Fatalf("post-loop tail calls=%d/%d, want 1/5", *oneTailCalls, *fiveTailCalls)
	}
	if len(one.PostLoopTrace()) != 5 || len(five.PostLoopTrace()) != 25 {
		t.Fatalf("post-loop event counts=%d/%d, want 5/25", len(one.PostLoopTrace()), len(five.PostLoopTrace()))
	}
}

func TestZeroTickStepDoesNotMutateSimulationOrTail(t *testing.T) {
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 5},
		Snapshot: frame.NewBuffer(),
	}
	s.EnablePhaseTrace()
	s.Step(5)
	if got := s.PhaseTrace(); len(got) != 0 {
		t.Fatalf("zero-tick phase trace=%v, want empty", got)
	}
	if s.Snapshot.Current() != nil {
		t.Fatal("zero-tick step published a frame")
	}
	if got := s.PostLoopTrace(); len(got) != 0 {
		t.Fatalf("zero-tick post-loop trace=%v, want empty", got)
	}
}

func TestPostLoopDeadlineRingSlidesDueHeadsWithWrap(t *testing.T) {
	state := &postLoopState{}
	state.ring.count = 3
	state.ring.head = deadlineRingSize - 1
	state.ring.effectiveDeadline[29] = 4
	state.ring.effectiveDeadline[0] = 5
	state.ring.effectiveDeadline[1] = 10
	state.ring.slide(10)
	if state.ring.head != 1 || state.ring.count != 1 {
		t.Fatalf("ring head/count=%d/%d, want 1/1 after due-head slides", state.ring.head, state.ring.count)
	}
	if state.ring.effectiveDeadline[1] != 10 {
		t.Fatalf("ring removed the next live entry: deadlines=%v", state.ring.effectiveDeadline[:3])
	}
	state.ring.slide(10)
	if state.ring.head != 1 || state.ring.count != 1 {
		t.Fatalf("ring advanced an equal deadline: head/count=%d/%d, want 1/1", state.ring.head, state.ring.count)
	}
}

func TestPostLoopPendingCompactionIsStable(t *testing.T) {
	var expired []uint32
	state := &postLoopState{}
	state.pending.entries = []pendingEntry{
		{deadline: 3, expire: func() { expired = append(expired, 3) }},
		{deadline: 12},
		{deadline: 1, expire: func() { expired = append(expired, 1) }},
		{deadline: 10, expire: func() { expired = append(expired, 10) }},
	}
	state.pending.compact(10)
	if !reflect.DeepEqual(expired, []uint32{3, 1}) {
		t.Fatalf("expired callbacks=%v, want [3 1]", expired)
	}
	if len(state.pending.entries) != 2 || state.pending.entries[0].deadline != 12 || state.pending.entries[1].deadline != 10 {
		t.Fatalf("pending survivors=%+v, want stable deadlines 12,10", state.pending.entries)
	}
	state.pending.compact(11)
	if !reflect.DeepEqual(expired, []uint32{3, 1, 10}) {
		t.Fatalf("expired callbacks=%v, want [3 1 10] after strict boundary", expired)
	}
	if len(state.pending.entries) != 1 || state.pending.entries[0].deadline != 12 {
		t.Fatalf("pending survivors=%+v, want stable deadline 12", state.pending.entries)
	}
}
