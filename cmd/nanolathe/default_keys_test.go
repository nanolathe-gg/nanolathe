package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// Both consuming handlers must select the same indexed target before their
// ordinary screen callback. Definition lookup alone does not establish the
// Enter/Space admission order [07 R-WGT-01 §2].
func TestDefaultKeysThroughBothInputHandlers(t *testing.T) {
	for _, battle := range []bool{false, true} {
		name := "frontend"
		if battle {
			name = "battle options"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				key           input.Key
				change        func(*ui.Panel)
				anti, shading int
				focus         int
			}{
				{name: "Enter prefers default", key: input.KeyEnter, shading: 1, focus: 2},
				{name: "Space uses focus", key: input.KeySpace, anti: 1},
				{name: "inactive default falls back", key: input.KeyEnter, change: func(p *ui.Panel) { p.SetActiveAt(2, false) }, anti: 1},
				{name: "grey default falls back", key: input.KeyEnter, change: func(p *ui.Panel) { p.Window.Gadgets[2].GrayedOut = 1 }, anti: 1},
				{name: "non-grey upper bits", key: input.KeyEnter, change: func(p *ui.Panel) { p.Window.Gadgets[2].GrayedOut = 2 }, shading: 1, focus: 2},
				{name: "Space never uses default", key: input.KeySpace, change: func(p *ui.Panel) { p.SetActiveAt(1, false) }},
				{name: "grey focus refuses", key: input.KeySpace, change: func(p *ui.Panel) { p.Window.Gadgets[1].GrayedOut = 1 }},
				{name: "focused slider excluded", key: input.KeySpace, change: func(p *ui.Panel) { p.Window.Gadgets[1].Kind = gui.KindScrollBar }},
				{name: "focused label excluded", key: input.KeySpace, change: func(p *ui.Panel) { p.Window.Gadgets[1].Kind = gui.KindLabel }},
				{name: "surface needs no hotness", key: input.KeySpace, change: func(p *ui.Panel) { p.Window.Gadgets[1].Kind = gui.KindSurface }, anti: 1},
				{name: "list ignores button grey word", key: input.KeySpace, change: func(p *ui.Panel) { p.Window.Gadgets[1].Kind = gui.KindListBox; p.Window.Gadgets[1].GrayedOut = 1 }, anti: 1},
				{name: "missing default falls back", key: input.KeyEnter, change: func(p *ui.Panel) { p.Window.Header.CrDefault = "missing" }, anti: 1},
				{name: "default admits non-pointer kind", key: input.KeyEnter, change: func(p *ui.Panel) { p.Window.Gadgets[2].Kind = gui.KindPicture }, shading: 1, focus: 2},
				{name: "default editor receives focus setup", key: input.KeyEnter, change: func(p *ui.Panel) {
					p.Window.Gadgets[2].Kind = gui.KindTextBox
					p.Window.Gadgets[2].Attribs = 1
					p.Window.Gadgets[2].MaxChars = 12
				}, shading: 1, focus: 2},
				{name: "later duplicate focus stays indexed", key: input.KeySpace, change: func(p *ui.Panel) { p.SetActiveAt(1, false); p.SetFocus(3) }, anti: 1, focus: 3},
				{name: "captured editor retains Enter", key: input.KeyEnter, change: func(p *ui.Panel) { p.FocusEditor(4) }, focus: 4},
				{name: "captured editor retains Space", key: input.KeySpace, change: func(p *ui.Panel) { p.FocusEditor(4) }, focus: 4},
			} {
				t.Run(tc.name, func(t *testing.T) {
					oldPanel, oldAssets, oldState, oldClient := optionsPanel, optionsAssets, optionsState, clPtr
					t.Cleanup(func() { optionsPanel, optionsAssets, optionsState, clPtr = oldPanel, oldAssets, oldState, oldClient })
					w := &gui.Window{Focus: 1, Header: gui.Header{CrDefault: "SHADING"}, Gadgets: []gui.Gadget{
						{Kind: gui.KindPanel},
						{Kind: gui.KindButton, Name: "ANTI", Active: 1},
						{Kind: gui.KindButton, Name: "SHADING", Active: 1},
						{Kind: gui.KindButton, Name: "ANTI", Active: 1},
						{Kind: gui.KindTextBox, Name: "EDITOR", Active: 1, Attribs: 1, MaxChars: 12},
					}}
					p := ui.NewPanel(w)
					if tc.change != nil {
						tc.change(p)
					}
					shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain)}
					shell.frontend.Panels.Replace(p)
					optionsPanel, optionsAssets, optionsState = p, &retailPanelAssets{window: w}, &retailOptionsState{inBattle: battle, pressed: -1}
					cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
					if err != nil {
						t.Fatal(err)
					}
					clPtr = nil
					cl.Input().Kbd.SetKey(tc.key, true)
					if battle {
						(&battleSession{shell: shell}).handleBattleOptionsInput(cl)
					} else {
						shell.menuInput(cl)
					}
					wantFocus := tc.focus
					if wantFocus == 0 {
						wantFocus = 1
					}
					if p.Focused() != wantFocus {
						t.Fatalf("callback focus=%d, want selected record %d", p.Focused(), wantFocus)
					}
					if w.Gadgets[wantFocus].Kind == gui.KindTextBox && !p.EditorCaptured() {
						t.Fatal("focused editor missing capture/setup")
					}
					if shell.display.AntiAlias != tc.anti || shell.display.Shading != tc.shading {
						t.Fatalf("display writes anti/shading=%d/%d, want %d/%d", shell.display.AntiAlias, shell.display.Shading, tc.anti, tc.shading)
					}
				})
			}
		})
	}
}
