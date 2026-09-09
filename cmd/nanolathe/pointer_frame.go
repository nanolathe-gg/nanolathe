package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// pointerFrame is the one adapter from the published pointer record to the
// widget service. A published record retains its original message kind,
// position, modifiers, buttons, and timestamp; legacy samples without a
// record retain their live-edge behaviour [07 §2][07 R-WGT-01 §1].
func pointerFrame(in *input.State, tokens []input.Token, timerAdvanced bool) ui.WidgetFrame {
	frame := ui.WidgetFrame{Tokens: tokens, TimerAdvanced: timerAdvanced}
	if in == nil {
		return frame
	}
	mouse, _ := in.PointerSample()
	frame.PointerX, frame.PointerY = int32(mouse.X), int32(mouse.Y)
	if mouse.Held(input.MouseButtonLeft) {
		frame.HeldButtons |= 1
	}
	if mouse.Held(input.MouseButtonRight) {
		frame.HeldButtons |= 2
	}
	if event, ok := in.CurrentPointer(); ok {
		frame.PointerEvents = append(frame.PointerEvents, event)
		return frame
	}
	// Manually-authored tests and semantic replay samples predate native
	// records. Keep their live-state edge form without claiming a message
	// identity that was never supplied.
	if mouse.Pressed(input.MouseButtonLeft) {
		frame.PointerEvents = append(frame.PointerEvents, input.PointerEvent{Kind: input.LeftDown, X: frame.PointerX, Y: frame.PointerY})
	}
	if mouse.Released(input.MouseButtonLeft) {
		frame.PointerEvents = append(frame.PointerEvents, input.PointerEvent{Kind: input.LeftUp, X: frame.PointerX, Y: frame.PointerY})
	}
	if mouse.Pressed(input.MouseButtonRight) {
		frame.PointerEvents = append(frame.PointerEvents, input.PointerEvent{Kind: input.RightDown, X: frame.PointerX, Y: frame.PointerY})
	}
	if mouse.Released(input.MouseButtonRight) {
		frame.PointerEvents = append(frame.PointerEvents, input.PointerEvent{Kind: input.RightUp, X: frame.PointerX, Y: frame.PointerY})
	}
	return frame
}

// publishedPointer is for non-widget consumers that need the record's point
// and modifiers while leaving keyboard-held queries on the live input state.
func publishedPointer(in *input.State) (input.MouseState, input.Modifiers) {
	if in == nil {
		return input.MouseState{}, input.Modifiers{}
	}
	return in.PointerSample()
}
