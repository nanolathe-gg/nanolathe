package triggers

import "testing"

func TestRetailTriggerImageOrderAndRestore(t *testing.T) {
	victory := []*Trigger{New(KindDestroyAllUnits, ""), New(KindKillUnitType, "ARMCOM", 7)}
	victory[0].Completed = true
	defeat := []*Trigger{New(KindDeathTimerRunsOut, "")}
	defeat[0].Celebrated = true
	accounts, err := RetailTriggerImage(victory, defeat)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 || accounts[0].Name != "VictoryCondition_DestroyAllUnits" || accounts[1].Name != "VictoryCondition_KillUnitType" || accounts[2].Name != "DefeatCondition_DeathTimerRunsOut" {
		t.Fatalf("account order = %#v", accounts)
	}
	if len(accounts[1].Ints) != 3 || accounts[1].Ints[0] != (RetailTriggerInt{Name: "Satisfied"}) || accounts[1].Ints[1] != (RetailTriggerInt{Name: "Celebrated"}) || accounts[1].Ints[2] != (RetailTriggerInt{Name: "NumLeftToKill", Value: 7}) {
		t.Fatalf("counted account items = %#v", accounts[1].Ints)
	}
	restoredWin := []*Trigger{New(KindDestroyAllUnits, ""), New(KindKillUnitType, "ARMCOM", 99)}
	restoredLose := []*Trigger{New(KindDeathTimerRunsOut, "")}
	if err := RestoreRetailTriggerAccounts(restoredWin, restoredLose, accounts); err != nil {
		t.Fatal(err)
	}
	if !restoredWin[0].Completed || restoredWin[1].Args[0] != 7 || !restoredLose[0].Celebrated {
		t.Fatalf("restored values win=%#v lose=%#v", restoredWin, restoredLose)
	}
}

func TestRetailTriggerImageRejectsUnknownKind(t *testing.T) {
	if _, err := RetailTriggerImage([]*Trigger{{Kind: Kind(KindCount)}}, nil); err == nil {
		t.Fatal("unknown trigger kind accepted")
	}
}

func TestDefaultTriggerCelebrationSurvivesRetailSave(t *testing.T) {
	victory, defeat := EnsureDefaults(nil, nil)
	victory[0].Celebrated = true
	accounts, err := RetailTriggerImage(victory, defeat)
	if err != nil {
		t.Fatal(err)
	}
	restoredVictory, restoredDefeat := EnsureDefaults(nil, nil)
	if err := RestoreRetailTriggerAccounts(restoredVictory, restoredDefeat, accounts); err != nil {
		t.Fatal(err)
	}
	if !restoredVictory[0].Celebrated {
		t.Fatalf("default victory celebration was not restored: %+v", restoredVictory[0])
	}
}
