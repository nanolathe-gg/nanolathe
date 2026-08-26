package mission

import (
	"fmt"

	"github.com/nanolathe/nanolathe/vfs"
)

// NextCampaignMission reports whether the campaign contains a mission after curIdx.
// It re-reads the campaign TDF losslessly via DiscoverCampaign so the
// contiguous MISSION0..N walk until first gap [08 "Campaign discovery"] is
// the source of truth, with provenance preserved from the VFS. The check is
// presentation-safe and never invents a mission.
// Progression is linear next = cur+1 [07 §11] [08 "Progression"]; any
// branching, side-specific, or registry-gated unlock beyond that is not
// established and is TODO(question).
// Returns (nextIdx, true) when next exists, (0,false) when campaign complete
// or on error. Caller should treat false as campaign-complete and return to menu.
func NextCampaignMission(fs vfs.FSOps, campaignPath string, curIdx int) (int, bool, error) {
	if fs == nil {
		return 0, false, fmt.Errorf("mission: nil filesystem")
	}
	if campaignPath == "" {
		return 0, false, fmt.Errorf("mission: empty campaign path")
	}
	if curIdx < 0 {
		return 0, false, fmt.Errorf("mission: invalid campaign index %d", curIdx)
	}
	c, err := DiscoverCampaign(fs, campaignPath)
	if err != nil {
		return 0, false, err
	}
	next := curIdx + 1
	if next < len(c.Missions) {
		return next, true, nil
	}
	// No next: contiguous walk stopped at first gap, so next is absent [08 "Campaign discovery"] C1.
	return 0, false, nil
}

// IsCampaignComplete reports whether curIdx is the last mission in the campaign.
// It uses the same lossless discovery as NextCampaignMission [08 "Campaign discovery"].
func IsCampaignComplete(fs vfs.FSOps, campaignPath string, curIdx int) (bool, error) {
	_, ok, err := NextCampaignMission(fs, campaignPath, curIdx)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// CampaignMissionCount returns the number of contiguous missions in campaignPath.
// Lossless parse with provenance [fmt tdf] [08 "Campaign discovery"].
func CampaignMissionCount(fs vfs.FSOps, campaignPath string) (int, error) {
	if fs == nil {
		return 0, fmt.Errorf("mission: nil filesystem")
	}
	c, err := DiscoverCampaign(fs, campaignPath)
	if err != nil {
		return 0, err
	}
	return len(c.Missions), nil
}

// TODO(question): retail behavior beyond linear index+1 advance not established.
// Covers: branching progression, side-gated campaigns, AllMissions registry bit
// unlock gating, and whether losing Continue re-presents briefing or remains at
// map screen [08 "Progression"] [P0-05] [07 §11]. For now losing Continue
// retains main-menu return; see frontend.go // TODO(question) losing path.
