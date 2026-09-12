package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// resultViewForTest is the frozen terminal frame the results controller is
// installed from [03 §2.4].
func resultViewForTest(won bool) frame.ResultView {
	kind := "defeat"
	if won {
		kind = "victory"
	}
	return frame.ResultView{Ended: true, Kind: kind}
}

// The between-missions save names the next mission whenever one exists — win
// or loss — because the writer runs Advance before writing Mission/Map
// [08 R-CAMP-01 §8 "Between-missions save quirk"].
func TestContinuationSummaryNamesTheAdvancedMission(t *testing.T) {
	projection := PostBattleSummary{
		Campaign:        "camps/Arm Campaign.tdf",
		Mission:         "The Great Escape",
		Map:             "Anteer Straight",
		Side:            1,
		Difficulty:      2,
		MissionIndex:    1,
		BetweenMissions: true,
	}
	projection.Thumbs[0] = 'W'
	summary := ContinuationSummary(projection, ContinuationSaveMetadata{
		Description: "slot one", GameID: "1000", Players: 3, GameTime: 5400,
	})
	if summary.BetweenMissions != 1 {
		t.Fatalf("BetweenMissions=%d, want 1", summary.BetweenMissions)
	}
	if summary.Gametype != GametypeCampaign {
		t.Fatalf("Gametype=%d, want %d", summary.Gametype, GametypeCampaign)
	}
	if summary.Mission != "The Great Escape" || summary.MapName != summary.Mission {
		t.Fatalf("Mission=%q Map=%q, want the same advanced name", summary.Mission, summary.MapName)
	}
	if summary.Difficulty != 2 || summary.Side != 1 || summary.Players != 3 {
		t.Fatalf("Difficulty/Side/Players = %d/%d/%d, want 2/1/3", summary.Difficulty, summary.Side, summary.Players)
	}
	if len(summary.Thumbs) != 25 || summary.Thumbs[0] != 'W' {
		t.Fatalf("Thumbs=%q, want a 25-byte array whose first mark is W", summary.Thumbs)
	}
	if summary.GameTime != 5400 || summary.Description != "slot one" || summary.GameID != "1000" {
		t.Fatalf("caller metadata lost: %+v", summary)
	}
	if len(summary.RadarImage) != 0 {
		t.Fatal("continuation wrote a Radar Image box; that box is live-battle only")
	}
}

// The controller's own Advance is what supplies the successor, so a save taken
// after a loss still names the next mission [08 R-CAMP-01 §8].
func TestPostBattleSummaryAdvancesAfterALoss(t *testing.T) {
	controller := NewPostBattleController(resultViewForTest(false), PostBattleConfig{
		Kind: PostBattleCampaign, MissionIndex: 0, HasNext: true,
		Mission: "First", NextMission: "Second", CampaignCDOK: true,
	})
	projection := controller.Summary()
	if projection.Mission != "Second" || projection.MissionIndex != 1 {
		t.Fatalf("after a loss the results save named %q/%d, want Second/1", projection.Mission, projection.MissionIndex)
	}
}

// Without a successor the played mission is named [08 R-CAMP-01 §8].
func TestPostBattleSummaryKeepsTheLastMission(t *testing.T) {
	controller := NewPostBattleController(resultViewForTest(true), PostBattleConfig{
		Kind: PostBattleCampaign, MissionIndex: 4, Mission: "Last", CampaignCDOK: true,
	})
	if projection := controller.Summary(); projection.Mission != "Last" || projection.MissionIndex != 4 {
		t.Fatalf("final mission named %q/%d, want Last/4", projection.Mission, projection.MissionIndex)
	}
}

// The path builder truncates the assembled path at its last dot and appends
// .SAV; the strip is not path-component aware [08 "File naming and write
// policy"] [08 R-SAVE-02 §1].
func TestRetailSavePathStripsAtTheLastDot(t *testing.T) {
	dir := filepath.Join("root", "savegame")
	if got, want := RetailSavePath(dir, "slot one"), filepath.Join(dir, "slot one")+".SAV"; got != want {
		t.Fatalf("plain name -> %q, want %q", got, want)
	}
	if got, want := RetailSavePath(dir, "v1.2 final"), filepath.Join(dir, "v1")+".SAV"; got != want {
		t.Fatalf("dotted name -> %q, want %q", got, want)
	}
	if got := RetailSavePath("root.d/savegame", "slot"); !strings.HasSuffix(got, ".SAV") || strings.Contains(got, "slot") {
		t.Fatalf("a dot in the directory is not component aware: got %q", got)
	}
	if got := RetailSavePath(dir, "   "); got != "" {
		t.Fatalf("empty name -> %q, want no path at all", got)
	}
}

