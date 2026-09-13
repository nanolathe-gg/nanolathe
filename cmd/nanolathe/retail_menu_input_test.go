package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The lobby callback writes the campaign reader as well as the setup record
// [08 "Skirmish configuration"], including the Hard-to-Easy wrap.
func TestSkirmishDifficultyCallbackUpdatesCampaignSelector(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "Difficulty", Stages: 3}}}
	g := &gameShell{frontend: ui.NewFrontend(modeMenuSingle), missionDifficultyValue: 2,
		assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuSkirmish: {window: w}}}}
	g.openMenu(modeMenuSkirmish)
	if g.missionDifficulty() != 0 || g.activePanel() == nil {
		t.Fatal("skirmish entry did not adopt lobby difficulty")
	}
	for _, want := range []int{1, 2, 0} {
		g.activateGadget("Difficulty")
		if g.setup.Difficulty != want || g.missionDifficulty() != want {
			t.Fatalf("difficulty setup/campaign = %d/%d, want %d", g.setup.Difficulty, g.missionDifficulty(), want)
		}
	}
}

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

func TestWidgetTokensRetainObservedNavigationAfterQueuedText(t *testing.T) {
	in := input.NewState()
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'})
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyUp})
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyDown})
	first := widgetTokens(in)
	if len(first) != 3 || first[0].Rune != 'x' || first[1].Key != input.KeyUp || first[2].Key != input.KeyDown {
		t.Fatalf("first tokens=%#v", first)
	}
	in.DiscardTokens(1)
	second := widgetTokens(in)
	if len(second) != 2 || second[0].Key != input.KeyUp || second[1].Key != input.KeyDown {
		t.Fatalf("retained navigation=%#v", second)
	}
	in.DiscardTokens(1)
	third := widgetTokens(in)
	if len(third) != 1 || third[0].Key != input.KeyDown {
		t.Fatalf("tail navigation=%#v", third)
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
		for passes := 0; cl.Input().PendingTokens() != 0 && passes < 29; passes++ {
			shell.menuInput(cl)
		}
		if pending := cl.Input().PendingTokens(); pending != 0 {
			t.Fatalf("ordinary menu batch %d left %d tokens after ordered service", batch, pending)
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

// Space fires the focused record. An inactive earlier duplicate must not
// veto that record through a second by-name lookup [07 R-WGT-01 §2].
func TestMenuFocusedDuplicateDoesNotRecheckFirstNamesActivity(t *testing.T) {
	shell, _, cl := editorMenuShell(t)
	panel := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: "SINGLE", Active: 0},
		{Kind: gui.KindButton, Name: "SINGLE", Active: 1},
	}})
	shell.frontend.Panels.Replace(panel)
	panel.SetFocus(2)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeySpace})
	shell.menuInput(cl)
	if shell.frontend.Mode != modeMenuSingle {
		t.Fatalf("focused duplicate did not fire: mode=%v", shell.frontend.Mode)
	}
}

// The screen dispatcher must use the bound default's record, without
// trimming, case-folding an authored exact name or skipping to a second
// prefix when the first is inactive [07 R-FE-01 §12][07 R-WGT-01 §2].
func TestMenuEscapeUsesExactDefaultAndSeparatePrefix(t *testing.T) {
	for _, tc := range []struct {
		name, authored string
		firstActive    uint8
		second         bool
		want           shellMode
	}{
		{"PrevMenu", "", 1, false, modeMenuMain},
		{"PREVMENU", "prevmenu", 1, false, modeMenuSingle},
		{" PREVMENU", "", 1, false, modeMenuSingle},
		{"PREVMENU", "", 0, true, modeMenuSingle},
	} {
		t.Run(tc.name+tc.authored, func(t *testing.T) {
			shell, _, cl := editorMenuShell(t)
			w := &gui.Window{Header: gui.Header{EscDefault: tc.authored}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: tc.name, Active: tc.firstActive}}}
			if tc.second {
				w.Gadgets = append(w.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: "PREVMENU", Active: 1})
			}
			shell.frontend.SetMode(modeMenuSingle)
			shell.frontend.Panels.Replace(ui.NewPanel(w))
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
			shell.menuInput(cl)
			if shell.frontend.Mode != tc.want {
				t.Fatalf("Escape changed mode to %v, want %v", shell.frontend.Mode, tc.want)
			}
		})
	}
}

// SELMAP authors uppercase PREVMENU, unlike the surrounding screens. Both
// its pointer action and Escape default close without committing the preview
// selection [07 R-FE-01 §2][07 R-FE-01 §5].
func TestMapCancelAuthoredCallback(t *testing.T) {
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMap), mapReturn: modeMenuSkirmish}
	shell.activateGadget("PREVMENU")
	if shell.frontend.Mode != modeMenuSkirmish {
		t.Fatal("authored Cancel callback did not return to skirmish")
	}
	if frontendCue(modeMenuMap, frontendCallbackKey("PREVMENU")) != "Previous" {
		t.Fatal("authored Cancel callback lost its Previous cue")
	}
}

func TestRetailMapCancelPreservesSelectedSkirmishMap(t *testing.T) {
	for _, escape := range []bool{false, true} {
		name := "click"
		if escape {
			name = "escape"
		}
		t.Run(name, func(t *testing.T) {
			shell, _, cl := retailAssetShell(t)
			shell.openMenu(modeMenuSkirmish)
			original := shell.setup.MapName
			shell.activateGadget("SelectMap")
			p := shell.activePanel()
			if shell.frontend.Mode != modeMenuMap {
				t.Fatal("map chooser did not open")
			}
			preview := (shell.mapIdx + 1) % len(shell.maps)
			shell.commitListSelection("MAPNAMES", preview)
			if shell.maps[preview] == original {
				t.Fatal("fixture needs a different preview map")
			}
			if escape {
				cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
				shell.menuInput(cl)
			} else {
				r := p.Window.PlacedRect(p.Index("PREVMENU"))
				in := cl.Input()
				in.Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
				in.Mouse.SetButton(input.MouseButtonLeft, true)
				shell.menuInput(cl)
				in.Mouse.ResetEdges()
				in.Mouse.SetButton(input.MouseButtonLeft, false)
				shell.menuInput(cl)
				in.Mouse.ResetEdges()
			}
			if shell.frontend.Mode != modeMenuSkirmish {
				t.Fatal("Cancel did not return to skirmish")
			}
			if shell.setup.MapName != original {
				t.Fatalf("Cancel committed preview: map=%q, want %q", shell.setup.MapName, original)
			}
			if len(shell.frontend.Panels.Entries()) != 1 {
				t.Fatal("Cancel left the chooser on the window stack")
			}
		})
	}
}
