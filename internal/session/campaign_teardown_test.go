package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
)

func TestCampaignTeardownLeavesSkirmishBankAlone(t *testing.T) {
	s := &Session{Mission: &mission.Mission{Type: mission.TypeSkirmish}, CampaignSlot: 2}
	s.Progress.Thumbs[2] = 'U'
	before := s.Progress
	s.CommitCampaignTeardown()
	if s.Progress != before {
		t.Fatal("skirmish teardown wrote a campaign mark")
	}
}
