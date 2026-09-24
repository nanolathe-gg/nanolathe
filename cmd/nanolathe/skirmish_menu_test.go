package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestRetailSkirmishTenPlayerShortcut(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	g.openMenu(modeMenuSkirmish)
	for _, r := range "*X" {
		if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r}) {
			t.Fatal("enqueue shortcut token")
		}
		g.menuInput(cl)
	}
	p := g.activePanel()
	if g.setup.NumPlayers != 10 || p == nil || p.Index("Player9") < 0 || !p.ActiveOf("Player9") {
		t.Fatalf("shortcut count/last row = %d/%v", g.setup.NumPlayers, p)
	}
	writeShellShot(t, cl, os.Getenv("NANOLATHE_SKIRMISH_TEN_SHOT"))
	clickRowGadget(t, g, p, cl, "Player9", input.MouseButtonLeft)
	if g.retailControllers[9] != 2 || !g.activePanel().ActiveOf("Side9") {
		t.Fatal("tenth row did not become a configurable computer player")
	}
}

func TestSkirmishTypedPlayerCountRebuildsTenRows(t *testing.T) {
	g := &gameShell{
		frontend: ui.NewFrontend(modeMenuSingle),
		setup:    newSkirmishMenuConfig("test"),
		assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{
			modeMenuSkirmish: {window: &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}}},
		}},
	}
	g.openMenu(modeMenuSkirmish)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	previousClient := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previousClient })
	for _, r := range "*viii" {
		cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
	for cl.Input().PendingTokens() != 0 {
		g.menuInput(cl)
	}
	if g.setup.NumPlayers != 8 || g.activePanel().Index("Player7") < 0 || g.activePanel().Index("Player8") >= 0 {
		t.Fatalf("*VIII produced count %d and %d gadgets", g.setup.NumPlayers, len(g.activePanel().Window.Gadgets))
	}
	for _, r := range "*X" {
		cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
		g.menuInput(cl)
	}
	if g.setup.NumPlayers != 10 || g.activePanel().Index("Player9") < 0 {
		t.Fatalf("*X produced count %d with no tenth row", g.setup.NumPlayers)
	}
	if got := g.captureSettings().Skirmish.NumPlayers; got != 10 {
		t.Fatalf("persisted setup count = %d, want 10", got)
	}
	if got := g.activePanel().Window.Gadgets[g.activePanel().Index("Player9")].Rect.Y; got != 259 {
		t.Fatalf("tenth row Y = %d, want layout rebuilt for 10 rows", got)
	}
}

func TestSkirmishTypedPlayerCountNeedsContiguousSequence(t *testing.T) {
	g := &gameShell{frontend: ui.NewFrontend(modeMenuSkirmish), setup: newSkirmishMenuConfig("test")}
	for _, token := range []input.Token{
		{Kind: input.TokenText, Rune: '*'},
		{Kind: input.TokenEdit, Key: input.KeyLeft},
		{Kind: input.TokenText, Rune: 'X'},
	} {
		g.skirmishPlayerCountToken(token)
	}
	if g.setup.NumPlayers != session.SkirmishDefaultPlayers {
		t.Fatalf("interrupted *X changed count to %d", g.setup.NumPlayers)
	}
}

