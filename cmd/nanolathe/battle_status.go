package main

// The terminal result, the transient status line, game speed and pause
// [07 §11] [07 R-CAM-01 §3].

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// isResultVisible reports whether the authoritative result overlay should be shown [RS-05][08][P1-01].
// It is presentation-only and reads only the committed result view (I6).
// The overlay is visible when the terminal result is latched (Ended) and has not been dismissed.
func (b *battleSession) isResultVisible() bool {
	if b == nil || b.battleState().Input.ResultDismissed {
		return false
	}
	cur, ok := b.currentSnapshot()
	return ok && cur.Result.Ended
}

// resultView returns the current authoritative result view for overlay [RS-05].
func (b *battleSession) resultView() frame.ResultView {
	if b == nil {
		return frame.ResultView{}
	}
	cur, ok := b.currentSnapshot()
	if !ok || !cur.Result.Ended {
		return frame.ResultView{}
	}
	return cur.Result
}

// doResultAction executes the result overlay button action through the state graph [RS-05][08 "Session states"].
func (b *battleSession) doResultAction(kind ui.ResultAction, cl *client.Client) {
	if b == nil {
		return
	}
	switch kind {
	case ui.ResultActionSkirmish:
		if b.returnToSkirmish != nil {
			b.returnToSkirmish(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuSkirmish)
		} else if b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		b.battleState().Input.ResultDismissed = true
	case ui.ResultActionMainMenu:
		if b.postBattle != nil {
			if b.postBattle.Handle(session.PostBattleControlMainMenu, b.postBattleNow()) {
				b.consumePostBattleEffects(b.postBattleNow(), cl)
			}
			return
		}
		if b.returnToMenu != nil {
			b.returnToMenu(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuMain)
		}
		b.battleState().Input.ResultDismissed = true
	case ui.ResultActionContinue:
		if b.postBattle == nil {
			return
		}
		if b.postBattle.Handle(session.PostBattleControlStart, b.postBattleNow()) {
			b.consumePostBattleEffects(b.postBattleNow(), cl)
			b.routePostBattleStart(cl)
		}
	}
}

func resultContinuesCampaign(view frame.ResultView) bool {
	return view.Ended && !view.Draw && strings.EqualFold(view.Kind, "victory")
}

// adjustGameSpeed emits a concrete UI scheduling intent; Session performs the
// clamp and applies it at the scheduling boundary [07 §11][07 §2].
func (b *battleSession) adjustGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	b.applyBattleSchedule(ui.SpeedIntent(delta))
}

func (b *battleSession) setGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	// The clamp is the setter's: 1..20 on signed compares, and the
	// announcement is posted only when the clamped target actually changes
	// [07 R-CAM-01 §3]. AdjustSpeed owns both.
	newReq, changed := b.sess.AdjustSpeed(delta)
	if !changed {
		return
	}
	// The announcement goes to the message ring as kind 2, with no source unit
	// and silent [07 R-CAM-01 §3]. Retail draws it nowhere else: the shared
	// ring's own message-column line (internal/client's drawMessageLines) is
	// the entire drawn representation, and the line ages out on the ring's
	// own (textscroll + 1) x 30 tick bound [07 R-HUD-03 §14.3].
	b.messageRing().PostSilent(frame.SpeedAnnouncement(int(newReq)), frame.MessageClassSpeed, b.currentTick())
}

// togglePause flips the pause bit. Retail's established pause presentation is
// the authored igpaused title; the exact localized status strings are unknown,
// so this path intentionally emits no invented text [07 §11].
func (b *battleSession) togglePause() {
	if b == nil || b.sess == nil {
		return
	}
	b.applyBattleSchedule(ui.PauseIntent(!b.battleState().Paused()))
}

// messageRing is the battle shell's presentation message ring [07 R-HUD-03
// §14.3]. Retail has one ring: it is fed by unit captions and the audio
// queue (internal/client) as well as by the shell's own hotkeys (F3, F12)
// and the game-speed announcement, so this returns the one instance
// installBattleClient wired onto the presentation client, not a second copy.
func (b *battleSession) messageRing() *frame.MessageRing {
	if b == nil || b.cl == nil {
		return nil
	}
	return b.cl.MessageRing()
}

// currentTick is the committed tick used to stamp presentation ring lines.
func (b *battleSession) currentTick() uint32 {
	if f, ok := b.currentSnapshot(); ok {
		return f.Tick
	}
	return 0
}
