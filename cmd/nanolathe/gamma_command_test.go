package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestGammaCommandPersistenceAndOptionConversion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var pal palette.Tables
	pal.Base[7] = [4]byte{120, 120, 120, 0}
	cl.SetPalette(&pal)
	b := &battleSession{cl: cl}
	for _, tc := range []struct {
		value, loaded int
		channel       byte
	}{
		{0, 0, 0}, {-5, -5, 196}, {99, 99, 255}, {10, 12, 120},
	} {
		b.dispatchLocalCommand("+Gamma " + strconv.Itoa(tc.value))
		// A later write-all must preserve the live integer, including 10.
		b.dispatchLocalCommand("+IFace 0")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw settings.Settings
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		loaded, err := settings.Load()
		if err != nil || raw.Display.Gamma != tc.value || loaded.Display.Gamma != tc.loaded || cl.DisplayPalette()[7][0] != tc.channel {
			t.Fatalf("gamma %d: written=%d loaded=%d channel=%d err=%v", tc.value, raw.Display.Gamma, loaded.Display.Gamma, cl.DisplayPalette()[7][0], err)
		}
	}
	oldClient := clPtr
	t.Cleanup(func() { clPtr = oldClient })
	clPtr = cl
	shell := &gameShell{display: settings.DefaultDisplay()}
	shell.restoreRetailOptionsSnapshot(retailOptionsSnapshot{display: settings.Display{Gamma: 0}})
	if cl.DisplayPalette()[7][0] != 60 {
		t.Fatal("restored slider zero did not apply half brightness")
	}
	shell.applyRetailVisualOptions(cl)
	if cl.DisplayPalette()[7][0] != 60 {
		t.Fatal("display button changed gamma")
	}
}
