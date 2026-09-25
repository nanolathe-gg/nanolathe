package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Until the save sidecar records mutators, a load applies none
// (docs/DESIGN_MODS_MUTATORS.md §7.3 step 1), so every save of a battle
// running with mutators is refused with a message: at the save dialog's open
// from the battle menu and from the results screen, at the load dialog's
// switch to saving, and at the commit. Loading is unaffected.
func TestSavingIsRefusedWhileMutatorsAreActive(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, dir := retailShellForTest(t)
	var mutators content.Mutators
	if err := mutators.SetFactor("health", content.MutatorSteps[len(content.MutatorSteps)-1]); err != nil {
		t.Fatal(err)
	}
	shell.battle = &battleSession{shell: shell, sess: &session.Session{Mutators: mutators}}
	refused := func(when string) {
		t.Helper()
		modal := shell.frontend.Panels.Modal()
		if modal == nil || modal.Message() != mutatedSaveMessage {
			t.Fatalf("%s: no %q message", when, mutatedSaveMessage)
		}
		shell.frontend.Panels.CloseModal()
	}
	for _, source := range []saveLoadSource{saveLoadFromBattle, saveLoadFromResults} {
		if err := shell.openSaveLoadScreen(saveScreenMode, source); err != nil {
			t.Fatal(err)
		}
		if saveLoadUI != nil {
			t.Fatalf("source %d: the save dialog opened", source)
		}
		refused("open")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := session.WriteRetailContinuationSave(session.RetailSavePath(dir, "listed"), save.Summary{Description: "listed", Gametype: 1, BetweenMissions: 1, Side: 1, Players: 1}); err != nil {
		t.Fatal(err)
	}
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil || saveLoadUI == nil {
		t.Fatalf("the load dialog did not open: %v", err)
	}
	shell.activateSaveLoadGadget("SaveGame")
	if saveLoadUI.Mode() != loadScreenMode {
		t.Fatal("the load dialog switched to saving")
	}
	refused("switch")

	saveLoadUI.SetMode(saveScreenMode)
	saveLoadUI.SetName("forced")
	shell.commitSaveLoadWrite()
	refused("commit")
	if _, err := os.Stat(session.RetailSavePath(dir, "forced")); !os.IsNotExist(err) {
		t.Fatalf("a mutated battle was saved: %v", err)
	}
}
