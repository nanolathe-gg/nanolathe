package session

import (
	"bytes"
	"fmt"
	"testing"
)

// PROC-05 debug trace — a debug-only ordered event stream over the
// authoritative sub-tick [PLAN_03 "Authoritative composition", PROC-05 row].
// Per sub-tick it records: the global tick; for each phase 1..12 of [01 §4.4]
// the phase number and registry label plus the simulation-stream and
// CRT-stream state and cumulative draw counts immediately before and after
// the phase body [01 §7.1][01 §7.2][I4]; and the publication boundary that
// commits the frame after phase 12 [01 §4.4][I6].
//
// The tracer is TEST-ONLY instrumentation. Production code holds no reference
// to it, so an ordinary (untraced) run allocates nothing extra and cannot
// change behavior — there is no disabled branch in the tick path at all. The
// only production coupling is read-only: the tracer invokes the same
// registered phase methods step.go invokes, in the same order, and
// cross-checks every sub-tick against the phase ledger recordPhase writes
// when EnablePhaseTrace is on. If the registry in step.go is reordered or
// gains a state-changing call, the traced run and a plain
// stepAuthoritativePhases run diverge and
// TestDebugTraceTracedRunMatchesUntracedRun fails.
//
// Battle entry reseeds both streams fresh, so every pre-battle draw is wiped
// and the traced sequence starts from known stream state [R-CORE-02].

// debugPhaseLabels mirrors the registry labels step.go passes to recordPhase,
// in [01 §4.4] order. The per-sub-tick ledger cross-check fails if the two
// drift apart.
var debugPhaseLabels = [12]string{
	"phase1-network", "phase2-units", "phase3-projectiles", "phase4-effects",
	"phase5-orders", "phase6-feature", "phase7-sequences", "phase8-wind",
	"phase9-meteor", "phase10-shake", "phase11-objects", "phase12-cadence",
}

// debugPhaseEvent is one ordered event: either a phase observation (Phase
// 1..12, with stream snapshots around the phase body) or the sub-tick's
// publication boundary (Publish true).
type debugPhaseEvent struct {
	Tick uint32
	// Phase is the 1-based phase number; 0 for the publication event.
	Phase uint8
	Label string

	SimStateBefore uint32
	SimDrawsBefore uint64
	CrtStateBefore uint32
	CrtDrawsBefore uint64

	SimStateAfter uint32
	SimDrawsAfter uint64
	CrtStateAfter uint32
	CrtDrawsAfter uint64

	Publish       bool
	PublishedTick uint32
	PublishedOK   bool
}

// debugTracer collects the PROC-05 event stream for the sub-ticks it drives.
// It exists only inside tests.
type debugTracer struct {
	events  []debugPhaseEvent
	compact bytes.Buffer
	// ledgerMismatch records the first sub-tick where the production ledger
	// (recordPhase output) disagreed with the tracer's mirrored sequence.
	ledgerMismatch string
}

func newDebugTracer() *debugTracer { return &debugTracer{} }

