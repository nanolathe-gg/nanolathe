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
// Progression is linear: retail's advance helper "succeeds only when
// `count > index + 1`, then increments the index" and neither it nor the
// has-mission helper tests the W/L marks [08 R-CAMP-01 §1 "Index helpers"].
// `next < len(c.Missions)` is that same comparison.
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

// There is no branching, side-gated, or registry-gated advance to add here.
// [08 R-CAMP-01 §1 "Index helpers"] is exhaustive for the campaign record:
// advance is `count > index + 1` and the W/L marks are not consulted, so
// "progression by outcome is decided by the end-mission screen, not by the
// record". The mission-list build counts every mission and neither the
// new-game panel nor the end-mission screen filters it by the `AllMissions`
// registry bit [08 "Progression"]. The results screen's route is the authored
// `Start` control when progression has a next mission and `MainMenu`
// otherwise — the same route after a win and after a loss; only the outcome
// copy differs [08 R-CAMP-01 §6][07 §11].
//
// The one residual of the all-missions bit is the `AnyMsn` toggle's listbox
// selection effect, which is a front-end listbox question decided by manual
// retail observation, not a progression rule [08 "Missing and unknown" §"Sessions
// and campaign"].
