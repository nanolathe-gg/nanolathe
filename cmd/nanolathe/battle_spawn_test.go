package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func TestSpawnChatCapturesSubmissionPointer(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(100, 100))
	b.sess.Econ = &economy.Service{}
	b.sess.Econ.Players[0].Exists = true
	b.hud = &retailBattleHUD{talkWin: testTalkWindow()}
	in := b.cl.Input()
	in.Mouse.SetPosition(200, 100)
	if !b.openTalk(in, b.cl) {
		t.Fatal("TALK did not open")
	}
	in.Mouse.SetPosition(500, 350)
	x, y, z := b.cursorWorld(500, 350)
	enqueueText(in, "+SpAwN ARmCK")
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	b.serviceTalk(in)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanSpawn || pending[0].Spawn.Unit != "ARmCK" || pending[0].Spawn.X != x || pending[0].Spawn.Y != y || pending[0].Spawn.Z != z {
		t.Fatalf("spawn request = %+v", pending)
	}
	in.Mouse.SetPosition(300, 200)
	b.cam.X += 50 << 16
	if b.sess.PendingHumanCommands()[0].Spawn != pending[0].Spawn {
		t.Fatal("pointer movement retargeted command")
	}
}

func TestSpawnChatRejectsUsageChromeAndStrict(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		x, y       float32
		mode       gameplay.Mode
	}{
		{"missing", "+spawn", 300, 200, gameplay.Modern},
		{"extra", "+spawn armck extra", 300, 200, gameplay.Modern},
		{"chrome", "+spawn armck", 10, 450, gameplay.Modern},
		{"strict", "+spawn armck", 300, 200, gameplay.Strict31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(100, 100))
			b.sess.Gameplay = tc.mode
			b.cl.Input().Mouse.SetPosition(tc.x, tc.y)
			b.dispatchLocalCommand(tc.text)
			if len(b.sess.PendingHumanCommands()) != 0 {
				t.Fatal("invalid spawn was queued")
			}
			if len(b.messageRing().Visible()) == 0 {
				t.Fatal("missing chat feedback")
			}
		})
	}
}
