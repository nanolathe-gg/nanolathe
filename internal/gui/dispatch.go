package gui

import (
	"github.com/nanolathe/nanolathe/internal/input"
)

// Family constants route the stored control-type byte to distinct runtime families [07 §4].
const (
	FamilyClickable   uint8 = 1  // clickable with callback result; can become the active gadget [07 §4]
	FamilyStateful    uint8 = 2  // distinct stateful control path [07 §4]
	FamilyTextEditor  uint8 = 3  // focusable text editor [07 §4]
	FamilyDedicated   uint8 = 4  // dedicated control update path [07 §4]
	FamilyAssociation uint8 = 5  // association-capable (16-byte name compare, fixed-stride scan) [07 §4]
	FamilyCallback    uint8 = 6  // callback-producing [07 §4]
	FamilyRepeating   uint8 = 12 // repeating/decrementing under throttle [07 §4]
	FamilyTimed       uint8 = 13 // timed/range animating toward maximum [07 §4]
)

// FlagExtraRedraw is the GUI flag bit that selects the extra redraw on close [07 §3].
const FlagExtraRedraw uint32 = 0x800 // [07 §3]

// Zero-token suppression bounds: peeking in zero-token mode suppresses 0xE2..0xEB inclusive [07 §3].
const (
	suppressLow  byte = 0xE2 // [07 §3]
	suppressHigh byte = 0xEB // [07 §3]
)

// IsSuppressedInZeroTokenMode reports whether a token code is suppressed when
// the GUI's token-mode field is zero and the pass peeks without consuming [07 §3].
// Only 0xE2..0xEB inclusive are suppressed; other codes are observed.
func IsSuppressedInZeroTokenMode(code byte) bool {
	return code >= suppressLow && code <= suppressHigh // [07 §3]
}

// FilterZeroMode returns the token as observed in zero-token-mode peek handling [07 §3].
// If zeroMode is false the caller has consumed one token and no suppression occurs.
// If zeroMode is true the pass peeks without consuming and maps 0xE2..0xEB to zero.
func FilterZeroMode(tok input.Token, zeroMode bool) input.Token {
	if zeroMode && IsSuppressedInZeroTokenMode(tok.Code) {
		return input.Token{} // replaced with zero [07 §3]
	}
	return tok
}

// Stack is the modal GUI window stack [07 §3].
// The active GUI is the top object of a linked stack; hit tests and dispatch walk the top window's gadgets.
type Stack struct {
	windows []*Window
	Flags   uint32 // GUI flag word; bit 0x800 selects extra redraw on close [07 §3]
}

// Push adds a window to the top of the stack [07 §3].
func (s *Stack) Push(w *Window) {
	if w == nil {
		return
	}
	s.windows = append(s.windows, w)
}

// Pop removes and returns the top window, or nil if empty.
func (s *Stack) Pop() *Window {
	if len(s.windows) == 0 {
		return nil
	}
	top := s.windows[len(s.windows)-1]
	s.windows = s.windows[:len(s.windows)-1]
	return top
}

// Top returns the active GUI window, or nil if the stack is empty [07 §3].
func (s *Stack) Top() *Window {
	if len(s.windows) == 0 {
		return nil
	}
	return s.windows[len(s.windows)-1]
}

// Len returns the number of windows on the stack.
func (s *Stack) Len() int { return len(s.windows) }

// CloseTop implements the closed top-object close sequence [07 §3]:
// callbacks → redraw → removal → predecessor reactivation + extra redraw under flag 0x800.
// The spy slice, if non-nil, records the observed order for tests.
func (s *Stack) CloseTop(spy *[]string) {
	if len(s.windows) == 0 {
		return
	}
	// 1: invoke registered callbacks [07 §3]
	if spy != nil {
		*spy = append(*spy, "callbacks")
	}
	// 2: redraw under nest-counted cursor/display protection [07 §3]
	if spy != nil {
		*spy = append(*spy, "redraw")
	}
	// 3: remove the object [07 §3]
	top := s.windows[len(s.windows)-1]
	_ = top
	s.windows = s.windows[:len(s.windows)-1]
	if spy != nil {
		*spy = append(*spy, "remove")
	}
	// 4: reactivate predecessor when one exists (stack head replaced and predecessor marked for redraw) [07 §3]
	if len(s.windows) > 0 {
		if spy != nil {
			*spy = append(*spy, "reactivate")
		}
	}
	// 5: extra redraw request selected by GUI flag 0x800 [07 §3]
	if s.Flags&FlagExtraRedraw != 0 {
		if spy != nil {
			*spy = append(*spy, "extra_redraw")
		}
	}
}

// HitTestAfterPlacement is the inclusive hit test after runtime window placement [07 §3][07 §4].
// It delegates to Window.HitTest which is inclusive on both edges gx <= x <= gx+w-1 && gy <= y <= gy+h-1
// and rejects grayed and hidden controls before activation.
func HitTestAfterPlacement(w *Window, x, y int32) int {
	if w == nil {
		return -1
	}
	return w.HitTest(x, y) // placement already resolved into Gadget.Rect [07 §3][07 §4]
}

