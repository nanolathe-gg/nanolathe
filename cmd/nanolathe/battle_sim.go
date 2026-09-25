package main

import (
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	committedframe "github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The asynchronous simulation (docs/DESIGN_GPU_RENDERER.md §13.13).
//
// The modern window runs the session's sub-ticks on their own goroutine so a
// tick no longer shares a presented frame's budget with recording, Execute and
// Ebitengine's present. The host keeps every decision on the game goroutine,
// in the order the synchronous step makes them:
//
//  1. At the start of a host step it joins the batch the previous step
//     launched and applies what the batch left for the client — its captions,
//     its message-ring retire, its feature admissions — then feeds each
//     publication the batch made, oldest first, to the observers that ran
//     inside the batch before (camera follow and shake, the Enhanced history
//     layers), runs the follow hotkeys that wait for the batch, and drains
//     audio — all with the session quiescent.
//  2. Input is handled and commands are queued exactly as before, and the
//     session's PrepareStep releases this pump's sub-ticks.
//  3. When the host step returns, ExecuteStep runs those sub-ticks on the
//     simulation goroutine until the next host step joins it: about 33 ms at
//     1x.
//
// Commands therefore reach the same sub-ticks they would synchronously, and
// the authoritative sequence is unchanged; only where the sub-ticks run moves.
// Presentation shows only ticks the host has joined — at 1x the tick the
// simulation finished while it computes the next, one tick behind the
// synchronous view — so everything the host applies at the join is in place
// before a publication is drawn, and a pre-recorded pass and the Draw that
// consumes it name the same pair.

// simRun is one launched batch.
type simRun struct {
	sess *session.Session
	plan session.StepPlan
}

// battleSim is the simulation goroutine and the host's bookkeeping for it. All
// fields are owned by the game goroutine; the goroutine itself only receives
// runs and reports their duration.
type battleSim struct {
	runs chan simRun
	done chan time.Duration
	// running says a batch was launched and not yet joined; pending is the
	// plan this host step prepared, launched when the step returns.
	running bool
	pending *simRun
	// observed is the publication number the host last fed to its observers,
	// and observedTick that publication's tick; republished says it repeated
	// the tick before it, which only the paused-input boundary does.
	observed                   uint64
	observedTick               uint32
	observedValid, republished bool
	// followAfterBatch holds follow hotkeys that retail handles after the
	// sub-tick batch; they run at the join that completes it.
	followAfterBatch func()
	// lastRun is the most recent batch's wall time on its goroutine.
	lastRun time.Duration
	// admissions are feature definitions a batch admitted, applied to the
	// model texture registry at the join, when no recording pass reads it.
	admissions []*content.FeatureDef
	// retireTick is the last tick of a batch's executor tail. The tail's
	// message-ring retire acts on the client's ring, which recording passes
	// read, so the batch only notes it and the join applies it.
	retireTick    uint32
	retirePending bool
}

func newBattleSim() *battleSim {
	r := &battleSim{runs: make(chan simRun), done: make(chan time.Duration)}
	go r.serve()
	return r
}

// noteRetire stands in for the message-ring retire in the executor tail while
// the simulation goroutine runs the pumps [01 R-PLAT-02 §8].
func (r *battleSim) noteRetire(lastTick uint32) bool {
	r.retireTick, r.retirePending = lastTick, true
	return false
}

func (r *battleSim) serve() {
	ebitenapp.RaiseCurrentThread()
	for run := range r.runs {
		started := time.Now()
		run.sess.ExecuteStep(run.plan)
		r.done <- time.Since(started)
	}
}

// syncSimulationMode starts or stops the simulation goroutine to match the
// client's presentation: only the modern window path sets AsyncSimulation.
// It runs at the start of a host step, after any running batch was joined.
func (b *battleSession) syncSimulationMode(cl *client.Client) {
	if b == nil || b.sess == nil {
		return
	}
	want := cl != nil && cl.AsyncSimulation() && b.sess.Snapshot != nil
	switch {
	case want && b.sim == nil:
		r := newBattleSim()
		r.observed = b.sess.Snapshot.PublicationSeq()
		if cur := b.sess.Snapshot.Current(); cur != nil {
			r.observedTick, r.observedValid = cur.Tick, true
		}
		b.sim = r
		b.sess.BindMessageRetirement(r.noteRetire)
		// The pre-existing release stamp was taken after a synchronous step;
		// the next prepared pump restamps it at release.
	case !want && b.sim != nil:
		b.stopSimulation(cl)
	}
}

// joinSimulation waits for the batch the previous host step launched and
// applies, on the game goroutine, everything the synchronous step applied
// inside or right after that batch (see the file comment).
func (b *battleSession) joinSimulation(cl *client.Client) {
	if b == nil || b.sim == nil {
		return
	}
	r := b.sim
	if r.running {
		r.lastRun = <-r.done
		r.running = false
		if cl != nil {
			cl.NoteSimulationTime(r.lastRun)
		}
	}
	// In the synchronous order: captions a sub-tick's audio queue resolved,
	// then the tail's retire [01 R-PLAT-02 §8].
	if cl != nil {
		cl.DeferCaptions(false)
	}
	if r.retirePending {
		r.retirePending = false
		if cl != nil {
			cl.MessageRing().RetireOne(r.retireTick)
		}
	}
	for i, def := range r.admissions {
		b.modelTextures.AdmitFeatureDefinition(def)
		r.admissions[i] = nil
	}
	r.admissions = r.admissions[:0]
	if b.sess != nil && b.sess.Snapshot != nil {
		r.observed = b.sess.Snapshot.PublicationsSince(r.observed, func(f *committedframe.Frame) {
			r.republished = r.observedValid && f.Tick == r.observedTick
			r.observedTick, r.observedValid = f.Tick, true
			b.applyPublishedCamera(f)
			if cl != nil {
				cl.ObserveCommittedFrame(f)
			}
		})
	}
	if follow := r.followAfterBatch; follow != nil {
		r.followAfterBatch = nil
		follow()
	}
	if cl != nil {
		cl.TickPresentationAudio()
	}
}

// launchSimulation starts the batch this host step prepared. Until the join,
// captions the batch's audio queue resolves wait in the client.
func (b *battleSession) launchSimulation(cl *client.Client) {
	if b == nil || b.sim == nil || b.sim.pending == nil || b.sim.running {
		return
	}
	run := *b.sim.pending
	b.sim.pending = nil
	if cl != nil {
		cl.DeferCaptions(true)
	}
	b.sim.running = true
	b.sim.runs <- run
}

// stopSimulation joins any running batch as joinSimulation does and ends the
// goroutine. The session is then quiescent and back on the synchronous path.
func (b *battleSession) stopSimulation(cl *client.Client) {
	if b == nil || b.sim == nil {
		return
	}
	b.joinSimulation(cl)
	close(b.sim.runs)
	b.sim = nil
	if cl != nil {
		bindBattleMessageRetirement(b.sess, cl)
	} else if b.sess != nil {
		b.sess.BindMessageRetirement(nil)
	}
}

// prepareSimulationStep is the asynchronous half of the controller's step: it
// releases this pump's sub-ticks with PrepareStep, stamps the release for the
// presentation clock, and leaves ExecuteStep to the simulation goroutine.
func (b *battleSession) prepareSimulationStep(scaled int32) {
	plan := b.sess.PrepareStep(scaled)
	if plan.Ticks() > 0 {
		if b.millisSource == nil {
			b.millisSource = newMonotonicMillisSource()
		}
		// Stamped at release rather than when the batch finishes: presentation
		// paces the blend by when ticks became due, not by how long the batch
		// took on its goroutine.
		b.tickFiredAt = b.millisSource.Millis32()
		b.tickFiredCarry = b.sess.Clock.Carry
		b.tickFiredTick = b.sess.Clock.GlobalTick + uint32(plan.Ticks())
		b.tickFiredValid = true
	}
	b.simPaused = b.sess.Clock.Paused
	b.simActive = b.sess.Clock.Active
	if !plan.Runs() {
		return
	}
	if b.sess.HasPendingHumanCommand(session.HumanGameplay) {
		// A gameplay switch reassigns rule state the HUD reads while it draws
		// (construction facings, the community switches), so the pump that
		// applies it runs here, before any recording pass; the join still
		// observes its publications.
		b.sess.ExecuteStep(plan)
		return
	}
	b.sim.pending = &simRun{sess: b.sess, plan: plan}
}

// presentationTick names the committed tick the modern window presents under
// the asynchronous simulation: the one before the last released tick, but
// never one the host has not joined yet. At 1x the two agree; when a pump
// releases several ticks (2x, or catch-up after a stall) presentation stays on
// the newest joined tick until the next join. Holding the tick before the last
// release also keeps the name steady through pumps that release nothing (slow
// speeds) and through a pause, so neither moves the blended pose.
//
// A command applied at the paused-input boundary republishes the committed
// tick; that republication is presented at once, and unblended, because the
// buffer pairs no previous frame with a publication that repeats its tick.
func (b *battleSession) presentationTick() (uint32, bool) {
	if b == nil || b.sim == nil || !b.sim.observedValid {
		return 0, false
	}
	r := b.sim
	tick := r.observedTick
	if r.republished {
		return tick, true
	}
	if b.tickFiredValid && b.tickFiredTick > 0 && b.tickFiredTick-1 < tick {
		tick = b.tickFiredTick - 1
	}
	return tick, true
}
