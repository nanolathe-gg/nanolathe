package ui

import (
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Panel detents and throttle are authored battle-rail values [07 §6].  The
// timing is presentation-only; it never enters the simulation clock [I6].
const (
	// BattleEntryMode is the established production battle-composition mode
	// value. Its 0x04 bit starts the command panel visible [07 §6] C13; it is
	// not the unrelated digit-routing mode bit.
	BattleEntryMode byte   = 0x04
	PanelParked     int8   = -31
	PanelVisible    int8   = 0
	PanelThrottleMs uint32 = 15
)

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
	// BattleModalRestart replaces EXITMENU while the authored RESTART.GUI
	// child is open. ARMOPT remains the pause-owning root underneath it
	// [07 R-FE-01 §7][08 R-CAMP-01 §8].
	BattleModalRestart
)

// BattleModalAction is the concrete result of activating a modal control.
// Navigation remains owned by the battle composition root; UI state only
// reports the authored choice [07 "Tab options menu and manual exit"].
type BattleModalAction uint8

const (
	BattleModalActionNone BattleModalAction = iota
	BattleModalActionMainMenu
	BattleModalActionExitGame
	// ARMOPT routes LOADGAME and SAVEGAME to the two LOADGAME.GUI modes. Both
	// gadgets are available in a campaign (session kind 1) and a skirmish
	// (kind 2); only network multiplayer greys them, and this build has no
	// network session [07 R-FE-01 §7] [08 R-SAVE-02 §4].
	BattleModalActionSaveGame
	BattleModalActionLoadGame
	// ARMOPT's PREFS opens the options root as a child window over the battle.
	// ARMOPT stays on the modal chain underneath and the pause bit it set stays
	// set, so this reports the request and emits no schedule intent
	// [07 R-FE-01 §6][07 R-FE-01 §7].
	BattleModalActionPrefs
	// RESTART.GUI owns only a three-stage difficulty selector and the
	// request-producing button. The battle composition supplies the stage and
	// consumes the request because both cross the session boundary.
	BattleModalActionRestartDifficulty
	BattleModalActionRestart
)

// BattleScheduleIntent is a plain presentation value. Session applies it at
// the scheduling boundary synchronously, so a pause transition cannot wait
// for a simulation tick [01 §4.3][07 §11].
type BattleScheduleIntent struct {
	PauseSet   bool
	Pause      bool
	SpeedDelta int
}

// BattleInputState is the single owner of mutable battle presentation/input
// state. It contains no simulation handles or services: the command adapter
// reads this value, turns gestures into semantic session commands, and leaves
// application to Session.EnqueueHumanCommand [01 §4.4][07 §3][07 §9].
//
// Keeping the gesture latches together is important. A HUD press, placement
// press, or world drag owns the complete button gesture; separate owners can
// otherwise observe the same release and issue two actions.
type BattleInputState struct {
	Latch input.Latch

	DragActive       bool
	DragStartX       int32
	DragStartY       int32
	DragEndX         int32
	DragEndY         int32
	HUDCaptured      bool
	HUDPressX        int32
	HUDPressY        int32
	PlaceCaptured    bool
	ShiftHeld        bool
	ShiftLatchSticky bool
	PointerX         int32
	PointerY         int32
	PrevMouseX       float32
	PrevMouseY       float32

	BuildDef    string
	BuildFootX  int32
	BuildFootZ  int32
	BuildOK     bool
	BuildMX     int32
	BuildMY     int32
	BuildCellX  int32
	BuildCellZ  int32
	BuildSiteH  int32
	BuildSticky bool

	ResultDismissed bool
}

