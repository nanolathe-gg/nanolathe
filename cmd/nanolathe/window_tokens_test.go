package main

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
	"testing"
)

func windowTokenClient(t *testing.T) *client.Client {
	t.Helper()
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func queuedWindowTail(cl *client.Client) {
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'})
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
}

func TestBattleWindowTransitionsFlushOnlyOnOpen(t *testing.T) {
	cl := windowTokenClient(t)
	b := &battleSession{cl: cl, sess: &session.Session{Clock: &clock.State{}}}
	for _, step := range []struct {
		name string
		open func()
		want ui.BattleModal
	}{
		{"options", b.openBattleMenu, ui.BattleModalOptions},
		{"exit", func() { b.activateBattleMenuButton("EXIT", cl) }, ui.BattleModalExit},
		{"confirmation", func() { b.activateBattleMenuButton("MAINMENU", cl) }, ui.BattleModalConfirmMain},
	} {
		queuedWindowTail(cl)
		step.open()
		if b.battleState().Modal() != step.want || cl.Input().PendingTokens() != 0 {
			t.Fatalf("%s: modal=%v pending=%d", step.name, b.battleState().Modal(), cl.Input().PendingTokens())
		}
	}
	queuedWindowTail(cl)
	b.activateBattleMenuButton("CHOICE2", cl)
	if b.battleState().Modal() != ui.BattleModalOptions || cl.Input().PendingTokens() != 2 {
		t.Fatal("closing confirmation discarded tokens for surviving options")
	}
	cl.Input().DrainTokens()
	queuedWindowTail(cl)
	b.openBattleMenu()
	if cl.Input().PendingTokens() != 2 {
		t.Fatal("already-open options discarded new input")
	}
}

func TestResultWindowOpenFlushPreservesLaterInput(t *testing.T) {
	cl := windowTokenClient(t)
	w := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "MainMenu"}}}
	b := &battleSession{cl: cl, hud: &retailBattleHUD{resultWin: w}}
	queuedWindowTail(cl)
	b.prepareResultPanel()
	if b.hud.resultPanel == nil || cl.Input().PendingTokens() != 0 {
		t.Fatal("result open retained token tail")
	}
	queuedWindowTail(cl)
	b.prepareResultPanel()
	if cl.Input().PendingTokens() != 2 {
		t.Fatal("result redraw/preparation discarded fresh tokens")
	}
}

func TestRetailChildWindowOpenFlushesQueuedTail(t *testing.T) {
	resetSaveLoadScreenState(t)
	resetUnitInfoState(t)
	shell, _ := retailShellForTest(t)
	cl := windowTokenClient(t)
	old := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = old })
	queuedWindowTail(cl)
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromBattle); err != nil {
		t.Fatal(err)
	}
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("save window retained tokens")
	}
	queuedWindowTail(cl)
	if err := shell.showRetailMessage("Authored message"); err != nil {
		t.Fatal(err)
	}
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("message window retained tokens")
	}
	oldState, oldPanel, oldAssets := optionsState, optionsPanel, optionsAssets
	t.Cleanup(func() { optionsState, optionsPanel, optionsAssets = oldState, oldPanel, oldAssets })
	queuedWindowTail(cl)
	if err := shell.openRetailOptionsScreen(false); err != nil {
		t.Fatal(err)
	}
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("options root retained tokens")
	}
	queuedWindowTail(cl)
	shell.openRetailOptionsPage("speeds")
	if optionsState == nil || optionsState.page != "speeds" || cl.Input().PendingTokens() != 0 {
		t.Fatal("options page retained tokens or failed")
	}
	cat := unitInfoTestCatalog()
	h := &retailBattleHUD{cat: cat, fs: shell.cs.fs, shell: shell, hoveredGadget: 1, hoveredGadgetName: "armstump", hoveredGadgetOK: true}
	b := &battleSession{cl: cl, cat: cat, fs: shell.cs.fs, hud: h, sess: &session.Session{}}
	queuedWindowTail(cl)
	b.openUnitInfoScreen()
	if unitInfoUI == nil || cl.Input().PendingTokens() != 0 {
		t.Fatal("unit information open retained tokens or failed")
	}
	queuedWindowTail(cl)
	h.hoveredGadgetName = "missing"
	b.openUnitInfoScreen()
	if cl.Input().PendingTokens() != 2 {
		t.Fatal("refused information open discarded tokens")
	}
}

func TestCommandWindowOpenFlushesButCachedLookupPreservesTokens(t *testing.T) {
	cl := windowTokenClient(t)
	w := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}}
	h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": w}}
	b := &battleSession{cl: cl}
	f := &frame.Frame{}
	f.Selection.Handles = []pool.Handle{1}
	queuedWindowTail(cl)
	if got, _, err := h.windowForRequired(b, f); err != nil || got != w {
		t.Fatal("command window did not open")
	}
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("command open retained old tokens")
	}
	queuedWindowTail(cl)
	h.windowForRequired(b, f)
	if cl.Input().PendingTokens() != 2 {
		t.Fatal("cached command lookup discarded fresh tokens")
	}
	f.Selection.Handles = nil
	h.windowForRequired(b, f)
	if cl.Input().PendingTokens() != 2 {
		t.Fatal("command close discarded tokens")
	}
	f.Selection.Handles = []pool.Handle{2}
	h.windowForRequired(b, f)
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("reopening cached command window retained old tokens")
	}
	queuedWindowTail(cl)
	f.Selection.Handles = []pool.Handle{3}
	h.windowForRequired(b, f)
	if cl.Input().PendingTokens() != 0 {
		t.Fatal("selection change retained old command-window tokens")
	}
}
