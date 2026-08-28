package ui

// ResultAction is the semantic action emitted by an authored end-mission
// control. The frontend adapter turns it into the existing battle transition;
// UI does not touch session state [07 §11].
type ResultAction uint8

const (
	ResultActionNone ResultAction = iota
	ResultActionContinue
	ResultActionMainMenu
)

// ResultActionForControl accepts only authored ENDMSN route controls. Unknown
// controls remain inert rather than gaining guessed aliases or synthetic routes
// [07 §11].
func ResultActionForControl(name string) ResultAction {
	switch Key(name) {
	case "start":
		return ResultActionContinue
	case "mainmenu":
		return ResultActionMainMenu
	}
	return ResultActionNone
}
