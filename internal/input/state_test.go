package input

import (
	"reflect"
	"testing"
)

func TestStateFromSamplePreservesExplicitEdges(t *testing.T) {
	tests := []struct {
		name        string
		sample      Sample
		wantDown    bool
		wantHeld    bool
		wantMouse   bool
		wantRelease bool
	}{
		{name: "idle", sample: Sample{}},
		{name: "held without edge", sample: Sample{HeldKeys: []Key{KeyEscape}, Buttons: MouseButtons{Left: true}}, wantHeld: true, wantMouse: true},
		{name: "press", sample: Sample{PressedKeys: []Key{KeyEscape}, HeldKeys: []Key{KeyEscape}, Buttons: MouseButtons{Left: true}, PressedButtons: [4]bool{false, true}}, wantDown: true, wantHeld: true, wantMouse: true},
		{name: "release", sample: Sample{ReleasedButtons: [4]bool{false, true}}, wantRelease: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := StateFromSample(tt.sample)
			if got := in.Kbd.KeyDown(KeyEscape); got != tt.wantDown {
				t.Fatalf("escape KeyDown=%v want %v", got, tt.wantDown)
			}
			if got := in.Kbd.KeyHeld(KeyEscape); got != tt.wantHeld {
				t.Fatalf("escape KeyHeld=%v want %v", got, tt.wantHeld)
			}
			if got := in.Mouse.Held(MouseButtonLeft); got != tt.wantMouse {
				t.Fatalf("left Held=%v want %v", got, tt.wantMouse)
			}
			if got := in.Mouse.Pressed(MouseButtonLeft); got != (tt.name == "press") {
				t.Fatalf("left Pressed=%v want %v", got, tt.name == "press")
			}
			if got := in.Mouse.Released(MouseButtonLeft); got != tt.wantRelease {
				t.Fatalf("left Released=%v want %v", got, tt.wantRelease)
			}
		})
	}
}

func TestStatePointerQueueRetainsSameIntervalButtonOrder(t *testing.T) {
	in := NewState()
	if !in.EnqueuePointer(PointerEvent{Kind: LeftDown, X: 10, Y: 20}) {
		t.Fatal("down enqueue failed")
	}
	if !in.EnqueuePointer(PointerEvent{Kind: LeftUp, X: 11, Y: 21}) {
		t.Fatal("up enqueue failed")
	}
	if in.Mouse.Held(MouseButtonLeft) {
		t.Fatal("live left held state survived up")
	}
	for _, want := range []PointerEventKind{LeftDown, LeftUp} {
		event, queued := in.PopPointer()
		if !queued || event.Kind != want {
			t.Fatalf("PopPointer() = %+v, queued=%v; want %v, true", event, queued, want)
		}
	}
}

func TestStatePointerQueueKeepsEventModifiersAndTimestamp(t *testing.T) {
	in := NewState()
	event := PointerEvent{Kind: LeftDoubleClick, X: 12, Y: 34, Modifiers: Modifiers{Shift: true, Ctrl: true}, Timestamp: 77}
	in.Kbd.SetKey(KeyShift, true)
	in.Kbd.SetKey(KeyCtrl, true)
	if !in.EnqueuePointer(event) {
		t.Fatal("enqueue failed")
	}
	in.Kbd.SetKey(KeyShift, false)
	in.Kbd.SetKey(KeyCtrl, false)
	got, queued := in.PopPointer()
	if !queued {
		t.Fatal("PopPointer used motion fallback")
	}
	if got.Kind != LeftDoubleClick || got.Modifiers != event.Modifiers || got.Timestamp != event.Timestamp {
		t.Fatalf("queued event = %+v, want double-click identity, original modifiers, and timestamp", got)
	}
}

