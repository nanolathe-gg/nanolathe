package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestBuilderOptionsPageTransaction(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g.attachSettings()
	t.Cleanup(g.closeRetailOptionsScreen)
	g.openMenu(modeMenuSingle)
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("BUILDERS")
	if optionsState.page != "builders" {
		t.Fatal("builder category did not open")
	}
	for _, names := range builderOptionNames {
		for _, name := range names {
			index := optionsPanel.Index(name)
			if index < 0 || optionsPanel.Window.Gadgets[index].ButtonArt == nil {
				t.Fatalf("missing control art for %s", name)
			}
		}
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "builder-options.png"))
	}
	g.activateRetailOptionsGadget("NCYCLE")
	g.activateRetailOptionsGadget("NDOUBLE")
	g.activateRetailOptionsGadget("BGHOLD")
	g.activateRetailOptionsGadget("BPROAM")
	if g.builderOptions.Guard[0] != 2 || g.builderOptions.Patrol[2] != 2 {
		t.Fatal("controls did not advance once")
	}
	g.activateRetailOptionsGadget("UNDO")
	if g.builderOptions != settings.DefaultBuilderOptions() || g.presentation.CommunitySelection != 0 || g.presentation.DoubleClickSelection != 0 {
		t.Fatal("undo did not restore the six values")
	}
	g.activateRetailOptionsGadget("BGHOLD")
	g.activateRetailOptionsGadget("NCYCLE")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.BuilderOptions.Guard[0] != 2 || saved.Presentation.CommunitySelection != 1 {
		t.Fatal("OK did not persist builder preferences")
	}
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("BUILDERS")
	g.activateRetailOptionsGadget("RESTORE")
	g.activateRetailOptionsGadget("CANCEL")
	if g.builderOptions.Guard[0] != 2 || g.presentation.CommunitySelection != 1 {
		t.Fatal("cancel did not restore the saved preference")
	}
	if err := g.openRetailOptionsScreen(true); err != nil {
		t.Fatal(err)
	}
	g.activateRetailOptionsGadget("BUILDERS")
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "builder-options-battle.png"))
	}
}
