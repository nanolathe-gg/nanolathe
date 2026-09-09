package ui

import "github.com/nanolathe-gg/nanolathe/internal/gui"

// ResultAction is the semantic action emitted by an authored end-mission
// control. It is passed across the UI boundary as a typed value; no control
// name or string route is used after this point [07 §11].
type ResultAction uint8

const (
	ResultActionNone ResultAction = iota
	ResultActionContinue
	ResultActionMainMenu
	// ResultActionSkirmish is retained for the existing skirmish return route;
	// ENDMSN itself does not author a control that emits it [07 §11].
	ResultActionSkirmish
)

// ResultActionForControl accepts only authored ENDMSN route controls. Unknown
// controls remain inert rather than gaining guessed aliases or synthetic routes
// [07 §11].
func ResultActionForControl(name string) ResultAction {
	switch gui.CallbackName(name) {
	case "Start":
		return ResultActionContinue
	case "MainMenu":
		return ResultActionMainMenu
	}
	return ResultActionNone
}
