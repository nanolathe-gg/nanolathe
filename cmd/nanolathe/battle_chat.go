package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

type battleChatState struct {
	active      bool
	ownsFrame   bool
	lastCommand string
}

func talkOpenToken(in *input.State) bool {
	if in == nil {
		return false
	}
	tokens := in.PeekTokens()
	return len(tokens) != 0 && tokens[0].Kind == input.TokenEdit && tokens[0].Key == input.KeyEnter
}

func (b *battleSession) openTalk(in *input.State, cl *client.Client) bool {
	if b == nil || b.hud == nil || b.chat.active || battleSessionKind(b) == 3 {
		return false
	}
	b.hud.openTalkWindow()
	p := b.hud.talkPanel
	if p == nil {
		return false
	}
	index := p.Index("TALK")
	if index < 0 {
		return false
	}
	// Opening a generic window flushes the old producer ring before the new
	// focused editor receives input [07 R-WGT-01 §1][07 §5 "Chat"].
	if in != nil {
		in.DiscardTokens(in.PendingTokens())
	}
	p.SetTextAt(index, "")
	if !p.FocusEditor(index) {
		return false
	}
	b.chat.active, b.chat.ownsFrame = true, true
	if b.dragScrollActive {
		b.endDragScroll(cl)
	}
	b.playUICue(cl, "SmallButton")
	return true
}

func (b *battleSession) closeTalk() {
	if b == nil || b.hud == nil || b.hud.talkPanel == nil {
		return
	}
	b.hud.talkPanel.SetText("TALK", "")
	b.hud.talkPanel.ResetPress()
	b.chat.active = false
}

func (b *battleSession) talkMeasure(index int, text string) int {
	if b == nil || b.hud == nil || b.hud.talkWin == nil || index < 0 || index >= len(b.hud.talkWin.Gadgets) {
		return len(text)
	}
	gad := b.hud.talkWin.Gadgets[index]
	if b.hud.modalFont != nil {
		return retailGAFTextWidth(b.hud.modalFont, text)
	}
	font := b.hud.talkWin.Font(b.hud.fs, gad.FontNumber)
	if font == nil {
		font = b.hud.guiFont
	}
	if font == nil {
		return len(text)
	}
	return client.MeasureText(font, text)
}

func (b *battleSession) serviceTalk(in *input.State) {
	if b == nil || b.hud == nil || !b.chat.active || b.hud.talkPanel == nil || in == nil {
		return
	}
	b.chat.ownsFrame = true
	frame := pointerFrame(in, in.PeekTokens(), false)
	result := b.hud.talkPanel.ServiceFrame(frame, ui.WidgetHooks{Measure: b.talkMeasure})
	if result.Fired && result.FiredIndex == b.hud.talkPanel.Index("TALK") {
		text := b.hud.talkPanel.TextOf("TALK")
		if text != "" {
			b.commitLocalChat(text)
		}
		b.closeTalk()
	}
	// The dialog owns the complete input frame, including records after its
	// closing Enter/Escape. None may reach a battle child opened later.
	in.DiscardTokens(in.PendingTokens())
}

func (b *battleSession) commitLocalChat(text string) {
	if b == nil || b.sess == nil || b.sess.Econ == nil || int(b.sess.LocalOwner) >= len(b.sess.Econ.Players) {
		return
	}
	b.dispatchLocalCommand(text)
	name := b.sess.Econ.Players[b.sess.LocalOwner].Name
	if ring := b.messageRing(); ring != nil {
		ring.Append("<"+name+"> "+text, 4, pool.Handle(0), 10, b.currentTick())
	}
}

// talkOwnedInput preserves pointer position for the presentation pass while
// removing every world-command edge, held button and keyboard state. The
// controller still advances the simulation budget on the frame [07 §3].
func talkOwnedInput(in *input.State, delta float64) input.Sample {
	sample := input.Sample{Elapsed: delta}
	if in != nil && in.Mouse != nil {
		sample.MouseX, sample.MouseY = int32(in.Mouse.X), int32(in.Mouse.Y)
	}
	return sample
}

func (h *retailBattleHUD) drawTalk(c *client.Client, b *battleSession) {
	if h == nil || b == nil || !b.chat.active || h.talkPanel == nil {
		return
	}
	h.drawGUIWindowState(c, h.talkWin, nil, "", h.talkPanel, nil)
}
