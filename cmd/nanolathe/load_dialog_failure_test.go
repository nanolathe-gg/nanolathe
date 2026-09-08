package main

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
)

// A listed save can fail the later load preflight. The refusal must cover the
// same selected load dialog, preserving its buffers and live battle [08 R-SAVE-02 §2].
func TestLoadRefusalRetainsSelectedDialog(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gameType int32
		remove   bool
	}{
		{name: "invalid game type", gameType: 3},
		{name: "unresolved mission", gameType: 1},
		{name: "removed after listing", gameType: 1, remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetSaveLoadScreenState(t)
			shell, dir := retailShellForTest(t)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := session.RetailSavePath(dir, "selected")
			summary := save.Summary{Description: "selected", Gametype: tc.gameType, BetweenMissions: 1, Mission: "authored missing mission", Side: 1, Players: 1}
			if err := session.WriteRetailContinuationSave(path, summary); err != nil {
				t.Fatal(err)
			}
			old := &battleSession{shell: shell, sess: &session.Session{}}
			shell.battle = old
			if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil {
				t.Fatal(err)
			}
			screen, panel, assets := saveLoadUI, saveLoadPanel, saveLoadAssets
			shell.selectSaveLoadRow(0)
			side := panel.TextOf("SIDE")
			if tc.remove {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			shell.activateSaveLoadGadget("LOAD")
			if modal := shell.frontend.Panels.Modal(); modal == nil || modal.Message() != retailInvalidSaveMessage {
				t.Fatal("missing invalid-save refusal")
			}
			if saveLoadUI != screen || saveLoadPanel != panel || saveLoadAssets != assets || shell.battle != old {
				t.Fatal("preflight refusal retired live state")
			}
			cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
			if err != nil {
				t.Fatal(err)
			}
			cl.Input().Kbd.SetKey(input.KeyEnter, true)
			shell.menuInput(cl)
			if shell.frontend.Panels.Modal() != nil || !shell.saveLoadPanelActive() {
				t.Fatal("refusal did not return to the load dialog")
			}
			entry, ok := saveLoadUI.SelectedEntry()
			if !ok || entry.Path != path || panel.TextOf("SIDE") != side || len(saveLoadUI.sideNames) == 0 {
				t.Fatal("refusal changed selection or side table")
			}
		})
	}
}
