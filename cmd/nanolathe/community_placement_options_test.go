package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestCommunityPlacementOptionsTransaction(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g.attachSettings()
	g.presentation.BuildRotateKey = "r"
	t.Cleanup(g.closeRetailOptionsScreen)
	g.openMenu(modeMenuSingle)
	g.activateGadget("Options")
	g.activateRetailOptionsGadget("PLACEMENT")
	if optionsState == nil || optionsState.page != "placement" {
		t.Fatal("Placement page did not open")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-placement.png"))
	}

	g.activateRetailOptionsGadget("NPREVIEW")
	g.activateRetailOptionsGadget("NROVERLAY")
	g.activateRetailOptionsGadget("NORDERDRAG")
	g.activateRetailOptionsGadget("NTEAMNANO")
	g.activateRetailOptionsGadget("NMEXSNAP")
	g.activateRetailOptionsGadget("NSNAPMOD")
	if p := g.presentation; p.NanoframePreview != 1 || p.BuildRotationOverlay != 0 || p.QueuedOrderDrag != 1 || p.TeamColorNanolathe != 1 || p.MexSnapRadius != 0 || p.ClickSnapOverrideKey != "ctrl" {
		t.Fatalf("live Placement preferences=%+v", p)
	}
	g.activateRetailOptionsGadget("UNDO")
	if p := g.presentation; p.NanoframePreview != 0 || p.BuildRotationOverlay != 1 || p.QueuedOrderDrag != 0 || p.TeamColorNanolathe != 0 || p.MexSnapRadius != -1 || p.ClickSnapOverrideKey != "alt" || p.BuildRotateKey != "r" {
		t.Fatalf("Placement undo=%+v", p)
	}

	g.activateRetailOptionsGadget("NWRECKSNAP")
	g.activateRetailOptionsGadget("NSNAPMOD")
	g.activateRetailOptionsGadget("PREV")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p := saved.Presentation; p.WreckSnapRadius != 0 || p.ClickSnapOverrideKey != "ctrl" || p.BuildRotateKey != "r" {
		t.Fatalf("saved Placement preferences=%+v", p)
	}
	if err := g.openRetailOptionsScreen(true); err != nil {
		t.Fatal(err)
	}
	g.activateRetailOptionsGadget("PLACEMENT")
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "community-placement-battle.png"))
	}
	g.activateRetailOptionsGadget("RESTORE")
	g.activateRetailOptionsGadget("CANCEL")
	if g.presentation.ClickSnapOverrideKey != "ctrl" {
		t.Fatal("cancel did not restore Placement preferences")
	}

}

func TestCommunityPlacementRadiusStages(t *testing.T) {
	spec := communityPlacementRadiusSpec{maximum: 3, defaultRadius: 2, inBattle: true}
	if got := communityPlacementRadiusStage(-1, spec); got != 0 {
		t.Fatalf("auto stage=%d, want 0", got)
	}
	if got := communityPlacementRadiusStage(0, spec); got != 1 {
		t.Fatalf("off stage=%d, want 1", got)
	}
	if got := communityPlacementRadiusStage(3, spec); got != 4 {
		t.Fatalf("radius-3 stage=%d, want 4", got)
	}
	if got := communityPlacementRadiusStage(7, spec); got != 0 {
		t.Fatalf("out-of-range stage=%d, want resolved Auto", got)
	}
	if text := communityPlacementRadiusText("Mex", spec); text != "Mex: Auto 2|Mex: Off|Mex: 1|Mex: 2|Mex: 3" {
		t.Fatalf("radius labels=%q", text)
	}
}
