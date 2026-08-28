package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// W0-3 parity baseline [PLAN_03 "Authoritative composition", PROC-05 row]
// [docs/WORK_UNITS.md W0-3].
//
// Golden values below were captured on branch parity/baseline-trace from main
// 1ed9416 with only these test-only files added; test files add no behavior,
// so the values are reproducible at that base by running this test. W0-3
// semantics: the baseline prevents accidental UNREVIEWED change to the
// authoritative tick's observable trace. It does NOT establish correctness —
// a stable trace is not evidence that it matches retail; the research spec is
// the only correctness authority.
//
// On mismatch the failure names the diverged component as a compact hash
// comparison. The full compact event stream is printed only when
// NANOLATHE_BASELINE_DUMP is set, keeping ordinary failure output small.

const (
	baselineSubTicks        = 120
	baselineSimSeed  uint32 = 424242
	baselineCrtSeed  uint32 = 90210
)

func baselineDumpEnabled() bool { return os.Getenv("NANOLATHE_BASELINE_DUMP") != "" }

// shortSHA is the 64-bit truncated SHA-256 used for every baseline component.
func shortSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// buildBaselineFixture assembles the tiny deterministic W0-3 fixture: authored
// definitions only (no retail bytes), a 32×32 flat map, and two opposing
// commanders (owners 0 and 1). Owner 0 is ordered across owner 1's column so
// the queue, path scheduler, movement integration, and occupancy machinery
// advance deterministically alongside the economy, wind, meteor, and shake
// phases. Seeding here wipes every pre-battle draw [R-CORE-02].
func buildBaselineFixture(t *testing.T) *Session {
	t.Helper()
	s := strictNewSessionWithUnits(t, 2, baselineSimSeed, baselineCrtSeed)
	s.SeedSessionRNG(baselineSimSeed, baselineCrtSeed)

	mover, target := unitForOwner(s, 0), unitForOwner(s, 1)
	if mover == nil || target == nil {
		t.Fatal("baseline fixture: opposing units missing")
	}
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("baseline fixture: Move_Ground descriptor missing")
	}
	// Goal eight cells beyond the opposing unit on the same diagonal, so the
	// route crosses the target's cell.
	goalX := world.CellToWorld(world.WorldToCell(target.X)+8) + numeric.Fixed(524288)
	goalZ := world.CellToWorld(world.WorldToCell(target.Z)+8) + numeric.Fixed(524288)
	q := orders.QueueForUnit(mover)
	if q == nil {
		t.Fatal("baseline fixture: mover has no order queue")
	}
	q.Push(id, orders.NewMoveNode(id, goalX, goalZ, s.Clock.GlobalTick, mover.Handle, false))
	return s
}

// unitForOwner returns the first live unit of the given owner in slot-ascending
// order [I1].
func unitForOwner(s *Session, owner uint8) *units.Unit {
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == owner {
			return u
		}
	}
	return nil
}

// baselineCapture is one component per line of the W0-3 record.
type baselineCapture struct {
	InitialSummary string // tick + both stream states/draws + unit roster at entry
	FinalSimState  uint32
	FinalSimDraws  uint64
	FinalCrtState  uint32
	FinalCrtDraws  uint64
	TraceHash      string // compact hash of the PROC-05 event stream (traced runs only)
	FinalUnitHash  string
	QueueHash      string
	SchedulerHash  string
}

// captureBaseline runs the fixture for baselineSubTicks sub-ticks and records
// the W0-3 components. When traced, the run goes through the PROC-05 debug
// tracer (proven state-identical to the production registry path by
// TestDebugTraceTracedRunMatchesUntracedRun) so the per-tick compact trace
// hash can be captured.
func captureBaseline(t *testing.T, traced bool) (baselineCapture, *debugTracer) {
	t.Helper()
	s := buildBaselineFixture(t)
	cap := baselineCapture{InitialSummary: baselineInitialSummary(s)}

	var tr *debugTracer
	if traced {
		tr = newDebugTracer()
	}
	for i := 0; i < baselineSubTicks; i++ {
		tick := s.Clock.BeginSubTick()
		if traced {
			tr.traceSubTick(s)
		} else {
			s.stepAuthoritativePhases(tick)
		}
	}

	cap.FinalSimState = s.SimRNG().State
	cap.FinalSimDraws = s.SimRNG().Draws()
	cap.FinalCrtState = s.CrtRNG().State
	cap.FinalCrtDraws = s.CrtRNG().Draws()
	if traced {
		cap.TraceHash = tr.CompactHash()
	}
	cap.FinalUnitHash = baselineUnitHash(s)
	cap.QueueHash = baselineQueueHash(s)
	cap.SchedulerHash = baselineSchedulerHash(s)
	return cap, tr
}

