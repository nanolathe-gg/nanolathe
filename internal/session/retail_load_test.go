package session

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/save"
)

func retailLoadBank(t *testing.T, summary save.Summary, includeSummary bool) *save.Bank {
	t.Helper()
	// WriteSummary emits BetweenMissions=1 when IsBattle is false (the zero
	// value), even if the caller is modeling a battle save. Set the authored
	// Summary mode explicitly so this fixture preserves the [08 "Summary"]
	// distinction instead of silently changing the route under test.
	if summary.BetweenMissions == 0 {
		summary.IsBattle = true
	}
	b := save.NewBuilder(save.RetailTag)
	if includeSummary {
		save.WriteSummary(b, summary)
	}
	bank, err := save.OpenBytes(b.Bytes(), save.RetailTag)
	if err != nil {
		t.Fatalf("open authored bank: %v", err)
	}
	return bank
}

func TestPreflightRetailLoadRequiresSummary(t *testing.T) {
	_, err := PreflightRetailLoad(retailLoadBank(t, save.Summary{}, false))
	if !errors.Is(err, ErrRetailSummaryMissing) {
		t.Fatalf("missing Summary error = %v, want ErrRetailSummaryMissing", err)
	}

	_, err = PreflightRetailLoad(nil)
	if !errors.Is(err, ErrRetailSummaryMissing) {
		t.Fatalf("nil bank error = %v, want ErrRetailSummaryMissing", err)
	}
}

func TestPreflightRetailLoadGametypeAndBetweenMissionsPolarity(t *testing.T) {
	tests := []struct {
		name     string
		gametype int32
		between  int32
		want     RetailLoadRoute
	}{
		{name: "campaign continuation", gametype: GametypeCampaign, between: 1, want: RetailLoadRouteCampaignContinuation},
		{name: "campaign battle", gametype: GametypeCampaign, between: 0, want: RetailLoadRouteBattleRestoration},
		{name: "multiplayer continuation marker", gametype: GametypeMultiplayer, between: 1, want: RetailLoadRouteCampaignContinuation},
		{name: "multiplayer battle", gametype: GametypeMultiplayer, between: 0, want: RetailLoadRouteBattleRestoration},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PreflightRetailLoad(retailLoadBank(t, save.Summary{
				Campaign:        "ARM",
				Mission:         "MISSION1",
				Gametype:        tc.gametype,
				BetweenMissions: tc.between,
			}, true))
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if got.Route != tc.want {
				t.Fatalf("route = %v, want %v", got.Route, tc.want)
			}
			if got.Summary.Campaign != "ARM" || got.Summary.Mission != "MISSION1" {
				t.Fatalf("Summary metadata = %#v, want authored campaign/mission", got.Summary)
			}
		})
	}
}

func TestPreflightRetailLoadRejectsUnknownGametype(t *testing.T) {
	for _, gametype := range []int32{0, 3, -1, 99} {
		got, err := PreflightRetailLoad(retailLoadBank(t, save.Summary{Gametype: gametype}, true))
		if !errors.Is(err, ErrRetailGametypeInvalid) {
			t.Errorf("gametype %d: error = %v, want ErrRetailGametypeInvalid", gametype, err)
		}
		if got.Route != RetailLoadRouteInvalid || got.Summary.Gametype != 0 {
			t.Errorf("gametype %d: result = %#v, want zero route/result", gametype, got)
		}
	}
}

func TestLoadRetailSaveReturnsMetadataAndDoesNotConstructSession(t *testing.T) {
	// This campaign continuation has no authored VFS/catalog dependency in the
	// test. Success therefore proves this boundary does not call
	// NewMissionWithProgress (or enter any RNG/projectile continuation path).
	got, err := LoadRetailSave(retailLoadBank(t, save.Summary{
		Campaign:        "ARM",
		Mission:         "MISSION1",
		Gametype:        GametypeCampaign,
		BetweenMissions: 1,
	}, true))
	if err != nil {
		t.Fatalf("campaign continuation load: %v", err)
	}
	if got.Route != RetailLoadRouteCampaignContinuation {
		t.Fatalf("route = %v, want campaign continuation", got.Route)
	}
	if got.Summary.Campaign != "ARM" || got.Summary.Mission != "MISSION1" {
		t.Fatalf("metadata = %#v, want authored campaign/mission", got.Summary)
	}
}

func TestLoadRetailSaveBattleRestorationIsExplicitlyUnsupported(t *testing.T) {
	got, err := LoadRetailSave(retailLoadBank(t, save.Summary{
		Gametype: GametypeMultiplayer,
	}, true))
	if !errors.Is(err, ErrRetailBattleRestorationUnsupported) {
		t.Fatalf("battle restoration error = %v, want ErrRetailBattleRestorationUnsupported", err)
	}
	if got.Route != RetailLoadRouteBattleRestoration {
		t.Fatalf("route = %v, want battle restoration", got.Route)
	}
}
