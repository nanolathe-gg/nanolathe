package triggers

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// rawTriggerAccounts is the conversion the session save path performs before
// the bank writer sees the image; restoring goes back through the shipped
// RestoreSaveAccounts [08 R-TRIG-01 §8].
func rawTriggerAccounts(in []RetailTriggerAccount) []save.RawAccount {
	out := make([]save.RawAccount, 0, len(in))
	for _, account := range in {
		raw := save.RawAccount{Name: account.Name}
		for _, item := range account.Ints {
			raw.Ints = append(raw.Ints, save.IntItem{Name: item.Name, Value: item.Value})
		}
		for _, box := range account.Boxes {
			raw.Boxes = append(raw.Boxes, save.RawBox{Name: box.Name, Number: box.Number, Data: append([]byte(nil), box.Data...)})
		}
		out = append(out, raw)
	}
	return out
}

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
	if err := RestoreSaveAccounts(restoredWin, restoredLose, rawTriggerAccounts(accounts)); err != nil {
		t.Fatal(err)
	}
	if !restoredWin[0].Completed || restoredWin[1].Args[0] != 7 || !restoredLose[0].Celebrated {
		t.Fatalf("restored values win=%#v lose=%#v", restoredWin, restoredLose)
	}
}

// TestKillAllMobileUnitsWritesNumUnitsFirst locks the item order of the one
// condition that persists a third item ahead of the flags: KillAllMobileUnits
// writes NumUnits, then Satisfied, then Celebrated [08 R-TRIG-01 §8]. The
// counted death families instead APPEND their NumLeftToKill after the flags,
// so the two three-item shapes are not interchangeable.
//
// The value is 0 because this port keeps no scratch count for the condition
// (retail's is recomputed before any read, so the round trip is byte-level
// only); the ordering is the contract under test.
func TestKillAllMobileUnitsWritesNumUnitsFirst(t *testing.T) {
	trigger := New(KindKillAllMobileUnits, "")
	trigger.Completed = true
	trigger.Celebrated = true
	accounts, err := RetailTriggerImage([]*Trigger{trigger}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != "VictoryCondition_KillAllMobileUnits" {
		t.Fatalf("accounts = %#v", accounts)
	}
	want := []RetailTriggerInt{
		{Name: "NumUnits", Value: 0},
		{Name: "Satisfied", Value: 1},
		{Name: "Celebrated", Value: 1},
	}
	if len(accounts[0].Ints) != len(want) {
		t.Fatalf("items = %#v, want %#v", accounts[0].Ints, want)
	}
	for i := range want {
		if accounts[0].Ints[i] != want[i] {
			t.Fatalf("items = %#v, want %#v", accounts[0].Ints, want)
		}
	}
	// Round-tripping the account must not fail and must not disturb the flags.
	restored := New(KindKillAllMobileUnits, "")
	if err := RestoreSaveAccounts([]*Trigger{restored}, nil, rawTriggerAccounts(accounts)); err != nil {
		t.Fatal(err)
	}
	if !restored.Completed || !restored.Celebrated {
		t.Fatalf("restored = %#v", restored)
	}
	// NumUnits is not a NumLeftToKill: it must never reach the argument slot,
	// whichever condition the account names.
	records := rawTriggerAccounts(accounts)
	records[0].Ints[0].Value = 77
	reject := New(KindKillAllMobileUnits, "")
	if err := RestoreSaveAccounts([]*Trigger{reject}, nil, records); err != nil {
		t.Fatal(err)
	}
	if reject.Args[0] != 0 {
		t.Fatalf("NumUnits leaked into Args[0] = %d", reject.Args[0])
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
	if err := RestoreSaveAccounts(restoredVictory, restoredDefeat, rawTriggerAccounts(accounts)); err != nil {
		t.Fatal(err)
	}
	if !restoredVictory[0].Celebrated {
		t.Fatalf("default victory celebration was not restored: %+v", restoredVictory[0])
	}
}
