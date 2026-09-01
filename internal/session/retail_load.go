package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/vfs"
)

// RetailLoadRoute is the load branch selected by the Summary account. A
// BetweenMissions value of one selects campaign continuation; absent or zero
// selects battle restoration. [08 "battle versus campaign continuations and timing"]
type RetailLoadRoute uint8

const (
	RetailLoadRouteInvalid RetailLoadRoute = iota
	RetailLoadRouteCampaignContinuation
	RetailLoadRouteBattleRestoration
)

var (
	// ErrRetailSummaryMissing means that the required Summary account is absent.
	ErrRetailSummaryMissing = errors.New("session: retail save missing Summary account")
	// ErrRetailGametypeInvalid is returned for every Gametype other than 1 or 2.
	ErrRetailGametypeInvalid = errors.New("session: retail save has invalid gametype")
	// ErrRetailLoadDependenciesMissing means the caller requested a live load
	// without supplying the mounted content and seed dependencies needed to
	// construct its detached candidate.
	ErrRetailLoadDependenciesMissing = errors.New("session: retail load dependencies missing")
)

// RetailCampaignContinuation is the typed state carried from a between-
// missions Summary into the existing briefing route. Thumbs is copied with
// retail's 25-byte bound and reset to all U when the source is not exactly 25
// bytes [08 R-CAMP-01 §8].
type RetailCampaignContinuation struct {
	CampaignPath string
	CampaignName string
	MissionName  string
	MissionIndex int
	Difficulty   int
	Side         int
	Thumbs       [25]byte
}

// RetailLoadResult is the route and detached candidate produced from a save.
// Exactly one of Battle and Continuation is populated for a successful load.
// Route-only callers use PreflightRetailLoad.
type RetailLoadResult struct {
	Summary      save.Summary
	Route        RetailLoadRoute
	Battle       *RetailBattleStage
	Continuation *RetailCampaignContinuation
}

// PreflightRetailLoad validates the Summary account and selects the established
// retail load branch. It does not inspect bulk accounts, seed RNGs, restore
// projectiles, or construct a Session.
func PreflightRetailLoad(bank *save.Bank) (RetailLoadResult, error) {
	if bank == nil {
		return RetailLoadResult{}, ErrRetailSummaryMissing
	}
	summary, ok := save.ReadSummary(bank)
	if !ok {
		return RetailLoadResult{}, ErrRetailSummaryMissing
	}
	if summary.Gametype != GametypeCampaign && summary.Gametype != GametypeMultiplayer {
		return RetailLoadResult{}, fmt.Errorf("%w: %d", ErrRetailGametypeInvalid, summary.Gametype)
	}

	route := RetailLoadRouteBattleRestoration
	if summary.BetweenMissions == 1 {
		route = RetailLoadRouteCampaignContinuation
	}
	return RetailLoadResult{Summary: summary, Route: route}, nil
}

// LoadRetailSave fully prepares the detached battle candidate or authored
// continuation. No live Session or presentation state is touched; callers
// that only need route validation should use PreflightRetailLoad.
func LoadRetailSave(bank *save.Bank, deps RetailLoadDeps) (RetailLoadResult, error) {
	return LoadRetailSaveWithDeps(bank, deps)
}

// LoadRetailSaveWithDeps prepares a complete detached load result. Branching
// is strictly on Summary.BetweenMissions == 1: every other value is an
// in-battle restoration [08 R-SAVE-02 §11].
func LoadRetailSaveWithDeps(bank *save.Bank, deps RetailLoadDeps) (RetailLoadResult, error) {
	preflight, err := PreflightRetailLoad(bank)
	if err != nil {
		return RetailLoadResult{}, err
	}
	if deps.FS == nil {
		return RetailLoadResult{}, ErrRetailLoadDependenciesMissing
	}
	if preflight.Route == RetailLoadRouteCampaignContinuation {
		continuation, err := resolveRetailContinuation(deps.FS, preflight.Summary)
		if err != nil {
			return RetailLoadResult{}, err
		}
		return RetailLoadResult{Summary: preflight.Summary, Route: preflight.Route, Continuation: continuation}, nil
	}
	stage, err := StageRetailBattle(bank, deps)
	if err != nil {
		return RetailLoadResult{}, err
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		return RetailLoadResult{}, err
	}
	return RetailLoadResult{Summary: preflight.Summary, Route: preflight.Route, Battle: stage}, nil
}

// LoadRetailSavePath is the production file-path entrypoint. It parses the
// retail HAPIBANK and only returns after detached staging and restoration have
// completed successfully.
func LoadRetailSavePath(path string, deps RetailLoadDeps) (RetailLoadResult, error) {
	bank, err := save.Open(path)
	if err != nil {
		return RetailLoadResult{}, fmt.Errorf("session: open retail save %q: %w", path, err)
	}
	return LoadRetailSaveWithDeps(bank, deps)
}

// LoadRetailSaveBytes is the equivalent production entrypoint for callers
// that already own the file bytes (for example, a platform file dialog).
func LoadRetailSaveBytes(data []byte, deps RetailLoadDeps) (RetailLoadResult, error) {
	bank, err := save.OpenBytes(data)
	if err != nil {
		return RetailLoadResult{}, fmt.Errorf("session: open retail save bytes: %w", err)
	}
	return LoadRetailSaveWithDeps(bank, deps)
}

func resolveRetailContinuation(fs vfs.FSOps, summary save.Summary) (*RetailCampaignContinuation, error) {
	if strings.TrimSpace(summary.Campaign) == "" {
		return nil, fmt.Errorf("session: retail continuation missing campaign identity")
	}
	campaign, err := mission.DiscoverCampaign(fs, summary.Campaign)
	if err != nil {
		return nil, fmt.Errorf("session: retail continuation campaign: %w", err)
	}
	if campaign == nil {
		return nil, fmt.Errorf("session: retail continuation campaign is unavailable")
	}
	var stub mission.Stub
	found := false
	for _, candidate := range campaign.Missions {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(summary.Mission)) {
			stub = candidate
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("session: retail continuation mission %q not found in campaign %q", summary.Mission, campaign.OriginalPath)
	}
	var thumbs [25]byte
	if len(summary.Thumbs) == len(thumbs) {
		copy(thumbs[:], summary.Thumbs)
	} else {
		for i := range thumbs {
			thumbs[i] = 'U'
		}
	}
	return &RetailCampaignContinuation{
		CampaignPath: campaign.Path,
		CampaignName: campaign.Name,
		MissionName:  stub.Name,
		MissionIndex: stub.Index,
		Difficulty:   int(summary.Difficulty),
		Side:         int(summary.Side),
		Thumbs:       thumbs,
	}, nil
}