// A written continuation bank reopens, decodes, and takes the continuation
// route [08 "battle versus campaign continuations and timing"].
func TestWriteRetailContinuationSaveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := RetailSavePath(dir, "resume")
	summary := ContinuationSummary(PostBattleSummary{
		Campaign: "camps/Arm Campaign.tdf", Mission: "Second", MissionIndex: 1, BetweenMissions: true,
	}, ContinuationSaveMetadata{Description: "resume", GameID: "42", Players: 1})
	if err := WriteRetailContinuationSave(path, summary); err != nil {
		t.Fatalf("write: %v", err)
	}
	bank, err := save.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	decoded, ok := save.ReadSummary(bank)
	if !ok {
		t.Fatal("written bank has no Summary account")
	}
	if decoded.BetweenMissions != 1 || decoded.Mission != "Second" || decoded.Description != "resume" {
		t.Fatalf("decoded summary=%+v", decoded)
	}
	result, err := PreflightRetailLoad(bank)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if result.Route != RetailLoadRouteCampaignContinuation {
		t.Fatalf("route=%d, want campaign continuation", result.Route)
	}
}

// A bank that is not a continuation is refused by the continuation writer
// rather than silently written without the routing marker.
func TestWriteRetailContinuationSaveRequiresTheMarker(t *testing.T) {
	if err := WriteRetailContinuationSave(filepath.Join(t.TempDir(), "x.SAV"), save.Summary{Gametype: 1}); err == nil {
		t.Fatal("a summary without BetweenMissions=1 was accepted")
	}
	if err := WriteRetailContinuationSave("", save.Summary{BetweenMissions: 1}); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

// An in-battle save runs through the existing projection and writer with the
// runtime supplying every caller-owned family, and the bank it produces is
// accepted by the ordinary load path on its battle-restoration route
// [08 "Save-file organization"] [08 R-SAVE-02 §11].
func TestInBattleSaveRoundTripsThroughTheLoadPath(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	stepRetail(s, 2)

	summary := RetailBattleSummary(s, "battle slot", "1000", SkirmishDefaultUnitLimit)
	if summary.BetweenMissions != 0 {
		t.Fatalf("an in-battle save carried BetweenMissions=%d; that marker is the continuation route", summary.BetweenMissions)
	}
	if summary.Gametype != GametypeMultiplayer {
		t.Fatalf("skirmish Gametype=%d, want the skirmish type %d", summary.Gametype, GametypeMultiplayer)
	}
	if summary.MapName != retailMap || summary.Players == 0 {
		t.Fatalf("summary=%+v", summary)
	}

	in, err := s.RetailBattleSaveInputs(summary, save.Camera{XPosition: 100, ZPosition: 200})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	// Mapping is the explored-memory word grid, one word per four terrain
	// cells [08 R-SAVE-02 §12] [05 R-SHARE-01 §6].
	if want := int(s.World.CellW) * int(s.World.CellH) / 2; len(in.Mapping) != want {
		t.Fatalf("Mapping has %d bytes, want %d", len(in.Mapping), want)
	}
	if len(in.StableIDs) == 0 {
		t.Fatal("no live unit received a stable identifier")
	}

	path := RetailSavePath(t.TempDir(), "battle slot")
	if err := s.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write battle save: %v", err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{FS: f.fs, SimSeed: 1, CRTSeed: 1})
	if err != nil {
		t.Fatalf("the written battle bank was refused by the load path: %v", err)
	}
	if loaded.Route != RetailLoadRouteBattleRestoration {
		t.Fatalf("route=%d, want battle restoration", loaded.Route)
	}
	if loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("battle restoration produced no detached candidate")
	}
}
