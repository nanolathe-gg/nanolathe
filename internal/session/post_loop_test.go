package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestStepSeparatesPhasesPublicationAndPostLoopTail(t *testing.T) {
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: frame.NewBuffer(),
	}
	s.EnablePhaseTrace()
	s.EnablePostLoopTrace()
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
		RetireMessageLine: func(lastTick uint32) {
			events = append(events, "ring")
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
	if got, want := s.PostLoopTrace(), []string{"barrier-1", "barrier-2", "barrier-3", "message-ring-retire", "eyeball-expire"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-loop trace=%v, want %v", got, want)
	}
	if got, want := events, []string{"barrier", "barrier", "barrier", "ring"}; !reflect.DeepEqual(got, want) {
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
	// The end-condition block rides the local slot's settlement deadline, so the
	// fixture needs a due player record for the block to reach the predicates
	// at all [08 R-TRIG-01 §6][05 R-ECO-01 §1]. Slot 0 is the local human, due
	// at tick 0; with one participating player the kind-2 victory sweep finds no
	// unskipped slot and answers true on that due.
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: frame.NewBuffer(),
		Units:    units.NewSliced(1, &content.Catalog{}),
		Mission:  &mission.Mission{Type: mission.TypeSkirmish},
		Skirmish: SkirmishConfig{NumPlayers: 1},
		Econ:     &economy.Service{},
	}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	// Countdown 0 is one step from the crossing, so the tick-5 due is terminal.
	s.Latch.Countdown = 0
	s.EnablePhaseTrace()
	s.EnablePostLoopTrace()

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
	one.EnablePostLoopTrace()
	five.EnablePostLoopTrace()
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

func TestZeroTickStepRetiresAtMostOneMessageWithoutPublication(t *testing.T) {
	s := &Session{
		State:    StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 5},
		Snapshot: frame.NewBuffer(),
	}
	s.EnablePhaseTrace()
	s.EnablePostLoopTrace()
	ring := frame.NewMessageRing()
	ring.Configure(4, 0)
	ring.Append("first", 1, 0, 10, 0)
	ring.Append("second", 1, 0, 10, 0)
	s.BindMessageRetirement(ring.RetireOne)
	s.Clock.GlobalTick = 31
	s.Step(5)
	if got := s.PhaseTrace(); len(got) != 0 {
		t.Fatalf("zero-tick phase trace=%v, want empty", got)
	}
	if s.Snapshot.Current() != nil {
		t.Fatal("zero-tick step published a frame")
	}
	if got := s.PostLoopTrace(); len(got) != 5 {
		t.Fatalf("zero-tick post-loop trace=%v, want one complete tail", got)
	}
	if got := ring.Visible(); len(got) != 1 || got[0].Text != "second" {
		t.Fatalf("first zero-tick pump lines=%#v, want only second", got)
	}
	s.Step(5)
	if got := ring.Visible(); len(got) != 0 {
		t.Fatalf("second zero-tick pump lines=%#v, want empty", got)
	}
}

func TestPostLoopTraceIsOptInAndBounded(t *testing.T) {
	makeSession := func() *Session {
		return &Session{State: StateBattle, Clock: &clock.State{Requested: 10, Active: 10}, Snapshot: frame.NewBuffer()}
	}
	off := makeSession()
	for now := int32(1); now <= 6000; now++ {
		off.Step(now)
	}
	if got := off.PostLoopTrace(); got != nil {
		t.Fatalf("trace without opt-in = %v, want nil", got)
	}
	if capacity := cap(postLoopStateFor(off).trace); capacity != 0 {
		t.Fatalf("trace without opt-in retained capacity %d, want zero", capacity)
	}
	zero := makeSession()
	zero.SetPostLoopTraceLimit(0)
	zero.EnablePostLoopTrace()
	zero.Step(5)
	if len(zero.PostLoopTrace()) != 0 || zero.PostLoopTraceDropped() != 5 {
		t.Fatal("explicit zero trace capacity was replaced by the default")
	}
	plain, on := makeSession(), makeSession()
	plain.Step(5)
	plain.Step(10)
	on.SetPostLoopTraceLimit(7)
	on.EnablePostLoopTrace()
	on.Step(5)
	on.Step(10)
	if got, want := on.PostLoopTrace(), []string{"message-ring-retire", "eyeball-expire", "barrier-1", "barrier-2", "barrier-3", "message-ring-retire", "eyeball-expire"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded trace=%v, want surviving tail order %v", got, want)
	}
	if got := on.PostLoopTraceDropped(); got != 3 {
		t.Fatalf("trace drops=%d, want 3", got)
	}
	if plain.Clock.GlobalTick != on.Clock.GlobalTick || plain.SimRNG().State != on.SimRNG().State || plain.SimRNG().Draws() != on.SimRNG().Draws() || plain.CrtRNG().State != on.CrtRNG().State || plain.CrtRNG().Draws() != on.CrtRNG().Draws() {
		t.Fatal("post-loop tracing changed authoritative clock or RNG state")
	}
}
