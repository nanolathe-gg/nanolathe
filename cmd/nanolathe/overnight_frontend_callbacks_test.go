package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Exercise the authored names through the real callback, rather than only its
// cue-name helper [07 R-FE-01 §4][07 R-FE-01 §5].
func TestRetailCampaignAndMapAuthoredCallbacks(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g, _, _ := retailAssetShell(t)
	g.openMissionMenu(false)
	for _, tc := range []struct {
		name string
		side int
	}{{"Core", 1}, {"Arm", 0}} {
		g.activateGadget(tc.name)
		if g.missionSide != tc.side || len(g.campaignOptions) == 0 || g.campaignIdx != 0 || g.missionIdx != 0 {
			t.Fatalf("%s callback did not select side and rebuild campaign list", tc.name)
		}
	}
	g.activateGadget("Missions")
	if g.briefing == nil || g.briefingPanel == nil {
		t.Fatal("Missions callback did not open selected briefing")
	}
	g.briefing, g.briefingPanel = nil, nil
	g.openMenu(modeMenuSkirmish)
	g.activateGadget("SelectMap")
	if g.frontend.Mode != modeMenuMap || len(g.maps) < 2 {
		t.Fatal("map chooser unavailable")
	}
	g.mapIdx = 1
	want := g.maps[1]
	g.activateGadget("MAPNAMES")
	if g.setup.MapName != want || g.frontend.Mode != modeMenuSkirmish {
		t.Fatal("MAPNAMES callback did not commit selected map and return")
	}
}

// An unusable initial GUI must fail construction; a shell with no MAINMENU
// input owner cannot recover by refusing a later child [07 §5].
func TestRetailUnusableMainMenuFailsShellConstruction(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	_, cs, _ := retailAssetShell(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "guis"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "guis", "mainmenu.gui"), []byte("[broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cs.fs.MountDirectory(dir, 1000000); err != nil {
		t.Fatal(err)
	}
	g, err := newGameShell(Options{Root: cs.root}, cs)
	if err == nil || g != nil || !strings.Contains(strings.ToLower(err.Error()), "mainmenu.gui") {
		t.Fatalf("unusable MAINMENU produced shell=%v err=%v", g != nil, err)
	}
}
