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

func (m *MouseState) Pressed(b MouseButton) bool  { return m != nil && m.edges[b] }
func (m *MouseState) Released(b MouseButton) bool { return m != nil && m.released[b] }
func (m *MouseState) Held(b MouseButton) bool     { return m != nil && m.buttons[b] }
func (m *MouseState) Scrolled() bool              { return m != nil && m.scrolled }
func (m *MouseState) Moved() bool                 { return m != nil && m.moved }

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

func (m *MouseState) SetWheel(dx, dy float32) {
	if m == nil {
		return
	}
	m.scrolled = dx != 0 || dy != 0
	m.ScrollX, m.ScrollY = dx, dy
}

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

func (m *MouseState) ButtonState(b MouseButton) int {
	if m != nil && m.Held(b) {
		return 1
	}
	return 0
}

type KeyboardState struct {
	edges [KeyCount]bool
	held  [KeyCount]bool
}

func (k *KeyboardState) HasShift() bool       { return k != nil && k.held[KeyShift] }
func (k *KeyboardState) KeyDown(key Key) bool { return k != nil && key < KeyCount && k.edges[key] }
func (k *KeyboardState) KeyHeld(key Key) bool { return k != nil && key < KeyCount && k.held[key] }

func (k *KeyboardState) SetKey(key Key, down bool) {
	if k == nil || key >= KeyCount {
		return
	}
	prev := k.held[key]
	k.edges[key] = down && !prev
	k.held[key] = down
}

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
}

func NewState() *State { return &State{Mouse: &MouseState{}, Kbd: &KeyboardState{}} }

type MouseButtons struct{ Left, Middle, Right bool }
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

// SampleFromState copies the client edge's complete state into a value. The
// logical battle surface is authored at 640×480 [07 §1].
func SampleFromState(in *State, elapsed float64) Sample {
	s := Sample{Elapsed: elapsed}
	if in == nil {
		return s
	}
	if m := in.Mouse; m != nil {
		s.MouseX, s.MouseY = logical(m.X, 640), logical(m.Y, 480)
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
