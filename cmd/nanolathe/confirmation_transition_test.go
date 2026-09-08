package main

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"testing"
)

type confirmationStage struct{ battle *battleSession }

func (s confirmationStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.battle.hud.drawBattleMenu(c, s.battle)
}

func confirmationFixture(t *testing.T) (*battleSession, *client.Client) {
	t.Helper()
	bank := &formats.GAF{}
	for i, name := range []string{"options", "exit", "confirm"} {
		bank.Entries = append(bank.Entries, panelTestBank(t, name, byte(20+i*30), 9).Entries...)
	}
	h := &retailBattleHUD{common: bank,
		optionsWin: &gui.Window{Rect: gui.Rect{W: 12, H: 24}, Header: gui.Header{Panel: "options"}},
		exitWin:    &gui.Window{Rect: gui.Rect{X: 14, Y: 2, W: 14, H: 20}, Header: gui.Header{Panel: "exit"}},
		confirmWin: &gui.Window{Rect: gui.Rect{X: 8, Y: 8, W: 28, H: 8}, Header: gui.Header{Panel: "confirm"}}}
	b := &battleSession{hud: h, sess: &session.Session{Clock: &clock.State{}}}
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 40, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	c.SetUIStage(confirmationStage{b})
	return b, c
}

func TestConfirmationReplacesExitAndReturnsToPausedOptions(t *testing.T) {
	for _, choice := range []string{"MAINMENU", "EXITGAME"} {
		for _, cancel := range []string{"No", "Enter", "Escape"} {
			t.Run(choice+"/"+cancel, func(t *testing.T) {
				b, c := confirmationFixture(t)
				b.openBattleMenu()
				b.activateBattleMenuButton("EXIT", c)
				if got := c.ComposeFrameSnapshot().Indexed[4*40+20]; got == 0 {
					t.Fatal("fixture did not paint exit panel")
				}
				b.activateBattleMenuButton(choice, c)
				shot := c.ComposeFrameSnapshot()
				if got := shot.Indexed[4*40+20]; got != 0 {
					t.Fatalf("confirmation retained exit-only pixel: %d", got)
				}
				if shot.Indexed[4*40+4] == 0 || shot.Indexed[12*40+20] == 0 {
					t.Fatal("missing options or confirmation")
				}
				if choice == "EXITGAME" && b.battleState().ConfirmTitle() != "Surrender this battle and exit to Windows?" {
					t.Fatal("wrong single-player exit title")
				}
				switch cancel {
				case "No":
					b.activateBattleMenuButton("CHOICE2", c)
				case "Enter":
					c.Input().Kbd.SetKey(input.KeyEnter, true)
					b.handleBattleMenuInput(c.Input(), c)
				case "Escape":
					c.Input().Kbd.SetKey(input.KeyEscape, true)
					b.handleBattleMenuInput(c.Input(), c)
				}
				if b.battleState().Modal() != ui.BattleModalOptions || !b.sess.Clock.Paused || !b.battleState().Paused() {
					t.Fatal("cancel did not return to paused options")
				}
				shot = c.ComposeFrameSnapshot()
				if shot.Indexed[4*40+20] != 0 || shot.Indexed[12*40+20] != 0 {
					t.Fatal("cancel recreated a child window")
				}
				b.closeBattleMenu()
				if b.sess.Clock.Paused || b.battleState().Paused() {
					t.Fatal("closing root did not resume")
				}
			})
		}
	}
}
