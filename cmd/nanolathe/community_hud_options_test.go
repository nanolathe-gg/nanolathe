package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
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
	if optionsPanel.Index("HELPTEXT") < 0 {
		t.Fatal("HUD applicability hints have no visible help target")
	}
	for _, name := range []string{"NHEALTH", "NCOUNTERS", "NRELOAD", "NVETERAN", "NGROUPS", "NALLIES", "NWEATHER", "NVICTORY"} {
		if optionsPanel.HelpOf(name) == "" {
			t.Fatalf("%s has no applicability hint", name)
		}
	}
	if client.DamageBars() || optionsPanel.StageAt(optionsPanel.Index("NHEALTH")) != 0 {
		t.Fatal("health bars should open at the saved default")
	}
	reload := optionsPanel.Window.PlacedRect(optionsPanel.Index("NRELOAD"))
	g.updateHoverHelp(reload.X+1, reload.Y+1)
	if got, want := optionsPanel.TextOf("HELPTEXT"), optionsPanel.HelpOf("NRELOAD"); got != want {
		t.Fatalf("reload hint = %q, want %q", got, want)
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-hud.png"))
	}
	g.activateRetailOptionsGadget("NCOUNTERS")
	g.activateRetailOptionsGadget("NGROUPS")
	g.activateRetailOptionsGadget("NHEALTH")
	if !cl.CommunityHUDOptions().Counters || !cl.CommunityHUDOptions().DisableGroupNumbers || !client.DamageBars() {
		t.Fatal("live HUD preferences not applied")
	}
	g.activateRetailOptionsGadget("UNDO")
	if cl.CommunityHUDOptions().Counters || cl.CommunityHUDOptions().DisableGroupNumbers || client.DamageBars() {
		t.Fatal("undo changed default HUD settings")
	}
	g.activateRetailOptionsGadget("NHEALTH")
	g.activateRetailOptionsGadget("NWEATHER")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Presentation.WeatherReport != 1 || !saved.DamageBarsEnabled() {
		t.Fatal("HUD preference not persisted")
	}
	if err := g.openRetailOptionsScreen(true); err != nil {
		t.Fatal(err)
	}
	g.activateRetailOptionsGadget("COMMUNITYHUD")
	counters := optionsPanel.Window.PlacedRect(optionsPanel.Index("NCOUNTERS"))
	g.updateHoverHelp(counters.X+1, counters.Y+1)
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-hud-battle.png"))
	}
	g.activateRetailOptionsGadget("RESTORE")
	if client.DamageBars() || g.presentation.WeatherReport != 0 {
		t.Fatal("restore did not reset the HUD page defaults")
	}
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation.WeatherReport != 1 || !client.DamageBars() {
		t.Fatal("cancel did not restore the entry HUD preferences")
	}
}
