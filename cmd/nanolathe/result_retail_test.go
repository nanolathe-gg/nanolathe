//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestRetailEndMissionAuthoredControls(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer fs.Close()

	window, err := gui.LoadWithTranslation(fs, "guis/endmsn.gui", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, gadget := range window.Gadgets {
		seen[gadget.Name] = true
	}
	for _, name := range []string{"Start", "LoadGame", "SaveGame", "MainMenu", "Difficulty"} {
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
	if i := window.GadgetIndex(name); i >= 0 {
		return &window.Gadgets[i]
	}
	return nil
}
