package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// These exercise the two production adapters.  The list names deliberately
// cover the save, map and mission surfaces: selection follows focused record
// identity, never a screen-mode shortcut [07 R-WGT-01 §§2,4].
func TestKeyboardMatrixListsThroughFrontendHandler(t *testing.T) {
	for _, name := range []string{"GAMES", "MAPNAMES", "Missions"} {
		t.Run(name, func(t *testing.T) {
			p := ui.NewPanel(&gui.Window{Focus: 1, Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel},
				{Kind: gui.KindListBox, Name: name, Active: 1, Attribs: 0x10, ItemHeight: 15, Rect: gui.Rect{W: 40, H: 42}},
			}})
			p.SetListAt(1, []string{"zero", "one", "two", "three", "four"})
			p.SetListMaxTopAt(1, 2)
			p.SetFocus(1)
			p.ListAt(1).SetSelected(3)
			shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
			shell.frontend.Panels.Replace(p)
			in := input.NewState()
			if !in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyDown}) {
				t.Fatal("enqueue list token")
			}
			shell.serviceMenuWidgets(p, in)
			if l := p.ListAt(1); l.Selected() != 4 || l.Top() != 1 {
				t.Fatalf("selected/top=%d/%d, want 4/1 after one keyboard scroll", l.Selected(), l.Top())
			}
			if !in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyDown}) {
				t.Fatal("enqueue endpoint token")
			}
			shell.serviceMenuWidgets(p, in)
			if l := p.ListAt(1); l.Selected() != 4 || l.Top() != 1 {
				t.Fatalf("endpoint selected/top=%d/%d, want clamped 4/1", l.Selected(), l.Top())
			}
		})
	}
}

func TestKeyboardMatrixHorizontalSliderThroughBattleOptionsHandler(t *testing.T) {
	oldPanel, oldState := optionsPanel, optionsState
	t.Cleanup(func() { optionsPanel, optionsState = oldPanel, oldState })
	p := ui.NewPanel(&gui.Window{Focus: 1, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindScrollBar, Name: "GAME", Active: 1, Assoc: 1, Range: 3, Rect: gui.Rect{W: 20, H: 4}},
	}})
	optionsPanel = p
	p.SetFocus(1)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
	shell.frontend.Panels.Replace(p)
	battle := &battleSession{shell: shell}
	for _, want := range []int{1, 2, 2} {
		in := input.NewState()
		if !in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyRight}) {
			t.Fatal("enqueue slider token")
		}
		battle.serviceBattleOptionsWidgets(p, in)
		if got := p.SliderKnobAt(1); got != want {
			t.Fatalf("knob=%d, want %d", got, want)
		}
	}
}
