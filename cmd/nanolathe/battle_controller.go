package main

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/input"
)

// BattleMouseButtons is the logical mouse state consumed by the battle
// controller. Coordinates and button state are expressed in the negotiated
// 640x480 battle surface, not in device pixels [07 §1][07 §8].
type BattleMouseButtons struct {
	Left   bool
	Middle bool
	Right  bool
}

// BattleModifiers is the platform-neutral modifier state. Keeping modifiers
// in the logical frame makes a replay independent of Ebitengine's key names.
type BattleModifiers struct {
	Shift bool
	Ctrl  bool
	Alt   bool
}

// BattleInputFrame is one presentation input sample. It is deliberately a
// value type: the Ebitengine adapter and deterministic replays both submit the
// same frame to BattleController.Step, while the controller alone decides
// which canonical battle command (if any) is issued [07 §8][07 §9].
//
// PressedKeys and HeldKeys carry the complete keyboard state so production
// hotkeys continue to use the existing input decision path. The slices are
// copied by the controller before use; callers may reuse their frame storage.
type BattleInputFrame struct {
	MouseX, MouseY int32
	Buttons        BattleMouseButtons
	Modifiers      BattleModifiers
	Escape         bool
	WheelX, WheelY float32
	Elapsed        float64
	PressedKeys    []input.Key
	HeldKeys       []input.Key
}

// BattleInputFrameFromClient converts the production Ebitengine snapshot into
// the logical frame consumed by BattleController. This is the only adapter
// from presentation input to battle input; replay code does not inspect or
// mutate client.InputState.
func BattleInputFrameFromClient(in *client.InputState, elapsed float64) BattleInputFrame {
	f := BattleInputFrame{Elapsed: elapsed}
	if in == nil {
		return f
	}
	if in.Mouse != nil {
		f.MouseX = logicalBattleCoordinate(in.Mouse.X, 640)
		f.MouseY = logicalBattleCoordinate(in.Mouse.Y, 480)
		f.Buttons = BattleMouseButtons{
			Left:   in.Mouse.Held(input.MouseButtonLeft),
			Middle: in.Mouse.Held(input.MouseButtonMiddle),
			Right:  in.Mouse.Held(input.MouseButtonRight),
		}
		f.WheelX, f.WheelY = in.Mouse.ScrollX, in.Mouse.ScrollY
	}
	if in.Kbd != nil {
		for key := input.Key(1); key < input.KeyCount; key++ {
			if in.Kbd.KeyDown(key) {
				f.PressedKeys = append(f.PressedKeys, key)
			}
			if in.Kbd.KeyHeld(key) {
				f.HeldKeys = append(f.HeldKeys, key)
			}
		}
		f.Modifiers = BattleModifiers{
			Shift: in.Kbd.HasShift(),
			Ctrl:  in.Kbd.KeyHeld(input.KeyCtrl),
			Alt:   in.Kbd.KeyHeld(input.KeyAlt),
		}
		f.Escape = in.Kbd.KeyDown(input.KeyEscape)
	}
	return f
}

func logicalBattleCoordinate(v float32, limit int32) int32 {
	if math.IsNaN(float64(v)) || v <= 0 {
		return 0
	}
	max := float32(limit - 1)
	if v >= max {
		return limit - 1
	}
	return int32(v)
}

// BattleController is the single production/replay input seam. It owns only
// presentation input state and timing; authoritative mutation remains in the
// existing battleSession.handleInput and Session.Step calls.
type BattleController struct {
	battle *battleSession
	in     *client.InputState
}

func NewBattleController(b *battleSession) *BattleController {
	return &BattleController{battle: b, in: &client.InputState{
		Mouse: &client.MouseState{},
		Kbd:   &client.KeyboardState{},
	}}
}

// Step feeds one logical input frame through the production battle decision
// path and advances the existing presentation-to-simulation budget. A zero
// elapsed frame is useful for command-only replays and does not tick the
// simulation.
func (c *BattleController) Step(frame BattleInputFrame, cl *client.Client) {
	if c == nil || c.battle == nil || c.in == nil {
		return
	}
	c.applyInput(frame)
	c.battle.handleInput(c.in, cl)
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
	// Animated model textures tick with the simulation frame count
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if ran := c.battle.sess.Clock.GlobalTick - beforeTick; ran > 0 && cl != nil {
		cl.TickTextureAnimators(int(ran))
		if cursors := cl.Cursors(); cursors != nil {
			cursors.Step(int(ran))
		}
	}
}

func (c *BattleController) applyInput(frame BattleInputFrame) {
	m := c.in.Mouse
	k := c.in.Kbd
	m.ClearEdges()
	k.ClearEdges()
	m.InjectMouseMove(float32(logicalBattleCoordinate(float32(frame.MouseX), 640)), float32(logicalBattleCoordinate(float32(frame.MouseY), 480)))
	m.InjectMouseButton(input.MouseButtonLeft, frame.Buttons.Left)
	m.InjectMouseButton(input.MouseButtonMiddle, frame.Buttons.Middle)
	m.InjectMouseButton(input.MouseButtonRight, frame.Buttons.Right)
	m.InjectWheel(frame.WheelX, frame.WheelY)
	var desired [input.KeyCount]bool
	for _, key := range frame.PressedKeys {
		if key > input.KeyNone && key < input.KeyCount {
			desired[key] = true
		}
	}
	for _, key := range frame.HeldKeys {
		if key > input.KeyNone && key < input.KeyCount {
			desired[key] = true
		}
	}
	// Explicit modifiers are also accepted for replay authors that do not
	// need to enumerate the complete key vocabulary.
	desired[input.KeyShift] = desired[input.KeyShift] || frame.Modifiers.Shift
	desired[input.KeyCtrl] = desired[input.KeyCtrl] || frame.Modifiers.Ctrl
	desired[input.KeyAlt] = desired[input.KeyAlt] || frame.Modifiers.Alt
	desired[input.KeyEscape] = desired[input.KeyEscape] || frame.Escape
	// Apply the complete desired state exactly once per key. InjectKey compares
	// against the prior held state, preserving press/release edges while a
	// continuously-held key does not retrigger [07 §2].
	for key := input.Key(1); key < input.KeyCount; key++ {
		k.InjectKey(key, desired[key])
	}
}
