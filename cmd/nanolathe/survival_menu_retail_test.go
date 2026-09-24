//go:build retail

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The Survival entry (docs/DESIGN_SURVIVAL.md §9): SINGLE carries a Survival
// button, it opens SKIRMISH.GUI with the Survival rows and wave controls, and
// backing out restores the skirmish rows untouched. With
// NANOLATHE_SHOT_DIR set, each screen is written there as a PNG for review.
func TestSurvivalMenuEntryKeepsSkirmishRows(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	shell.settingsWritable = false
	shot := survivalMenuShooter(t, shell)

	shell.activateGadget("SINGLE")
	if shell.frontend.Mode != modeMenuSingle {
		t.Fatalf("SINGLE -> mode %d", shell.frontend.Mode)
	}
	if p := shell.activePanel(); p == nil || p.Index(survivalButton) < 0 {
		t.Fatalf("SINGLE has no Survival button")
	}
	shot("single")
	skirmish := shell.setup
	shell.activateGadget(survivalButton)
	if shell.frontend.Mode != modeMenuSkirmish || !shell.survivalMenu {
		t.Fatalf("Survival -> mode %d, survival screen %v", shell.frontend.Mode, shell.survivalMenu)
	}
	p := shell.activePanel()
	for _, name := range []string{survivalPaceButton, survivalAirButton, survivalSeaButton} {
		if p.Index(name) < 0 {
			t.Fatalf("Survival screen lacks %s", name)
		}
	}
	if shell.setup.NumPlayers != survivalRows || shell.retailControllers[0] != 1 {
		t.Fatalf("Survival rows: %d players, row 0 controller %d", shell.setup.NumPlayers, shell.retailControllers[0])
	}
	shot("survival-solo")
	shell.activateGadget("Player1")
	shell.activateGadget(survivalPaceButton)
	shell.activateGadget(survivalAirButton)
	shot("survival-buddy")
	cfg := shell.survivalConfig()
	if !cfg.Survival.Enabled || cfg.NumPlayers != 3 || cfg.Survival.Pace != 1 || !cfg.Survival.NoAir {
		t.Fatalf("Survival config: %d players, options %+v", cfg.NumPlayers, cfg.Survival)
	}
	if cfg.Players[1].Controller != session.SkirmishControllerComputer || cfg.Players[1].AllyGroup != cfg.Players[0].AllyGroup {
		t.Fatalf("buddy row: %+v", cfg.Players[1])
	}
	// The screen's setup builds a battle: a buddy, an attacker, no victory.
	request, err := skirmishBattleRequest(shell.opts, shell.cs, cfg, headless.ScenarioSurvival, nil, newBattleSeedSource(Options{Seed: 3}))
	if err != nil {
		t.Fatal(err)
	}
	battle, err := headless.ComposeFreshBattle(request.value)
	if err != nil {
		t.Fatalf("the Survival screen's setup does not compose: %v", err)
	}
	if !battle.Session.IsSurvival() || battle.Session.Skirmish.NumPlayers != 3 {
		t.Fatalf("composed battle: survival %v, %d players", battle.Session.IsSurvival(), battle.Session.Skirmish.NumPlayers)
	}
	shell.activateGadget("PrevMenu")
	if shell.frontend.Mode != modeMenuSingle || shell.survivalMenu {
		t.Fatalf("PrevMenu -> mode %d, survival screen %v", shell.frontend.Mode, shell.survivalMenu)
	}
	if shell.setup.NumPlayers != skirmish.NumPlayers || shell.setup.Players != skirmish.Players {
		t.Fatalf("the Survival screen changed the skirmish rows")
	}
	shell.activateGadget(survivalButton)
	if shell.retailControllers[1] != 2 || shell.survival.pace != 1 {
		t.Fatalf("the Survival screen forgot its rows on reopening")
	}
}

// survivalMenuShooter composes the shell's current screen to a PNG in
// NANOLATHE_SHOT_DIR; without it, it does nothing.
func survivalMenuShooter(t *testing.T, shell *gameShell) func(string) {
	dir := os.Getenv("NANOLATHE_SHOT_DIR")
	if dir == "" {
		return func(string) {}
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	return func(name string) {
		cl.BeginPresentationFrame()
		img := cl.ComposeFrame()
		f, err := os.Create(filepath.Join(dir, "menu-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
