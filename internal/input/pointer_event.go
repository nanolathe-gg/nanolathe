package input

// PointerEventKind is the semantic mouse-message vocabulary consumed by a
// widget pass. Host double-click recognition belongs at the platform edge;
// the widget service deliberately accepts the already-classified event.
type PointerEventKind uint8

const (
	LeftDown PointerEventKind = iota + 1
	LeftDoubleClick
	LeftUp
	RightDown
	RightDoubleClick
	RightUp
)

// PointerEvent is one ordered semantic pointer event for a host frame.
type PointerEvent struct {
	Kind      PointerEventKind
	X, Y      int32
	Modifiers Modifiers
}
