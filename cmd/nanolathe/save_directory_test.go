package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/save"
)

func TestSaveDirectoryUsesResolvedInstall(t *testing.T) {
	root, override := t.TempDir(), t.TempDir()
	shell := &gameShell{cs: &contentSet{root: root}}
	if got, want := shell.saveLoadDir(), filepath.Join(root, "savegame"); got != want {
		t.Fatalf("discovered install save directory = %q, want %q", got, want)
	}
	shell.opts.Root = override
	if got, want := shell.saveLoadDir(), filepath.Join(override, "savegame"); got != want {
		t.Fatalf("explicit save directory = %q, want %q", got, want)
	}
}

// The discovered install owns both save/load dialogs even when the launch
// directory contains an unusable savegame entry [08 R-SAVE-02 §1].
func TestDiscoveredInstallReusesExistingSaveDirectory(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, dir := retailShellForTest(t)
	// Keep the mounted retail assets, but redirect the discovered root so this
	// test writes only to its own temporary directory.
	shell.cs.root = shell.opts.Root
	shell.opts.Root = ""
	t.Chdir(t.TempDir())
	if err := os.WriteFile("savegame", []byte("launch directory conflict"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeContinuationSlot(t, dir, "existing slot", time.Unix(1_700_000_000, 0))
	for _, mode := range []saveLoadMode{saveScreenMode, loadScreenMode, saveScreenMode} {
		if err := shell.openSaveLoadScreen(mode, saveLoadFromBattle); err != nil {
			t.Fatalf("open mode %d with existing directory: %v", mode, err)
		}
		if saveLoadUI == nil || len(saveLoadUI.Entries()) != 1 || saveLoadUI.Entries()[0].Path != path {
			t.Fatalf("mode %d did not find the existing install save", mode)
		}
		shell.closeSaveLoadScreen()
	}
	writeContinuationSlot(t, dir, "existing slot", time.Unix(1_700_000_001, 0))
	if _, err := save.Open(path); err != nil {
		t.Fatalf("overwritten save is unreadable: %v", err)
	}
	if data, err := os.ReadFile("savegame"); err != nil || string(data) != "launch directory conflict" {
		t.Fatalf("launch directory entry changed: %q, %v", data, err)
	}
}
