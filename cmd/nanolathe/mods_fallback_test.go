package main

import (
	"errors"
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
	mod := installFixtureMod(t, lib, "broken", &modlibrary.Metadata{Schema: 1, ID: "broken", Name: "Broken", Version: "2", ContentProfile: "profiles/missing.json"})
	saveModChoice(t, settings.ModSelection{ID: mod.ID, Version: mod.Version}, nil, "")
	code, out, errOut := installerRun(t, "--check-install", "--root", base)
	if code != 0 || out != "" || !strings.Contains(errOut, "nanolathe: warning: the saved mod broken@2 does not start") {
		t.Fatalf("broken saved mod: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, _, errOut = installerRun(t, "--check-install", "--root", base, "--mod", mod.ID)
	if code != 1 || strings.Contains(errOut, "warning") || !strings.Contains(errOut, "content profile") {
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
