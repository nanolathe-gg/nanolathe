package input

// MouseState and KeyboardState are the platform-neutral host-frame sample.
// They contain presentation state only; a session sees a copied semantic
// sample and never polls a device [07 §2][07 §3].
type MouseState struct {
	X, Y             float32
	ScrollX, ScrollY float32
	scrolled         bool
	edges            [4]bool
	released         [4]bool
	buttons          [4]bool
	moved            bool
}

// Pressed reports the press edge of b in this host frame.
func (m *MouseState) Pressed(b MouseButton) bool { return m != nil && m.edges[b] }

// Released reports the release edge of b in this host frame.
func (m *MouseState) Released(b MouseButton) bool { return m != nil && m.released[b] }

// Held reports whether b is down.
func (m *MouseState) Held(b MouseButton) bool { return m != nil && m.buttons[b] }

// Scrolled reports whether the wheel moved in this host frame.
func (m *MouseState) Scrolled() bool { return m != nil && m.scrolled }

// Moved reports whether the pointer position changed in this host frame.
func (m *MouseState) Moved() bool { return m != nil && m.moved }

// SetPosition records the pointer position and derives the moved flag.
func (m *MouseState) SetPosition(x, y float32) {
	if m == nil {
		return
	}
	m.moved = m.X != x || m.Y != y
	m.X, m.Y = x, y
}

// SetButton updates a button and derives its press/release edge. It is used by
// the client platform edge and by construction of a semantic replay sample.
func (m *MouseState) SetButton(btn MouseButton, down bool) {
	if m == nil || btn >= MouseButton(4) {
		return
	}
	b := int(btn)
	prev := m.buttons[b]
	m.edges[b] = down && !prev
	m.released[b] = !down && prev
	m.buttons[b] = down
}

// SetWheel records this host frame's wheel delta and the scrolled flag.
func (m *MouseState) SetWheel(dx, dy float32) {
	if m == nil {
		return
	}
	m.scrolled = dx != 0 || dy != 0
	m.ScrollX, m.ScrollY = dx, dy
}

// ResetEdges clears the per-host-frame edges: press, release, wheel and
// movement. Held button state survives.
func (m *MouseState) ResetEdges() {
	if m == nil {
		return
	}
	m.edges = [4]bool{}
	m.released = [4]bool{}
	m.scrolled = false
	m.moved = false
	m.ScrollX, m.ScrollY = 0, 0
}

// ButtonState is Held as the 0/1 word the authored controls compare against.
func (m *MouseState) ButtonState(b MouseButton) int {
	if m != nil && m.Held(b) {
		return 1
	}
	return 0
}

// KeyboardState is the per-host-frame key sample: one press edge and one held
// bit per key in the platform-neutral vocabulary [07 §2].
type KeyboardState struct {
	edges [KeyCount]bool
	held  [KeyCount]bool
}

// HasShift reports whether either shift key is down.
func (k *KeyboardState) HasShift() bool { return k != nil && k.held[KeyShift] }

// KeyDown reports the press edge of key in this host frame.
func (k *KeyboardState) KeyDown(key Key) bool { return k != nil && key < KeyCount && k.edges[key] }

// KeyHeld reports whether key is down.
func (k *KeyboardState) KeyHeld(key Key) bool { return k != nil && key < KeyCount && k.held[key] }

// SetKey records a key and derives its press edge.
func (k *KeyboardState) SetKey(key Key, down bool) {
	if k == nil || key >= KeyCount {
		return
	}
	prev := k.held[key]
	k.edges[key] = down && !prev
	k.held[key] = down
}

// ResetEdges clears the per-host-frame press edges. Held state survives.
func (k *KeyboardState) ResetEdges() {
	if k != nil {
		k.edges = [KeyCount]bool{}
	}
}

// State is the one canonical host-frame input sample held by the client edge.
// Its values are copied into Sample before UI and battle dispatch, so no
// mutable client model is shared with command handling [I6].
type State struct {
	Mouse *MouseState
	Kbd   *KeyboardState

	tokens TokenRing
}

// NewState returns an empty host-frame sample with both halves allocated.
func NewState() *State { return &State{Mouse: &MouseState{}, Kbd: &KeyboardState{}} }

// EnqueueToken records an ordered platform token. It does not affect held
// state, which remains queryable through Kbd [07 §2].
func (s *State) EnqueueToken(token Token) bool {
	if s == nil {
		return false
	}
	return s.tokens.Enqueue(token)
}

// DrainTokens takes the pending token history in producer order.
func (s *State) DrainTokens() []Token {
	if s == nil {
		return nil
	}
	return s.tokens.Drain()
}

// PendingTokens reports how many ordered tokens await a consumer.
func (s *State) PendingTokens() int {
	if s == nil {
		return 0
	}
	return s.tokens.Len()
}

// PeekTokens returns the pending token sequence without consuming it.
func (s *State) PeekTokens() []Token {
	if s == nil {
		return nil
	}
	return s.tokens.Peek()
}

