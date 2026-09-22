package ebitenapp

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestHostInputBufferBriefClickAndCatchUpRelease(t *testing.T) {
	var b hostInputBuffer
	// A press and release inside one refresh snapshot has a press edge but
	// no physical hold. Ebitengine still exposes it as down for that snapshot.
	b.add(sampledInput{x: 10, y: 20, buttons: input.MouseButtons{Left: true}, pressedButtons: input.MouseButtons{Left: true}})
	b.add(sampledInput{x: 30, y: 40, timestamp: 5})
	in := input.NewState()
	clicks := doubleClickRecognizer{}
	applyInputWith(in, b.take(), &clicks)
	if p, ok := in.CurrentPointer(); !ok || p.Kind != input.LeftDown || p.X != 30 || p.Y != 40 || p.Timestamp != 5 {
		t.Fatalf("brief click = %+v, valid=%v", p, ok)
	}
	applyInputWith(in, b.take(), &clicks)
	if p, ok := in.CurrentPointer(); !ok || p.Kind != input.LeftUp {
		t.Fatalf("catch-up release = %+v, valid=%v", p, ok)
	}
	applyInputWith(in, b.take(), &clicks)
	if p, _ := in.CurrentPointer(); p.Kind != input.PointerEventNone {
		t.Fatalf("replayed click: %+v", p)
	}
}

func TestHostInputBufferDoesNotRetainReleasedPriorHold(t *testing.T) {
	var b hostInputBuffer
	held := sampledInput{buttons: input.MouseButtons{Left: true, Middle: true, Right: true}}
	held.heldButtons = held.buttons
	held.keys[input.KeyLeft], held.heldKeys[input.KeyLeft] = true, true
	b.add(held)
	b.take()
	// Being held early in the next interval is not a new press.
	b.add(held)
	b.add(sampledInput{})
	got := b.take()
	if got.buttons != (input.MouseButtons{}) || got.keys[input.KeyLeft] {
		t.Fatalf("released prior hold survived: buttons=%+v key=%v", got.buttons, got.keys[input.KeyLeft])
	}
	b.add(held)
	if got := b.take(); got.buttons != held.buttons || !got.keys[input.KeyLeft] {
		t.Fatal("latest physical hold was lost")
	}
	if got := b.take(); got.buttons != held.buttons || !got.keys[input.KeyLeft] {
		t.Fatal("catch-up drain lost physical hold")
	}
}

func TestHostInputBufferPreservesBriefChordAndDefersClipboard(t *testing.T) {
	var b hostInputBuffer
	reads := 0
	paste := sampledInput{modifiers: input.Modifiers{Ctrl: true}, characters: []rune{'v'}, clipboard: func() input.ClipboardText {
		reads++
		return input.ClipboardText{Text: "paste", Available: true}
	}}
	paste.keys[input.KeyV], paste.pressedKeys[input.KeyV] = true, true
	b.add(paste)
	b.add(sampledInput{clipboard: paste.clipboard})
	got := b.take()
	if !got.keys[input.KeyV] || !got.keys[input.KeyCtrl] || !got.modifiers.Ctrl || reads != 0 {
		t.Fatalf("chord lost or clipboard read early: ctrl=%v v=%v reads=%d", got.modifiers.Ctrl, got.keys[input.KeyV], reads)
	}
	in := input.NewState()
	applyInput(in, got)
	tokens := in.DrainTokens()
	if reads != 1 || len(tokens) != 1 || !isPasteToken(tokens[0]) || tokens[0].Clipboard.Text != "paste" {
		t.Fatalf("paste=%+v reads=%d", tokens, reads)
	}
	applyInput(in, b.take())
	if reads != 1 || in.PendingTokens() != 0 || in.Kbd.KeyHeld(input.KeyCtrl) || in.Kbd.KeyHeld(input.KeyV) {
		t.Fatal("catch-up repeated paste or held released chord")
	}
}

func TestHostInputBufferModifierReleaseRetainsCurrentIntervalOnly(t *testing.T) {
	var b hostInputBuffer
	// Ebitengine retains modifiers on their release snapshot even if their
	// press happened in an earlier interval. Catch-up must not retain it.
	b.add(sampledInput{modifiers: input.Modifiers{Shift: true, Ctrl: true, Alt: true}, command: true})
	b.add(sampledInput{})
	got := b.take()
	if !got.modifiers.Shift || !got.modifiers.Ctrl || !got.modifiers.Alt || !got.command {
		t.Fatal("modifier release lost within interval")
	}
	got = b.take()
	if got.modifiers != (input.Modifiers{}) || got.command || got.keys[input.KeyShift] || got.keys[input.KeyCtrl] || got.keys[input.KeyAlt] {
		t.Fatal("modifier release replayed in catch-up")
	}
}

func TestHostInputBufferOrdersTextAndTransfersSlices(t *testing.T) {
	var b hostInputBuffer
	b.add(sampledInput{characters: []rune("first")})
	b.add(sampledInput{modifiers: input.Modifiers{Alt: true}, characters: []rune("ignore")})
	b.add(sampledInput{command: true, characters: []rune("ignore")})
	b.add(sampledInput{characters: []rune("second")})
	queued := b.take()
	b.add(sampledInput{characters: []rune("replacement")})
	b.take()
	in := input.NewState()
	applyInput(in, queued)
	var text []rune
	for _, token := range in.DrainTokens() {
		text = append(text, token.Rune)
	}
	if string(text) != "firstsecond" {
		t.Fatalf("ordered text after later buffer reuse = %q", text)
	}
}

func TestHostInputBufferSumsScrollAndDrainsOnce(t *testing.T) {
	var b hostInputBuffer
	pinches := []input.PinchEvent{{Began: true, Delta: .25}, {Ended: true, Delta: -.125}}
	b.add(sampledInput{wheelX: 1, wheelY: 2, zoomWheelY: 3, panX: 4, panY: 5, pinches: pinches[:1]})
	b.add(sampledInput{wheelX: -.5, wheelY: 3, zoomWheelY: -1, panX: -2, panY: 7, pinches: pinches[1:]})
	got := b.take()
	if got.wheelX != .5 || got.wheelY != 5 || got.zoomWheelY != 2 || got.panX != 2 || got.panY != 12 || !reflect.DeepEqual(got.pinches, pinches) {
		t.Fatalf("scroll batch = %+v", got)
	}
	b.add(sampledInput{pinches: []input.PinchEvent{{Delta: 1}}})
	b.take()
	if !reflect.DeepEqual(got.pinches, pinches) {
		t.Fatal("queued pinch backing storage was reused")
	}
	empty := b.take()
	if empty.wheelX != 0 || empty.wheelY != 0 || empty.zoomWheelY != 0 || empty.panX != 0 || empty.panY != 0 || len(empty.pinches) != 0 || len(empty.characters) != 0 {
		t.Fatalf("one-shot input replayed: %+v", empty)
	}
}

// Model idle 120 Hz collection feeding 30 Hz service, including a queued copy.
// Keep this microbenchmark separate from graphics and input-device costs.
func BenchmarkHostInputBufferIdle(b *testing.B) {
	var buffer hostInputBuffer
	sample := sampledInput{x: 100, y: 200}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buffer.add(sample)
		buffer.add(sample)
		buffer.add(sample)
		buffer.add(sample)
		if got := buffer.take(); got.x != sample.x || got.y != sample.y {
			b.Fatal("pointer lost")
		}
	}
}
