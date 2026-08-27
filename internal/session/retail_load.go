package session

import (
	"errors"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/save"
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
	// ErrRetailBattleRestorationUnsupported is explicit because the established
	// Units, script, feature, and trigger bodies are not a complete path yet.
	ErrRetailBattleRestorationUnsupported = errors.New("session: retail battle restoration unsupported")
)

// RetailLoadResult is the route and authored metadata read from Summary. It
// contains no Session or native snapshot.
type RetailLoadResult struct {
	Summary save.Summary
	Route   RetailLoadRoute
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

// LoadRetailSave performs retail-save preflight and exposes only the route
// boundary currently supported by Nanolathe. Campaign continuation returns
// Summary metadata for the caller/front end. Full in-battle restoration returns
// an explicit unsupported sentinel because its Units/script/feature/trigger
// bodies are incomplete.
func LoadRetailSave(bank *save.Bank) (RetailLoadResult, error) {
	preflight, err := PreflightRetailLoad(bank)
	if err != nil {
		return RetailLoadResult{}, err
	}
	if preflight.Route == RetailLoadRouteBattleRestoration {
		return preflight, ErrRetailBattleRestorationUnsupported
	}

	// TODO(question): map Summary.Campaign and Summary.Mission to the exact
	// authored mission path and NewMissionWithProgress arguments. Campaign
	// catalog/path evidence and mission-index conversion would settle this;
	// until then this boundary returns metadata without constructing a session.
	return RetailLoadResult{Summary: preflight.Summary, Route: preflight.Route}, nil
}
