package ui

import "testing"

// ARMOPT routes LOADGAME and SAVEGAME to the two LOADGAME.GUI modes; both are
// available in a campaign and a skirmish, and only a network session greys
// them [07 R-FE-01 §7] [08 R-SAVE-02 §4].
func TestOptionsMenuRoutesSaveAndLoad(t *testing.T) {
	s := NewProductionBattleState()
	s.OpenOptions()
	if got := s.Activate("SAVEGAME"); got != BattleModalActionSaveGame {
		t.Fatalf("SAVEGAME -> %d, want the save action", got)
	}
	if s.Modal() != BattleModalOptions {
		t.Fatal("opening the save dialog closed ARMOPT; the dialog opens over it")
	}
	if got := s.Activate("LOADGAME"); got != BattleModalActionLoadGame {
		t.Fatalf("LOADGAME -> %d, want the load action", got)
	}
	// The two route names belong to ARMOPT alone; EXITMENU and YESORNO must
	// not acquire them by spelling.
	s.ShowExit()
	if got := s.Activate("SAVEGAME"); got != BattleModalActionNone {
		t.Fatalf("EXITMENU SAVEGAME -> %d, want no action", got)
	}
	s.ShowConfirmation(true)
	if got := s.Activate("LOADGAME"); got != BattleModalActionNone {
		t.Fatalf("YESORNO LOADGAME -> %d, want no action", got)
	}
}
