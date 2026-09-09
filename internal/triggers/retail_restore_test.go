package triggers

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/save"
)

func TestRestoreSaveAccountsTypedState(t *testing.T) {
	win := New(KindKillUnitType, "ARMCOM", 4)
	lose := New(KindUnitTypeKilled, "CORECOM", 3)
	records := []save.RawAccount{
		{Name: "VictoryCondition_KillUnitType", Ints: []save.IntItem{{Name: "Satisfied", Value: 1}, {Name: "Celebrated", Value: 1}, {Name: "NumLeftToKill", Value: 2}, {Name: "NumUnits", Value: 99}}},
		{Name: "DefeatCondition_UnitTypeKilled", Ints: []save.IntItem{{Name: "NumLeftToKill", Value: 1}}},
	}
	if err := RestoreSaveAccounts([]*Trigger{win}, []*Trigger{lose}, records); err != nil {
		t.Fatal(err)
	}
	if !win.Completed || !win.Celebrated || win.Args[0] != 2 || lose.Args[0] != 1 {
		t.Fatalf("restored triggers: win=%#v lose=%#v", win, lose)
	}
}

func TestRestoreSaveAccountsRejectsAmbiguousAccount(t *testing.T) {
	records := []save.RawAccount{{Name: "VictoryCondition_DestroyAllUnits", Ints: []save.IntItem{{Name: "Satisfied", Value: 1}}}}
	err := RestoreSaveAccounts([]*Trigger{New(KindDestroyAllUnits, ""), New(KindDestroyAllUnits, "")}, nil, records)
	if err == nil {
		t.Fatal("ambiguous trigger account accepted")
	}
}
