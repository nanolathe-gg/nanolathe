package ui

// ResultAction is the semantic action emitted by an authored end-mission
// control. The frontend adapter turns it into the existing battle transition;
// UI does not touch session state [07 §11].
type ResultAction uint8

const (
	ResultActionNone ResultAction = iota
	ResultActionContinue
)

// ResultActionForControl accepts only the authored control whose continuation
// behavior is established. Unknown controls remain inert rather than gaining
// guessed aliases or synthetic routes.
func ResultActionForControl(name string) ResultAction {
	if Key(name) == "start" {
		return ResultActionContinue
	}
	return ResultActionNone
}
