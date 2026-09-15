package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// spawnChatCommand captures the submission frame's pointer, before TALK's
// input ownership or a later camera movement can change the requested point.
// Nanolathe Modern policy: DESIGN_INTERFACE_HUD_INPUT "Modern spawn command".
func (b *battleSession) spawnChatCommand(words []string) {
	say := func(text string) {
		if ring := b.messageRing(); ring != nil {
			ring.Append(text, 4, 0, 10, b.currentTick())
		}
	}
	if b.sess == nil {
		return
	}
	if b.sess.Gameplay.Normalize() != gameplay.Modern {
		say("+spawn requires Modern gameplay")
		return
	}
	if len(words) != 2 {
		say("Usage: +spawn <unit>, for example +spawn armck")
		return
	}
	if b.cl == nil || b.cam == nil || b.cl.Input() == nil || b.cl.Input().Mouse == nil {
		return
	}
	mouse := b.cl.Input().Mouse
	x, y := int32(mouse.X), int32(mouse.Y)
	if !b.overWorld(x, y) {
		say("Point at the battlefield before submitting +spawn")
		return
	}
	wx, wy, wz := b.cursorWorld(x, y)
	_ = b.sess.EnqueueHumanCommand(session.HumanCommand{
		Kind:  session.HumanSpawn,
		Spawn: session.HumanSpawnCommand{Unit: words[1], X: wx, Y: wy, Z: wz},
	})
}
