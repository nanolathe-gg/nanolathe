package ui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestBattleStateModalChainAndReleaseCapture(t *testing.T) {
	s := NewBattleState()
	if got := s.Modal(); got != BattleModalClosed {
		t.Fatalf("initial modal=%d, want closed", got)
	}
	if intent := s.OpenOptions(); !intent.PauseSet || !intent.Pause || s.Modal() != BattleModalOptions {
		t.Fatalf("open options modal=%d intent=%+v", s.Modal(), intent)
	}
	s.PressModal(4)
	if got, ok := s.ReleaseModal(3); ok || got != -1 {
		t.Fatalf("release outside captured gadget got=%d ok=%t", got, ok)
	}
	s.PressModal(4)
	if got, ok := s.ReleaseModal(4); !ok || got != 4 {
		t.Fatalf("release inside captured gadget got=%d ok=%t", got, ok)
	}
	s.ShowExit()
	s.ShowConfirmation(true)
	if got := s.Activate("CHOICE2"); got != BattleModalActionNone || s.Modal() != BattleModalExit {
		t.Fatalf("choice2 modal=%d action=%d", s.Modal(), got)
	}
	s.ShowConfirmation(false)
	if got := s.Activate("CHOICE1"); got != BattleModalActionExitGame || s.Modal() != BattleModalConfirmExit {
		t.Fatalf("choice1 action=%d modal=%d", got, s.Modal())
	}
	if intent := s.Back(); intent.PauseSet || s.Modal() != BattleModalExit {
		t.Fatalf("back from confirm intent=%+v modal=%d", intent, s.Modal())
	}
	if intent := s.Back(); intent.PauseSet || s.Modal() != BattleModalOptions {
		t.Fatalf("back from exit intent=%+v modal=%d", intent, s.Modal())
	}
	if intent := s.Back(); !intent.PauseSet || intent.Pause || s.Modal() != BattleModalClosed {
		t.Fatalf("back from options intent=%+v modal=%d", intent, s.Modal())
	}
}

func TestBattleStateScheduleIntentValues(t *testing.T) {
	if got := PauseIntent(true); !got.PauseSet || !got.Pause || got.SpeedDelta != 0 {
		t.Fatalf("pause intent=%+v", got)
	}
	if got := SpeedIntent(-1); got.PauseSet || got.SpeedDelta != -1 {
		t.Fatalf("speed intent=%+v", got)
	}
}

func TestBattleStateOwnsInputAndPlacementState(t *testing.T) {
	s := NewBattleState()
	if s.Input.Latch != input.LatchNormal || s.Input.BuildDef != "" {
		t.Fatalf("initial input state=%+v", s.Input)
	}
	s.ArmPlacement("armmex", 2, 3)
	if s.Input.Latch != input.LatchMobileBuild || s.Input.BuildDef != "armmex" || s.Input.BuildFootX != 2 || s.Input.BuildFootZ != 3 {
		t.Fatalf("armed placement=%+v", s.Input)
	}
	s.Input.BuildOK = true
	s.Input.BuildCellX, s.Input.BuildCellZ, s.Input.BuildSiteH = 4, 5, 6
	s.ClearPlacement()
	if s.Input.Latch != input.LatchNormal || s.Input.BuildDef != "" || s.Input.BuildOK || s.Input.BuildCellX != 0 || s.Input.BuildSiteH != 0 {
		t.Fatalf("cleared placement=%+v", s.Input)
	}
	s.Input.DragActive = true
	s.Input.HUDCaptured = true
	s.Input.PlaceCaptured = true
	s.Input.ShiftLatchSticky = true
	s.Input.PointerX, s.Input.PointerY = 10, 20
	s.Input.ShiftHeld = true
	s.Input.StatusMessage, s.Input.StatusUntil = "paused", 90
	s.Input.ResultDismissed = true
	s.ResetInteraction()
	if s.Input.DragActive || s.Input.HUDCaptured || s.Input.PlaceCaptured || s.Input.ShiftLatchSticky || s.Input.PointerX != 0 || s.Input.PointerY != 0 || s.Input.ShiftHeld || s.Input.StatusMessage != "" || s.Input.StatusUntil != 0 || s.Input.ResultDismissed || s.Input.Latch != input.LatchNormal {
		t.Fatalf("reset interaction=%+v", s.Input)
	}
}
