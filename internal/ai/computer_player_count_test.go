package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// TestComputerPlayerCountIsTheControlByteTwoRows locks the count the profile
// applier is driven with [08 R-AI-01 §12][08 R-AI-01 §18]: a slot counts when
// its record EXISTS and its control byte is 2. A human at 1, a remote peer at
// 3 and an empty row do not, and neither does a control-byte-2 row whose record
// was never filled.
func TestComputerPlayerCountIsTheControlByteTwoRows(t *testing.T) {
	var svc economy.Service
	if got := computerPlayerCount(&svc); got != 0 {
		t.Fatalf("empty rows = %d, want 0", got)
	}
	if got := computerPlayerCount(nil); got != 0 {
		t.Fatalf("nil service = %d, want 0", got)
	}

	// Slot 0 human, slots 1 and 3 computer, slot 2 a remote peer, slot 4 a
	// control-byte-2 row that does not exist.
	svc.Players[0].Exists, svc.Players[0].ControllerState = true, 1
	svc.Players[1].Exists, svc.Players[1].ControllerState = true, controlByteComputer
	svc.Players[2].Exists, svc.Players[2].ControllerState = true, 3
	svc.Players[3].Exists, svc.Players[3].ControllerState = true, controlByteComputer
	svc.Players[4].ControllerState = controlByteComputer

	if got := computerPlayerCount(&svc); got != 2 {
		t.Fatalf("count = %d, want 2 [08 R-AI-01 §18]", got)
	}
}

// TestSelectionAppliesTheProfileOncePerComputerPlayer drives the count through
// the production seam: the first candidate scoring applies the profile grammar,
// and it does so once per control-byte-2 row, so a definition fragment naming a
// CATEGORY — which locks nothing — is applied 2·k times [08 R-AI-01 §18].
func TestSelectionAppliesTheProfileOncePerComputerPlayer(t *testing.T) {
	authored := map[string]string{"armck": "weight LEVEL1 0.5"}
	// 100 → 50 → 25 with one computer player; two more applications take it
	// 25 → 12 (12.5 truncates) → 6 with two [08 R-AI-01 §12] [I3].
	for _, tc := range []struct {
		players int
		want    int32
	}{
		{1, 25},
		{2, 6},
	} {
		profile := grammarProfile("", DifficultyHard)
		catalog := grammarCatalog(t, authored)
		strat := &Strategic{
			Counts:       map[string]int32{"armck": 0},
			ClassVectors: map[string]ClassVector{"armck": {C0: 40, C1: 30, C2: 30}},
		}
		sel := &testSelector{player: 1, profile: profile, strategic: strat, catalog: catalog}
		econ := testEcon(1, 800, 1000, 400, 500, 0, 0, 0, 0)
		econ.Players[1].Exists = true
		if tc.players > 1 {
			econ.Players[2].Exists, econ.Players[2].ControllerState = true, controlByteComputer
		}

		rng.SeedGlobal(1, 0)
		SelectWithCandidates(sel, testBuilder("armck"), econ, []string{"armck"})

		for _, member := range []string{"ARMCK", "ARMFLASH", "CORCK"} {
			if got := profile.WeightFor(member); got != tc.want {
				t.Fatalf("%d computer player(s): %s = %d, want %d [08 R-AI-01 §18]",
					tc.players, member, got, tc.want)
			}
		}
	}
}
