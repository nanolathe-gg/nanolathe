package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// Admission must reach the two real handlers before the same options callback
// runs; a passing standalone predicate does not protect these paths
// [07 R-WGT-01 §3][07 R-WGT-01 §1 step 8].
func TestButtonQuickKeysThroughBothInputHandlers(t *testing.T) {
	for _, battle := range []bool{false, true} {
		name := "frontend"
		if battle {
			name = "battle options"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				change  func(*ui.Panel)
				capture int
				alt     bool
				want    int
				focus   int
			}{
				{name: "ordinary", capture: -1, want: 1, focus: 1},
				{name: "grey low bit", capture: -1, change: func(p *ui.Panel) { p.Window.Gadgets[1].GrayedOut = 1 }, focus: 2},
				{name: "grey upper bits permit", capture: -1, change: func(p *ui.Panel) { p.Window.Gadgets[1].GrayedOut = 2 }, want: 1, focus: 1},
				{name: "inactive", capture: -1, change: func(p *ui.Panel) { p.SetActiveAt(1, false) }, focus: 2},
				{name: "changed authored key", capture: -1, change: func(p *ui.Panel) { p.Window.Gadgets[1].QuickKey = 'Z' }, focus: 2},
				{name: "lowercase authored key", capture: -1, change: func(p *ui.Panel) { p.Window.Gadgets[1].QuickKey = 'a' }, want: 1, focus: 1},
				{name: "captured button uses pointer path", capture: 1, focus: 2},
				{name: "other button capture permits", capture: 2, want: 1, focus: 1},
				{name: "other slider capture permits", capture: 2, change: func(p *ui.Panel) { p.Window.Gadgets[2].Kind = gui.KindScrollBar }, want: 1, focus: 1},
				{name: "text capture blocks", capture: -1, change: func(p *ui.Panel) { p.FocusEditor(4) }, focus: 4},
				{name: "Alt permits text capture", capture: -1, alt: true, change: func(p *ui.Panel) { p.FocusEditor(4) }, want: 1, focus: 1},
				{name: "later duplicate retains index", capture: -1, change: func(p *ui.Panel) {
					p.Window.Gadgets[1].GrayedOut = 1
					p.Window.Gadgets[3].QuickKey = 'A'
				}, want: 1, focus: 3},
			} {
				t.Run(tc.name, func(t *testing.T) {
					oldPanel, oldAssets, oldState, oldClient := optionsPanel, optionsAssets, optionsState, clPtr
					t.Cleanup(func() { optionsPanel, optionsAssets, optionsState, clPtr = oldPanel, oldAssets, oldState, oldClient })
					w := &gui.Window{Focus: 2, Header: gui.Header{DefaultFocus: "SHADING"}, Gadgets: []gui.Gadget{
						{Kind: gui.KindPanel},
						{Kind: gui.KindButton, Name: "ANTI", Active: 1, QuickKey: 'A'},
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
					optionsPanel, optionsAssets, optionsState = p, &retailPanelAssets{window: w}, &retailOptionsState{inBattle: battle}
					p.SetPressed(tc.capture)
					cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
					if err != nil {
						t.Fatal(err)
					}
					clPtr = nil
					if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'A'}) {
						t.Fatal("enqueue quick-key token")
					}
					cl.Input().Kbd.SetKey(input.KeyAlt, tc.alt)
					if battle {
						(&battleSession{shell: shell}).handleBattleOptionsInput(cl)
					} else {
						shell.menuInput(cl)
					}
					if shell.display.AntiAlias != tc.want || p.Focused() != tc.focus {
						t.Fatalf("callback value/focus=%d/%d, want %d/%d", shell.display.AntiAlias, p.Focused(), tc.want, tc.focus)
					}
				})
			}
		})
	}
}
