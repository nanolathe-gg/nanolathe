package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

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

// The result latch stops further sub-ticks. The final joined publication must
// therefore replace the usual one-tick presentation delay or the end title and
// ENDMSN keep seeing the previous, nonterminal battle frame forever
// [07 §11][08 R-CAMP-01 §6][I6].
func TestAsynchronousPresentationReachesTerminalPublication(t *testing.T) {
	for _, outcome := range []string{"victory", "defeat"} {
		t.Run(outcome, func(t *testing.T) {
			buf := frame.NewBuffer()
			buf.BeginWrite()
			if err := buf.Publish(10); err != nil {
				t.Fatal(err)
			}
			b := &battleSession{sess: &session.Session{Snapshot: buf}, tickFiredValid: true, tickFiredTick: 11}
			var cl *client.Client
			cl, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480,
				PresentationTick: b.presentationTick,
				JoinSimulation:   func() { b.stopSimulation(cl) },
			})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetAsyncSimulation(true)
			defer cl.SetAsyncSimulation(false)
			b.syncSimulationMode(cl)
			cl.PinPresentation()
			if cur := cl.PresentedFrame(); cur == nil || cur.Tick != 10 {
				t.Fatal("the running batch did not hold the last joined battle frame")
			}

			buf.BeginWrite().Result = frame.ResultView{Ended: true, Kind: outcome}
			if err := buf.Publish(11); err != nil {
				t.Fatal(err)
			}
			cl.PinPresentation()
			if cur := cl.PresentedFrame(); cur == nil || cur.Tick != 10 || cur.Result.Ended {
				t.Fatal("presentation consumed the terminal frame before the host joined it")
			}
			for range 3 {
				b.joinSimulation(cl)
				cl.PinPresentation()
				cur := cl.PresentedFrame()
				if cur == nil {
					t.Fatal("presentation lost the final publication")
				}
				if cur.Tick != 11 || !cur.Result.Ended || cur.Result.Kind != outcome {
					t.Fatalf("joined terminal tick 11 but presented tick %d, ended=%v, kind=%q", cur.Tick, cur.Result.Ended, cur.Result.Kind)
				}
			}
		})
	}
}
