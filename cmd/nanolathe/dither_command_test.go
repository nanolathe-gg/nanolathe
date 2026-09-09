package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The stored DWORD contributes only its low bit; the command writes the live
// bit immediately, while VISUALS RESTORE clears it only in the front end
// [07 R-CAM-01 §6][07 R-FE-01 §6][07 R-FE-01 §11].
func TestDitherSettingsCommandAndRestoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	var stored settings.Settings
	for _, tc := range []struct {
		blob      string
		raw, want int
	}{
		{blob: "{\"version\":1,\"display\":{\"ditheredFog\":2}}\n", raw: 2, want: 0},
		{blob: "{\"version\":1,\"display\":{\"ditheredFog\":3}}\n", raw: 3, want: 1},
	} {
		if err := os.WriteFile(path, []byte(tc.blob), 0o644); err != nil {
			t.Fatal(err)
		}
		var err error
		stored, err = settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		if stored.Display.DitheredFog != tc.want || stored.Display.DitheredFogEnabled() != (tc.want != 0) {
			t.Fatalf("stored DitheredFog %d loaded as %d/%t, want low bit %d", tc.raw, stored.Display.DitheredFog, stored.Display.DitheredFogEnabled(), tc.want)
		}
	}
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	applyVisualOptions(cl, stored.Display)
	if !cl.DitheredFog() {
		t.Fatal("stored DitheredFog bit did not reach the client")
	}

	direct := &battleSession{cl: cl}
	direct.dispatchLocalCommand("+Dither ignored")
	stored, err = settings.Load()
	if err != nil || cl.DitheredFog() || stored.Display.DitheredFog != 0 {
		t.Fatalf("direct Dither clear = live %t stored %d err=%v", cl.DitheredFog(), stored.Display.DitheredFog, err)
	}

	shell := &gameShell{display: settings.DefaultDisplay(), settingsWritable: true}
	shellBattle := &battleSession{cl: cl, shell: shell}
	shellBattle.dispatchLocalCommand("+dItHeR")
	stored, err = settings.Load()
	if err != nil || !cl.DitheredFog() || shell.display.DitheredFog != 1 || stored.Display.DitheredFog != 1 {
		t.Fatalf("shell Dither set = live %t shell %d stored %d err=%v", cl.DitheredFog(), shell.display.DitheredFog, stored.Display.DitheredFog, err)
	}

	// A later direct write-all command must carry the current live dither bit,
	// just as it carries deferred vehicle and feature shadow changes.
	if err := settings.Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	direct.dispatchLocalCommand("+IFace 0")
	stored, err = settings.Load()
	if err != nil || stored.Display.DitheredFog != 1 {
		t.Fatalf("later direct settings write stored DitheredFog %d, want live bit 1: %v", stored.Display.DitheredFog, err)
	}

	previousClient, previousState, previousPanel := clPtr, optionsState, optionsPanel
	t.Cleanup(func() {
		clPtr, optionsState, optionsPanel = previousClient, previousState, previousPanel
	})
	clPtr, optionsPanel = cl, nil
	shell.display.DitheredFog = 1
	optionsState = &retailOptionsState{page: "visuals"}
	shell.restoreRetailOptionsDefaults()
	if shell.display.DitheredFog != 0 || cl.DitheredFog() {
		t.Fatalf("front-end RESTORE left DitheredFog = %d/%t, want clear", shell.display.DitheredFog, cl.DitheredFog())
	}

	shell.display.DitheredFog = 1
	cl.SetDitheredFog(true)
	optionsState = &retailOptionsState{page: "visuals", inBattle: true}
	shell.restoreRetailOptionsDefaults()
	if shell.display.DitheredFog != 1 || !cl.DitheredFog() {
		t.Fatalf("battle RESTORE changed DitheredFog = %d/%t, want retained", shell.display.DitheredFog, cl.DitheredFog())
	}
}
