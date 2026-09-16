package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func battleOptionsAcceleratorFixture(gadgets []gui.Gadget) (*battleSession, *input.State) {
	window := &gui.Window{Rect: gui.Rect{W: 120, H: 60}, Gadgets: append([]gui.Gadget{{Kind: gui.KindPanel}}, gadgets...)}
	h := &retailBattleHUD{optionsWin: window, optionsBuilt: true, optionsPanel: ui.NewPanel(window)}
	state := ui.NewProductionBattleState()
	state.OpenOptions()
	return &battleSession{hud: h, battleUI: state}, input.NewState()
}

// The ARMOPT caller owns an ordinary zero-token child: its first matching
// active button claims the token and routes through BattleState by the fired
// record, while a rejected key stays available to battle input [07 R-WGT-01
// §§1-3][07 R-FE-01 §7].
func TestBattleOptionsChildAcceleratorClaimsOnlyAcceptedToken(t *testing.T) {
	b, in := battleOptionsAcceleratorFixture([]gui.Gadget{
		{Kind: gui.KindButton, Name: "EXIT", Active: 1, QuickKey: 'E', Rect: gui.Rect{X: 4, Y: 4, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "CANCEL", Active: 1, QuickKey: 'E', Rect: gui.Rect{X: 30, Y: 4, W: 20, H: 12}},
	})
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'e'})
	b.handleBattleMenuInput(in, nil)
	if got := b.battleState().Modal(); got != ui.BattleModalExit {
		t.Fatalf("first matching accelerator modal=%v, want EXITMENU", got)
	}
	if got := in.PendingTokens(); got != 0 {
		t.Fatalf("claimed accelerator tokens=%d, want 0", got)
	}

	// A runtime key rewrite is observed on the retained panel.  The old key is
	// not consumed, and the changed authored key fires the same indexed record.
	b, in = battleOptionsAcceleratorFixture([]gui.Gadget{{Kind: gui.KindButton, Name: "EXIT", Active: 1, QuickKey: 'E', Rect: gui.Rect{X: 4, Y: 4, W: 20, H: 12}}})
	b.hud.optionsWin.Gadgets[1].QuickKey = 'Z'
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'e'})
	b.handleBattleMenuInput(in, nil)
	if got := b.battleState().Modal(); got != ui.BattleModalOptions {
		t.Fatalf("old rewritten key changed modal=%v", got)
	}
	if got := in.PendingTokens(); got != 1 {
		t.Fatalf("unclaimed rewritten key tokens=%d, want 1", got)
	}
	in.DiscardTokens(1)
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'z'})
	b.handleBattleMenuInput(in, nil)
	if got := b.battleState().Modal(); got != ui.BattleModalExit {
		t.Fatalf("changed authored key modal=%v, want EXITMENU", got)
	}
}

func TestBattleChildAcceleratorRejectsGreyInactiveAndCapturedButton(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*battleSession)
	}{
		{name: "low grey bit", change: func(b *battleSession) { b.hud.optionsWin.Gadgets[1].GrayedOut = 1 }},
		{name: "inactive", change: func(b *battleSession) { b.hud.optionsPanel.SetActiveAt(1, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, in := battleOptionsAcceleratorFixture([]gui.Gadget{{Kind: gui.KindButton, Name: "EXIT", Active: 1, QuickKey: 'E', Rect: gui.Rect{X: 4, Y: 4, W: 20, H: 12}}})
			tc.change(b)
			in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'e'})
			b.handleBattleMenuInput(in, nil)
			if got := b.battleState().Modal(); got != ui.BattleModalOptions {
				t.Fatalf("rejected accelerator modal=%v", got)
			}
			if got := in.PendingTokens(); got != 1 {
				t.Fatalf("rejected accelerator tokens=%d, want 1", got)
			}
		})
	}

	b, in := battleOptionsAcceleratorFixture([]gui.Gadget{{Kind: gui.KindButton, Name: "EXIT", Active: 1, QuickKey: 'E', Rect: gui.Rect{X: 4, Y: 4, W: 20, H: 12}}})
	in.Mouse.SetPosition(6, 6)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleMenuInput(in, nil)
	if capture := b.hud.optionsPanel.CaptureIndex(); capture != 1 {
		t.Fatalf("pointer capture=%d, want EXIT index 1", capture)
	}
	in.Mouse.ResetEdges()
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'e'})
	b.handleBattleMenuInput(in, nil)
	if got := b.battleState().Modal(); got != ui.BattleModalOptions {
		t.Fatalf("captured-button accelerator modal=%v", got)
	}
	if got := in.PendingTokens(); got != 1 {
		t.Fatalf("captured-button accelerator tokens=%d, want 1", got)
	}
}

func TestEndMissionChildAcceleratorReturnsFiredRecord(t *testing.T) {
	window := &gui.Window{Rect: gui.Rect{W: 120, H: 40}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "MainMenu", Active: 1, QuickKey: 'M', Rect: gui.Rect{X: 4, Y: 4, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "LoadGame", Active: 1, QuickKey: 'L', Rect: gui.Rect{X: 30, Y: 4, W: 20, H: 12}},
	}}
	h := &retailBattleHUD{resultWin: window, resultPanel: ui.NewPanel(window)}
	in := input.NewState()
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'l'})
	name, fired := h.resultControlName(in)
	if !fired || name != "LoadGame" {
		t.Fatalf("ENDMSN accelerator=(%q,%t), want LoadGame/true", name, fired)
	}
	if got := in.PendingTokens(); got != 0 {
		t.Fatalf("ENDMSN claimed tokens=%d, want 0", got)
	}
}

// This is intentionally opt-in: it reads the installed battle child GUI files
// and verifies their assigned accelerators survive the runtime builder before
// the retained service sees them.  The test remains skipped without the retail
// asset tag, as the repository carries no retail bytes.
func TestBattleChildRetailAssignedAccelerators(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	for _, tc := range []struct {
		path string
		name string
		key  byte
	}{
		{path: "guis/armopt.gui", name: "EXIT", key: 'E'},
		{path: "guis/exitmenu.gui", name: "CANCEL", key: 'C'},
		{path: "guis/yesorno.gui", name: "CHOICE2", key: 'N'},
		{path: "guis/restart.gui", name: "RESTART", key: 'R'},
		{path: "guis/endmsn.gui", name: "MainMenu", key: 'M'},
	} {
		t.Run(tc.path, func(t *testing.T) {
			window, err := gui.LoadWithTranslation(cs.fs, tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			(&gameShell{cs: cs, quickKeyPreclearDisabled: true}).installRetailWindowButtonArt(window, nil)
			index := window.GadgetIndex(tc.name)
			if index < 1 || window.Gadgets[index].QuickKey != tc.key {
				t.Fatalf("%s quickkey index/value=%d/%q, want %q", tc.name, index, window.Gadgets[index].QuickKey, tc.key)
			}
			t.Logf("%s assigned accelerator %q", tc.name, window.Gadgets[index].QuickKey)
		})
	}
}
