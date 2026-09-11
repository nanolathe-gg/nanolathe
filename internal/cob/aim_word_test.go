package cob

import "testing"

// Save restoration copies the whole readiness word; ordinary receiver
// delivery preserves zero or writes exactly one [08 R-SAVE-WEAPON-01]
// [04 R-CB-01 §6].
func TestRestoredAimWordFollowsLiveReceiverWrites(t *testing.T) {
	var aim AimSlot
	aim.RestoreReadyWord(0x89abcdef)
	if !aim.Ready || aim.IssueBit || aim.ReadyWord() != 0x89abcdef {
		t.Fatal("restored readiness was normalized or granted a request latch")
	}
	aim.CompleteAim(0)
	if aim.ReadyWord() != 0x89abcdef {
		t.Fatal("zero delivery altered the restored readiness word")
	}
	aim.CompleteAim(-7)
	if aim.ReadyWord() != 1 {
		t.Fatal("nonzero delivery must write exactly one")
	}
	aim.Ready = false
	if aim.ReadyWord() != 0 {
		t.Fatal("cleared readiness serialized an obsolete word")
	}
}
