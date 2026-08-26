package client

import (
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe/nanolathe/internal/input"
)

// MouseState is the per-frame mouse snapshot. X/Y are window coordinates;
// edge flags (Pressed) hold for exactly one update, Held persists while down.
type MouseState struct {
	X, Y float32

	ScrollX, ScrollY float32
	scrolled         bool

	edges    [4]bool // just-pressed this update, indexed by MouseButton
	released [4]bool // just-released this update, indexed by MouseButton
	buttons  [4]bool // currently down
	moved    bool
}

// Pressed reports a button that went down this update.
func (m *MouseState) Pressed(b input.MouseButton) bool { return m.edges[b] }

// Released reports a button that went up this update.
func (m *MouseState) Released(b input.MouseButton) bool { return m.released[b] }

// Held reports a button currently down.
func (m *MouseState) Held(b input.MouseButton) bool { return m.buttons[b] }

// Scrolled reports wheel motion this update.
func (m *MouseState) Scrolled() bool { return m.scrolled }

// Moved reports cursor motion this update.
func (m *MouseState) Moved() bool { return m.moved }

// SetPosition places the pointer directly. The windowed path never calls this
// — pollEbiten owns the position there — but headless composition (screenshots,
// probes) has no window system to read the pointer from [07 §8].
func (m *MouseState) SetPosition(x, y float32) {
	m.moved = m.X != x || m.Y != y
	m.X, m.Y = x, y
}

// ButtonState returns 1 while a button is down, 0 otherwise.
func (m *MouseState) ButtonState(b input.MouseButton) int {
	if m.buttons[b] {
		return 1
	}
	return 0
}

// KeyboardState is the per-frame keyboard snapshot with retail-style queries:
// KeyDown is the this-update press edge, KeyHeld the continuous state.
type KeyboardState struct {
	edges [input.KeyCount]bool
	held  [input.KeyCount]bool
}

// HasShift reports either shift key held, for additive selection.
func (k *KeyboardState) HasShift() bool {
	return k.held[input.KeyShift]
}

// KeyDown reports a key that went down this update.
func (k *KeyboardState) KeyDown(key input.Key) bool { return k.edges[key] }

// KeyHeld reports a key currently down.
func (k *KeyboardState) KeyHeld(key input.Key) bool { return k.held[key] }

// InputState bundles the keyboard and mouse snapshots refreshed at the start
// of every update, before the Step callback runs.
type InputState struct {
	Mouse *MouseState
	Kbd   *KeyboardState
}

func newInputState() *InputState {
	return &InputState{Mouse: &MouseState{}, Kbd: &KeyboardState{}}
}

// pollEbiten refreshes the snapshot from Ebitengine. Edge flags reset here so
// they describe exactly the frame about to be stepped and drawn.
func (in *InputState) pollEbiten() {
	m := in.Mouse
	k := in.Kbd
	m.edges = [4]bool{}
	m.released = [4]bool{}
	m.scrolled = false
	m.moved = false
	k.edges = [input.KeyCount]bool{}

	cx, cy := ebiten.CursorPosition()
	if float32(cx) != m.X || float32(cy) != m.Y {
		m.moved = true
	}
	m.X = float32(cx)
	m.Y = float32(cy)
	// Fix middle-button polling [F-P0-003][F-P1-008]: previous table mapped
	// MouseButtonNone to Middle and skipped Middle entirely. Correct mapping
	// polls Left/Middle/Right via stable array indexed by MouseButton.
	mappings := []struct {
		btn input.MouseButton
		eb  ebiten.MouseButton
	}{
		{input.MouseButtonLeft, ebiten.MouseButtonLeft},
		{input.MouseButtonMiddle, ebiten.MouseButtonMiddle},
		{input.MouseButtonRight, ebiten.MouseButtonRight},
	}
	for _, mp := range mappings {
		b := int(mp.btn)
		down := ebiten.IsMouseButtonPressed(mp.eb)
		m.edges[b] = down && !m.buttons[b]
		m.released[b] = !down && m.buttons[b]
		m.buttons[b] = down
	}
	wx, wy := ebiten.Wheel()
	if wx != 0 || wy != 0 {
		m.scrolled = true
		m.ScrollX = float32(wx)
		m.ScrollY = float32(wy)
	} else {
		m.ScrollX = 0
		m.ScrollY = 0
	}
	for key := input.Key(1); key < input.KeyCount; key++ {
		if ek, ok := ebitenKey(key); ok {
			down := ebiten.IsKeyPressed(ek)
			k.edges[key] = down && !k.held[key]
			k.held[key] = down
		}
	}
}

// --- Headless test injection helpers (presentation-only, never touch sim) ---

// InjectMouseButton sets the held state for a button and synthesizes the
// corresponding edge flags for the next handleInput tick. Used by headless
// tests to simulate presses without an Ebitengine window [07 §8].
func (m *MouseState) InjectMouseButton(btn input.MouseButton, down bool) {
	if m == nil || btn >= input.MouseButton(4) {
		return
	}
	b := int(btn)
	prev := m.buttons[b]
	m.edges[b] = down && !prev
	m.released[b] = !down && prev
	m.buttons[b] = down
}

// InjectMouseMove moves the cursor to x,y and marks moved [07 §8].
func (m *MouseState) InjectMouseMove(x, y float32) {
	if m == nil {
		return
	}
	m.moved = m.X != x || m.Y != y
	m.X, m.Y = x, y
}

// InjectWheel injects wheel deltas for the next tick (presentation-only zoom) [07 §10].
func (m *MouseState) InjectWheel(dx, dy float32) {
	if m == nil {
		return
	}
	m.scrolled = dx != 0 || dy != 0
	m.ScrollX = dx
	m.ScrollY = dy
}

// InjectKey sets a key's held/edge state for the next tick.
func (k *KeyboardState) InjectKey(key input.Key, down bool) {
	if k == nil || key >= input.KeyCount {
		return
	}
	prev := k.held[key]
	k.edges[key] = down && !prev
	k.held[key] = down
}

// ClearEdges resets per-frame edge flags without touching held state. Useful
// between injected ticks in headless tests.
func (m *MouseState) ClearEdges() {
	if m == nil {
		return
	}
	m.edges = [4]bool{}
	m.released = [4]bool{}
	m.scrolled = false
	m.moved = false
	m.ScrollX, m.ScrollY = 0, 0
}
func (k *KeyboardState) ClearEdges() {
	if k == nil {
		return
	}
	k.edges = [input.KeyCount]bool{}
}

// ebitenKey maps the platform-neutral vocabulary onto Ebitengine keys.
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
	case input.KeyShift:
		return ebiten.KeyShiftLeft, true
	case input.KeyCtrl:
		return ebiten.KeyControlLeft, true
	case input.KeyAlt:
		return ebiten.KeyAltLeft, true
	}
	return 0, false
}
