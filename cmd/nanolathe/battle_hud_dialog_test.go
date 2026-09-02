package main

import (
	"image/color"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
)

// frontendDialogStage renders only the last composition layer, so a changed
// pixel names the dialog and nothing else.
type frontendDialogStage struct {
	hud    *retailBattleHUD
	battle *battleSession
}

func (s frontendDialogStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.hud.drawFrontendDialog(c, s.battle)
}

// `ARMOPT`'s SAVEGAME opens `LOADGAME.GUI` as a child window over the battle,
// and the battle composer paints it there [07 R-FE-01 §7] [07 R-FE-01 §8].
// Before this unit the dialog was driven but never painted, because the
// shell's own draw returns early in battle mode.
func TestFrontendDialogPaintsOverTheBattleOnlyWhileOpen(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	h := &retailBattleHUD{}
	battle := &battleSession{shell: shell, sess: &session.Session{}, hud: h}
	shell.battle = battle

	buf := frame.NewBuffer()
	buf.BeginWrite()
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetUIStage(frontendDialogStage{hud: h, battle: battle})

	closed := c.ComposeFrame()
	if got := closed.RGBAAt(300, 200); got != (color.RGBA{A: 255}) {
		t.Fatalf("a battle with no dialog painted %#v at the dialog's centre", got)
	}

	battle.battleState().OpenOptions()
	battle.activateBattleMenuButton("SAVEGAME", nil)
	if !shell.saveLoadPanelActive() {
		t.Fatal("ARMOPT SAVEGAME did not put the dialog on the panel stack")
	}
	open := c.ComposeFrame()
	// The authored window is 494x420 at (81,27); its backdrop covers the
	// centre of the screen.
	if got := open.RGBAAt(300, 200); got == (color.RGBA{A: 255}) {
		t.Fatal("the open save dialog painted nothing over the battle")
	}
	// Nothing outside the authored window rectangle is touched.
	if got := open.RGBAAt(600, 460); got != (color.RGBA{A: 255}) {
		t.Fatalf("the dialog painted %#v outside its authored rectangle", got)
	}

	shell.activateSaveLoadGadget("CANCEL")
	if shell.saveLoadPanelActive() {
		t.Fatal("CANCEL left the dialog on the stack")
	}
	closed = c.ComposeFrame()
	if got := closed.RGBAAt(300, 200); got != (color.RGBA{A: 255}) {
		t.Fatalf("the cancelled dialog still painted %#v over the battle", got)
	}
}
