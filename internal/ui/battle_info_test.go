package ui

import "testing"

// ARMOPT's `MISSION` branches on the session kind: a campaign mission reaches
// the in-battle briefing, every other kind the read-only game-settings
// overlay; `HELP` always reaches HELP.GUI. Each child's `OK` returns to the
// surviving options root, which never resumes the battle
// [07 R-FE-01 §7][07 R-WGT-01 §1].
func TestOptionsMissionAndHelpOpenTheirChildren(t *testing.T) {
	for _, tc := range []struct {
		name     string
		campaign bool
		button   string
		want     BattleModal
		action   BattleModalAction
	}{
		{"skirmish MISSION", false, "MISSION", BattleModalGameOptions, BattleModalActionMission},
		{"campaign MISSION", true, "MISSION", BattleModalBriefing, BattleModalActionMission},
		{"skirmish HELP", false, "HELP", BattleModalHelp, BattleModalActionHelp},
		{"campaign HELP", true, "HELP", BattleModalHelp, BattleModalActionHelp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewProductionBattleState()
			s.SetCampaign(tc.campaign)
			s.OpenOptions()
			if action := s.Activate(tc.button); action != tc.action {
				t.Fatalf("%s reported action %d, want %d", tc.button, action, tc.action)
			}
			if got := s.Modal(); got != tc.want {
				t.Fatalf("%s opened modal %d, want %d", tc.button, got, tc.want)
			}
			if !s.HasOptionsLayer() {
				t.Fatal("the options root did not survive under its child")
			}
			if intent := s.Activate("OK"); intent != BattleModalActionNone {
				t.Fatalf("the child's OK reported action %d, want none", intent)
			}
			if got := s.Modal(); got != BattleModalOptions {
				t.Fatalf("the child's OK left modal %d, want the options root", got)
			}
		})
	}
}

// Escape is the modal chain's back transition, and it returns each child to
// the options root without resuming the battle [07 R-FE-01 §7].
func TestBackFromOptionsChildrenKeepsThePause(t *testing.T) {
	for _, modal := range []BattleModal{BattleModalBriefing, BattleModalGameOptions, BattleModalHelp} {
		s := NewProductionBattleState()
		s.OpenOptions()
		switch modal {
		case BattleModalBriefing:
			s.ShowBriefing()
		case BattleModalGameOptions:
			s.ShowGameOptions()
		case BattleModalHelp:
			s.ShowHelp()
		}
		if s.Modal() != modal {
			t.Fatalf("child %d did not open", modal)
		}
		intent := s.Back()
		if intent.PauseSet {
			t.Fatalf("closing child %d emitted a schedule intent", modal)
		}
		if s.Modal() != BattleModalOptions {
			t.Fatalf("Escape from child %d left modal %d", modal, s.Modal())
		}
	}
}

// The two page controls report a content request and leave the child open
// [07 R-FE-01 §7][07 R-HUD-03 §10].
func TestOptionsChildPageControlsStayOpen(t *testing.T) {
	s := NewProductionBattleState()
	s.OpenOptions()
	s.ShowHelp()
	if action := s.Activate("Page"); action != BattleModalActionHelpPage {
		t.Fatalf("HELP Page reported action %d", action)
	}
	if s.Modal() != BattleModalHelp {
		t.Fatal("HELP Page closed the window")
	}
	s = NewProductionBattleState()
	s.SetCampaign(true)
	s.OpenOptions()
	s.Activate("MISSION")
	for _, name := range []string{"TextRegion", "MOREBAR"} {
		if action := s.Activate(name); action != BattleModalActionBriefingPage {
			t.Fatalf("briefing %s reported action %d", name, action)
		}
		if s.Modal() != BattleModalBriefing {
			t.Fatalf("briefing %s closed the window", name)
		}
	}
}
