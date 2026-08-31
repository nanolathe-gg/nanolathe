package client

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/input"
)

// Input types are defined by internal/input; aliases keep the client edge
// source-compatible while ensuring UI and command code share one state model.
type MouseState = input.MouseState
type KeyboardState = input.KeyboardState
type InputState = input.State

func newInputState() *input.State { return input.NewState() }

// pollInput is the only production device-polling path. Ebitengine remains at
// this edge; downstream code receives the platform-neutral input.State [I6].
func pollInput(in *input.State) {
	if in == nil || in.Mouse == nil || in.Kbd == nil {
		return
	}
	m, k := in.Mouse, in.Kbd
	m.ResetEdges()
	k.ResetEdges()
	cx, cy := ebiten.CursorPosition()
	m.SetPosition(float32(cx), float32(cy))
	for _, mp := range []struct {
		btn input.MouseButton
		eb  ebiten.MouseButton
	}{
		{input.MouseButtonLeft, ebiten.MouseButtonLeft},
		{input.MouseButtonMiddle, ebiten.MouseButtonMiddle},
		{input.MouseButtonRight, ebiten.MouseButtonRight},
	} {
		m.SetButton(mp.btn, ebiten.IsMouseButtonPressed(mp.eb))
	}
	wx, wy := ebiten.Wheel()
	m.SetWheel(float32(wx), float32(wy))
	for key := input.Key(1); key < input.KeyCount; key++ {
		down := false
		if key == input.KeyShift {
			down = ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight)
		} else if ek, ok := ebitenKey(key); ok {
			down = ebiten.IsKeyPressed(ek)
		}
		k.SetKey(key, down)
	}
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
	case input.KeyShift:
		return ebiten.KeyShiftLeft, true
	case input.KeyCtrl:
		return ebiten.KeyControlLeft, true
	case input.KeyAlt:
		return ebiten.KeyAltLeft, true
	}
	return 0, false
}
