package ebitenapp

import (
	"testing"
	"time"
)

func TestHostCadenceIndependentOfRefresh(t *testing.T) {
	for _, hz := range []int{20, 30, 60, 120, 144, 240} {
		var clock hostClock
		start := time.Unix(1, 0)
		bodies := 0
		for frame := 0; frame <= hz*10; frame++ {
			bodies += clock.advance(start.Add(time.Duration(frame) * time.Second / time.Duration(hz)))
		}
		if bodies != 301 { // One initialization, then ten seconds at 30 Hz.
			t.Fatalf("%d Hz produced %d host steps, want 301", hz, bodies)
		}
	}
}

func TestHostCadenceStallAndRecovery(t *testing.T) {
	var clock hostClock
	start := time.Unix(1, 0)
	clock.advance(start)
	if got := clock.advance(start.Add(time.Hour)); got != 5 {
		t.Fatalf("resume ran %d steps, want bounded catch-up of 5", got)
	}
	if got := clock.advance(start.Add(time.Hour)); got != 0 {
		t.Fatalf("discarded stall debt leaked into next frame: %d steps", got)
	}
	if got := clock.advance(start.Add(time.Hour + time.Second/30)); got != 1 {
		t.Fatalf("normal cadence after resume: got %d steps", got)
	}
}

func TestHostCadenceAndDeferredBodiesStayBalanced(t *testing.T) {
	var clock hostClock
	var ledger updateLedger
	start := time.Unix(1, 0)
	owed, bodies, issued := 0, 0, 0
	for frame := range 1201 {
		for range clock.advance(start.Add(time.Duration(frame) * time.Second / 120)) {
			issued++
			owed++
			for range ledger.call(true) {
				owed--
				bodies++
			}
		}
		// Exercise skipped presents: tails run at 30 Hz, with a one-second gap.
		if frame%4 == 0 && (frame < 400 || frame > 520) && ledger.tail() {
			owed--
			bodies++
		}
		if owed < 0 || owed > 1 {
			t.Fatalf("frame %d: %d inputs outstanding", frame, owed)
		}
	}
	if bodies+owed != issued || issued != 301 {
		t.Fatalf("issued %d, ran %d, pending %d", issued, bodies, owed)
	}
}
