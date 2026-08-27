package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/mission"
)

// TestCampaignResultCommitsToCampaignSlot locks P0-010. Both campaign latch
// paths used to commit to slot 0 regardless of which mission was running, so
// finishing any mission other than mission zero corrupted mission zero's
// progress and left the real mission unrecorded. Exactly one W/L cell may
// change, and it must be the running slot's.
func TestCampaignResultCommitsToCampaignSlot(t *testing.T) {
	for _, tc := range []struct {
		name string
		win  bool
		want byte
	}{{"win", true, 'W'}, {"loss", false, 'L'}} {
		t.Run(tc.name, func(t *testing.T) {
			const slot = 4
			s := &Session{
				Clock:        &clock.State{Requested: 10, Active: 10},
				Mission:      &mission.Mission{Type: mission.TypeCampaign},
				State:        StateBattle,
				Latch:        NewEndLatch(),
				CampaignSlot: slot,
			}
			s.Progress.ApplyCampaignResult(s.CampaignSlot, tc.win)

			if got := s.Progress.WL[slot]; got != tc.want {
				t.Fatalf("campaign slot %d: got %q want %q", slot, got, tc.want)
			}
			for i := range s.Progress.WL {
				if i == slot {
					continue
				}
				if s.Progress.WL[i] != 0 {
					t.Fatalf("slot %d was written while completing slot %d: %q", i, slot, s.Progress.WL[i])
				}
			}
		})
	}
}

// TestControllerPredicates locks P0-023. Composition used to ask "is the
// controller nonzero?" to mean "is this a computer player". Observer is a
// distinct nonzero controller, so an observer slot was handed an AI manager,
// units, economy actions and a share of result ownership. The legacy
// controller value 2 must keep counting as a computer.
func TestControllerPredicates(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		controller                int
		human, observer, computer bool
	}{
		{"human", SkirmishControllerHuman, true, false, false},
		{"computer", SkirmishControllerComputer, false, false, true},
		{"legacy computer 2", 2, false, false, true},
		{"observer", SkirmishControllerObserver, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := SkirmishPlayer{Controller: tc.controller}
			if p.IsHuman() != tc.human || p.IsObserver() != tc.observer || p.IsComputer() != tc.computer {
				t.Fatalf("controller %d: human=%v observer=%v computer=%v; want %v/%v/%v",
					tc.controller, p.IsHuman(), p.IsObserver(), p.IsComputer(),
					tc.human, tc.observer, tc.computer)
			}
		})
	}
}