func TestSkirmishMenuDefaultsAndOpponentCount(t *testing.T) {
	cfg := newSkirmishMenuConfig("small")
	if cfg.MapName != "small" || cfg.NumPlayers != session.SkirmishDefaultPlayers {
		t.Fatalf("menu defaults map/players = %q/%d", cfg.MapName, cfg.NumPlayers)
	}
	if cfg.Difficulty != session.SkirmishDefaultDifficulty ||
		cfg.Location != session.SkirmishDefaultLocation ||
		cfg.CommanderDeath != session.SkirmishDefaultCommanderDeath ||
		cfg.Mapping != session.SkirmishDefaultMapping ||
		cfg.LineOfSight != session.SkirmishDefaultLineOfSight ||
		cfg.LOSType != session.SkirmishDefaultLOSType {
		t.Fatalf("menu scalar defaults = %+v", cfg)
	}
	if cfg.Players[0].Controller != session.SkirmishDefaultController {
		t.Fatalf("slot 0 controller = %d", cfg.Players[0].Controller)
	}
	for i := 1; i < cfg.NumPlayers; i++ {
		if cfg.Players[i].Controller == session.SkirmishDefaultController {
			t.Fatalf("active opponent slot %d is not computer-controlled", i)
		}
	}

	g := &gameShell{setup: cfg, selectedSlot: 3}
	g.setOpponentCount(1)
	if g.setup.NumPlayers != 2 || g.selectedSlot != 1 {
		t.Fatalf("one-opponent setup players/selection = %d/%d", g.setup.NumPlayers, g.selectedSlot)
	}
	if g.setup.Players[0].Controller != 0 || g.setup.Players[1].Controller == 0 || g.setup.Players[2].Controller != 0 {
		t.Fatalf("one-opponent controllers = %d/%d/%d", g.setup.Players[0].Controller, g.setup.Players[1].Controller, g.setup.Players[2].Controller)
	}
	g.setOpponentCount(9)
	if g.setup.NumPlayers != session.SkirmishMaxPlayers {
		t.Fatalf("maximum-opponent setup players = %d", g.setup.NumPlayers)
	}
}

func TestSkirmishMenuRetailResourceButtons(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"decrement floor", decreaseResource(200), 200},
		{"decrement", decreaseResource(1000), 500},
		{"increment from floor quirk", increaseResource(200), 500},
		{"increment", increaseResource(500), 1000},
		{"increment cap", increaseResource(9500), 10000},
		{"increment at cap", increaseResource(10000), 10000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestSkirmishMenuRetailLineOfSightCycle(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("small")}
	if got := lineOfSightName(g.setup.LineOfSight, g.setup.LOSType); got != "ELEVATION" {
		t.Fatalf("default LOS label = %q", got)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 1 || g.setup.LOSType != 0 || lineOfSightName(g.setup.LineOfSight, g.setup.LOSType) != "FLAT" {
		t.Fatalf("second LOS state = %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 0 || g.setup.LOSType != 1 || lineOfSightName(g.setup.LineOfSight, g.setup.LOSType) != "MAPPED" {
		t.Fatalf("third LOS state = %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
	g.cycleLineOfSight(1)
	if g.setup.LineOfSight != 1 || g.setup.LOSType != 1 {
		t.Fatalf("LOS cycle did not wrap: %d/%d", g.setup.LineOfSight, g.setup.LOSType)
	}
}

// setOpponentCount is the opponent-count slot initialization these tests
// drive. The shipped SKIRMISH screen keeps its rows in retailControllers and
// compacts them in skirmishConfigForStart, so no menu route calls this
// [08 "Skirmish configuration"].
func (g *gameShell) setOpponentCount(opponents int) {
	if opponents < 1 {
		opponents = 1
	}
	if opponents > session.SkirmishMaxPlayers-1 {
		opponents = session.SkirmishMaxPlayers - 1
	}
	g.setup.NumPlayers = opponents + 1
	g.retailControllersSet = true
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		switch {
		case i == 0:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
			g.retailControllers[i] = 1
		case i < g.setup.NumPlayers:
			g.setup.Players[i].Controller = 1
			g.retailControllers[i] = 2
		default:
			g.setup.Players[i].Controller = session.SkirmishDefaultController
			g.retailControllers[i] = 0
		}
	}
	g.setup.ApplyDefaults()
	if g.selectedSlot >= g.setup.NumPlayers {
		g.selectedSlot = g.setup.NumPlayers - 1
	}
}

// lineOfSightName is the caption these tests read off the cycled line-of-sight
// pair. The authored gadget carries its own caption, so no shipped painter
// composes this string [08 "Skirmish configuration"].
func lineOfSightName(enabled, losType int) string {
	if enabled == 0 {
		return "MAPPED"
	}
	if losType == 0 {
		return "FLAT"
	}
	return "ELEVATION"
}
