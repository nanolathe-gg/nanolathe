package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The skirmish row's controller cycle splits retail's Computer stage by the
// row's AI (DESIGN_INTERFACE_HUD_INPUT §2.6 "Computer AI"): a row made
// Computer starts on the Modern AI, the next click makes it Classic, and a
// Classic row leaves the Computer stage as retail's does. The caption names
// the AI.
func TestComputerRowsCycleModernThenClassic(t *testing.T) {
	g, p, cl := skirmishRowShell(t, []int{1, 0, 0}, []int{0, 1, 2})
	g.refreshRetailPanel()
	steps := []struct {
		controller int
		ai         ai.Controller
		caption    string
	}{
		{2, ai.ControllerModern, "Modern AI"},
		{2, ai.ControllerClassic, "Classic AI"},
		{0, ai.ControllerClassic, "Open"},
		{2, ai.ControllerModern, "Modern AI"},
	}
	for i, want := range steps {
		clickRowGadget(t, g, p, cl, "Player1", input.MouseButtonLeft)
		row := g.setup.Players[1]
		if g.retailControllers[1] != want.controller || row.AI != want.ai {
			t.Fatalf("click %d: row 1 is controller %d with the %s AI, want %d and %s", i+1, g.retailControllers[1], row.AI, want.controller, want.ai)
		}
		if got := g.activePanel().TextAt(g.activePanel().Index("Player1")); got != want.caption {
			t.Fatalf("click %d: caption %q, want %q", i+1, got, want.caption)
		}
	}
	// A right click on the row's name does nothing, as it did before.
	clickRowGadget(t, g, p, cl, "Player1", input.MouseButtonRight)
	if g.retailControllers[1] != 2 || g.setup.Players[1].AI != ai.ControllerModern {
		t.Fatal("a right click changed the row")
	}
	if help := g.activePanel().HelpAt(g.activePanel().Index("Player1")); !strings.Contains(help, "Modern AI") {
		t.Fatalf("row help %q does not name the AI", help)
	}
}

// With no human row shown, a Classic computer row still steps to Player as
// retail's Computer row does [08 R-SKIR-01 §1].
func TestAClassicRowLeavesTheComputerStageAsRetail(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("m"), retailControllersSet: true}
	g.setup.NumPlayers = 2
	g.retailControllers = [session.SkirmishMaxPlayers]int{2, 2}
	g.cycleRetailController(0)
	if g.retailControllers[0] != 1 || g.setup.Players[0].Controller != session.SkirmishDefaultController {
		t.Fatalf("a Classic row with no human became %d", g.retailControllers[0])
	}
}

// The row build's all-Open fallback adds a computer slot, so it plays the
// Modern AI; the start conversion carries each computer row's AI through
// the compaction and gives a human row none.
func TestTheLobbyCarriesEachRowsAIToTheBattle(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("m")}
	g.setup.NumPlayers = 4
	g.ensureRetailSkirmishControllers()
	if g.retailControllers[1] != 2 || g.setup.Players[1].AI != ai.ControllerModern {
		t.Fatal("the fallback's computer row is not Modern")
	}
	g.retailControllers = [session.SkirmishMaxPlayers]int{1, 0, 2, 2}
	g.setup.Players[0].AI = ai.ControllerModern // a stale choice on the human row
	g.setup.Players[2].AI = ai.ControllerClassic
	g.setup.Players[3].AI = ai.ControllerModern
	cfg := g.skirmishConfigForStart("m")
	if cfg.NumPlayers != 3 {
		t.Fatalf("start setup has %d players", cfg.NumPlayers)
	}
	for i, want := range []ai.Controller{ai.ControllerClassic, ai.ControllerClassic, ai.ControllerModern} {
		if cfg.Players[i].AI != want {
			t.Fatalf("start row %d plays %s, want %s", i, cfg.Players[i].AI, want)
		}
	}
}

