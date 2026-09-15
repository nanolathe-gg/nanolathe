package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Authored opening policy (GPU §36): input and the authoritative clock cannot
// advance during the opening, and its elapsed time cannot become tick debt.
func TestArrivalHoldsGameplayAndRebasesHandoff(t *testing.T) {
	cl := &client.Client{}
	cl.SetFocused(true)
	cl.StartArrival(frame.UnitView{Slot: 1})
	state := &clock.State{GlobalTick: 7, ScaledAnchor: 10, Requested: 10, Active: 10}
	b := &battleSession{sess: &session.Session{Clock: state}, millisSource: &scriptedMillisSource{samples: []uint32{10000}}}
	before := *state
	for i := 0; i < 120; i++ {
		b.viewerStep(1.0/60, cl)
	}
	if cl.ArrivalSeconds() != 0 || *state != before {
		t.Fatal("window startup consumed the opening")
	}
	cl.MarkArrivalPresented()
	for i := 0; i < 30; i++ {
		b.viewerStep(1.0/60, cl)
	}
	if *state != before {
		t.Fatal("intro advanced authoritative clock")
	}
	if !cl.ArrivalActive() {
		t.Fatal("intro ended before impact")
	}
	for i := 0; i < 200 && cl.ArrivalActive(); i++ {
		b.viewerStep(1.0/60, cl)
	}
	if cl.ArrivalActive() || state.GlobalTick != before.GlobalTick {
		t.Fatal("handoff advanced simulation or never completed")
	}
	if state.ScaledAnchor != 300 {
		t.Fatalf("handoff anchor = %d", state.ScaledAnchor)
	}
	if ticks := state.AdvanceSP(301); ticks != 1 {
		t.Fatalf("first gameplay budget = %d; intro produced catch-up debt", ticks)
	}
}