// BattleState owns all mutable authored battle-interface state that is not
// simulation state: modal windows, latches, gestures, placement, status, and
// result dismissal. The command composition layer only adapts this state to
// Session.EnqueueHumanCommand [07 §3][07 §9].
type BattleState struct {
	modal        BattleModal
	pressed      int
	pressedModal BattleModal
	// paused is the UI's synchronous pause truth. It is initialized to the
	// running production state and updated only by the scheduling boundary;
	// committed frames may synchronize it until that first UI-issued pause
	// transition, because a paused session can publish no newer tick [01 §4.3]
	// [07 §11].
	paused              bool
	pauseScheduleIssued bool

	// Input is intentionally public as a plain value so the cmd adapter can
	// render and update it without introducing another state bridge. No mutable
	// simulation state is reachable through this value [I6].
	Input BattleInputState

	// PanelOffset, PanelTarget, and PanelLastThrottle are the sole owner of the
	// §6 slide. Only the input / host-frame update advances it [07 §6][I6].
	//
	// What the offset moves is the bottom slide strip — `Game Time` /
	// `Total Units` / `Game Speed` — which "slides up from the bottom edge of
	// the view when Space is held" [07 R-HUD-03 §1 "the panel-slide gate"] and
	// is drawn at `(x, yBottom + off)`, off screen at 0 [07 R-HUD-04 §4]. It is
	// NOT a side-rail offset: PANELSIDE is stamped at (0,0) and nowhere else,
	// and "every rail window and gadget rectangle" is fixed in authored
	// coordinates [07 R-HUD-05]. The one reader is the composer's slide-strip
	// draw; nothing else may translate art or a hit test by this word
	// (WU-19-223).
	//
	// PanelParked/PanelVisible keep the names §6's parenthetical labels gave
	// them (-31 "parked", 0 "fully visible"); by the two later closures above
	// the strip is fully drawn at -31 and invisible at 0. The arithmetic is the
	// same under either label, so the constant names are left alone.
	PanelOffset       int8
	PanelTarget       int8
	PanelLastThrottle uint32
	panelCue          func(string)
}

// NewBattleState returns a closed modal state with no captured press. The
// entering session mode byte is the sole source for the initial rail detent:
// bit 0x04 means visible, while a clear bit means parked [07 §6] C13.
func NewBattleState(modeByte byte) *BattleState {
	offset := PanelParked
	if modeByte&0x04 != 0 {
		offset = PanelVisible
	}
	return &BattleState{modal: BattleModalClosed, pressed: -1, paused: false, Input: BattleInputState{Latch: input.LatchNormal}, PanelOffset: offset, PanelTarget: offset}
}

// NewProductionBattleState applies the established battle-entry composition
// value. Keeping this in ui makes every production construction path use the
// same canonical initialization while tests can still exercise both mode
// polarities through NewBattleState [07 §6] C13.
func NewProductionBattleState() *BattleState {
	return NewBattleState(BattleEntryMode)
}

// SetPanelCue installs the optional authored cue sink. UI owns when a detent
// transition occurs; the composition root owns how the cue is played.
func (s *BattleState) SetPanelCue(cue func(string)) {
	if s != nil {
		s.panelCue = cue
	}
}

// SetPanelTarget applies the retail Space/editor polarity [07 §6] C14.
func (s *BattleState) SetPanelTarget(spaceHeld, editorFocused bool) {
	if s == nil {
		return
	}
	if spaceHeld && !editorFocused {
		s.PanelTarget = PanelParked
	} else {
		s.PanelTarget = PanelVisible
	}
}

