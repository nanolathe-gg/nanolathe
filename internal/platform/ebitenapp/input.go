package ebitenapp

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// sampledInput is one complete Ebiten poll. The adapter collects all of these
// current values before it creates an event record; Ebiten does not expose the
// native ordering of changes that happened between polls [07 §2].
type sampledInput struct {
	x, y       int32
	buttons    input.MouseButtons
	modifiers  input.Modifiers
	wheelX     float32
	wheelY     float32
	zoomWheelY float32
	panX, panY float64
	pinches    []input.PinchEvent
	keys       [input.KeyCount]bool
	characters []rune
	timestamp  uint32
}

// readInput is the production device-polling path. Host shortcuts are consumed
// before applyInput publishes the platform-neutral state [I6]. timestamp is
// the scaled 30-Hz host clock supplied by app.
func readInput(timestamp uint32) sampledInput {
	cx, cy := ebiten.CursorPosition()
	sample := sampledInput{
		x: int32(cx), y: int32(cy), timestamp: timestamp,
		buttons: input.MouseButtons{
			Left:   ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft),
			Middle: ebiten.IsMouseButtonPressed(ebiten.MouseButtonMiddle),
			Right:  ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight),
		},
		modifiers: input.Modifiers{
			Shift: ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight),
			Ctrl:  ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight),
			Alt:   ebiten.IsKeyPressed(ebiten.KeyAltLeft) || ebiten.IsKeyPressed(ebiten.KeyAltRight),
		},
		characters: ebiten.AppendInputChars(nil),
	}
	wx, wy := ebiten.Wheel()
	scroll := nativeScroll.take(wx, wy)
	sample.wheelX, sample.wheelY = float32(scroll.x), float32(scroll.y)
	sample.zoomWheelY = float32(scroll.zoomY)
	sample.panX, sample.panY, sample.pinches = scroll.panX, scroll.panY, scroll.pinches
	for key := input.Key(1); key < input.KeyCount; key++ {
		switch key {
		case input.KeyShift:
			sample.keys[key] = sample.modifiers.Shift
		case input.KeyCtrl:
			sample.keys[key] = sample.modifiers.Ctrl
		case input.KeyAlt:
			sample.keys[key] = sample.modifiers.Alt
		default:
			if ek, ok := ebitenKey(key); ok {
				sample.keys[key] = ebiten.IsKeyPressed(ek)
			}
		}
	}
	return sample
}

// applyInput establishes the one host service's live state and one published
// pointer record. The transition enumeration is only an observation order for
// a polling API; it does not claim to restore native message chronology
// [07 §2][01 R-PLAT-01 §6].
func applyInput(in *input.State, sample sampledInput) {
	if in == nil || in.Mouse == nil || in.Kbd == nil {
		return
	}
	m, k := in.Mouse, in.Kbd
	m.ResetEdges()
	k.ResetEdges()

	for key := input.Key(1); key < input.KeyCount; key++ {
		wasHeld := k.KeyHeld(key)
		down := sample.keys[key]
		k.SetKey(key, down)
		if down && !wasHeld && keyboardTokenKey(key) {
			in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: key})
		}
	}

	wasLeft := m.Held(input.MouseButtonLeft)
	wasRight := m.Held(input.MouseButtonRight)
	m.SetPosition(float32(sample.x), float32(sample.y))
	m.SetWheel(sample.wheelX, sample.wheelY)
	m.ZoomScrollY = sample.zoomWheelY
	m.PanX, m.PanY = sample.panX, sample.panY
	m.Pinches = append([]input.PinchEvent(nil), sample.pinches...)
	// Middle has no semantic pointer record, but remains an independent held
	// sample for legacy consumers.
	m.SetButton(input.MouseButtonMiddle, sample.buttons.Middle)

	event := input.PointerEvent{
		X: sample.x, Y: sample.y, Modifiers: sample.modifiers,
		Buttons: sample.buttons, Timestamp: sample.timestamp,
	}
	in.UpdatePointerMotion(event)
	if wasLeft != sample.buttons.Left {
		if sample.buttons.Left {
			event.Kind = input.LeftDown
		} else {
			event.Kind = input.LeftUp
		}
		in.EnqueuePointer(event)
	}
	if wasRight != sample.buttons.Right {
		if sample.buttons.Right {
			event.Kind = input.RightDown
		} else {
			event.Kind = input.RightUp
		}
		in.EnqueuePointer(event)
	}
	// TODO(T25): Ebiten polling exposes no native message order, key-repeat
	// history, or double-click identity. Do not synthesize those details here.
	in.PublishPointer()

	// AppendInputChars preserves character order within its own batch. Ebiten
	// does not order that batch against the polled physical transitions above.
	for _, r := range sample.characters {
		in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
}

func keyboardTokenKey(key input.Key) bool {
	switch key {
	case input.KeyBackspace, input.KeyDelete, input.KeyHome, input.KeyEnd,
		input.KeyLeft, input.KeyRight, input.KeyUp, input.KeyDown, input.KeyTab,
		input.KeyEnter, input.KeyEscape:
		return true
	}
	return false
}

