package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestPointerFrameRetainsPublishedDoubleClickRecord(t *testing.T) {
	in := input.NewState()
	want := input.PointerEvent{
		Kind:      input.LeftDoubleClick,
		X:         41,
		Y:         73,
		Buttons:   input.MouseButtons{Left: true, Right: true},
		Modifiers: input.Modifiers{Ctrl: true, Shift: true},
		Timestamp: 99,
	}
	if !in.EnqueuePointer(want) || !in.PublishPointer() {
		t.Fatal("publish double-click record")
	}
	// Later live motion and key state must not rebuild the widget event.
	in.UpdatePointerMotion(input.PointerEvent{X: 300, Y: 301})
	in.Kbd.SetKey(input.KeyAlt, true)

	frame := pointerFrame(in, nil, false)
	if frame.PointerX != want.X || frame.PointerY != want.Y || frame.HeldButtons != 3 || len(frame.PointerEvents) != 1 || frame.PointerEvents[0] != want {
		t.Fatalf("widget frame = %+v, want published double-click %+v", frame, want)
	}
}

func TestPointerFrameDoesNotSynthesizeMoreThanPublishedRecord(t *testing.T) {
	in := input.NewState()
	if !in.EnqueuePointer(input.PointerEvent{Kind: input.LeftDown, X: 8, Y: 9, Buttons: input.MouseButtons{Left: true}}) || !in.EnqueuePointer(input.PointerEvent{Kind: input.LeftUp, X: 10, Y: 11}) || !in.PublishPointer() {
		t.Fatal("publish queued down")
	}
	frame := pointerFrame(in, nil, false)
	if len(frame.PointerEvents) != 1 || frame.PointerEvents[0].Kind != input.LeftDown {
		t.Fatalf("frame events = %#v, want only first published edge", frame.PointerEvents)
	}
}