func TestStatePointerQueueUsesLatestMotionFallback(t *testing.T) {
	in := NewState()
	in.UpdatePointerMotion(PointerEvent{Kind: LeftDown, X: 7, Y: 8, Modifiers: Modifiers{Alt: true}, Timestamp: 10})
	if !in.EnqueuePointer(PointerEvent{Kind: RightDown, X: 20, Y: 21, Timestamp: 11}) {
		t.Fatal("button enqueue failed")
	}
	in.UpdatePointerMotion(PointerEvent{Kind: RightUp, X: 30, Y: 31, Modifiers: Modifiers{Shift: true}, Timestamp: 12})

	button, queued := in.PopPointer()
	if !queued || button.Kind != RightDown {
		t.Fatalf("first PopPointer() = %+v, queued=%v; want queued right down", button, queued)
	}
	motion, queued := in.PopPointer()
	if queued || motion.Kind != PointerEventNone || motion.X != 30 || motion.Y != 31 || !motion.Modifiers.Shift || motion.Timestamp != 12 {
		t.Fatalf("motion fallback = %+v, queued=%v", motion, queued)
	}
}

func TestStatePointerQueueFullRefusalStillUpdatesLiveButtons(t *testing.T) {
	in := NewState()
	for sequence := 0; sequence < pointerRingSlots-1; sequence++ {
		if !in.EnqueuePointer(PointerEvent{Kind: LeftDown, X: int32(sequence)}) {
			t.Fatalf("enqueue %d failed", sequence)
		}
	}
	if in.EnqueuePointer(PointerEvent{Kind: RightDown, X: 99}) {
		t.Fatal("full queue accepted right down")
	}
	if !in.Mouse.Held(MouseButtonRight) {
		t.Fatal("full queue refusal lost live right held state")
	}
}

// The current pointer record can lag behind physical held state [07 §2]
// [07 R-CAM-01 §11]. Alternate drag must see the serviced record's right bit.
func TestStatePointerQueueKeepsEventButtons(t *testing.T) {
	in := NewState()
	down := PointerEvent{Kind: RightDown, Buttons: MouseButtons{Right: true}, Modifiers: Modifiers{Ctrl: true}, Timestamp: 8}
	up := PointerEvent{Kind: RightUp, Timestamp: 9}
	if !in.EnqueuePointer(down) || !in.EnqueuePointer(up) {
		t.Fatal("enqueue failed")
	}
	if in.Mouse.Held(MouseButtonRight) {
		t.Fatal("latest live right state remains held")
	}
	for _, want := range []PointerEvent{down, up} {
		got, queued := in.PopPointer()
		if !queued || got != want {
			t.Fatalf("pointer = %+v, queued=%v, want %+v", got, queued, want)
		}
	}
	motion := PointerEvent{X: 30, Buttons: MouseButtons{Left: true, Middle: true}, Timestamp: 10}
	in.UpdatePointerMotion(motion)
	got, queued := in.PopPointer()
	if queued || got != motion {
		t.Fatalf("motion = %+v, queued=%v, want %+v", got, queued, motion)
	}
}

func TestStatePointerQueueRejectsInvalidBeforeMutation(t *testing.T) {
	for _, kind := range []PointerEventKind{PointerEventNone, PointerEventKind(255)} {
		in := NewState()
		in.EnqueuePointer(PointerEvent{Kind: LeftDown, X: 4, Y: 6})
		in.Mouse.ResetEdges()
		beforeMouse, beforeQueue := *in.Mouse, in.pointers
		if in.EnqueuePointer(PointerEvent{Kind: kind, X: 90, Y: 91, Buttons: MouseButtons{Right: true}}) {
			t.Fatal("invalid kind accepted")
		}
		if !reflect.DeepEqual(*in.Mouse, beforeMouse) || in.pointers != beforeQueue {
			t.Fatalf("kind %d changed live pointer or queue", kind)
		}
	}
}

