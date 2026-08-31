package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/vfs"
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

func retailContinuationFS(t *testing.T) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "camps"), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("\n[HEADER]\n{\n campaignside=ARM;\n}\n[MISSION0]\n{\n missionfile=A.ota;\n missionname=First;\n}\n[MISSION1]\n{\n missionfile=B.ota;\n missionname=Second;\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "camps", "c.tdf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	return fs
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

func TestPreflightRetailLoadDoesNotConstructSession(t *testing.T) {
	// Route-only callers use preflight; production LoadRetailSave requires
	// dependencies and constructs a detached continuation.
	got, err := PreflightRetailLoad(retailLoadBank(t, save.Summary{
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

func TestLoadRetailSaveBattleRestorationPreflightDoesNotUseUnsupportedSentinel(t *testing.T) {
	got, err := PreflightRetailLoad(retailLoadBank(t, save.Summary{
		Gametype: GametypeMultiplayer,
	}, true))
	if err != nil {
		t.Fatalf("battle restoration preflight: %v", err)
	}
	if got.Route != RetailLoadRouteBattleRestoration {
		t.Fatalf("route = %v, want battle restoration", got.Route)
	}
}

func TestLoadRetailSaveWithDepsMapsContinuationIdentityAndThumbs(t *testing.T) {
	fs := retailContinuationFS(t)
	defer fs.Close()
	thumbs := "ABCDEFGHIJKLMNOPQRSTUVWXY"
	b := save.NewBuilder(save.RetailTag)
	save.WriteSummary(b, save.Summary{
		Campaign: "camps/c.tdf", Mission: "sEcOnD", Gametype: GametypeCampaign,
		BetweenMissions: 1, Difficulty: 2, Side: 1, Thumbs: thumbs,
	})
	bank, err := save.OpenBytes(b.Bytes(), save.RetailTag)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadRetailSaveWithDeps(bank, RetailLoadDeps{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if got.Continuation == nil || got.Continuation.MissionIndex != 1 || got.Continuation.Difficulty != 2 || got.Continuation.Side != 1 {
		t.Fatalf("continuation = %#v", got.Continuation)
	}
	if got.Continuation.CampaignPath != "camps/c.tdf" || got.Continuation.MissionName != "Second" {
		t.Fatalf("identity = %#v", got.Continuation)
	}
	if string(got.Continuation.Thumbs[:]) != thumbs {
		t.Fatalf("thumbs = %q, want %q", got.Continuation.Thumbs, thumbs)
	}
}
