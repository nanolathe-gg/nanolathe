package ebitenapp

import (
	"runtime"

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
	command    bool
	clipboard  func() input.ClipboardText
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
		command:    runtime.GOOS == "darwin" && (ebiten.IsKeyPressed(ebiten.KeyMetaLeft) || ebiten.IsKeyPressed(ebiten.KeyMetaRight)),
		clipboard:  readHostClipboard,
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
	applyInputWith(in, sample, &nativeDoubleClick)
}

// applyInputWith is applyInput against a caller-supplied double-click
// recognizer, so a test can drive a press pair without the process-wide one.
func applyInputWith(in *input.State, sample sampledInput, clicks *doubleClickRecognizer) {
	if in == nil || in.Mouse == nil || in.Kbd == nil {
		return
	}
	in.ShortcutTokenMode = true
	in.ShortcutToken = input.Token{}
	m, k := in.Mouse, in.Kbd
	m.ResetEdges()
	k.ResetEdges()

	for key := input.Key(1); key < input.KeyCount; key++ {
		wasHeld := k.KeyHeld(key)
		down := sample.keys[key]
		if sample.command && key != input.KeyV && key != input.KeyShift && key != input.KeyCtrl && key != input.KeyAlt {
			down = false
		}
		k.SetKey(key, down)
		if down && !wasHeld {
			if token, ok := sampledKeyToken(key, sample); ok {
				if isPasteToken(token) && sample.clipboard != nil {
					token.Clipboard = portableClipboardText(sample.clipboard())
				}
				in.EnqueueToken(token)
			}
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
			// The second press of a pair carries the double-click identity
			// instead of a plain press, the way the operating system replaced
			// the second press message in retail. One transition stays one
			// record: publication takes a single record per host service, and
			// the widget pass treats the double-click as a press that also
			// fires [07 R-WGT-01 §4]. See doubleclick.go for the host policy.
			event.Kind = input.LeftDown
			if clicks != nil && clicks.press(sample.x, sample.y, sample.timestamp) {
				event.Kind = input.LeftDoubleClick
			}
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
	// TODO(T25): Ebiten polling exposes no native message order and no
	// key-repeat history. Do not synthesize those details here. The left
	// double-click above is the one reconstruction, and only because its
	// inputs — interval and rectangle — are host settings rather than retail
	// behaviour.
	in.PublishPointer()

	// AppendInputChars preserves character order within its own batch. Ebiten
	// does not order that batch against the polled physical transitions above.
	// Alt system-key translation supplies its own raw character token and
	// has no character-message companion in retail [07 R-CAM-01 §14].
	if sample.modifiers.Alt || sample.command {
		return
	}
	for _, r := range sample.characters {
		// Some hosts deliver a printable companion to Ctrl+V. The composed
		// paste token already owns that input; never append a stray v.
		if sample.modifiers.Ctrl && sample.keys[input.KeyV] && (r == 'v' || r == 'V' || r == '\x16') {
			continue
		}
		in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
}

// Cmd+V is a host alias for the retail Ctrl+V token, not a Ctrl held-state
// alias. Other Cmd combinations produce no game keyboard tokens.
func sampledKeyToken(key input.Key, sample sampledInput) (input.Token, bool) {
	if sample.command {
		if key == input.KeyV && !sample.modifiers.Alt {
			return input.Token{Kind: input.TokenEdit, Key: input.KeyV, Ctrl: true}, true
		}
		return input.Token{}, false
	}
	return translatedKeyToken(key, sample.modifiers)
}

func isPasteToken(token input.Token) bool {
	return token.Kind == input.TokenEdit && (token.Key == input.KeyInsert || token.Key == input.KeyV && token.Ctrl)
}

func keyboardTokenKey(key input.Key) bool {
	switch key {
	case input.KeyBackspace, input.KeyDelete, input.KeyInsert, input.KeyHome, input.KeyEnd,
		input.KeyPrior, input.KeyNext, input.KeyPause,
		input.KeyLeft, input.KeyRight, input.KeyUp, input.KeyDown, input.KeyTab,
		input.KeyEnter, input.KeyEscape:
		return true
	}
	return key >= input.KeyF1 && key <= input.KeyF12
}

// translatedKeyToken is the observed key-down translator. Ordinary text stays
// with the host character batch; Ctrl composition and Alt system keys retain
// their separate established identities [07 §2][07 R-CAM-01 §14].
func translatedKeyToken(key input.Key, modifiers input.Modifiers) (input.Token, bool) {
	composable := key >= input.KeyA && key <= input.KeyZ || key >= input.Key0 && key <= input.Key9 || key >= input.KeyF1 && key <= input.KeyF12
	if modifiers.Ctrl && composable {
		return input.Token{Kind: input.TokenEdit, Key: key, Ctrl: true}, true
	}
	if keyboardTokenKey(key) {
		return input.Token{Kind: input.TokenEdit, Key: key}, true
	}
	if modifiers.Alt || modifiers.Ctrl {
		var r rune
		switch {
		case key >= input.KeyA && key <= input.KeyZ:
			r = 'a' + rune(key-input.KeyA)
		case key >= input.Key0 && key <= input.Key9:
			r = '0' + rune(key-input.Key0)
		default:
			switch key {
			case input.KeyMinus:
				r = '-'
			case input.KeyEqual:
				r = '='
			case input.KeyBackquote:
				r = '`'
			case input.KeyComma:
				r = ','
			case input.KeyPeriod:
				r = '.'
			}
		}
		if r != 0 {
			return input.Token{Kind: input.TokenText, Rune: r}, true
		}
	}
	return input.Token{}, false
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
