package session

import "github.com/nanolathe-gg/nanolathe/internal/mission"

// CommitCampaignTeardown records the current mission's W/L mark from the live
// win bit. Both the ordinary ending transition and manual battle teardown use
// this writer: an unfinished mission therefore records L even if victory is
// pending in the countdown [08 R-CAMP-01 §7][08 R-CAMP-01 §8].
func (s *Session) CommitCampaignTeardown() {
	if s == nil || s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
		return
	}
	s.Progress.ApplyCampaignResult(s.CampaignSlot, s.Latch.IsWin())
}
