package ui

import "testing"

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
