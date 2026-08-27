package ui

// BattleModal is the authored in-battle modal window currently at the top of
// the modal chain. The chain is ARMOPT -> EXITMENU -> YESORNO [07 "Tab
// options menu and manual exit"].
type BattleModal uint8

const (
	BattleModalClosed BattleModal = iota
	BattleModalOptions
	BattleModalExit
	BattleModalConfirmMain
	BattleModalConfirmExit
)

// BattleModalAction is the concrete result of activating a modal control.
// Navigation remains owned by the battle composition root; UI state only
// reports the authored choice [07 "Tab options menu and manual exit"].
type BattleModalAction uint8

const (
	BattleModalActionNone BattleModalAction = iota
	BattleModalActionMainMenu
	BattleModalActionExitGame
)

// BattleScheduleIntent is a plain presentation value. Session applies it at
// the scheduling boundary synchronously, so a pause transition cannot wait
// for a simulation tick [01 §4.3][07 §11].
type BattleScheduleIntent struct {
	PauseSet   bool
	Pause      bool
	SpeedDelta int
}

// BattleState owns the mutable authored modal state that is not simulation
// state. The order latch and build-placement values remain in the battle
// command composition for this unit because their world/HUD owner is shared
// with the existing command dispatcher [07 §3][07 §9].
type BattleState struct {
	modal        BattleModal
	pressed      int
	pressedModal BattleModal
}

// NewBattleState returns a closed modal state with no captured press.
func NewBattleState() *BattleState {
	return &BattleState{modal: BattleModalClosed, pressed: -1}
}

// Modal reports the top authored modal window.
func (s *BattleState) Modal() BattleModal {
	if s == nil {
		return BattleModalClosed
	}
	return s.modal
}

// HasOptionsLayer reports whether ARMOPT remains visible beneath the active
// child modal. EXITMENU and YESORNO are SAVE UNDER children [07 §11].
func (s *BattleState) HasOptionsLayer() bool {
	return s != nil && s.modal != BattleModalClosed
}

// HasExitLayer reports whether EXITMENU remains visible beneath YESORNO.
func (s *BattleState) HasExitLayer() bool {
	if s == nil {
		return false
	}
	return s.modal == BattleModalExit || s.modal == BattleModalConfirmMain || s.modal == BattleModalConfirmExit
}

// ConfirmTitle selects the authored confirmation title variant.
func (s *BattleState) ConfirmTitle() string {
	if s == nil {
		return ""
	}
	switch s.modal {
	case BattleModalConfirmMain:
		return "Surrender this battle and return to main menu?"
	case BattleModalConfirmExit:
		return "Exit the Battle"
	default:
		return ""
	}
}

// OpenOptions opens ARMOPT and emits the synchronous local pause intent.
func (s *BattleState) OpenOptions() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	s.modal = BattleModalOptions
	s.ClearModalPress()
	return BattleScheduleIntent{PauseSet: true, Pause: true}
}

// CloseOptions closes the complete modal chain and emits the synchronous
// resume intent. This is the only modal transition that resumes battle time.
func (s *BattleState) CloseOptions() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	s.modal = BattleModalClosed
	s.ClearModalPress()
	return BattleScheduleIntent{PauseSet: true, Pause: false}
}

// Back applies the authored Escape/back transition and returns the pause
// intent only when the root options window is closed.
func (s *BattleState) Back() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	s.ClearModalPress()
	switch s.modal {
	case BattleModalOptions:
		return s.CloseOptions()
	case BattleModalConfirmMain, BattleModalConfirmExit:
		s.modal = BattleModalExit
	case BattleModalExit:
		s.modal = BattleModalOptions
	}
	return BattleScheduleIntent{}
}

// ShowExit pushes EXITMENU over ARMOPT.
func (s *BattleState) ShowExit() {
	if s != nil && s.modal == BattleModalOptions {
		s.modal = BattleModalExit
		s.ClearModalPress()
	}
}

// ShowConfirmation pushes YESORNO over EXITMENU with the requested outcome.
func (s *BattleState) ShowConfirmation(mainMenu bool) {
	if s == nil || s.modal != BattleModalExit {
		return
	}
	if mainMenu {
		s.modal = BattleModalConfirmMain
	} else {
		s.modal = BattleModalConfirmExit
	}
	s.ClearModalPress()
}

// Activate applies one authored modal button and returns a concrete action for
// the composition owner. Only CHOICE1 commits YESORNO [07 §11].
func (s *BattleState) Activate(name string) BattleModalAction {
	if s == nil {
		return BattleModalActionNone
	}
	switch s.modal {
	case BattleModalOptions:
		switch Key(name) {
		case "ok", "cancel":
			s.CloseOptions()
		case "exit":
			s.ShowExit()
		}
	case BattleModalExit:
		switch Key(name) {
		case "cancel":
			s.modal = BattleModalOptions
		case "mainmenu":
			s.ShowConfirmation(true)
		case "exitgame":
			s.ShowConfirmation(false)
		}
	case BattleModalConfirmMain, BattleModalConfirmExit:
		switch Key(name) {
		case "choice2", "cancel":
			s.modal = BattleModalExit
		case "choice1":
			if s.modal == BattleModalConfirmMain {
				return BattleModalActionMainMenu
			}
			return BattleModalActionExitGame
		}
	}
	return BattleModalActionNone
}

// PressModal captures a gadget and the modal that owned it. A later release
// must match both values to activate, preserving release-inside semantics.
func (s *BattleState) PressModal(index int) {
	if s == nil {
		return
	}
	s.pressed = index
	s.pressedModal = s.modal
}

// ReleaseModal completes the captured gesture only when the same gadget is
// still under the pointer and the modal has not changed meanwhile.
func (s *BattleState) ReleaseModal(index int) (int, bool) {
	if s == nil {
		return -1, false
	}
	pressed, modal := s.pressed, s.pressedModal
	s.ClearModalPress()
	if pressed < 0 || pressed != index || modal != s.modal {
		return -1, false
	}
	return pressed, true
}

// ClearModalPress cancels an in-flight modal gesture.
func (s *BattleState) ClearModalPress() {
	if s == nil {
		return
	}
	s.pressed = -1
	s.pressedModal = BattleModalClosed
}

// PauseIntent creates the concrete pause value used by modal entry/exit and
// by the pause hotkey.
func PauseIntent(paused bool) BattleScheduleIntent {
	return BattleScheduleIntent{PauseSet: true, Pause: paused}
}

// SpeedIntent creates a concrete relative speed request.
func SpeedIntent(delta int) BattleScheduleIntent {
	return BattleScheduleIntent{SpeedDelta: delta}
}