// traceSubTick drives ONE authoritative sub-tick with per-phase stream
// capture, mirroring stepAuthoritativePhases ([01 §4.4] phases 1..12, sharing
// tail, cleanup/result tail, publication [I6]). The global tick increments
// before phase 1 via BeginSubTick [01 §4.4].
func (t *debugTracer) traceSubTick(s *Session) {
	tick := s.Clock.BeginSubTick()
	s.EnablePhaseTrace() // ledger cross-check spine; recording is behavior-neutral

	t.tracePhase(s, tick, 1, func() { s.phaseNetwork(tick) })
	t.tracePhase(s, tick, 2, func() { s.phaseUnits(tick) })
	t.tracePhase(s, tick, 3, func() { s.phaseProjectiles(tick) })
	t.tracePhase(s, tick, 4, func() { s.phaseEffects(tick) })
	t.tracePhase(s, tick, 5, func() { s.phaseOrders(tick) })
	t.tracePhase(s, tick, 6, func() { s.phaseFeatureLifecycle(tick) })
	t.tracePhase(s, tick, 7, func() { s.phaseSequences(tick) })
	t.tracePhase(s, tick, 8, func() { s.phaseWind(tick) })
	t.tracePhase(s, tick, 9, func() { s.phaseMeteorShower(tick) })
	t.tracePhase(s, tick, 10, func() { s.phaseCameraShake(tick) })
	t.tracePhase(s, tick, 11, func() { s.phaseObjectSweeps(tick) })
	t.tracePhase(s, tick, 12, func() { s.phaseCadenceFlip(tick) })

	// Sharing is the transport tail after phase 12, then cleanup/result,
	// then exactly one publication per completed sub-tick [01 §4.4][I6].
	s.stepSharingPhase(tick)
	s.stepCleanupAndResultPhase(tick)
	s.publishSnapshot(tick)
	pubTick, ok := s.Snapshot.PublishedTick()
	t.events = append(t.events, debugPhaseEvent{Tick: tick, Publish: true, PublishedTick: pubTick, PublishedOK: ok})
	fmt.Fprintf(&t.compact, "%d|publish|%d|%v\n", tick, pubTick, ok)

	t.crossCheckLedger(s)
}

// tracePhase snapshots both streams immediately before and after the phase
// body [01 §7.1][01 §7.2][I4]. Draws() is a debug counter and never sim input.
func (t *debugTracer) tracePhase(s *Session, tick uint32, num uint8, body func()) {
	simB, simDB := s.SimRNG().State, s.SimRNG().Draws()
	crtB, crtDB := s.CrtRNG().State, s.CrtRNG().Draws()
	body()
	ev := debugPhaseEvent{
		Tick: tick, Phase: num, Label: debugPhaseLabels[num-1],
		SimStateBefore: simB, SimDrawsBefore: simDB,
		CrtStateBefore: crtB, CrtDrawsBefore: crtDB,
		SimStateAfter: s.SimRNG().State, SimDrawsAfter: s.SimRNG().Draws(),
		CrtStateAfter: s.CrtRNG().State, CrtDrawsAfter: s.CrtRNG().Draws(),
	}
	t.events = append(t.events, ev)
	fmt.Fprintf(&t.compact, "%d|%d|%s|%08x:%d|%08x:%d|%08x:%d|%08x:%d\n",
		tick, num, ev.Label,
		ev.SimStateBefore, ev.SimDrawsBefore, ev.CrtStateBefore, ev.CrtDrawsBefore,
		ev.SimStateAfter, ev.SimDrawsAfter, ev.CrtStateAfter, ev.CrtDrawsAfter)
}

// crossCheckLedger verifies, per sub-tick, that the production ledger
// recorded exactly the twelve labels this tracer believes it ran, and that
// the ledger's cumulative draw totals agree with the tracer's after-snapshots
// at each phase boundary [01 §4.4]. A disagreement is retained and asserted
// by the tests — it means the registry in step.go drifted from the mirrored
// sequence even if no state changed.
func (t *debugTracer) crossCheckLedger(s *Session) {
	ledger := s.PhaseTrace()
	deltas := s.PhaseDrawDeltas()
	if t.ledgerMismatch != "" || len(ledger) != len(debugPhaseLabels) || len(deltas) != len(debugPhaseLabels) {
		if t.ledgerMismatch == "" {
			t.ledgerMismatch = fmt.Sprintf("ledger has %d labels / %d deltas, want 12/12", len(ledger), len(deltas))
		}
		return
	}
	base := len(t.events) - 13
	for i, label := range debugPhaseLabels {
		ev := t.events[base+i]
		if ledger[i] != label {
			t.ledgerMismatch = fmt.Sprintf("tick %d phase %d: ledger %q, tracer %q", ev.Tick, i+1, ledger[i], label)
			return
		}
		if deltas[i].SimDelta != ev.SimDrawsAfter || deltas[i].CrtDelta != ev.CrtDrawsAfter {
			t.ledgerMismatch = fmt.Sprintf("tick %d phase %d (%s): ledger draws %d/%d, tracer %d/%d",
				ev.Tick, i+1, label, deltas[i].SimDelta, deltas[i].CrtDelta, ev.SimDrawsAfter, ev.CrtDrawsAfter)
			return
		}
	}
}

