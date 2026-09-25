package main

import "testing"

// The asynchronous host names only ticks it has joined, holds the tick before
// the last release through a pause, and presents a paused-input republication
// at once (battle_sim.go, DESIGN_GPU_RENDERER §13.13).
func TestPresentationTickNamesOnlyJoinedTicks(t *testing.T) {
	b := &battleSession{sim: &battleSim{}, tickFiredValid: true}
	if _, named := b.presentationTick(); named {
		t.Fatal("named a tick before the first join")
	}
	for _, c := range []struct {
		name               string
		observed, released uint32
		republished        bool
		want               uint32
	}{
		{"1x: the batch computing 11 runs", 10, 11, false, 10},
		{"2x: the batch computing 11 and 12 runs", 10, 12, false, 10},
		{"paused after joining 11", 11, 11, false, 10},
		{"paused-input republication of 11", 11, 11, true, 11},
	} {
		b.sim.observedTick, b.sim.observedValid, b.sim.republished = c.observed, true, c.republished
		b.tickFiredTick = c.released
		if got, named := b.presentationTick(); !named || got != c.want {
			t.Errorf("%s: presentationTick = %d, %v; want %d", c.name, got, named, c.want)
		}
	}
}
