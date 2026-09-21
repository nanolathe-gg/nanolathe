package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func pausedHostCell(cells int32) numeric.Fixed { return numeric.Fixed(cells) * 16 << 16 }

func pausedHostBuildNodes(u *units.Unit, product string) int {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return 0
	}
	n := 0
	for _, node := range q.Primary() {
		if node != nil && node.BuildDefKey == product {
			n++
		}
	}
	return n
}

// The whole point of the paused-input boundary is that the host's own dispatch
// path sees the new selection: every dispatcher resolves its actor from the
// committed command page, so a build clicked after a paused selection must be
// addressed to the unit the player just selected and to nothing else
// (DESIGN_INTERFACE_HUD_INPUT §3.12) [07 §9].
func TestPausedMobileBuildDispatchesToTheUnitSelectedWhilePaused(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.State = session.StateBattle
	first := placeUnit(b, "armcons", pausedHostCell(3), pausedHostCell(3))
	second := placeUnit(b, "armcons", pausedHostCell(9), pausedHostCell(9))

	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{first.Handle}}}); err != nil {
		t.Fatal(err)
	}
	b.sess.Step(1)
	f, ok := b.currentSnapshot()
	if !ok || f.CommandPage.Builder != first.Handle {
		t.Fatalf("first tick did not publish the first builder: %+v", f.CommandPage.Builder)
	}

	b.sess.SetPaused(true)
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{second.Handle}}}); err != nil {
		t.Fatal(err)
	}
	b.sess.Step(2)

	if err := b.DispatchMobileBuild("armsolar", pausedHostCell(12), 0, pausedHostCell(12), false); err != nil {
		t.Fatalf("paused mobile build not dispatched: %v", err)
	}
	pending := b.sess.PendingHumanCommands()
	var build *session.HumanCommand
	for i := range pending {
		if pending[i].Kind == session.HumanMobileBuild {
			build = &pending[i]
		}
	}
	if build == nil {
		t.Fatal("the paused dispatch enqueued no mobile build")
	}
	if got := build.MobileBuild.Builder; got != second.Handle {
		t.Fatalf("paused build addressed builder %d, want the newly selected %d", got, second.Handle)
	}
	if len(pending) != 1 {
		t.Fatalf("the paused boundary left %d commands queued, want only the new build", len(pending))
	}

	b.sess.SetPaused(false)
	b.sess.Step(2)
	if got := pausedHostBuildNodes(second, "armsolar"); got != 1 {
		t.Fatalf("the selected builder holds %d build orders, want exactly one", got)
	}
	if got := pausedHostBuildNodes(first, "armsolar"); got != 0 {
		t.Fatalf("the previously selected builder holds %d build orders, want none", got)
	}
}

// The options window is modal: with its bit set the pump skips the host frame
// entirely, so no input is classified, dispatched or drained
// [01 R-PLAT-01 §1 step 2]. Closing it returns the paused battle to an
// ordinary input boundary.
func TestBattleOptionsWindowSuppressesThePausedInputBoundary(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.State = session.StateBattle
	subject := placeUnit(b, "armcons", pausedHostCell(3), pausedHostCell(3))
	b.sess.Step(1)

	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions || !b.sess.Clock.Paused {
		t.Fatalf("opening the options window did not pause a modal battle: modal=%d paused=%t",
			b.battleState().Modal(), b.sess.Clock.Paused)
	}
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: []pool.Handle{subject.Handle}}}); err != nil {
		t.Fatal(err)
	}
	b.viewerStep(0, b.cl)
	if len(b.sess.PendingHumanCommands()) != 1 {
		t.Fatal("the modal options window drained the input queue")
	}
	if f, ok := b.currentSnapshot(); !ok || f.CommandPage.Builder != 0 {
		t.Fatal("the modal options window published a selection")
	}

	b.closeBattleMenu()
	b.sess.SetPaused(true)
	b.battleState().SetPauseTruth(true)
	b.viewerStep(0, b.cl)
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("a paused battle with the options window closed did not drain its input")
	}
	f, ok := b.currentSnapshot()
	if !ok || f.CommandPage.Builder != subject.Handle {
		t.Fatalf("the host frame published builder %d, want %d", f.CommandPage.Builder, subject.Handle)
	}
	if f.Tick != b.sess.Clock.GlobalTick {
		t.Fatalf("the paused host frame published tick %d, want the committed %d", f.Tick, b.sess.Clock.GlobalTick)
	}
}
