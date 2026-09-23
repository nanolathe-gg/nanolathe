package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A right press cancels an open chat line as Escape does and is consumed:
// it neither cancels the armed latch nor clears the selection. With chat
// closed the same press keeps its battle meaning. The chat rule is a
// Supported inference from a retail maintainer's observation [07 §5 "Chat"].
func TestTalkRightPressCancelsAndIsConsumed(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.LocalOwner = 0
	b.sess.Econ = &economy.Service{}
	b.sess.Econ.Players[0].Exists = true
	b.sess.Econ.Players[0].Name = "Player"
	b.hud = &retailBattleHUD{talkWin: testTalkWindow()}
	b.millisSource = &chatMillis{}
	b.controller = NewBattleController(b, b.millisSource)
	u := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	replaceSelectionForTest(t, b, u)
	cl := b.cl
	cl.SetFocused(true)
	in := cl.Input()
	in.Mouse.SetPosition(320, 200)
	rightPress := func() {
		in.Mouse.ResetEdges()
		in.Mouse.SetButton(input.MouseButtonRight, true)
		b.viewerStep(0, cl)
		in.Mouse.ResetEdges()
		in.Mouse.SetButton(input.MouseButtonRight, false)
		b.viewerStep(0, cl)
		applyPendingBattleCommands(b)
	}

	for _, latch := range []input.Latch{input.LatchAttack, input.LatchNormal} {
		b.battleState().Input.Latch = latch
		in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
		b.viewerStep(0, cl)
		enqueueText(in, "discard")
		b.viewerStep(0, cl)
		if !b.chat.active || b.hud.talkPanel.TextOf("TALK") != "discard" {
			t.Fatal("chat did not open with the typed line")
		}
		rightPress()
		if b.chat.active || b.hud.talkPanel.TextOf("TALK") != "" || b.hud.talkPanel.EditorCaptured() {
			t.Fatalf("latch %v: right press did not cancel and clear the chat line", latch)
		}
		if got := len(cl.MessageRing().Visible()); got != 0 {
			t.Fatalf("latch %v: right press committed the line (%d visible)", latch, got)
		}
		if b.battleState().Input.Latch != latch || u.Flags&hud.SelectionFlag == 0 {
			t.Fatalf("latch %v: the cancelling press reached the battle", latch)
		}
	}

	b.battleState().Input.Latch = input.LatchAttack
	rightPress()
	if b.battleState().Input.Latch != input.LatchNormal || u.Flags&hud.SelectionFlag == 0 {
		t.Fatal("with chat closed, right press did not cancel the armed latch alone")
	}
	rightPress()
	if u.Flags&hud.SelectionFlag != 0 {
		t.Fatal("with chat closed, idle right press did not deselect")
	}
}
