//go:build retail

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestRetailEndMissionAuthoredControls(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("retail assets unavailable: %v", err)
		}
		root = filepath.Join(home, "TotalAnnihilation")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer fs.Close()

	window, err := gui.Load(fs, "guis/endmsn.gui")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, gadget := range window.Gadgets {
		seen[strings.ToLower(gadget.Name)] = true
	}
	for _, name := range []string{"start", "loadgame", "savegame", "mainmenu", "difficulty"} {
		if !seen[name] {
			t.Fatalf("endmsn.gui missing authored %s control", name)
		}
	}
	for _, gadget := range window.Gadgets {
		if gadget.Name == "Start" || gadget.Name == "MainMenu" {
			if gadget.Active != 0 {
				t.Fatalf("retail ENDMSN %s unexpectedly starts active: %d", gadget.Name, gadget.Active)
			}
		}
	}
	if start := findGadget(window, "Start"); start == nil || start.Rect.X != 460 || start.Rect.Y != 315 || start.Rect.W != 120 || start.Rect.H != 20 {
		t.Fatalf("retail ENDMSN Start geometry changed: %#v", start)
	}
	if got := resultActionForControl("Start"); got != ui.ResultActionContinue {
		t.Fatalf("authored Start action = %q", got)
	}
	if got := resultActionForControl("Continue"); got != ui.ResultActionNone {
		t.Fatalf("non-authored Continue action = %q", got)
	}

	gaf, err := formats.LoadGAFFile(fs, "anims/endmsn.gaf")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outcdivider", "victory", "defeat"} {
		entry, ok := gaf.Find(name)
		if !ok || len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
			t.Fatalf("endmsn.gaf missing authored %s entry", name)
		}
	}
	for _, name := range []string{"victory", "defeat"} {
		entry, _ := gaf.Find(name)
		frame := entry.Frames[0].Frame
		if frame.XOffset != 320 || frame.YOffset != 240 {
			t.Fatalf("endmsn.gaf %s anchor = (%d,%d), want (320,240)", name, frame.XOffset, frame.YOffset)
		}
	}
}

func findGadget(window *gui.Window, name string) *gui.Gadget {
	if window == nil {
		return nil
	}
	for i := range window.Gadgets {
		if strings.EqualFold(window.Gadgets[i].Name, name) {
			return &window.Gadgets[i]
		}
	}
	return nil
}
