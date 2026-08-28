// Platform-neutral key and mouse-button vocabulary.
//
// These mirror the retail virtual-key roles (movement, modifiers, arrows)
// without binding presentation to any one window system [07 §2].
package input

// Key identifies a keyboard key for the windowed client paths.
type Key uint8

const (
	KeyNone Key = iota
	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyLeft
	KeyUp
	KeyRight
	KeyDown
	KeyHome
	KeyEnd
	KeyPrior // PageUp
	KeyNext  // PageDown
	KeyInsert
	KeyDelete
	KeyBackspace
	KeyTab
	KeySpace
	KeyEnter
	KeyEscape
	KeyPause
	KeyShift
	KeyCtrl
	KeyAlt
	KeyMinus // '-' / '_'  [07 §2] game-speed decrease retail equivalent
	KeyEqual // '=' / '+'  [07 §2] game-speed increase retail equivalent
	KeyNumpadAdd
	KeyNumpadSubtract
	KeyCount
)

// MouseButton identifies a mouse button for the windowed client paths.
type MouseButton uint8

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
)
