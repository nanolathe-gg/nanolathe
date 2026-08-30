package main

// Host-clock isolation for the production input replays.
//
// BattleController.Step samples a clock.MillisSource and feeds
// clock.ScaledNow(sample) straight into Session.Step, where AdvanceSP takes the
// signed difference against the session's scaled-time anchor [01 §4.1][01 §4.2].
// A controller built without an explicit source falls back to the package-wide
// monotonic host source, which is anchored at *process* start — so the first
// sample a fixture battle ever sees is the whole `go test` process uptime, and
// AdvanceSP spends the clamped five-sub-tick burst before the replay's first
// click is ever enqueued. That makes the outcome of a replay depend on how much
// wall-clock the tests that ran earlier consumed, which is ambient process
// state, not the fixture's own.
//
// The production seam already takes the source as a constructor argument for
// exactly this reason, so the fix belongs in the fixture: every input replay in
// this package builds its controller through newReplayController, which pins
// the host clock and leaves the fixture's own clock driver as the only thing
// that advances the session. The process-wide monotonic origin itself is
// deliberate and stays as it is — retail's timebase input is one global
// wall-clock reading, not a per-battle stopwatch [01 §4.1].
//
// The placement and command-queue replays survived the ambient clock only
// because they step anchor-relatively (s.Step(s.Clock.ScaledAnchor + 1)) and
// assert on queue contents rather than tick counts. They were still taking the
// burst: measured mid-replay, an ambient controller moved a placement fixture
// from tick 2 to tick 7 in one step. Nothing about that was intended.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// pinnedHostMillis is a host millisecond source frozen at one reading.
// scriptedMillisSource repeats its last sample forever, so a single entry pins
// the scaled clock for the whole replay.
func pinnedHostMillis(ms uint32) *scriptedMillisSource {
	return &scriptedMillisSource{samples: []uint32{ms}}
}

// newReplayController builds the production controller with a pinned host
// clock. It is the constructor the deterministic input replays use: the
// controller still runs the real decision path, but contributes no wall-clock
// budget of its own.
func newReplayController(b *battleSession) *BattleController {
	return NewBattleController(b, pinnedHostMillis(0))
}

// TestBattleReplayIgnoresHostProcessUptime is the isolation guard. It runs the
// same select replay the production input tests run and asserts that the
// session's scaled-time anchor after the replay is the pinned sample and
// nothing else — i.e. no host process uptime reached Session.Step.
//
// Dropping the injected source regresses this immediately: the package's own
// suite takes seconds, one scaled unit is ~34 ms, and at nominal speed each
// scaled unit of delta is a sub-tick — so the anchor lands in the tens or
// hundreds and both assertions below fail.
func TestBattleReplayIgnoresHostProcessUptime(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.battleState().Input.Latch = input.LatchNormal
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	applyPendingBattleCommands(b)
	tickAfterSetup := b.sess.Clock.GlobalTick

	c := newReplayController(b)
	sx, sy := screenPos(b.cam, commander)
	frame := BattleInputFrame{MouseX: sx, MouseY: sy, Buttons: BattleMouseButtons{Left: true}}
	c.Step(frame, nil)
	c.Step(frame, nil)
	frame.Buttons.Left = false
	c.Step(frame, nil)

	if got := b.sess.Clock.ScaledAnchor; got != 0 {
		t.Fatalf("host uptime reached the session clock: scaled anchor = %d, want the pinned sample 0", got)
	}
	if got := b.sess.Clock.GlobalTick; got != tickAfterSetup {
		t.Fatalf("pinned replay advanced GlobalTick from %d to %d: the controller spent a wall-clock budget", tickAfterSetup, got)
	}

	// With the host clock pinned, the fixture's own clock driver is what makes
	// the queued selection due, and the replay resolves identically every run.
	applyPendingBattleCommands(b)
	if commander.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("pinned replay did not select commander at screen=%d,%d", sx, sy)
	}
}
