// Package input contains the small retail input value types shared by GUI
// event records. Device polling and queueing are owned by the client boundary;
// no production caller uses the former ring-buffer adapter.
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