// CompactHash returns a short hash of the whole captured event stream — the
// per-tick compact trace component of the W0-3 baseline.
func (t *debugTracer) CompactHash() string {
	return shortSHA(t.compact.Bytes())
}

// Dump returns the full compact event stream for diagnostics. Callers gate it
// behind a verbose flag or environment variable.
func (t *debugTracer) Dump() string { return t.compact.String() }

// TestDebugTraceTracedRunMatchesUntracedRun is the PROC-05 zero-effect proof:
// a traced run (per-phase stream capture around the registered phase methods)
// and an untraced run (the single production registry call
// stepAuthoritativePhases) produce identical final authoritative state and
// identical stream states and draw counts on the same seeds. Any divergence
// means the tracer's mirrored sequence has drifted from the registry in
// step.go. The untraced run also confirms the phase ledger stays empty when
// never enabled.
func TestDebugTraceTracedRunMatchesUntracedRun(t *testing.T) {
	const subTicks = 120
	runTraced := func() (*Session, *debugTracer) {
		s := strictNewSessionWithUnits(t, 2, 424242, 90210)
		s.SeedSessionRNG(424242, 90210)
		tr := newDebugTracer()
		for i := 0; i < subTicks; i++ {
			tr.traceSubTick(s)
		}
		return s, tr
	}
	runUntraced := func() *Session {
		s := strictNewSessionWithUnits(t, 2, 424242, 90210)
		s.SeedSessionRNG(424242, 90210)
		for i := 0; i < subTicks; i++ {
			tick := s.Clock.BeginSubTick()
			s.stepAuthoritativePhases(tick)
		}
		return s
	}

	traced, tr := runTraced()
	untraced := runUntraced()

	if tr.ledgerMismatch != "" {
		t.Fatalf("phase ledger disagrees with the traced sequence: %s", tr.ledgerMismatch)
	}
	if got, want := HashState(traced), HashState(untraced); got != want {
		t.Fatalf("traced run diverged from untraced run: state hash %s vs %s", got, want)
	}
	if traced.SimRNG().State != untraced.SimRNG().State || traced.SimRNG().Draws() != untraced.SimRNG().Draws() {
		t.Fatalf("sim stream diverged: traced %08x/%d vs untraced %08x/%d [I4]",
			traced.SimRNG().State, traced.SimRNG().Draws(), untraced.SimRNG().State, untraced.SimRNG().Draws())
	}
	if traced.CrtRNG().State != untraced.CrtRNG().State || traced.CrtRNG().Draws() != untraced.CrtRNG().Draws() {
		t.Fatalf("CRT stream diverged: traced %08x/%d vs untraced %08x/%d [I4][01 §7.2]",
			traced.CrtRNG().State, traced.CrtRNG().Draws(), untraced.CrtRNG().State, untraced.CrtRNG().Draws())
	}
	if got := untraced.PhaseTrace(); len(got) != 0 {
		t.Fatalf("phase ledger recorded %d entries while never enabled", len(got))
	}
	if len(tr.events) != subTicks*13 {
		t.Fatalf("event stream = %d events, want %d (12 phases + 1 publication per sub-tick)", len(tr.events), subTicks*13)
	}
	// Every sub-tick published exactly its own tick [I6].
	for i := 0; i < subTicks; i++ {
		ev := tr.events[i*13+12]
		if !ev.Publish || !ev.PublishedOK || ev.PublishedTick != uint32(i+1) {
			t.Fatalf("sub-tick %d publication boundary = %+v", i+1, ev)
		}
	}
}
