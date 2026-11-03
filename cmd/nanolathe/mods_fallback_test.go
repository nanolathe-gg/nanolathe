package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// A saved mod that mounts but whose battle or shell cannot be built falls
// back to no mod, the way the menu start does, for the direct --map view as
// well (§4.3 "A missing mod at start"); the same failure of a --mod is an
// error and nothing is retried.
func TestSavedModBuildFailureFallsBackButModFlagDoesNot(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "unbuildable", &modlibrary.Metadata{Schema: 1, ID: "unbuildable", Name: "Unbuildable", Version: "1"})
	buildFailure := errors.New("nanolathe: catalog compile failed: logical path units/fixture.fbi, providers searched [unbuildable], expected a complete compiled catalog")
	var starts []string
	start := func(_ Options, cs *contentSet) (string, error) {
		if cs.mod != nil {
			starts = append(starts, cs.mod.ID)
			return "", buildFailure
		}
		starts = append(starts, "none")
		return "started", nil
	}

	saveModChoice(t, settings.ModSelection{ID: mod.ID}, nil, "")
	launch := Options{Root: base}
	cs, err := openContent(launch)
	if err != nil || cs.mod == nil || !cs.savedMod {
		t.Fatalf("the saved mod did not mount: %v", err)
	}
	result, running, err := startWithSavedModFallback(launch, launch, cs, start)
	if err != nil || result != "started" || running == nil || running.mod != nil {
		t.Fatalf("saved mod fallback = %q, %v", result, err)
	}
	defer running.Close()
	if strings.Join(starts, ",") != "unbuildable,none" || !strings.Contains(running.modNotice, "Unbuildable 1") {
		t.Fatalf("starts %v, notice %q", starts, running.modNotice)
	}

	starts = nil
	flagged := Options{Root: base, Mod: mod.ID, ModSet: true}
	cs, err = openContent(flagged)
	if err != nil || cs.mod == nil || cs.savedMod {
		t.Fatalf("--mod did not mount: %v", err)
	}
	defer cs.Close()
	if _, _, err := startWithSavedModFallback(flagged, flagged, cs, start); !errors.Is(err, buildFailure) || len(starts) != 1 {
		t.Fatalf("--mod build failure = %v after starts %v, want the failure and no retry", err, starts)
	}
}

// --check-install validates what a start would mount. A saved mod never
// stops a start, so a broken or missing saved mod is a warning and the base
// install decides the exit status; the same mod named by --mod fails the
// check (§4.3).
func TestCheckInstallWarnsAboutTheSavedModOnly(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureConfig(t, lib, "broken", brokenConfig)
	saveModChoice(t, settings.ModSelection{ID: mod.ID, Version: mod.Version}, nil, "")
	code, out, errOut := installerRun(t, "--check-install", "--root", base)
	if code != 0 || out != "" || !strings.Contains(errOut, "nanolathe: warning: the saved mod broken@2 does not start") {
		t.Fatalf("broken saved mod: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, _, errOut = installerRun(t, "--check-install", "--root", base, "--mod", mod.ID)
	if code != 1 || strings.Contains(errOut, "warning") || !strings.Contains(errOut, "gamedata/moveinfo.tdf") {
		t.Fatalf("broken --mod: exit %d, stderr %q", code, errOut)
	}

	saveModChoice(t, settings.ModSelection{ID: "ghost"}, nil, "")
	code, _, errOut = installerRun(t, "--check-install", "--root", base)
	if code != 0 || !strings.Contains(errOut, "nanolathe: warning: Mod ghost is not installed") {
		t.Fatalf("missing saved mod: exit %d, stderr %q", code, errOut)
	}
	code, _, errOut = installerRun(t, "--check-install", "--root", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "gamedata/moveinfo.tdf") {
		t.Fatalf("broken base with a saved mod: exit %d, stderr %q", code, errOut)
	}
}

// Issue #186: a saved mod whose config moves gamedata (as TA: Escalation's
// does) can be the only supplier of the required products, so the base
// install starts with it and not without it. When the mod's shell then fails
// to build, the remount without it fails too, and that error must keep the
// saved mod's own failure and name the providers it searched rather than
// "[none]". The remount mounts the base install the first mount resolved,
// not a second resolution of the host.
func TestSavedModFallbackFailureKeepsTheModsCause(t *testing.T) {
	base, lib := modFixture(t)
	for _, name := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if err := os.Remove(filepath.Join(base, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	writeModFixtureFile(t, base, "units/base.fbi", "[UNITINFO] { }\n")
	dir := filepath.Join(t.TempDir(), "moved")
	for _, name := range []string{"gamedatE/moveinfo.tdf", "gamedatE/sidedata.tdf"} {
		writeModFixtureFile(t, dir, name, "// authored startup fixture\n")
	}
	writeModFixtureFile(t, dir, modlibrary.MetadataFile, `{"schema":2,"id":"moved","name":"Moved","version":"3","content":{"layout":{"gamedata":"gamedatE"}}}`)
	mod, err := lib.InstallDirectory(dir, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	buildFailure := errors.New("nanolathe: shell bind failed: logical path guiE/mainmenu.gui, providers searched [moved], expected a menu")
	start := func(_ Options, cs *contentSet) (string, error) {
		if cs.mod != nil {
			return "", buildFailure
		}
		return "started", nil
	}

	saveModChoice(t, settings.ModSelection{ID: mod.ID}, nil, "")
	t.Setenv("NANOLATHE_TA_ROOT", base)
	launch := Options{}
	cs, err := openContent(launch)
	if err != nil || cs.mod == nil || !cs.savedMod {
		t.Fatalf("the saved mod did not mount: %v", err)
	}
	t.Setenv("NANOLATHE_TA_ROOT", t.TempDir())
	_, running, err := startWithSavedModFallback(launch, launch, cs, start)
	if running != nil {
		_ = running.Close()
	}
	if err == nil {
		t.Fatal("the start without the mod succeeded on a base that lacks gamedata")
	}
	text := err.Error()
	if !errors.Is(err, buildFailure) || !strings.Contains(text, "moved@3") || !strings.Contains(text, "guiE/mainmenu.gui") {
		t.Fatalf("fallback failure %q does not carry the saved mod's cause", text)
	}
	if strings.Contains(text, "providers searched [none]") || !strings.Contains(text, base) {
		t.Fatalf("fallback failure %q does not name the base install it searched", text)
	}
}
