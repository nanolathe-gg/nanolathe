// Package input holds the retail input rings and command latch [07 §2][GAP T22].
//
// Rings are the fixed-capacity circular queues that the window procedure
// writes and the battle dispatcher drains. Translation from Ebitengine input
// state to the retail token vocabulary happens at the boundary so nothing
// downstream sees backend-specific types (PLAN_04A C5).
//
// Keyboard ring holds 30 entries with 29 usable; mouse ring holds 24 six-dword
// entries with the same refusal rule [07 §2] (C5). A push when full is
// refused, never an overwrite (reserved-slot wraparound; oldest entry is never
// overwritten). The producer computes next first; if it equals the consumer the
// record is refused and neither index moves.
package input

// Token is a translated keyboard token in the retail vocabulary [07 §2].
//
// Code is the virtual-key translation (e.g., 0xF8 pause, 0xF0 home, 0xEE
// insert, 0x20 space, ASCII for 'A'..'Z', etc.). Char is the WM_CHAR value
// when the token originates from WM_CHAR; otherwise zero. Ctrl indicates the
// Ctrl-held composition state used by the virtual-key translator. Token zero
// (Code 0, Char 0) means no new input in the retail dispatcher.
type Token struct {
	Code byte
	Char byte
	Ctrl bool
}

// MouseRecord is a timestamped six-dword button record [07 §2].
//
// Each record carries the X coordinate (low word of the message position), the
// Y coordinate (high word), the message's key-state word, a scaled tick count
// (GetTickCount() * timeScale / 1000), the original message number, and an
// explicit double-click field. Left down/up (0x201/0x202) and right down/up
// (0x204/0x205) produce records with Double clear; left/right double-clicks
// (0x203/0x206) set Double. WM_MOUSEMOVE (0x200) builds the same six-dword
// record but feeds a separate motion-state path instead of the button queue.
// Middle button (0x207) and wheel (0x20A) have no dedicated case and fall
// through to default processing [07 §2].
type MouseRecord struct {
	X        int16
	Y        int16
	KeyState uint32
	Tick     uint32
	Msg      uint32
	Double   bool
}

// Rings holds the retail input rings [07 §2] C5.
//
// Keyboard ring holds 30 entries with 29 usable; mouse ring holds 24 six-dword
// entries (each 24 bytes) with the same refusal rule. The underlying arrays
// are fixed; one slot is always reserved so full is detected as next == consumer
// and oldest is never overwritten.
type Rings struct {
	keys [30]Token
	// keyHead is the consumer index (next to pop), keyTail is the producer
	// index (next to write). Empty when head==tail, full when next(tail)==head.
	keyHead int
	keyTail int

	mice [24]MouseRecord
	// mouseHead is consumer, mouseTail is producer, same empty/full rule.
	mouseHead int
	mouseTail int
}

// PushKey enqueues t. Returns false if the ring is full (refused, never
// overwrite) [07 §2] C5. 29 pushes succeed; the 30th is refused.
func (r *Rings) PushKey(t Token) bool {
	next := r.keyTail + 1
	if next == len(r.keys) {
		next = 0
	}
	if next == r.keyHead {
		return false
	}
	r.keys[r.keyTail] = t
	r.keyTail = next
	return true
}

// PushMouse enqueues m. Returns false if the ring is full with the same
// reserved-slot refusal rule [07 §2] C5. With 24 slots, 23 pushes succeed;
// the 24th is refused.
func (r *Rings) PushMouse(m MouseRecord) bool {
	next := r.mouseTail + 1
	if next == len(r.mice) {
		next = 0
	}
	if next == r.mouseHead {
		return false
	}
	r.mice[r.mouseTail] = m
	r.mouseTail = next
	return true
}

// PopKey dequeues the oldest keyboard token. Returns false if empty.
func (r *Rings) PopKey() (Token, bool) {
	if r.keyHead == r.keyTail {
		return Token{}, false
	}
	t := r.keys[r.keyHead]
	next := r.keyHead + 1
	if next == len(r.keys) {
		next = 0
	}
	r.keyHead = next
	return t, true
}

// PopMouse dequeues the oldest mouse record. Returns false if empty.
// Not part of the minimal public API but provided for symmetry and for the
// session to drain mouse input; behavior mirrors PopKey [07 §2].
func (r *Rings) PopMouse() (MouseRecord, bool) {
	if r.mouseHead == r.mouseTail {
		return MouseRecord{}, false
	}
	m := r.mice[r.mouseHead]
	next := r.mouseHead + 1
	if next == len(r.mice) {
		next = 0
	}
	r.mouseHead = next
	return m, true
}

// PeekKey returns the next token without consuming it.
func (r *Rings) PeekKey() (Token, bool) {
	if r.keyHead == r.keyTail {
		return Token{}, false
	}
	return r.keys[r.keyHead], true
}

// PeekMouse returns the next mouse record without consuming it.
func (r *Rings) PeekMouse() (MouseRecord, bool) {
	if r.mouseHead == r.mouseTail {
		return MouseRecord{}, false
	}
	return r.mice[r.mouseHead], true
}

// KeyLen returns the number of pending keyboard tokens (0..29) [07 §2].
func (r *Rings) KeyLen() int {
	if r.keyTail >= r.keyHead {
		return r.keyTail - r.keyHead
	}
	return len(r.keys) - r.keyHead + r.keyTail
}

// MouseLen returns the number of pending mouse records (0..23) [07 §2].
func (r *Rings) MouseLen() int {
	if r.mouseTail >= r.mouseHead {
		return r.mouseTail - r.mouseHead
	}
	return len(r.mice) - r.mouseHead + r.mouseTail
}

// KeyEmpty reports whether the keyboard ring is empty.
func (r *Rings) KeyEmpty() bool { return r.keyHead == r.keyTail }

// MouseEmpty reports whether the mouse ring is empty.
func (r *Rings) MouseEmpty() bool { return r.mouseHead == r.mouseTail }

// KeyFull reports whether the next PushKey would be refused.
func (r *Rings) KeyFull() bool {
	next := r.keyTail + 1
	if next == len(r.keys) {
		next = 0
	}
	return next == r.keyHead
}

// MouseFull reports whether the next PushMouse would be refused.
func (r *Rings) MouseFull() bool {
	next := r.mouseTail + 1
	if next == len(r.mice) {
		next = 0
	}
	return next == r.mouseHead
}

// ClearKey drains the keyboard ring.
func (r *Rings) ClearKey() { r.keyHead = r.keyTail }

// ClearMouse drains the mouse ring.
func (r *Rings) ClearMouse() { r.mouseHead = r.mouseTail }

// Clear drains both rings.
func (r *Rings) Clear() {
	r.ClearKey()
	r.ClearMouse()
}
