package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// writeContinuationSlot authors one continuation bank whose Description is the
// slot name, then stamps its modification time so the enumerator's sort key is
// deterministic.
func writeContinuationSlot(t *testing.T, dir, name string, when time.Time) string {
	t.Helper()
	path := session.RetailSavePath(dir, name)
	summary := session.ContinuationSummary(session.PostBattleSummary{
		Campaign: "camps/Arm Campaign.tdf", Mission: name, MissionIndex: 1, BetweenMissions: true,
	}, session.ContinuationSaveMetadata{Description: name, GameID: "1", Players: 1, GameTime: 5400})
	if err := session.WriteRetailContinuationSave(path, summary); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %q: %v", name, err)
	}
	return path
}

// The list is stably sorted ascending on the enumerator's time word, so the
// oldest file is first and the newest last [08 R-SAVE-02 §1].
func TestSaveListIsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_700_000_000, 0)
	writeContinuationSlot(t, dir, "newest", base.Add(2*time.Hour))
	writeContinuationSlot(t, dir, "oldest", base)
	writeContinuationSlot(t, dir, "middle", base.Add(time.Hour))

	entries := enumerateRetailSaves(dir)
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Description)
	}
	want := []string{"oldest", "middle", "newest"}
	if len(got) != len(want) {
		t.Fatalf("list=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("list=%v, want %v", got, want)
		}
	}
}

func TestSaveListKeepsEqualTimestampEncounterOrder(t *testing.T) {
	dir := t.TempDir()
	when := time.Unix(1_700_000_000, 0)
	writeContinuationSlot(t, dir, "alpha", when)
	writeContinuationSlot(t, dir, "bravo", when)
	writeContinuationSlot(t, dir, "charlie", when)

	entries := enumerateRetailSaves(dir)
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Description)
	}
	want := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("list=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("equal-timestamp list=%v, want %v", got, want)
		}
	}
}