func ebitenKey(k input.Key) (ebiten.Key, bool) {
	switch k {
	case input.KeyA:
		return ebiten.KeyA, true
	case input.KeyB:
		return ebiten.KeyB, true
	case input.KeyC:
		return ebiten.KeyC, true
	case input.KeyD:
		return ebiten.KeyD, true
	case input.KeyE:
		return ebiten.KeyE, true
	case input.KeyF:
		return ebiten.KeyF, true
	case input.KeyG:
		return ebiten.KeyG, true
	case input.KeyH:
		return ebiten.KeyH, true
	case input.KeyI:
		return ebiten.KeyI, true
	case input.KeyJ:
		return ebiten.KeyJ, true
	case input.KeyK:
		return ebiten.KeyK, true
	case input.KeyL:
		return ebiten.KeyL, true
	case input.KeyM:
		return ebiten.KeyM, true
	case input.KeyN:
		return ebiten.KeyN, true
	case input.KeyO:
		return ebiten.KeyO, true
	case input.KeyP:
		return ebiten.KeyP, true
	case input.KeyQ:
		return ebiten.KeyQ, true
	case input.KeyR:
		return ebiten.KeyR, true
	case input.KeyS:
		return ebiten.KeyS, true
	case input.KeyT:
		return ebiten.KeyT, true
	case input.KeyU:
		return ebiten.KeyU, true
	case input.KeyV:
		return ebiten.KeyV, true
	case input.KeyW:
		return ebiten.KeyW, true
	case input.KeyX:
		return ebiten.KeyX, true
	case input.KeyY:
		return ebiten.KeyY, true
	case input.KeyZ:
		return ebiten.KeyZ, true
	case input.Key0:
		return ebiten.Key0, true
	case input.Key1:
		return ebiten.Key1, true
	case input.Key2:
		return ebiten.Key2, true
	case input.Key3:
		return ebiten.Key3, true
	case input.Key4:
		return ebiten.Key4, true
	case input.Key5:
		return ebiten.Key5, true
	case input.Key6:
		return ebiten.Key6, true
	case input.Key7:
		return ebiten.Key7, true
	case input.Key8:
		return ebiten.Key8, true
	case input.Key9:
		return ebiten.Key9, true
	case input.KeyF1:
		return ebiten.KeyF1, true
	case input.KeyF2:
		return ebiten.KeyF2, true
	case input.KeyF3:
		return ebiten.KeyF3, true
	case input.KeyF4:
		return ebiten.KeyF4, true
	case input.KeyF5:
		return ebiten.KeyF5, true
	case input.KeyF6:
		return ebiten.KeyF6, true
	case input.KeyF7:
		return ebiten.KeyF7, true
	case input.KeyF8:
		return ebiten.KeyF8, true
	case input.KeyF9:
		return ebiten.KeyF9, true
	case input.KeyF10:
		return ebiten.KeyF10, true
	case input.KeyF11:
		return ebiten.KeyF11, true
	case input.KeyF12:
		return ebiten.KeyF12, true
	case input.KeyLeft:
		return ebiten.KeyLeft, true
	case input.KeyUp:
		return ebiten.KeyUp, true
	case input.KeyRight:
		return ebiten.KeyRight, true
	case input.KeyDown:
		return ebiten.KeyDown, true
	case input.KeyHome:
		return ebiten.KeyHome, true
	case input.KeyEnd:
		return ebiten.KeyEnd, true
	case input.KeyPrior:
		return ebiten.KeyPageUp, true
	case input.KeyNext:
		return ebiten.KeyPageDown, true
	case input.KeyInsert:
		return ebiten.KeyInsert, true
	case input.KeyDelete:
		return ebiten.KeyDelete, true
	case input.KeyBackspace:
		return ebiten.KeyBackspace, true
	case input.KeyTab:
		return ebiten.KeyTab, true
	case input.KeySpace:
		return ebiten.KeySpace, true
	case input.KeyEnter:
		return ebiten.KeyEnter, true
	case input.KeyEscape:
		return ebiten.KeyEscape, true
	case input.KeyPause:
		return ebiten.KeyPause, true
	case input.KeyMinus:
		return ebiten.KeyMinus, true
	case input.KeyEqual:
		return ebiten.KeyEqual, true
	case input.KeyNumpadAdd:
		return ebiten.KeyNumpadAdd, true
	case input.KeyNumpadSubtract:
		return ebiten.KeyNumpadSubtract, true
	case input.KeyBackquote:
		return ebiten.KeyBackquote, true
	case input.KeyComma:
		return ebiten.KeyComma, true
	case input.KeyPeriod:
		return ebiten.KeyPeriod, true
	case input.KeyShift:
		return ebiten.KeyShiftLeft, true
	case input.KeyCtrl:
		return ebiten.KeyControlLeft, true
	case input.KeyAlt:
		return ebiten.KeyAltLeft, true
	}
	return 0, false
}
