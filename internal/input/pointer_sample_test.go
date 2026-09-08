package input

import "testing"

func TestPointerSampleUsesQueuedEdgesAndModifiersWithoutChangingLiveKeys(t *testing.T) {
	in := NewState()
	down := PointerEvent{Kind: LeftDown, X: 12, Y: 34, Buttons: MouseButtons{Left: true}, Modifiers: Modifiers{Shift: true}}
	up := PointerEvent{Kind: LeftUp, X: 56, Y: 78, Modifiers: Modifiers{Ctrl: true}}
	in.EnqueuePointer(down)
	in.EnqueuePointer(up)
	in.UpdatePointerMotion(PointerEvent{X: 90, Y: 91})
	in.Kbd.SetKey(KeyAlt, true)
	for _, want := range []PointerEvent{down, up} {
		in.PublishPointer()
		for consumer := 0; consumer < 2; consumer++ {
			mouse, modifiers := in.PointerSample()
			if mouse.X != float32(want.X) || mouse.Y != float32(want.Y) || modifiers != want.Modifiers || mouse.Held(MouseButtonLeft) != want.Buttons.Left || mouse.Pressed(MouseButtonLeft) != (want.Kind == LeftDown) || mouse.Released(MouseButtonLeft) != (want.Kind == LeftUp) {
				t.Fatalf("queued record projection = %+v %+v, want %+v", mouse, modifiers, want)
			}
			if !in.Kbd.KeyHeld(KeyAlt) || in.Kbd.HasShift() || in.Mouse.Held(MouseButtonLeft) || in.Mouse.X != 90 {
				t.Fatal("pointer projection changed the independent live sample")
			}
		}
	}
	in.PublishPointer()
	mouse, _ := in.PointerSample()
	if mouse.X != 90 || mouse.Pressed(MouseButtonLeft) || mouse.Released(MouseButtonLeft) {
		t.Fatal("motion fallback repeated a queued button edge")
	}
}
