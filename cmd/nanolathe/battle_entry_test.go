package main

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
)

func TestBattleEntryDirectAndMenuCompositionMatch(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("retail assets unavailable: %v", err)
		}
		root = home + "/TotalAnnihilation"
	}
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	directSession, directCatalog, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	directClient, err := client.New(client.Options{Width: retailScreenW, Height: retailScreenH, Buffer: &frame.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := composeBattleEntry(directSession, directCatalog, cs, directClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	detachBattleAudio(directClient, directSession)

	menuSession, menuCatalog, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	menuClient, err := client.New(client.Options{Width: retailScreenW, Height: retailScreenH, Buffer: &frame.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	previousClient := clPtr
	clPtr = menuClient
	defer func() { clPtr = previousClient }()
	if err := shell.enterBattle(menuSession, menuCatalog); err != nil {
		t.Fatal(err)
	}
	defer shell.returnFromBattle(menuClient)

	// Both entry paths clamp and center the same authored terrain camera; the
	// playable insets, rather than raw terrain extents, own its bounds [07 §10].
	if got, want := *shell.battle.cam, *direct.cam; !reflect.DeepEqual(got, want) {
		t.Fatalf("menu camera = %+v, direct camera = %+v", got, want)
	}
	wantMapW := int32(menuSession.World.CellW * 16)
	wantMapH := int32(menuSession.World.CellH * 16)
	if menuSession.World.PlayRight != 0 {
		wantMapW = menuSession.World.PlayRight
	}
	if menuSession.World.PlayBottom != 0 {
		wantMapH = menuSession.World.PlayBottom
	}
	if shell.battle.cam.MapW != wantMapW || shell.battle.cam.MapH != wantMapH {
		t.Fatalf("camera bounds = (%d,%d), playable terrain bounds = (%d,%d)", shell.battle.cam.MapW, shell.battle.cam.MapH, wantMapW, wantMapH)
	}

	// Both callers install the same composed HUD window set. Compare canonical
	// keys so Go map order is irrelevant [I1].
	directWindows := battleHUDWindowNames(direct.hud)
	menuWindows := battleHUDWindowNames(shell.battle.hud)
	if !reflect.DeepEqual(menuWindows, directWindows) {
		t.Fatalf("menu HUD windows = %v, direct HUD windows = %v", menuWindows, directWindows)
	}
	// GEN is the authored general command page, distinct from the battle-root
	// MAIN2 GUI [07 §6][07 §9].
	requiredGeneral := strings.ToLower(shell.battle.hud.side.NamePrefix) + "gen"
	if !containsSortedName(menuWindows, requiredGeneral) {
		t.Fatalf("HUD windows %v do not include authored general command window %q", menuWindows, requiredGeneral)
	}
}

func battleHUDWindowNames(h *retailBattleHUD) []string {
	if h == nil {
		return nil
	}
	names := make([]string, 0, len(h.windows)+4)
	for name := range h.windows {
		names = append(names, name)
	}
	if h.optionsWin != nil {
		names = append(names, "modal:options")
	}
	if h.exitWin != nil {
		names = append(names, "modal:exit")
	}
	if h.confirmWin != nil {
		names = append(names, "modal:confirm")
	}
	if h.resultWin != nil {
		names = append(names, "modal:result")
	}
	sort.Strings(names)
	return names
}

func containsSortedName(names []string, want string) bool {
	i := sort.SearchStrings(names, want)
	return i < len(names) && names[i] == want
}
