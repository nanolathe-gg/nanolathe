package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Drive the actual return boundary, including the session writer before its
// reference is dropped and the frontend bank retained afterwards.
func TestManualCampaignReturnRecordsCurrentWinBit(t *testing.T) {
	shell, _ := retailShellForTest(t)
	for _, tc := range []struct {
		name    string
		bits    uint16
		pending uint8
		want    byte
	}{
		{"unfinished", 0, 0, 'L'},
		{"pending win", 0, 1, 'L'},
		{"latched win", session.LatchBitWin1, 0, 'W'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign}, CampaignSlot: 12,
				Latch: session.EndLatch{Bits: tc.bits, Pending: tc.pending}}
			old.Progress.Thumbs[11] = 'W'
			old.Progress.Thumbs[12] = 'U'
			old.Progress.Thumbs[13] = 'L'
			shell.battle = &battleSession{sess: old, shell: shell}
			shell.returnFromBattle(nil)
			if shell.battle != nil || !shell.campaignProgressSet {
				t.Fatal("return did not retain progress and retire battle")
			}
			if got := shell.campaignProgress.Thumbs[12]; got != tc.want {
				t.Fatalf("current mark=%q, want %q", got, tc.want)
			}
			if shell.campaignProgress.Thumbs[11] != 'W' || shell.campaignProgress.Thumbs[13] != 'L' {
				t.Fatal("teardown changed another mission's mark")
			}
			saved := shell.campaignProgress
			shell.returnFromBattle(nil)
			if shell.campaignProgress != saved {
				t.Fatal("repeated return changed the retired bank")
			}
		})
	}
}
