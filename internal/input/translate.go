// Translation at the Ebitengine boundary into the retail token vocabulary [07 §2].
//
// Nothing downstream sees backend-specific types; the input package owns the
// translation so the session and client see only Token/MouseRecord in retail
// semantics (PLAN_04A C5).
package input

// Retail token codes for special keys [07 §2].
const (
	CodeSpace      byte = 0x20
	CodeHome       byte = 0xF0
	CodeEnd        byte = 0xF1
	CodePrior      byte = 0xF2 // PageUp
	CodeNext       byte = 0xF3 // PageDown
	CodeLeft       byte = 0xF4
	CodeUp         byte = 0xF5
	CodeRight      byte = 0xF6
	CodeDown       byte = 0xF7
	CodePause      byte = 0xF8
	CodeShift      byte = 0xF9
	CodeCtrl       byte = 0xFA
	CodeAlt        byte = 0xFB
	CodeInsert     byte = 0xEE
	CodeDelete     byte = 0xEF
	CodePasteBF    byte = 0xBF // clipboard paste token [07 §2]
	CodePasteEE    byte = 0xEE // alias paste token [07 §2]
	CodeScreenshot byte = 0xD6
	CodeEscMenu    byte = 0xE3
	CodeChatAlias  byte = 0x7E
)

// Retail mouse message numbers [07 §2].
const (
	MsgMouseMove    uint32 = 0x200
	MsgMouseLDown   uint32 = 0x201
	MsgMouseLUp     uint32 = 0x202
	MsgMouseLDouble uint32 = 0x203
	MsgMouseRDown   uint32 = 0x204
	MsgMouseRUp     uint32 = 0x205
	MsgMouseRDouble uint32 = 0x206
	MsgMouseMDown   uint32 = 0x207 // no dedicated case, falls to default [07 §2]
	MsgMouseWheel   uint32 = 0x20A // no dedicated case [07 §2]
)

// TranslateKey converts the platform-neutral key vocabulary used by the
// Ebitengine client into a retail Token [07 §2].
//
// Translation happens at the boundary so downstream code sees the retail token
// vocabulary, not backend-specific types (PLAN_04A C5). The virtual-key translator cases
// are exact per [07 §2]: VK_PAUSE→0xF8, VK_PRIOR→0xF2, VK_NEXT→0xF3,
// VK_END→0xF1, VK_HOME→0xF0, VK_LEFT→0xF4, VK_UP→0xF5, VK_RIGHT→0xF6,
// VK_DOWN→0xF7, VK_INSERT→0xEE, VK_DELETE→0xEF. F-keys and Ctrl composition
// are table-driven; OEM ranges 0xBA..0xC0 and 0xDB..0xDE remain unknown table
// aliases [07 §2]. This function covers the established submap and falls back
// to ASCII for letters/digits; unknown keys return Code 0 meaning no new input.
//
// ch is the WM_CHAR character value when originating from WM_CHAR; 0 otherwise.
// ctrl indicates whether Ctrl was held for composition (system-key mode where
// A..Z without Ctrl produce lowercase a..z).
func TranslateKey(key Key, ch byte, ctrl bool) Token {
	var code byte
	switch key {
	case KeyPause:
		code = CodePause // VK_PAUSE → 0xF8 [07 §2]
	case KeyPrior:
		code = CodePrior // VK_PRIOR → 0xF2 [07 §2]
	case KeyNext:
		code = CodeNext // VK_NEXT → 0xF3 [07 §2]
	case KeyEnd:
		code = CodeEnd // VK_END → 0xF1 [07 §2]
	case KeyHome:
		code = CodeHome // VK_HOME → 0xF0 [07 §2]
	case KeyLeft:
		code = CodeLeft // VK_LEFT → 0xF4 [07 §2]
	case KeyUp:
		code = CodeUp // VK_UP → 0xF5 [07 §2]
	case KeyRight:
		code = CodeRight // VK_RIGHT → 0xF6 [07 §2]
	case KeyDown:
		code = CodeDown // VK_DOWN → 0xF7 [07 §2]
	case KeyInsert:
		code = CodeInsert // VK_INSERT → 0xEE [07 §2]
	case KeyDelete:
		code = CodeDelete // VK_DELETE → 0xEF [07 §2]
	case KeySpace:
		code = CodeSpace // 0x20 [07 §2] held-state query also uses 0x20
	case KeyEscape:
		code = 0x1B
	case KeyTab:
		code = 0x09
	case KeyBackspace:
		code = 0x08
	case KeyEnter:
		code = 0x0D // Enter opens chat [07 §5]
	case KeyA, KeyB, KeyC, KeyD, KeyE, KeyF, KeyG, KeyH, KeyI, KeyJ, KeyK, KeyL, KeyM, KeyN, KeyO, KeyP, KeyQ, KeyR, KeyS, KeyT, KeyU, KeyV, KeyW, KeyX, KeyY, KeyZ:
		// Ordinary mode uses virtual-key translator; system-key mode without Ctrl
		// produces lowercase a..z [07 §2]. We preserve the retail behavior by
		// using Char when supplied, else uppercase Code. Ctrl composition is
		// table-driven [07 §2].
		if ch != 0 {
			code = ch
		} else {
			code = byte('A' + (key - KeyA))
		}
	case Key0, Key1, Key2, Key3, Key4, Key5, Key6, Key7, Key8, Key9:
		code = byte('0' + (key - Key0))
	case KeyF1, KeyF2, KeyF3, KeyF4, KeyF5, KeyF6, KeyF7, KeyF8, KeyF9, KeyF10, KeyF11, KeyF12:
		// F-keys are table-driven [07 §2]; map to VK_F1..VK_F12 range 0x70..0x7B
		// so they remain distinct from the held-state tokens 0xF0..0xFB.
		code = byte(0x70 + (key - KeyF1))
	default:
		// OEM ranges 0xBA..0xC0 and 0xDB..0xDE remain unknown table aliases
		// [07 §2]; return 0 meaning no new input token.
		if ch != 0 {
			code = ch
		} else {
			code = 0
		}
	}
	return Token{Code: code, Char: ch, Ctrl: ctrl}
}

// TranslateMouse builds a retail MouseRecord from Ebitengine mouse state [07 §2].
//
// x, y are window positions; they are truncated toward zero into int16 fields
// matching the low/high words of the retail message position. keyState is the
// message's key-state word (e.g., MK_LBUTTON). tick is the scaled tick count
// (GetTickCount() * timeScale / 1000) [07 §2]. msg is the original message
// number (0x200..0x206, etc.), and double indicates a double-click record
// (0x203/0x206) vs single (Double clear) [07 §2].
func TranslateMouse(x, y int16, keyState, tick, msg uint32, double bool) MouseRecord {
	return MouseRecord{
		X:        x,
		Y:        y,
		KeyState: keyState,
		Tick:     tick,
		Msg:      msg,
		Double:   double,
	}
}
