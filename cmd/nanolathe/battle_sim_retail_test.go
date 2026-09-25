//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The asynchronous simulation moves where the sub-ticks run, not what they
// compute: the same seeded skirmish, stepped through the window's host step
// with the same input, reaches the same authoritative state whether each
// pump's sub-ticks run inline or on the simulation goroutine
// (DESIGN_GPU_RENDERER §13.13). A human order mid-run covers the command
// boundary the two paths share.
//
// Each step also does what the window's Draw does, on the test goroutine: pin,
// drain, resolve the blend, digest and record a modern frame — beside the
// running batch in the asynchronous run. Under -race this is the check that
// recording reads nothing the simulation goroutine writes: the message ring,
// the effect-bank cache the commander's self-destruct reaches from the
// session's effect-timing resolver, and the pinned publications.
func TestAsynchronousSimulationMatchesSynchronous(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	type outcome struct {
		fingerprint string
		tick        uint32
		sim, crt    uint32
	}
	run := func(async bool) outcome {
		opts := Options{Roots: []string{retail}, Map: "ashap plateau", Seed: 7}
		cs, err := openContent(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()
		request, _, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
		if err != nil {
			t.Fatal(err)
		}
		authoritative, err := composeAuthoritativeBattle(request)
		if err != nil {
			t.Fatal(err)
		}
		sess := authoritative.Session
		var b *battleSession
		var cl *client.Client
		cl, err = client.New(client.Options{
			Buffer: sess.Snapshot, Width: 640, Height: 480,
			Step:             func(delta float64) { b.viewerStep(delta, cl) },
			PresentationTick: func() (uint32, bool) { return b.presentationTick() },
			JoinSimulation:   func() { b.stopSimulation(cl) },
		})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetModelFS(cs.unmappedMount)
		b, err = composeBattleEntryWithDetail(sess, sess.Catalog, cs, cl, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer b.teardown(cl)
		b.setSurfaceSize(640, 480)
		millis := &shotMillisSource{}
		b.millisSource = millis
		cl.SetAsyncSimulation(async)
		cl.SetEnhanced(true)
		cl.SetInterpolation(true)
		// gameShell.step's order: join, follow the presentation's mode, the
		// battle's host step, then launch what it released; then the Draw.
		hostStep := func() {
			b.joinSimulation(cl)
			b.syncSimulationMode(cl)
			millis.step++
			b.viewerStep(1.0/30, cl)
			b.launchSimulation(cl)
			cl.PinPresentation()
			cl.BeginPresentationFrame()
			_ = cl.ResolveTickFraction()
			_ = cl.PresentationDigest()
			_ = cl.RecordModernFrame()
		}
		for range 90 {
			hostStep()
		}
		b.joinSimulation(cl)
		cur, ok := b.currentSnapshot()
		if !ok {
			t.Fatal("no committed frame")
		}
		var commander *pool.Handle
		for _, u := range cur.Units {
			if u.Owner == cur.ViewingPlayer {
				h := u.Slot
				commander = &h
				if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
					Handles: []pool.Handle{h}, Code: 1,
					Position: orders.ResolvePos{X: u.X - numeric.Fixed(160<<16), Y: u.Y, Z: u.Z + numeric.Fixed(96<<16)},
				}}); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if commander == nil {
			t.Fatal("no local unit to order")
		}
		for range 60 {
			hostStep()
		}
		// The self-destruct admits named explosion art inside a sub-tick.
		b.joinSimulation(cl)
		if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelfDestruct,
			SelfDestruct: session.HumanSelfDestructCommand{Handles: []pool.Handle{*commander}}}); err != nil {
			t.Fatal(err)
		}
		for range 300 {
			hostStep()
		}
		b.stopSimulation(cl)
		fingerprint, err := sess.PartialStateFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		return outcome{fingerprint: fingerprint, tick: sess.Clock.GlobalTick, sim: sess.SimRNG().State, crt: sess.CrtRNG().State}
	}
	sync, async := run(false), run(true)
	if sync.tick < 400 {
		t.Fatalf("synchronous run reached tick %d; the battle did not advance", sync.tick)
	}
	if sync != async {
		t.Fatalf("asynchronous simulation diverged:\n sync  %+v\n async %+v", sync, async)
	}
}