// AdvancePanel advances one slide step at an explicit wall-clock timestamp.
// Early timestamps are ignored; accepted steps ease by remaining/3 with a
// one-pixel minimum, and detent transitions emit the established cues [07 §6].
func (s *BattleState) AdvancePanel(now uint32, spaceHeld, editorFocused bool) {
	if s == nil {
		return
	}
	s.SetPanelTarget(spaceHeld, editorFocused)
	if now-s.PanelLastThrottle < PanelThrottleMs {
		return
	}
	s.PanelLastThrottle = now
	if s.PanelOffset == s.PanelTarget {
		return
	}
	if (s.PanelOffset == PanelParked && s.PanelTarget == PanelVisible) || (s.PanelOffset == PanelVisible && s.PanelTarget == PanelParked) {
		if s.panelCue != nil {
			s.panelCue("Panel")
		}
	}
	remaining := int(s.PanelTarget) - int(s.PanelOffset)
	step := remaining / 3
	if step == 0 {
		if remaining > 0 {
			step = 1
		} else {
			step = -1
		}
	}
	next := int(s.PanelOffset) + step
	if remaining > 0 && next > int(s.PanelTarget) {
		next = int(s.PanelTarget)
	}
	if remaining < 0 && next < int(s.PanelTarget) {
		next = int(s.PanelTarget)
	}
	previous := s.PanelOffset
	s.PanelOffset = int8(next)
	if s.PanelOffset != previous && (s.PanelOffset == PanelVisible || s.PanelOffset == PanelParked) && s.panelCue != nil {
		s.panelCue("Options")
	}
}

// AdvancePanelNow is the wall-clock host-frame entry point [07 §6][I6].
func (s *BattleState) AdvancePanelNow(spaceHeld, editorFocused bool) {
	if s == nil {
		return
	}
	s.AdvancePanel(uint32(time.Now().UnixMilli()&0xffffffff), spaceHeld, editorFocused)
}

// Latch returns the currently armed semantic order. A nil state is idle.
func (s *BattleState) Latch() input.Latch {
	if s == nil || !s.Input.Latch.IsValid() {
		return input.LatchNormal
	}
	return s.Input.Latch
}

// SetLatch changes the semantic order latch. Invalid values are rejected so a
// malformed button name cannot leak an arbitrary dispatcher index into the
// command adapter [07 §9].
func (s *BattleState) SetLatch(l input.Latch) bool {
	if s == nil || !l.IsValid() {
		return false
	}
	s.Input.Latch = l
	return true
}

// ResetInteraction clears in-flight gesture state and returns the order latch
// to idle. Placement data is cleared too, matching the retail cancel path
// [07 §9].
func (s *BattleState) ResetInteraction() {
	if s == nil {
		return
	}
	// Reinitialize the complete presentation/input value so no stale pointer,
	// modifier, status, result, or gesture bit survives a modal/result reset.
	s.Input = BattleInputState{Latch: input.LatchNormal}
}

// ArmPlacement records the authored product and footprint for the mobile
// build ghost. The caller supplies compiled content values; UI never invents
// a product or validates simulation placement [07 §9].
func (s *BattleState) ArmPlacement(product string, footX, footZ int32) {
	if s == nil {
		return
	}
	s.Input.BuildDef = product
	s.Input.BuildFootX, s.Input.BuildFootZ = footX, footZ
	s.Input.BuildOK = false
	s.Input.BuildSticky = false
	s.Input.Latch = input.LatchMobileBuild
}

// ClearPlacement disarms the mobile-build ghost and clears its derived site.
func (s *BattleState) ClearPlacement() {
	if s == nil {
		return
	}
	s.Input.BuildDef = ""
	s.Input.BuildFootX, s.Input.BuildFootZ = 0, 0
	s.Input.BuildOK = false
	s.Input.BuildMX, s.Input.BuildMY = 0, 0
	s.Input.BuildCellX, s.Input.BuildCellZ = 0, 0
	s.Input.BuildSiteH = 0
	s.Input.BuildSticky = false
	if s.Input.Latch == input.LatchMobileBuild {
		s.Input.Latch = input.LatchNormal
	}
}

// Modal reports the top authored modal window.
func (s *BattleState) Modal() BattleModal {
	if s == nil {
		return BattleModalClosed
	}
	return s.modal
}

// HasOptionsLayer reports whether ARMOPT remains visible beneath the active
// child modal. The root survives exit/confirmation replacement [07 R-FE-01 §7].
func (s *BattleState) HasOptionsLayer() bool {
	return s != nil && s.modal != BattleModalClosed
}