// A file with no readable bank, no Summary, or no Description is dropped from
// both lists, so it can be neither loaded nor deleted through the interface
// [08 R-SAVE-02 §1].
func TestSaveListDropsFilesWithoutADescription(t *testing.T) {
	dir := t.TempDir()
	writeContinuationSlot(t, dir, "keep", time.Unix(1_700_000_000, 0))
	if err := os.WriteFile(filepath.Join(dir, "garbage.SAV"), []byte("not a bank"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A well-formed bank whose Summary carries no Description is dropped too.
	nameless := session.RetailSavePath(dir, "nameless")
	summary := session.ContinuationSummary(session.PostBattleSummary{Mission: "x", BetweenMissions: true}, session.ContinuationSaveMetadata{Players: 1})
	if err := session.WriteRetailContinuationSave(nameless, summary); err != nil {
		t.Fatal(err)
	}
	// A non-.SAV file is not a candidate at all.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := enumerateRetailSaves(dir)
	if len(entries) != 1 || entries[0].Description != "keep" {
		t.Fatalf("list=%+v, want the single described slot", entries)
	}
}

// Selecting a row copies the entry's description into GAMENAME, which is why
// saving over a selected slot is a silent truncate-overwrite [08 R-SAVE-02 §1].
func TestSaveScreenSelectionCopiesDescriptionIntoTheNameEdit(t *testing.T) {
	dir := t.TempDir()
	writeContinuationSlot(t, dir, "slot one", time.Unix(1_700_000_000, 0))
	screen := newSaveLoadScreen(saveScreenMode, dir, saveLoadFromResults)
	if len(screen.Entries()) != 1 {
		t.Fatalf("entries=%d, want 1", len(screen.Entries()))
	}
	screen.Select(0)
	if screen.Name() != "slot one" {
		t.Fatalf("GAMENAME=%q, want the selected description", screen.Name())
	}
	if got, want := screen.CommitPath(), session.RetailSavePath(dir, "slot one"); got != want {
		t.Fatalf("commit path=%q, want %q", got, want)
	}
}

// The save screen hides LoadGame, and DELETE when the list is empty; the load
// screen hides DELETE, GAMENAME and SaveGame [08 R-SAVE-02 §1] [07 R-FE-01 §8].
func TestSaveLoadHiddenControlSets(t *testing.T) {
	dir := t.TempDir()
	empty := newSaveLoadScreen(saveScreenMode, dir, saveLoadFromResults)
	if !containsName(empty.HiddenControls(), "LoadGame") || !containsName(empty.HiddenControls(), "DELETE") {
		t.Fatalf("empty save screen hides %v, want LoadGame and DELETE", empty.HiddenControls())
	}
	writeContinuationSlot(t, dir, "slot", time.Unix(1_700_000_000, 0))
	filled := newSaveLoadScreen(saveScreenMode, dir, saveLoadFromResults)
	if containsName(filled.HiddenControls(), "DELETE") {
		t.Fatalf("non-empty save screen hid DELETE: %v", filled.HiddenControls())
	}
	load := newSaveLoadScreen(loadScreenMode, dir, saveLoadFromFrontend)
	for _, name := range []string{"DELETE", "GAMENAME", "SaveGame"} {
		if !containsName(load.HiddenControls(), name) {
			t.Fatalf("load screen hides %v, want %s hidden", load.HiddenControls(), name)
		}
	}
}

// LOAD, GAMES and GAMENAME are treated identically by the handler, and DELETE
// removes the selected file without confirmation [08 R-SAVE-02 §1].
func TestSaveScreenControlVocabulary(t *testing.T) {
	dir := t.TempDir()
	path := writeContinuationSlot(t, dir, "slot", time.Unix(1_700_000_000, 0))
	screen := newSaveLoadScreen(saveScreenMode, dir, saveLoadFromResults)
	for _, name := range []string{"LOAD", "GAMES", "GAMENAME"} {
		if screen.Activate(name) != saveLoadCommit {
			t.Fatalf("%s did not commit", name)
		}
	}
	if screen.Activate("LOAD\x00tail") != saveLoadCommit {
		t.Fatal("terminated LOAD did not commit")
	}
	for _, name := range []string{"load", "LOAD ", " LOAD"} {
		if screen.Activate(name) != saveLoadNone {
			t.Fatalf("altered callback %q became active", name)
		}
	}
	if screen.Activate("CANCEL") != saveLoadCancel {
		t.Fatal("CANCEL did not cancel")
	}
	if screen.Activate("LoadGame") != saveLoadToLoad {
		t.Fatal("LoadGame did not switch direction")
	}
	if screen.Activate("SaveGame") != saveLoadNone {
		t.Fatal("SaveGame is a route to the direction already shown")
	}
	if screen.Activate("DELETE") != saveLoadNone {
		t.Fatal("DELETE acted with no selection")
	}
	screen.Select(0)
	if screen.Activate("DELETE") != saveLoadDelete {
		t.Fatal("DELETE did not delete with a selection")
	}
	screen.deleteSelected()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("DELETE left %s in place (err=%v)", path, err)
	}
	if len(screen.Entries()) != 0 {
		t.Fatalf("DELETE did not rebuild the list: %+v", screen.Entries())
	}
}

// The summary panel reads only the Summary account of the selected file
// [08 R-SAVE-02 §3].
func TestSummaryPanelFields(t *testing.T) {
	campaign := save.Summary{Gametype: 1, Players: 1, Campaign: "Arm", Mission: "Second", MapName: "Anteer", Difficulty: 1, GameTime: 5400}
	fields := retailSummaryPanelFields(campaign, true, []string{"ARM", "CORE"})
	if fields["GAMETYPE"] != "Single" || fields["MISSION"] != "Second" || fields["CAMPAIGN"] != "Arm" {
		t.Fatalf("campaign fields=%v", fields)
	}
	if fields["DIFF"] != "Medium" || fields["SIDE"] != "ARM" {
		t.Fatalf("campaign fields=%v", fields)
	}
	// 5400 ticks is three minutes at 30 Hz.
	if fields["TIME"] != "00:03:00" {
		t.Fatalf("TIME=%q, want 00:03:00", fields["TIME"])
	}
	skirmish := retailSummaryPanelFields(save.Summary{Gametype: 2, Players: 4, MapName: "Ashap Plateau"}, true, nil)
	if skirmish["GAMETYPE"] != "Skirmish (4 players)" || skirmish["MISSION"] != "Ashap Plateau" {
		t.Fatalf("skirmish fields=%v", skirmish)
	}
	if skirmish["CAMPAIGN"] != "" {
		t.Fatalf("skirmish showed a campaign field: %v", skirmish)
	}
	if got := retailSummaryPanelFields(save.Summary{Gametype: 1}, true, nil)["GAMETYPE"]; got != "???" {
		t.Fatalf("zero Players GAMETYPE=%q, want ???", got)
	}
	for name, value := range retailSummaryPanelFields(save.Summary{Gametype: 1, Players: 1}, false, nil) {
		if value != "" {
			t.Fatalf("unselected panel field %s=%q, want empty", name, value)
		}
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