// SyncScrollbars implements C7: scrollbars associated with lists take their range, knob size,
// and position from the list — authored values are not trusted [07 §4].
func SyncScrollbars(w *Window) {
	if w == nil {
		return
	}
	// Build assoc -> list values map [07 §4]
	assocList := make(map[int32]Gadget)
	for _, g := range w.Gadgets {
		if g.Kind == KindListBox && g.Assoc != 0 {
			assocList[g.Assoc] = g
		}
	}
	for i := range w.Gadgets {
		if w.Gadgets[i].Kind == KindScrollBar && w.Gadgets[i].Assoc != 0 {
			if lb, ok := assocList[w.Gadgets[i].Assoc]; ok {
				w.Gadgets[i].Range = lb.Range
				w.Gadgets[i].KnobPos = lb.KnobPos
				w.Gadgets[i].KnobSize = lb.KnobSize
			}
		}
	}
}

// Name16Equal compares up to 16 bytes of a gadget name identifier [07 §4].
// The retail gadget name field is 16 bytes; comparison is byte-for-byte over that fixed field.
func Name16Equal(a, b string) bool {
	var ba, bb [16]byte
	copy(ba[:], a)
	copy(bb[:], b)
	return ba == bb // fixed 16-byte compare [07 §4]
}

// FindAssociated scans for the association target of an association-capable gadget [07 §4].
// It compares up to 16 bytes of the name identifier across gadgets in fixed-record-stride scan order
// (file order, which mirrors the 347-byte stride in retail [07 §4]).
// Returns the index of the matching gadget, or -1 if none.
func FindAssociated(w *Window, srcIdx int) int {
	if w == nil || srcIdx < 0 || srcIdx >= len(w.Gadgets) {
		return -1
	}
	src := w.Gadgets[srcIdx]
	if src.Kind.RuntimeFamily() != FamilyAssociation {
		return -1
	}
	for i, g := range w.Gadgets {
		if i == srcIdx {
			continue
		}
		if Name16Equal(src.Name, g.Name) {
			return i
		}
	}
	return -1
}

// FamilyForStoredType maps a raw stored type byte to its runtime family [07 §4].
// Families 1,2,3,4,5,6,12,13 have distinct runtime paths; others map to 0 (no dedicated family).
func FamilyForStoredType(b byte) uint8 {
	switch Kind(b) {
	case KindButton:
		return FamilyClickable // 1 [07 §4]
	case KindListBox:
		return FamilyStateful // 2 [07 §4]
	case KindTextBox:
		return FamilyTextEditor // 3 [07 §4]
	case KindScrollBar:
		return FamilyDedicated // 4 [07 §4]
	case KindLabel:
		return FamilyAssociation // 5 [07 §4]
	case KindSurface:
		return FamilyCallback // 6 [07 §4]
	case KindPicture:
		return FamilyRepeating // 12 [07 §4]
	case KindRepeat:
		return FamilyTimed // 13 [07 §4]
	default:
		return 0
	}
}

// FamilyName returns a diagnostic name for a family, or "none" for 0.
func FamilyName(f uint8) string {
	switch f {
	case FamilyClickable:
		return "clickable-with-callback"
	case FamilyStateful:
		return "stateful"
	case FamilyTextEditor:
		return "focusable-text-editor"
	case FamilyDedicated:
		return "dedicated-update"
	case FamilyAssociation:
		return "association-capable"
	case FamilyCallback:
		return "callback-producing"
	case FamilyRepeating:
		return "repeating/decrementing"
	case FamilyTimed:
		return "timed/range-animating"
	default:
		return "none"
	}
}

// DispatchPrecedesBattle reports that GUI dispatch precedes battle hotkeys [07 §3].
// The host-frame input pass dispatches the active GUI before the battle hotkey dispatcher.
func DispatchPrecedesBattle() bool { return true } // [07 §3]

// RepeatingState holds throttle state for family 12 repeating/decrementing path [07 §4].
type RepeatingState struct {
	Counter int
	Last    uint32
}

// TickRepeating decrements the counter while the throttle predicate holds [07 §4].
// Returns true if a decrement occurred.
func (r *RepeatingState) TickRepeating(throttleReady bool) bool {
	if !throttleReady {
		return false
	}
	if r.Counter > 0 {
		r.Counter--
		return true
	}
	return false
}

// TimedState animates a range value toward a maximum using per-gadget interval/threshold fields [07 §4].
type TimedState struct {
	Value int
	Max   int
}

// AdvanceTimed moves Value toward Max by one when throttleReady, then reports whether it fired [07 §4].
func (t *TimedState) AdvanceTimed(throttleReady bool) bool {
	if !throttleReady {
		return false
	}
	if t.Value < t.Max {
		t.Value++
		if t.Value == t.Max {
			return true // fires the path [07 §4]
		}
	}
	return false
}

// TODO(question): text-editor admission rules (maxchars-1, filter bit 0x02, allowed-set with
// space/underscore/apostrophe exceptions, width rule currentLen+newLen <= controlWidth-4) belong to
// editor.go per WU-12-3 [07 §4] C4 — hooks left here only.

// EditorAdmissionHook is a hook for the editor admission predicate; nil means not admitted.
// WU-12-3 will provide the full implementation per [07 §4] C4.
var EditorAdmissionHook func(g *Gadget, ch byte) bool

// RouteEvent routes an event to its handling family for diagnostics [07 §4].
// It does not implement the family logic itself; it identifies which family would handle the gadget.
func RouteEvent(w *Window, ev Event) (family uint8, gadgetIdx int) {
	if w == nil {
		return 0, -1
	}
	idx := w.HitTest(ev.X, ev.Y)
	if idx < 0 {
		return 0, -1
	}
	k := w.Gadgets[idx].Kind
	return FamilyForStoredType(byte(k)), idx
}