func TestStatePublishesOneCachedPointerRecord(t *testing.T) {
	in := NewState()
	down := PointerEvent{Kind: LeftDown, X: 10, Y: 20, Modifiers: Modifiers{Shift: true}, Buttons: MouseButtons{Left: true}, Timestamp: 30}
	up := PointerEvent{Kind: LeftUp, X: 11, Y: 21, Buttons: MouseButtons{}, Timestamp: 31}
	if !in.EnqueuePointer(down) || !in.EnqueuePointer(up) {
		t.Fatal("enqueue failed")
	}
	if !in.PublishPointer() {
		t.Fatal("first publication failed")
	}
	for consumer := 0; consumer < 2; consumer++ {
		got, ok := in.CurrentPointer()
		if !ok || got != down {
			t.Fatalf("consumer %d current = %+v, valid=%v; want %+v", consumer, got, ok, down)
		}
	}
	if got := in.pointers.Len(); got != 1 {
		t.Fatalf("pending after one publication = %d, want 1", got)
	}
	if !in.PublishPointer() {
		t.Fatal("second publication failed")
	}
	if got, ok := in.CurrentPointer(); !ok || got != up {
		t.Fatalf("second current = %+v, valid=%v; want %+v", got, ok, up)
	}
}

func TestPointerPublicationRoundTripsIndependentlyOfLiveState(t *testing.T) {
	in := NewState()
	record := PointerEvent{Kind: RightDown, X: 40, Y: 50, Modifiers: Modifiers{Ctrl: true}, Buttons: MouseButtons{Right: true}, Timestamp: 99}
	if !in.EnqueuePointer(record) || !in.PublishPointer() {
		t.Fatal("pointer publication failed")
	}
	// The service record remains the right-down snapshot even after the live
	// device state advances before its copied consumer reads it.
	in.Kbd.SetKey(KeyCtrl, false)
	in.Kbd.SetKey(KeyAlt, true)
	in.Mouse.SetButton(MouseButtonRight, false)
	in.Mouse.SetButton(MouseButtonLeft, true)

	sample := SampleFromState(in, 0, SurfaceWidth, SurfaceHeight)
	if !sample.PointerValid || sample.Pointer != record {
		t.Fatalf("sample pointer = %+v, valid=%v; want %+v", sample.Pointer, sample.PointerValid, record)
	}
	if sample.Buttons.Right || !sample.Buttons.Left || sample.Modifiers.Ctrl || !sample.Modifiers.Alt {
		t.Fatalf("sample live state = buttons=%+v modifiers=%+v; want newer state", sample.Buttons, sample.Modifiers)
	}
	restored := StateFromSample(sample)
	if got, ok := restored.CurrentPointer(); !ok || got != record {
		t.Fatalf("restored current = %+v, valid=%v; want %+v", got, ok, record)
	}
	if restored.Mouse.Held(MouseButtonRight) || !restored.Mouse.Held(MouseButtonLeft) || restored.Kbd.KeyHeld(KeyCtrl) || !restored.Kbd.KeyHeld(KeyAlt) {
		t.Fatal("round trip did not retain independent newer live state")
	}
}

func TestPointerQueueRecordsSurviveSeparatePublishedSampleCopies(t *testing.T) {
	in := NewState()
	down := PointerEvent{Kind: LeftDown, X: 4, Y: 5, Modifiers: Modifiers{Shift: true}, Buttons: MouseButtons{Left: true}, Timestamp: 41}
	up := PointerEvent{Kind: LeftUp, X: 6, Y: 7, Modifiers: Modifiers{Alt: true}, Timestamp: 42}
	if !in.EnqueuePointer(down) || !in.EnqueuePointer(up) {
		t.Fatal("queue setup failed")
	}
	for _, want := range []PointerEvent{down, up} {
		if !in.PublishPointer() {
			t.Fatal("publication failed")
		}
		copied := StateFromSample(SampleFromState(in, 0, SurfaceWidth, SurfaceHeight))
		if got, ok := copied.CurrentPointer(); !ok || got != want {
			t.Fatalf("published sample record = %+v, valid=%v; want %+v", got, ok, want)
		}
	}
}

func TestLegacySampleDoesNotMaterializePointerHistory(t *testing.T) {
	in := StateFromSample(Sample{PressedButtons: [4]bool{false, true}, Buttons: MouseButtons{Left: true}})
	if _, ok := in.CurrentPointer(); ok {
		t.Fatal("legacy edge sample manufactured a pointer-history record")
	}
}