// baselineInitialSummary hashes the entry state: global tick, both stream
// states and draw counts, and the unit roster in slot-ascending order [I1].
func baselineInitialSummary(s *Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tick=%d sim=%08x/%d crt=%08x/%d", s.Clock.GlobalTick,
		s.SimRNG().State, s.SimRNG().Draws(), s.CrtRNG().State, s.CrtRNG().Draws())
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		fmt.Fprintf(&b, "|U%d:o%d:%s:%d,%d,%d", u.Handle, u.Owner, u.Def.CanonicalKey,
			int64(u.X.Raw()), int64(u.Y.Raw()), int64(u.Z.Raw()))
	}
	return shortSHA([]byte(b.String()))
}

// baselineUnitHash hashes every unit record in slot-ascending order [I1]:
// identity, position, health, build fraction, runtime status bits, movement
// word, and the per-unit economy/kill counters. float32 fields are hashed as
// their exact bit patterns.
func baselineUnitHash(s *Session) string {
	h := sha256.New()
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		state := byte('A')
		if u.Dying {
			state = 'D'
		}
		if !u.Alive {
			state = 'F'
		}
		fmt.Fprintf(h, "U%d:%c:o%d:%s:%d,%d,%d:%d/%d:%08x:%08x:%d:%d,%d,%d,%d:%d:%d:%t:%d:%08x:%08x|",
			u.Handle, state, u.Owner, u.Def.CanonicalKey,
			int64(u.X.Raw()), int64(u.Y.Raw()), int64(u.Z.Raw()),
			u.Health, u.MaxHealth,
			math.Float32bits(u.Remaining), u.Flags, u.Group,
			u.Move.Mode, u.Move.Heading, u.Move.Pitch, u.Move.Bank, int64(u.Move.Speed.Raw()),
			u.Pending, u.Activated, u.Kills,
			math.Float32bits(u.OrderGuard), math.Float32bits(u.SpotMetal))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// baselineQueueHash hashes both order-queue segments per unit, nodes in list
// order [I1][04 §3.2].
func baselineQueueHash(s *Session) string {
	h := sha256.New()
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		fmt.Fprintf(h, "Q%d:p%d", u.Handle, q.LenPrimary())
		for _, n := range q.Primary() {
			fmt.Fprintf(h, "[%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%s]",
				orders.DescriptorFor(n.ID).Name, n.Phase, n.Deadline, n.Target,
				int64(n.GoalX.Raw()), int64(n.GoalY.Raw()), int64(n.GoalZ.Raw()),
				n.Param1, n.Param2, n.Param3, n.MoveState, n.PathStatus,
				n.CreationTick, n.Flags, n.Satisfied, n.DynamicGate, n.BuildDefKey)
		}
		fmt.Fprintf(h, "s%d", q.LenSecondary())
		for _, n := range q.Secondary() {
			fmt.Fprintf(h, "[%s,%d,%d]", orders.DescriptorFor(n.ID).Name, n.Phase, n.Deadline)
		}
		h.Write([]byte("|"))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// baselineSchedulerHash hashes the path scheduler's pending requests in the
// scheduler's own deterministic order [04 §7.3][I1], then the published route
// and steer state per handle ascending.
func baselineSchedulerHash(s *Session) string {
	h := sha256.New()
	if s.Movement != nil {
		if s.Movement.Scheduler != nil {
			for _, r := range s.Movement.Scheduler.AllRequests() {
				fmt.Fprintf(h, "R%dp%d:%d,%d:%T:%v;", r.Unit, r.Player, r.Start.X, r.Start.Z, r.Goal, r.Goal)
			}
		}
		handles := make([]int, 0, len(s.Movement.Routes))
		for hdl := range s.Movement.Routes {
			handles = append(handles, int(hdl))
		}
		sort.Ints(handles)
		for _, raw := range handles {
			r := s.Movement.Routes[pool.Handle(raw)]
			if r == nil {
				continue
			}
			fmt.Fprintf(h, "T%d:%d,%v,%v,%d,", raw, r.Count, r.Active, r.Dirty, r.Status)
			for _, p := range r.Points[:r.Count] {
				fmt.Fprintf(h, "%d,%d;", p.X, p.Z)
			}
		}
		steers := make([]int, 0, len(s.Movement.Steers))
		for hdl := range s.Movement.Steers {
			steers = append(steers, int(hdl))
		}
		sort.Ints(steers)
		for _, raw := range steers {
			st := s.Movement.Steers[pool.Handle(raw)]
			if st == nil {
				continue
			}
			fmt.Fprintf(h, "S%d:%d,%d,%d,%d,%v,%d,%d;", raw, st.X, st.Z, st.Heading, st.PendingHeading, st.Dirty, st.Speed, st.HeightWord)
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// TestW03_ParityBaseline compares a fresh 120-sub-tick run of the fixture
// against the recorded baseline. The traced run and a plain production run
// are first proven state-identical, so the recorded components describe the
// authoritative tick, not the tracer. See the file comment for W0-3
// semantics: change-stopper, not a correctness oracle.
func TestW03_ParityBaseline(t *testing.T) {
	// Traced run and production run must agree before either is compared to
	// the baseline; this keeps the baseline honest about WHAT it pins. The
	// full equivalence proof lives in
	// TestDebugTraceTracedRunMatchesUntracedRun; under -short skip this
	// second fixture run (the redundant half) and compare the traced capture
	// directly.
	tracedCap, tr := captureBaseline(t, true)
	if tr.ledgerMismatch != "" {
		t.Fatalf("phase ledger disagrees with the traced sequence: %s", tr.ledgerMismatch)
	}
	if !testing.Short() {
		plainCap, _ := captureBaseline(t, false)
		if tracedCap.FinalUnitHash != plainCap.FinalUnitHash ||
			tracedCap.FinalSimState != plainCap.FinalSimState || tracedCap.FinalSimDraws != plainCap.FinalSimDraws ||
			tracedCap.FinalCrtState != plainCap.FinalCrtState || tracedCap.FinalCrtDraws != plainCap.FinalCrtDraws ||
			tracedCap.QueueHash != plainCap.QueueHash || tracedCap.SchedulerHash != plainCap.SchedulerHash {
			t.Fatal("traced run diverged from the production registry path; baseline is invalid until the tracer matches stepAuthoritativePhases")
		}
	}

	// Golden values: captured at main 1ed9416 + these test-only files (see
	// file comment). NOT a correctness claim.
	want := baselineCapture{
		InitialSummary: "f21d06b3a6f0d96d",
		FinalSimState:  0x56e7f6d5,
		FinalSimDraws:  2,
		FinalCrtState:  0x58c091cd,
		FinalCrtDraws:  241,
		TraceHash:      "6cf643a418157370",
		FinalUnitHash:  "bd599bebf55741f5",
		QueueHash:      "5d7164c0c1c9169a",
		SchedulerHash:  "cdb58393dec1701d",
	}

	check := func(name, got, golden string) {
		t.Helper()
		if got != golden {
			t.Errorf("W0-3 baseline divergence in %s:\n  got    %s\n  golden %s", name, got, golden)
		}
	}
	check("initial state summary", tracedCap.InitialSummary, want.InitialSummary)
	check("final sim RNG state", fmt.Sprintf("%08x/%d", tracedCap.FinalSimState, tracedCap.FinalSimDraws),
		fmt.Sprintf("%08x/%d", want.FinalSimState, want.FinalSimDraws))
	check("final CRT RNG state", fmt.Sprintf("%08x/%d", tracedCap.FinalCrtState, tracedCap.FinalCrtDraws),
		fmt.Sprintf("%08x/%d", want.FinalCrtState, want.FinalCrtDraws))
	check("per-tick compact trace", tracedCap.TraceHash, want.TraceHash)
	check("final unit state", tracedCap.FinalUnitHash, want.FinalUnitHash)
	check("queue state", tracedCap.QueueHash, want.QueueHash)
	check("path scheduler state", tracedCap.SchedulerHash, want.SchedulerHash)
	if t.Failed() && baselineDumpEnabled() {
		t.Logf("full PROC-05 event stream:\n%s", tr.Dump())
	}
	if t.Failed() && !baselineDumpEnabled() {
		t.Logf("set NANOLATHE_BASELINE_DUMP=1 (and -v) to print the full event stream")
	}
}