// The settings file keeps each row's AI with the lobby's other row choices:
// a Classic computer row stores "classic", a Modern row stores nothing, and a
// row without the word — every row of a file written before this encoding,
// whose Classic rows stored none — loads on the Modern AI (user decision
// 2026-09-25). A row that is not a computer player stores no word.
func TestTheSettingsFileKeepsEachRowsAI(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("m")}
	blob := settings.Defaults()
	blob.Skirmish.NumPlayers = 4
	blob.Skirmish.Players[0] = settings.Player{Controller: 1, Side: 0, Color: 0, AllyGroup: 5, Metal: 1000, Energy: 1000, AI: settings.PlayerAIClassic}
	blob.Skirmish.Players[1] = settings.Player{Controller: 2, Side: 1, Color: 1, AllyGroup: 5, Metal: 1000, Energy: 1000}
	blob.Skirmish.Players[2] = settings.Player{Controller: 2, Side: 0, Color: 2, AllyGroup: 5, Metal: 1000, Energy: 1000, AI: settings.PlayerAIClassic}
	blob.Skirmish.Players[3] = settings.Player{Controller: 2, Side: 1, Color: 3, AllyGroup: 5, Metal: 1000, Energy: 1000, AI: "modern"}
	g.applySettings(blob)
	for i, want := range []ai.Controller{ai.ControllerClassic, ai.ControllerModern, ai.ControllerClassic, ai.ControllerModern} {
		if g.setup.Players[i].AI != want {
			t.Fatalf("loaded row %d plays %s, want %s", i, g.setup.Players[i].AI, want)
		}
	}
	back := g.captureSettings()
	for i, want := range []string{"", "", settings.PlayerAIClassic, ""} {
		if back.Skirmish.Players[i].AI != want {
			t.Fatalf("captured row %d AI word %q, want %q", i, back.Skirmish.Players[i].AI, want)
		}
	}
	for i, want := range []bool{false, false, true, false} {
		data, err := json.Marshal(back.Skirmish.Players[i])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"ai"`) != want {
			t.Fatalf("row %d wrote %s", i, data)
		}
	}
}

// The Survival screen's buddy rows walk Open, Modern AI, Classic AI and back
// to Open, and the setup it builds carries each buddy's AI (DESIGN_SURVIVAL
// §9).
func TestSurvivalBuddiesCycleOpenModernClassic(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("m"), frontend: ui.NewFrontend(modeMenuSkirmish)}
	g.openSurvivalRowsForTest()
	walk := []struct {
		controller int
		ai         ai.Controller
	}{{2, ai.ControllerModern}, {2, ai.ControllerClassic}, {0, ai.ControllerClassic}}
	for i, want := range walk {
		if !g.activateSurvivalGadget("Player1") {
			t.Fatal("the Survival screen did not take its row")
		}
		if g.retailControllers[1] != want.controller || g.setup.Players[1].AI != want.ai {
			t.Fatalf("click %d: buddy is %d with the %s AI", i+1, g.retailControllers[1], g.setup.Players[1].AI)
		}
	}
	g.activateSurvivalGadget("Player1") // Modern
	g.activateSurvivalGadget("Player2") // Modern
	g.activateSurvivalGadget("Player2") // Classic
	cfg := g.survivalConfig()
	if cfg.NumPlayers != 4 || cfg.Players[1].AI != ai.ControllerModern || cfg.Players[2].AI != ai.ControllerClassic || cfg.Players[3].AI != ai.ControllerClassic {
		t.Fatalf("Survival setup rows: %+v", cfg.Players[:4])
	}
}

// openSurvivalRowsForTest swaps the Survival rows in without opening a
// window, as openSurvivalMenu does before its screen transition.
func (g *gameShell) openSurvivalRowsForTest() {
	g.assets = &menuAssets{panel: map[shellMode]*retailPanelAssets{
		modeMenuSkirmish: {window: &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}}},
	}}
	g.openSurvivalMenu()
}

// --ai-player marks a command-line battle's computer rows, all=modern marks
// every one, and a row that is not one is refused.
func TestTheAIPlayerFlagMarksComputerRows(t *testing.T) {
	for _, flag := range []string{"2=modern", "all=modern"} {
		opts, err := parseFlags([]string{"--map", "m", "--ai-player", flag}, &strings.Builder{})
		if err != nil {
			t.Fatal(err)
		}
		cfg := session.DirectSkirmishConfig("m")
		if err := applyCommandLineComputerAI(&cfg, opts.ComputerAI); err != nil || cfg.Players[1].AI != ai.ControllerModern || cfg.Players[0].AI != ai.ControllerClassic {
			t.Fatalf("%s: rows 1 and 2 = %s %s, %v", flag, cfg.Players[0].AI, cfg.Players[1].AI, err)
		}
	}
	cfg := session.DirectSkirmishConfig("m")
	if err := applyCommandLineComputerAI(&cfg, []session.ComputerAI{{Row: 1, Controller: ai.ControllerModern}}); err == nil {
		t.Fatal("marking the human row was accepted")
	}
	for _, args := range [][]string{
		{"--map", "m", "--ai-player", "2=smart"},
		{"--ai-player", "2=modern"},
		{"--map", "m", "--load-save", "x.sav", "--ai-player", "2=modern"},
	} {
		if _, err := parseFlags(args, &strings.Builder{}); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}
