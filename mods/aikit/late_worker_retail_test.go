//go:build retail

package aikit_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// lateProbe makes a Modern controller's worker late on purpose. Bound as the
// host's Probe, its StepBegin runs on the simulation thread at the start of
// each of the host's steps and its ThinkBegin on the worker goroutine at the
// start of every asynchronous think. Two thinks in three are held in
// ThinkBegin until the simulation thread has begun the step at which their
// batch is due, and then for a further 1 to 4 ms, so the worker is still
// thinking when the simulation thread reaches the join, whatever the host's
// speed. Part counts the joins that found the worker still busy, which are
// the simulation thread's waits (aikit.PartProbe). The probe reads no game
// state and changes none: it is host instrumentation [I6].
type lateProbe struct {
	reaction uint32 // the persona's reaction window, in ticks
	end      uint32 // the run's last tick: a think due after it is released there
	reached  atomic.Uint32
	thinks   atomic.Uint32
	held     atomic.Uint32
	waits    atomic.Uint32
}

// lateHoldLimit releases a held think whose host stopped stepping, so a
// controller whose player left the battle cannot hang the test. The scene's
// Modern players never leave.
const lateHoldLimit = 30 * time.Second

func (p *lateProbe) StepBegin(_ uint8, tick uint32) { p.reached.Store(tick) }

func (p *lateProbe) ThinkBegin(_ uint8, tick uint32) {
	n := p.thinks.Add(1)
	if n%3 == 0 {
		return
	}
	p.held.Add(1)
	due := min(tick+p.reaction, p.end)
	for limit := time.Now().Add(lateHoldLimit); p.reached.Load() < due && time.Now().Before(limit); {
		time.Sleep(50 * time.Microsecond)
	}
	time.Sleep(time.Duration(1+n%4) * time.Millisecond)
}

func (p *lateProbe) ThinkEnd(uint8, uint32)  {}
func (p *lateProbe) StepEnd(uint8, uint32)   {}
func (p *lateProbe) PrepBegin(uint8, uint32) {}
func (p *lateProbe) PrepEnd(uint8, uint32)   {}

func (p *lateProbe) Part(_ uint8, _ uint32, part aikit.StepPart) {
	if part == aikit.PartJoin {
		p.waits.Add(1)
	}
}

// A due Modern AI batch whose worker has not finished makes the simulation
// thread wait at the join rather than go on without it, so a late worker
// changes nothing (DESIGN_GAMEPLAY_RULES "The Modern AI controller",
// "Background thinking"; DESIGN_MULTIPLAYER §5.1 "how long the Modern AI
// thinks"). Two clients of the six-minute online composition run in
// lockstep; on the second, every Modern controller's worker is held at the
// start of two thinks in three until their batch is due. The clients agree
// at every 30-tick sample and at the end, receipts, fingerprint, controller
// outcomes and generator positions included, and the second client's
// simulation thread demonstrably waited for its late workers.
func TestLateModernAIWorkerChangesNothingRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	setup := onlineModernSetup(cat, 0, true)
	inputs, config := onlineModernRoom(t, setup)
	joinerInputs, joinerConfig := onlineModernJoiner(t, config)
	prompt := onlineModernReplica(t, inputs, config, 0)
	late := onlineModernReplica(t, joinerInputs, joinerConfig, 1)
	// Entry has begun each controller and may have its preparation in
	// flight. Both clients wait for it before the first granted tick, so
	// the probe is bound while no worker runs; the wait changes no game
	// (aikit.Host.Generator).
	for _, h := range modernHosts(t, prompt, setup) {
		h.Join()
	}
	probes := map[uint8]*lateProbe{}
	for row, h := range modernHosts(t, late, setup) {
		h.Join()
		if !h.Persona().Async || h.Persona().Reaction == 0 {
			t.Fatalf("row %d's persona %+v does not think on its worker", row, h.Persona())
		}
		p := &lateProbe{reaction: h.Persona().Reaction, end: onlineModernSpan}
		h.Probe = p
		probes[row] = p
	}
	begin := time.Now()
	out := runOnlineLockstep(t, []*session.Session{prompt, late}, setup, onlineModernSpan)
	elapsed := time.Since(begin)
	if out[0].tick != onlineModernSpan {
		t.Fatalf("the battle ended at tick %d, before the span", out[0].tick)
	}
	if out[0].digest != out[1].digest || out[0].fingerprint != out[1].fingerprint {
		t.Fatalf("the late client agreed at every sample but ended apart: digests %s and %s", out[0].digest, out[1].digest)
	}
	for row, st := range out[0].stats {
		if out[1].stats[row] != st || out[1].generators[row] != out[0].generators[row] {
			t.Fatalf("row %d's controller applied %+v (generator %d) on time and %+v (generator %d) late",
				row, st, out[0].generators[row], out[1].stats[row], out[1].generators[row])
		}
	}
	for row := uint8(0); row < 10; row++ {
		p, ok := probes[row]
		if !ok {
			continue
		}
		thinks, held, waits := p.thinks.Load(), p.held.Load(), p.waits.Load()
		t.Logf("row %d: %d thinks, %d held past their deadline, %d joins waited", row, thinks, held, waits)
		if held == 0 || waits == 0 {
			t.Fatalf("row %d's simulation thread never waited for its late worker: the test did not make it late", row)
		}
	}
	t.Logf("%d ticks of a prompt and a late client in %v; digest %s", out[0].tick, elapsed, out[0].digest)
}