// DiscardTokens removes an already-serviced token prefix.
func (s *State) DiscardTokens(n int) int {
	if s == nil {
		return 0
	}
	return s.tokens.Discard(n)
}

// MouseButtons is the three-button held state carried by a Sample.
type MouseButtons struct{ Left, Middle, Right bool }

// Modifiers is the modifier-key held state carried by a Sample.
type Modifiers struct{ Shift, Ctrl, Alt bool }

// Sample is a platform-neutral semantic input value. PressedKeys are edge
// events for this host frame; HeldKeys carry continuous state [07 §2].
type Sample struct {
	MouseX, MouseY                  int32
	Buttons                         MouseButtons
	Modifiers                       Modifiers
	WheelX, WheelY                  float32
	PressedButtons, ReleasedButtons [4]bool
	MouseMoved                      bool
	Elapsed                         float64
	PressedKeys                     []Key
	HeldKeys                        []Key
}

func logical(v float32, limit int32) int32 {
	if v <= 0 {
		return 0
	}
	max := float32(limit - 1)
	if v >= max {
		return limit - 1
	}
	return int32(v)
}

// SurfaceWidth and SurfaceHeight are the authored design space the interface
// is laid out in [07 §1]. They are the fallback for a caller that does not yet
// know the negotiated surface, never a clamp applied to a larger one.
const (
	SurfaceWidth  int32 = 640
	SurfaceHeight int32 = 480
)

// SampleFromState copies the client edge's complete state into a value.
//
// surfaceW/surfaceH are the negotiated presentation surface. The interface is
// authored in a logical 640×480 design space, but at a larger display mode the
// battle chrome is neither scaled nor letterboxed: it extends by rule and the
// pointer clamp follows the live surface at `W−1` / `H−1`
// [07 §1][07 R-HUD-05 "Anchored to the right edge W"]. Clamping the sample to
// the authored space instead made every pointer position past (639, 479)
// unreachable, so no world command — a build placement above all — could be
// issued outside the 640×480 corner of a larger mode. A non-positive size
// falls back to the authored design space.
func SampleFromState(in *State, elapsed float64, surfaceW, surfaceH int32) Sample {
	if surfaceW <= 0 {
		surfaceW = SurfaceWidth
	}
	if surfaceH <= 0 {
		surfaceH = SurfaceHeight
	}
	s := Sample{Elapsed: elapsed}
	if in == nil {
		return s
	}
	if m := in.Mouse; m != nil {
		s.MouseX, s.MouseY = logical(m.X, surfaceW), logical(m.Y, surfaceH)
		s.Buttons = MouseButtons{Left: m.Held(MouseButtonLeft), Middle: m.Held(MouseButtonMiddle), Right: m.Held(MouseButtonRight)}
		s.WheelX, s.WheelY = m.ScrollX, m.ScrollY
		for i := range s.PressedButtons {
			s.PressedButtons[i] = m.edges[i]
			s.ReleasedButtons[i] = m.released[i]
		}
		s.MouseMoved = m.moved
	}
	if k := in.Kbd; k != nil {
		for key := Key(1); key < KeyCount; key++ {
			if k.KeyDown(key) {
				s.PressedKeys = append(s.PressedKeys, key)
			}
			if k.KeyHeld(key) {
				s.HeldKeys = append(s.HeldKeys, key)
			}
		}
		s.Modifiers = Modifiers{Shift: k.HasShift(), Ctrl: k.KeyHeld(KeyCtrl), Alt: k.KeyHeld(KeyAlt)}
	}
	return s
}

// StateFromSample materializes a single host-frame state for semantic UI
// dispatch. It is not retained between frames, avoiding a second mutable
// input model in command code.
func StateFromSample(s Sample) *State {
	in := NewState()
	in.Mouse.SetPosition(float32(s.MouseX), float32(s.MouseY))
	in.Mouse.SetButton(MouseButtonLeft, s.Buttons.Left)
	in.Mouse.SetButton(MouseButtonMiddle, s.Buttons.Middle)
	in.Mouse.SetButton(MouseButtonRight, s.Buttons.Right)
	in.Mouse.SetWheel(s.WheelX, s.WheelY)
	in.Mouse.edges = s.PressedButtons
	in.Mouse.released = s.ReleasedButtons
	in.Mouse.moved = s.MouseMoved
	var desired [KeyCount]bool
	for _, key := range s.PressedKeys {
		if key > KeyNone && key < KeyCount {
			desired[key] = true
			in.Kbd.edges[key] = true
		}
	}
	for _, key := range s.HeldKeys {
		if key > KeyNone && key < KeyCount {
			desired[key] = true
		}
	}
	desired[KeyShift] = desired[KeyShift] || s.Modifiers.Shift
	desired[KeyCtrl] = desired[KeyCtrl] || s.Modifiers.Ctrl
	desired[KeyAlt] = desired[KeyAlt] || s.Modifiers.Alt
	for key := Key(1); key < KeyCount; key++ {
		in.Kbd.held[key] = desired[key]
	}
	return in
}
