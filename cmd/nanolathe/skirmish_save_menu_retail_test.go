//go:build retail

package main

// The single-player skirmish save/load loop as the windowed game runs it:
// a battle started from the skirmish setup (shell.startBattleLoad), stepped
// through shell.step with the modern renderer's asynchronous simulation
// goroutine enabled, saved from ARMOPT's SAVEGAME button through the
// LOADGAME.GUI dialog, and restored from ARMOPT's LOADGAME button. The
// computer opponent plays the Modern AI, which is the lobby default for a
// computer row the fallback adds (applyRetailSkirmishOpenRowsFallback).

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestMenuSkirmishSaveLoadWithAsyncSimulation(t *testing.T) {
	resetSaveLoadScreenState(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f := fuNewShell(t, 7)
	shell := f.shell
	// The modern window enables the asynchronous simulation goroutine at
	// its first interpolated Draw (ebitenapp); every save/load below must
	// work while sub-ticks run there.
	f.cl.SetAsyncSimulation(true)
	defer f.cl.SetAsyncSimulation(false)

	// A fresh lobby: human row 0, computer row 1 playing the Modern AI.
	shell.ensureRetailSkirmishControllers()
	shell.setup.NumPlayers = 2
	shell.retailControllers[0] = 1
	shell.retailControllers[1] = 2
	shell.setup.Players[1].AI = ai.ControllerModern
	shell.startBattleLoad(fuSkirmishMap)
	b := f.waitBattle("menu skirmish")
	sess := b.sess
	if sess.Skirmish.Players[1].AI != ai.ControllerModern {
		t.Fatalf("computer row AI = %v, want the Modern AI the lobby selected", sess.Skirmish.Players[1].AI)
	}
	t.Logf("skirmish: gameplay=%s rules=%s unitLimit=%d players=%d ai1=%v", sess.Gameplay, sess.Rules.Name, sess.Skirmish.UnitLimit, sess.Skirmish.NumPlayers, sess.Skirmish.Players[1].AI)

	stepShell := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			f.bind()
			if shell.battle == nil {
				t.Fatal("battle vanished while stepping")
			}
			f.millis.step++
			shell.step(1.0/30.0, f.cl)
		}
		// shell.step leaves the final batch running on the worker. Join before
		// callers inspect the live session or drive a dialog outside a step.
		shell.battle.joinSimulation(f.cl)
	}
	// Let both sides put real work in flight.
	stepShell(1800)
	// The window joins the launched batch at the start of the host step
	// that handles the save click, so the session is quiescent when the
	// dialog writes; join here for the same reason before reading or
	// saving state outside a step.
	shell.battle.joinSimulation(f.cl)
	sess = shell.battle.sess
	tick := sess.Clock.GlobalTick
	live := 0
	for slot := 1; slot < sess.Units.TotalRecords(); slot++ {
		if sess.Units.Unit(pool.Handle(slot)) != nil {
			live++
		}
	}
	t.Logf("at tick %d: %d live unit records", tick, live)
	if live < 2 {
		t.Fatalf("only %d live units at tick %d; the scene is not worth saving", live, tick)
	}

	// Save through ARMOPT, the way the player does. The install root is a
	// dotted one, as discovery finds Steam libraries beneath ~/.steam or
	// ~/.local/share: the bank must still land in its savegame directory.
	shell.opts.Root = filepath.Join(t.TempDir(), ".steam", "steamapps", "common", "Total Annihilation")
	b = shell.battle
	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("options menu did not open, modal=%v", b.battleState().Modal())
	}
	b.activateBattleMenuButton("SAVEGAME", f.cl)
	if !shell.saveLoadPanelActive() || saveLoadUI == nil || saveLoadUI.Mode() != saveScreenMode {
		t.Fatalf("SAVEGAME did not open the save dialog (active=%v)", shell.saveLoadPanelActive())
	}
	saveLoadUI.SetName("menu skirmish")
	shell.activateSaveLoadGadget("LOAD")
	if modal := shell.frontend.Panels.Modal(); modal != nil {
		t.Fatalf("save refused: %q", modal.Message())
	}
	wantPath := session.RetailSavePath(shell.saveLoadDir(), "menu skirmish")
	t.Logf("save dir=%q want path=%q", shell.saveLoadDir(), wantPath)
	if saveLoadUI != nil {
		t.Fatal("successful save did not close the dialog")
	}
	entries := enumerateRetailSaves(shell.saveLoadDir())
	found := false
	for _, e := range entries {
		if e.Path == wantPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved bank %q is not listed by the save enumerator (%d entries)", wantPath, len(entries))
	}

	// Advance so a load that does nothing cannot hide behind the tick.
	// The save left the battle on the paused ARMOPT window; OK closes it
	// and resumes.
	shell.battle.joinSimulation(f.cl)
	shell.battle.activateBattleMenuButton("OK", f.cl)
	stepShell(120)
	if got := shell.battle.sess.Clock.GlobalTick; got <= tick {
		t.Fatalf("battle did not advance after saving: tick %d -> %d", tick, got)
	}

	// Load through ARMOPT.
	shell.battle.joinSimulation(f.cl)
	old := shell.battle
	old.openBattleMenu()
	old.activateBattleMenuButton("LOADGAME", f.cl)
	if !shell.saveLoadPanelActive() || saveLoadUI == nil || saveLoadUI.Mode() != loadScreenMode {
		modal := ""
		if m := shell.frontend.Panels.Modal(); m != nil {
			modal = m.Message()
		}
		t.Fatalf("LOADGAME did not open the load dialog (active=%v modal=%q)", shell.saveLoadPanelActive(), modal)
	}
	row := -1
	for i, e := range saveLoadUI.Entries() {
		if e.Path == wantPath {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("load dialog does not list the save (%d entries)", len(saveLoadUI.Entries()))
	}
	shell.selectSaveLoadRow(row)
	shell.activateSaveLoadGadget("LOAD")
	if modal := shell.frontend.Panels.Modal(); modal != nil {
		t.Fatalf("load refused: %q", modal.Message())
	}
	restored := shell.battle
	if restored == nil || restored == old {
		t.Fatal("load did not replace the battle")
	}
	if got := restored.sess.Clock.GlobalTick; got != tick {
		t.Fatalf("restored battle resumes at tick %d, saved at %d", got, tick)
	}
	// The restored battle must tick and save again. It resumes paused, as
	// it was saved from the options menu; one resume intent (the Tab key's)
	// starts it.
	restored.applyBattleSchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: false})
	stepShell(60)
	if got := shell.battle.sess.Clock.GlobalTick; got <= tick {
		t.Fatalf("restored battle did not advance: tick %d -> %d", tick, got)
	}

	// Leave the battle for the front end and load the same save from
	// SINGLE's Load Game, then load it again in a fresh shell over a fresh
	// content set — the next-process route. The save directory is the one
	// the writing shell used.
	shell.battle.joinSimulation(f.cl)
	shell.teardownBattle(f.cl)
	shell.openMenu(modeMenuSingle)
	shell.bindFrontendClient(f.cl)
	loadFromFrontend := func(g *gameShell, when string) {
		t.Helper()
		if err := g.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
			t.Fatalf("%s: open load screen: %v", when, err)
		}
		if saveLoadUI == nil {
			msg := ""
			if m := g.frontend.Panels.Modal(); m != nil {
				msg = m.Message()
			}
			t.Fatalf("%s: load screen did not open (modal %q)", when, msg)
		}
		row := -1
		for i, e := range saveLoadUI.Entries() {
			if e.Path == wantPath {
				row = i
			}
		}
		if row < 0 {
			t.Fatalf("%s: frontend load list does not contain the save (%d entries)", when, len(saveLoadUI.Entries()))
		}
		g.selectSaveLoadRow(row)
		g.activateSaveLoadGadget("LOAD")
		if m := g.frontend.Panels.Modal(); m != nil {
			t.Fatalf("%s: frontend load refused: %q", when, m.Message())
		}
		if g.battle == nil || g.battle.sess == nil {
			t.Fatalf("%s: frontend load installed no battle", when)
		}
		if got := g.battle.sess.Clock.GlobalTick; got != tick {
			t.Fatalf("%s: restored battle resumes at tick %d, saved at %d", when, got, tick)
		}
	}
	loadFromFrontend(shell, "same shell")

	shell.battle.joinSimulation(f.cl)
	shell.teardownBattle(f.cl)
	fresh, freshDir := retailShellForTest(t)
	_ = freshDir
	// The fresh shell must address the same save directory: retailShellForTest
	// redirected its Root to its own temp dir, so point it at the writer's.
	fresh.opts.Root = shell.opts.Root
	fresh.bindFrontendClient(f.cl)
	loadFromFrontend(fresh, "fresh shell")
	fresh.battle.joinSimulation(f.cl)
	fresh.teardownBattle(f.cl)
}
