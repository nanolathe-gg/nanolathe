//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Both setup screens on the retail art: the skirmish screen with a Modern
// and a Classic computer row, and the Survival screen with one buddy of each
// kind. Each row's caption names its AI, and the battle each screen starts
// gives exactly the Modern rows the Modern AI, under Strict 3.1 as under
// Modern (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Per-player selection"). With NANOLATHE_SHOT_DIR set, both screens are
// written there as PNGs for review.
func TestSetupScreensChooseEachComputerRowsAIRetail(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	shell.settingsWritable = false
	shot := survivalMenuShooter(t, shell)
	caption := func(row string) string {
		p := shell.activePanel()
		return p.TextAt(p.Index(row))
	}

	shell.activateGadget("SINGLE")
	shell.activateGadget("Skirmish")
	if shell.frontend.Mode != modeMenuSkirmish || shell.survivalMenu {
		t.Fatalf("Skirmish -> mode %d", shell.frontend.Mode)
	}
	shell.setup.NumPlayers = 4
	shell.openMenu(modeMenuSkirmish)
	shell.retailControllers = [session.SkirmishMaxPlayers]int{1, 0, 0, 0}
	shell.activateGadget("Player1") // Open -> Modern AI
	shell.activateGadget("Player2") // Open -> Modern AI
	shell.activateGadget("Player2") // Modern AI -> Classic AI
	for row, want := range map[string]string{"Player0": "Player", "Player1": "Modern AI", "Player2": "Classic AI", "Player3": "Open"} {
		if got := caption(row); got != want {
			t.Fatalf("skirmish %s caption %q, want %q", row, got, want)
		}
	}
	shot("skirmish-computer-ai")
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		cfg := shell.skirmishConfigForStart(shell.setup.MapName)
		opts := shell.opts
		opts.Gameplay = mode
		request, err := skirmishBattleRequest(opts, shell.cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(Options{Seed: 3}))
		if err != nil {
			t.Fatal(err)
		}
		battle, err := headless.ComposeFreshBattle(request.value)
		if err != nil {
			t.Fatalf("%s: the skirmish screen's setup does not compose: %v", mode, err)
		}
		s := battle.Session
		if s.Rules.Name != string(mode) || s.AI[1].Controller != ai.ControllerModern || !s.ModernAIPlayer(1) || s.AI[2].Controller != ai.ControllerClassic || s.ModernAIPlayer(2) {
			t.Fatalf("%s: rows 2 and 3 composed as %s and %s", mode, s.AI[1].Controller, s.AI[2].Controller)
		}
	}
	shell.activateGadget("PrevMenu")

	shell.activateGadget(survivalButton)
	if !shell.survivalMenu {
		t.Fatal("the Survival screen did not open")
	}
	shell.activateGadget("Player1") // Open -> Modern AI
	shell.activateGadget("Player2") // Open -> Modern AI
	shell.activateGadget("Player2") // Modern AI -> Classic AI
	if got1, got2 := caption("Player1"), caption("Player2"); got1 != "Modern AI" || got2 != "Classic AI" {
		t.Fatalf("Survival buddy captions %q, %q", got1, got2)
	}
	shot("survival-computer-ai")
	cfg := shell.survivalConfig()
	opts := shell.opts
	opts.Gameplay = gameplay.Strict31
	request, err := skirmishBattleRequest(opts, shell.cs, cfg, headless.ScenarioSurvival, nil, newBattleSeedSource(Options{Seed: 3}))
	if err != nil {
		t.Fatal(err)
	}
	battle, err := headless.ComposeFreshBattle(request.value)
	if err != nil {
		t.Fatalf("the Survival screen's setup does not compose: %v", err)
	}
	s := battle.Session
	if !s.ModernAIPlayer(1) || s.ModernAIPlayer(2) || s.ModernAIPlayer(3) {
		t.Fatalf("Survival Modern AI players: %v %v %v, want the first buddy alone", s.ModernAIPlayer(1), s.ModernAIPlayer(2), s.ModernAIPlayer(3))
	}
	shell.activateGadget("PrevMenu")
}
