package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func editorMenuShell(t *testing.T) (*gameShell, *ui.Panel, *client.Client) {
	t.Helper()
	window := &gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindTextBox, Name: "GAMENAME", Active: 1, MaxChars: 12, Rect: gui.Rect{X: 4, Y: 4, W: 40, H: 16}},
	}}
	panel := ui.NewPanel(window)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(panel)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 64, Height: 48})
	if err != nil {
		t.Fatal(err)
	}
	return shell, panel, cl
}

// TestMenuEditorKeepsTokensAfterEscape locks the production consumer boundary:
// Escape ends this pass after its own token, leaving a later token in the
// ordered ring for the next service pass [07 R-WGT-01 §12].
func TestMenuEditorKeepsTokensAfterEscape(t *testing.T) {
	shell, panel, cl := editorMenuShell(t)
	panel.FocusEditor(1)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'})
	shell.menuInput(cl)
	if panel.EditorCaptured() || panel.TextOf("GAMENAME") != "" {
		t.Fatalf("Escape capture/text=%t/%q", panel.EditorCaptured(), panel.TextOf("GAMENAME"))
	}
	left := cl.Input().PeekTokens()
	if len(left) != 1 || left[0].Rune != 'x' {
		t.Fatalf("post-Escape pending tokens=%#v", left)
	}
	// The next ordinary service pass owns the retained tail and discards its
	// ignored text instead of carrying it into a later editor capture.
	shell.menuInput(cl)
	if pending := cl.Input().PendingTokens(); pending != 0 {
		t.Fatalf("next ordinary pass left %d post-Escape tokens", pending)
	}
}

func TestMenuEditorRightPressCapturesTextInput(t *testing.T) {
	shell, panel, cl := editorMenuShell(t)
	cl.Input().Mouse.SetPosition(5, 5)
	cl.Input().Mouse.SetButton(input.MouseButtonRight, true)
	shell.menuInput(cl)
	if !panel.EditorCaptured() || panel.Focused() != 1 {
		t.Fatalf("right press capture/focus=%t/%d", panel.EditorCaptured(), panel.Focused())
	}
}

// TestOrdinaryTokensDoNotEnterLaterSaveEditor proves the ordinary menu pass
// retires more than the ring's 29 usable slots before the save dialog opens;
// only newly typed text reaches GAMENAME [07 §2][07 R-WGT-01 §6].
func TestOrdinaryTokensDoNotEnterLaterSaveEditor(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 2; batch++ {
		for i := 0; i < 29; i++ {
			if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'}) {
				t.Fatalf("ordinary token %d/%d was refused before ring capacity", batch, i)
			}
		}
		shell.menuInput(cl)
		if pending := cl.Input().PendingTokens(); pending != 0 {
			t.Fatalf("ordinary menu batch %d left %d tokens", batch, pending)
		}
	}
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromResults); err != nil {
		t.Fatalf("open save screen: %v", err)
	}
	for _, r := range "fresh" {
		if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r}) {
			t.Fatal("enqueue fresh save name")
		}
	}
	shell.menuInput(cl)
	if got := saveLoadUI.Name(); got != "fresh" {
		t.Fatalf("GAMENAME=%q, want only fresh input", got)
	}
}

func TestBattleInputDiscardsOrdinaryTokens(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	in := input.NewState()
	for i := 0; i < 29; i++ {
		if !in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'}) {
			t.Fatalf("battle token %d refused before ring capacity", i)
		}
	}
	b.handleInput(in, nil)
	if pending := in.PendingTokens(); pending != 0 {
		t.Fatalf("battle ordinary pass left %d tokens", pending)
	}
}