// HasExitLayer reports whether EXITMENU itself is open. Its callback closes
// it before opening YESORNO [07 R-FE-01 §7].
func (s *BattleState) HasExitLayer() bool {
	if s == nil {
		return false
	}
	return s.modal == BattleModalExit
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
		return "Surrender this battle and exit to Windows?"
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
	case BattleModalConfirmMain, BattleModalConfirmExit, BattleModalRestart:
		s.modal = BattleModalOptions
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

// ShowConfirmation replaces EXITMENU with YESORNO above the surviving options
// root [07 R-FE-01 §7].
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

// ShowRestart replaces EXITMENU with RESTART.GUI above the surviving options
// root. The caller has already established that this is a campaign or
// skirmish battle; multiplayer never reaches this branch [07 R-FE-01 §7].
func (s *BattleState) ShowRestart() {
	if s == nil || s.modal != BattleModalExit {
		return
	}
	s.modal = BattleModalRestart
	s.ClearModalPress()
}

// Activate applies one authored modal button and returns a concrete action for
// the composition owner. Only CHOICE1 commits YESORNO [07 §11].
func (s *BattleState) Activate(name string) BattleModalAction {
	if s == nil {
		return BattleModalActionNone
	}
	name = gui.CallbackName(name)
	switch s.modal {
	case BattleModalOptions:
		switch name {
		case "OK", "CANCEL":
			s.CloseOptions()
		case "EXIT":
			s.ShowExit()
		case "SAVEGAME":
			// The dialog opens over ARMOPT, which stays on the modal chain
			// [07 R-FE-01 §7] [07 R-FE-01 §8].
			return BattleModalActionSaveGame
		case "LOADGAME":
			return BattleModalActionLoadGame
		case "PREFS":
			// The options root opens over ARMOPT, which stays on the modal
			// chain [07 R-FE-01 §6][07 R-FE-01 §7].
			return BattleModalActionPrefs
		}
	case BattleModalExit:
		switch name {
		case "CANCEL":
			s.modal = BattleModalOptions
		case "RESTART":
			s.ShowRestart()
		case "MAINMENU":
			s.ShowConfirmation(true)
		case "EXITGAME":
			s.ShowConfirmation(false)
		}
	case BattleModalConfirmMain, BattleModalConfirmExit:
		switch name {
		case "CHOICE2", "CANCEL":
			s.modal = BattleModalOptions
		case "CHOICE1":
			if s.modal == BattleModalConfirmMain {
				return BattleModalActionMainMenu
			}
			return BattleModalActionExitGame
		}
	case BattleModalRestart:
		switch name {
		case "CANCEL":
			s.modal = BattleModalOptions
		case "Difficulty":
			return BattleModalActionRestartDifficulty
		case "RESTART":
			return BattleModalActionRestart
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

// Paused reports the canonical UI pause truth. It is intentionally separate
// from the last committed frame: pausing may stop simulation before another
// tick-end publication can carry the new state [01 §4.3][I6].
func (s *BattleState) Paused() bool {
	return s != nil && s.paused
}

// SetPauseTruth records the actual result returned by Session.SetPaused at the
// scheduling boundary. Once UI has issued a pause transition, later committed
// frames cannot overwrite this synchronous truth with a stale tick [I6].
func (s *BattleState) SetPauseTruth(paused bool) {
	if s == nil {
		return
	}
	s.paused = paused
	s.pauseScheduleIssued = true
}

// SyncCommittedPause adopts the first/current committed pause value only
// before a UI-issued scheduling transition. This keeps initial presentation
// aligned while preserving immediate pause/unpause transitions [I6].
func (s *BattleState) SyncCommittedPause(paused bool) {
	if s == nil || s.pauseScheduleIssued {
		return
	}
	s.paused = paused
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
