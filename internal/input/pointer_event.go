package input

// PointerEventKind is the semantic mouse-message vocabulary consumed by a
// widget pass. Host double-click recognition belongs at the platform edge;
// the widget service deliberately accepts the already-classified event.
type PointerEventKind uint8

const (
	PointerEventNone PointerEventKind = iota
	LeftDown
	LeftDoubleClick
	LeftUp
	RightDown
	RightDoubleClick
	RightUp
)

// PointerEvent is one semantic pointer record. Timestamp is the scaled host
// time supplied by the platform edge; this package neither derives it nor
// recognizes double-clicks [07 §2][01 R-PLAT-01 §6].
type PointerEvent struct {
	Kind      PointerEventKind
	X, Y      int32
	Modifiers Modifiers
	Buttons   MouseButtons // event-time state, independent of the latest live buttons [07 §2]
	Timestamp uint32
}

func (kind PointerEventKind) button() (MouseButton, bool, bool) {
	switch kind {
	case LeftDown, LeftDoubleClick:
		return MouseButtonLeft, true, true
	case LeftUp:
		return MouseButtonLeft, false, true
	case RightDown, RightDoubleClick:
		return MouseButtonRight, true, true
	case RightUp:
		return MouseButtonRight, false, true
	default:
		return MouseButtonNone, false, false
	}
}
