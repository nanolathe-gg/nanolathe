package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"os"
	"path/filepath"
	"testing"
)

func TestCommunityHUDOptionsTransaction(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g.attachSettings()
	t.Cleanup(g.closeRetailOptionsScreen)
	g.openMenu(modeMenuSingle)
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("COMMUNITYHUD")
	if optionsState.page != "communityhud" {
		t.Fatal("HUD page did not open")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-hud.png"))
	}
	g.activateRetailOptionsGadget("NCOUNTERS")
	g.activateRetailOptionsGadget("NGROUPS")
	if !cl.CommunityHUDOptions().Counters || !cl.CommunityHUDOptions().DisableGroupNumbers {
		t.Fatal("live HUD preferences not applied")
	}
	g.activateRetailOptionsGadget("UNDO")
	if cl.CommunityHUDOptions().Counters || cl.CommunityHUDOptions().DisableGroupNumbers {
		t.Fatal("undo changed default HUD settings")
	}
	g.activateRetailOptionsGadget("NWEATHER")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Presentation.WeatherReport != 1 {
		t.Fatal("HUD preference not persisted")
	}
	if err := g.openRetailOptionsScreen(true); err != nil {
		t.Fatal(err)
	}
	g.activateRetailOptionsGadget("COMMUNITYHUD")
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-hud-battle.png"))
	}
	g.activateRetailOptionsGadget("RESTORE")
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation.WeatherReport != 1 {
		t.Fatal("cancel did not restore weather preference")
	}
}
