package input

// PointerSample projects the one published record for legacy pointer consumers.
// Keyboard held queries remain on Kbd; callers use the returned modifiers only
// for pointer actions. Middle-button and wheel extensions keep their live host
// values because retail has no queued records for them [07 §2]. Widget callers
// also read CurrentPointer to retain the distinct double-click message.
func (s *State) PointerSample() (MouseState, Modifiers) {
	var mouse MouseState
	var modifiers Modifiers
	if s == nil {
		return mouse, modifiers
	}
	if s.Mouse != nil {
		mouse = *s.Mouse
	}
	if s.Kbd != nil {
		modifiers = Modifiers{Shift: s.Kbd.HasShift(), Ctrl: s.Kbd.KeyHeld(KeyCtrl), Alt: s.Kbd.KeyHeld(KeyAlt)}
	}
	event, valid := s.CurrentPointer()
	if !valid {
		return mouse, modifiers
	}
	mouse.X, mouse.Y = float32(event.X), float32(event.Y)
	mouse.buttons[MouseButtonLeft] = event.Buttons.Left
	mouse.buttons[MouseButtonRight] = event.Buttons.Right
	mouse.edges[MouseButtonLeft], mouse.edges[MouseButtonRight] = false, false
	mouse.released[MouseButtonLeft], mouse.released[MouseButtonRight] = false, false
	if button, down, ok := event.Kind.button(); ok {
		mouse.edges[button] = down
		mouse.released[button] = !down
	}
	return mouse, event.Modifiers
}
