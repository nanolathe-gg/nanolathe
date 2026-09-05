package main

// The terminal result, the transient status line, game speed and pause
// [07 §11] [07 R-CAM-01 §3].

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
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

// setStatusMessage stores a transient on-screen message [07 §11][07 §2] presentation-only (I6).
func (b *battleSession) setStatusMessage(msg string) {
	if b == nil {
		return
	}
	cur, ok := b.currentSnapshot()
	b.battleState().Input.StatusMessage = msg
	if !ok {
		// Keep the semantic message latched for a later publication, but never
		// make it visible or assign a lifetime from the live clock [I6].
		b.battleState().Input.StatusUntil = 0
		return
	}
	// A ring line ages out (textscroll + 1) x 30 ticks after it is stored
	// [07 R-HUD-03 §14.3]; the same announcement is posted to the ring
	// (setGameSpeed, below) so the transient line's lifetime is drawn from
	// that ring's own TextScroll rather than a separate invented duration.
	textScroll := uint32(b.messageRing().TextScroll)
	b.battleState().Input.StatusUntil = cur.Tick + (textScroll+1)*30
}

// statusVisible reports whether the transient message should be drawn [07 §11].
func (b *battleSession) statusVisible(cur *frame.Frame) bool {
	if b == nil || cur == nil || b.battleState().Input.StatusMessage == "" {
		return false
	}
	return cur.Tick <= b.battleState().Input.StatusUntil
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
	// and silent [07 R-CAM-01 §3]. It is also latched as the transient status
	// line, which is what this build's HUD draws today.
	msg := frame.SpeedAnnouncement(int(newReq))
	b.messageRing().PostSilent(msg, frame.MessageClassSpeed, b.currentTick())
	b.setStatusMessage(msg)
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
// §14.3]. The caption ring bound to the audio queue lives in internal/client
// and is a second instance of the same value type; unifying the two is the
// business of that package's owner, not of the hotkey dispatcher.
func (b *battleSession) messageRing() *frame.MessageRing {
	if b == nil {
		return nil
	}
	if b.messages == nil {
		b.messages = frame.NewMessageRing()
	}
	return b.messages
}

// currentTick is the committed tick used to stamp presentation ring lines.
func (b *battleSession) currentTick() uint32 {
	if f, ok := b.currentSnapshot(); ok {
		return f.Tick
	}
	return 0
}

// messageColumnFontCache caches the primary UI font (fonts/comix.fnt) for one
// mounted install, the same install-keyed pattern cursorArtCache uses. The
// message column selects this font directly through the FNT drawer rather
// than the side's own console face or the GAF-font path [07 R-HUD-03 §14.4]
// [03 R-FONT-01 §5].
type messageColumnFontCache struct {
	fs   vfs.FSOps
	fnt  *formats.FNT
	done bool
}

var messageColumnFont messageColumnFontCache

// statusMessageFont resolves the message column's font for fs, loading and
// caching it once per mounted install.
func statusMessageFont(fs vfs.FSOps) *formats.FNT {
	if fs == nil {
		return nil
	}
	if messageColumnFont.done && messageColumnFont.fs == fs {
		return messageColumnFont.fnt
	}
	messageColumnFont = messageColumnFontCache{fs: fs, done: true}
	if fnt, err := formats.LoadFNTFile(fs, "fonts/comix.fnt"); err == nil {
		messageColumnFont.fnt = fnt
	}
	return messageColumnFont.fnt
}
