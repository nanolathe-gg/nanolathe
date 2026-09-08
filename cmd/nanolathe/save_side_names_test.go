package main

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
)

// The constructor does byte addition even on non-uppercase suffixes;
// preserving that distinction prevents a Unicode title-case substitute.
func TestSaveSideDisplayNames(t *testing.T) {
	sides := []*content.SideDef{{Name: "ARM"}, {Name: "CORE"}, {Name: "NOVA"}, {Name: "A9z"}, {Name: "Q\xff"}}
	names := retailSideDisplayNames(sides)
	for i, want := range []string{"Arm", "Core", "Nova", "AY\x9a", "Q\x1f"} {
		if got := retailSummaryPanelFields(save.Summary{Side: int32(i), Players: 1}, true, names)["SIDE"]; got != want {
			t.Fatalf("side %d = %q, want %q", i, got, want)
		}
	}
	if sides[0].Name != "ARM" {
		t.Fatal("display preparation mutated the compiled definition")
	}
}

// Load through the production frontend dialog, with no battle/catalog owner.
func TestSaveDialogLoadsSideNamesBeforeBattle(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, dir := retailShellForTest(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Arm", "Core"} {
		summary := save.Summary{Description: name, Side: int32(i), Players: 1, Gametype: 1, BetweenMissions: 1}
		if err := session.WriteRetailContinuationSave(session.RetailSavePath(dir, name), summary); err != nil {
			t.Fatal(err)
		}
	}
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
		t.Fatal(err)
	}
	if shell.battle != nil {
		t.Fatal("test unexpectedly started a battle")
	}
	for i := range saveLoadUI.entries {
		saveLoadUI.Select(i)
		shell.refreshSaveLoadPanel()
		entry, _ := saveLoadUI.SelectedEntry()
		if got := saveLoadPanel.TextOf("SIDE"); got != entry.Summary.Description {
			t.Fatalf("side %d = %q, want %q", entry.Summary.Side, got, entry.Summary.Description)
		}
	}
	shell.closeSaveLoadScreen()
	if shell.retailSideNames() != nil {
		t.Fatal("closed dialog retained its side table")
	}
}
