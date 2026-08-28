package main

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/input"
)

// BattleMouseButtons is the logical mouse state consumed by the battle
// controller. Coordinates and button state are expressed in the negotiated
// 640x480 battle surface, not in device pixels [07 §1][07 §8].
type BattleMouseButtons = input.MouseButtons

// BattleModifiers is the platform-neutral modifier state. Keeping modifiers
// in the logical frame makes a replay independent of Ebitengine's key names.
type BattleModifiers = input.Modifiers

// BattleInputFrame is one presentation input sample. It is deliberately a
// value type: the Ebitengine adapter and deterministic replays both submit the
// same frame to BattleController.Step, while the controller alone decides
// which canonical battle command (if any) is issued [07 §8][07 §9].
//
// PressedKeys and HeldKeys carry the complete keyboard state so production
// hotkeys continue to use the existing input decision path. The slices are
// copied by the controller before use; callers may reuse their frame storage.
type BattleInputFrame = input.Sample

// BattleController is the single production/replay input seam. It owns only
// presentation input state and timing; authoritative mutation remains in the
// existing battleSession.handleInput and Session.Step calls.
type BattleController struct {
	battle *battleSession
}

func NewBattleController(b *battleSession) *BattleController {
	return &BattleController{battle: b}
}

// Step feeds one logical input frame through the production battle decision
// path and advances the existing presentation-to-simulation budget. A zero
// elapsed frame is useful for command-only replays and does not tick the
// simulation.
func (c *BattleController) Step(frame BattleInputFrame, cl *client.Client) {
	if c == nil || c.battle == nil {
		return
	}
	c.battle.handleInput(input.StateFromSample(frame), cl)
	if c.battle.ended || c.battle.sess == nil || c.battle.sess.Clock == nil {
		return
	}
	beforeTick := c.battle.sess.Clock.GlobalTick
	if frame.Elapsed >= 0 && !math.IsNaN(frame.Elapsed) && !math.IsInf(frame.Elapsed, 0) {
		c.battle.msAccum += frame.Elapsed * 1000
	}
	// Invalid presentation samples do not alter the accumulator, but still
	// reuse its current scaled sample. Passing zero here would move the
	// scheduler anchor backwards after a prior valid frame [01 §4.2].
	scaled := int64(c.battle.msAccum * 30 / 1000)
	if scaled > 1<<30 {
		scaled = 1 << 30
	}
	c.battle.sess.Step(int32(scaled))
	// Registered model-texture players advance once for each simulation tick
	// [03 §4.4].
	if ran := c.battle.sess.Clock.GlobalTick - beforeTick; ran > 0 && cl != nil {
		cl.TickTextureAnimators(int(ran))
		if cursors := cl.Cursors(); cursors != nil {
			cursors.Step(int(ran))
		}
	}
}
