package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// A manual root stack disables selection, and naming a mod beside one is an
// error rather than a silent choice (§4.3).
func TestManualRootStackRejectsAModFlag(t *testing.T) {
	stack := []string{"/ta", "/mod"}
	if sel, err := resolveModSelection(Options{}, stack, stack); err != nil || !sel.manual || sel.mod != nil {
		t.Fatalf("manual stack selection = %+v, %v", sel, err)
	}
	if _, err := resolveModSelection(Options{Mod: "prota", ModSet: true}, stack, stack); err == nil {
		t.Fatal("--mod beside a manual root stack was accepted")
	}
	if sel, err := resolveModSelection(Options{Mod: "none", ModSet: true}, stack, stack); err != nil || !sel.manual {
		t.Fatalf("--mod none beside a manual stack = %+v, %v", sel, err)
	}
}

// modFixture is an authored two-file base install and an empty mod library,
// with the settings file and the data directory both in scratch space.
func modFixture(t *testing.T) (string, *modlibrary.Library) {
	t.Helper()
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := t.TempDir()
	for _, name := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		writeModFixtureFile(t, base, name, "// authored startup fixture\n")
	}
	lib, err := openModLibrary()
	if err != nil {
		t.Fatal(err)
	}
	return base, lib
}

func writeModFixtureFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installFixtureMod installs a folder holding one loose file and, unless meta
// is nil, the given metadata. No content validation runs, so a fixture can be
// a mod that would not start.
func installFixtureMod(t *testing.T, lib *modlibrary.Library, folder string, meta *modlibrary.Metadata) modlibrary.Mod {
	t.Helper()
	dir := filepath.Join(t.TempDir(), folder)
	writeModFixtureFile(t, dir, "units/fixture.fbi", "[UNITINFO] { }\n")
	if meta != nil {
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		writeModFixtureFile(t, dir, modlibrary.MetadataFile, string(data))
	}
	mod, err := lib.InstallDirectory(dir, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

func saveModChoice(t *testing.T, mod settings.ModSelection, mutators map[string]string, profile string) {
	t.Helper()
	s := settings.Defaults()
	s.Mod, s.Mutators, s.ContentProfile = mod, mutators, profile
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

// A mod's `requires` paths are enforced where it is mounted, not only on the
// screen (§4.2): --mod naming a mod whose requirements the base install does
// not meet is an error that names them, and the saved choice naming one
// starts with no mod and says so.
func TestModRequirementsAreEnforcedAtTheMount(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "needs", &modlibrary.Metadata{Schema: 1, ID: "needs", Name: "Needs", Version: "1", Requires: []string{"maps/expansion.ota"}})
	if _, err := openContent(Options{Root: base, Mod: mod.ID, ModSet: true}); err == nil || !strings.Contains(err.Error(), "maps/expansion.ota") {
		t.Fatalf("--mod with an unmet requirement = %v, want an error naming the path", err)
	}
	saveModChoice(t, settings.ModSelection{ID: mod.ID}, nil, "")
	cs, err := openContent(Options{Root: base})
	if err != nil {
		t.Fatalf("a saved mod with an unmet requirement stopped the start: %v", err)
	}
	if cs.mod != nil || !strings.Contains(cs.modNotice, "maps/expansion.ota") {
		t.Fatalf("saved mod with an unmet requirement: mod %v, notice %q", cs.mod, cs.modNotice)
	}
	_ = cs.Close()
	writeModFixtureFile(t, base, "maps/expansion.ota", "[GlobalHeader] { }\n")
	cs, err = openContent(Options{Root: base})
	if err != nil || cs.mod == nil || !cs.savedMod {
		t.Fatalf("with its requirement met the saved mod did not mount: %v", err)
	}
	_ = cs.Close()
}

// A mod that names no content profile, as a metadata-less local package does,
// has its profile detected; the saved contentProfile preference is never
// applied to a mod (§4.5, D12).
func TestLocalModUsesDetectionNotTheSavedProfile(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "My Pack", nil)
	if !mod.Local {
		t.Fatalf("a folder without metadata installed as %+v", mod.Metadata)
	}
	// The saved preference names a profile that renames gamedata, which this
	// fixture cannot satisfy: applying it would fail the mount.
	saveModChoice(t, settings.ModSelection{}, nil, "prota")
	cs, err := openContent(Options{Root: base, Mod: mod.ID, ModSet: true})
	if err != nil {
		t.Fatalf("the local mod did not mount: %v", err)
	}
	defer cs.Close()
	if cs.profile != "retail" {
		t.Fatalf("local mod profile = %q, want the detected retail profile", cs.profile)
	}
}

// Captures, benchmarks and displayless runs take neither the saved mod nor
// the saved mutators; their own --mod and --mutator flags still apply
// (§4.3, §6.6). The window takes both.
func TestNonInteractiveRunsIgnoreTheSavedSelection(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "saved", &modlibrary.Metadata{Schema: 1, ID: "saved", Name: "Saved", Version: "1"})
	saveModChoice(t, settings.ModSelection{ID: mod.ID}, map[string]string{"health": "2"}, "")
	for _, opts := range []Options{{Headless: true}, {Shot: "shot.png"}, {Film: "film.txt"}, {BattleBenchmark: "bench"}, {ShotDebris: "debris"}} {
		opts.Root = base
		cs, err := openContent(opts)
		if err != nil || cs.mod != nil {
			t.Fatalf("%+v mounted the saved mod: %v", opts, err)
		}
		_ = cs.Close()
		if m, err := resolveStartupMutators(opts); err != nil || !m.IsZero() {
			t.Fatalf("%+v took the saved mutators %q: %v", opts, m.String(), err)
		}
		opts.Mod, opts.ModSet, opts.MutatorArgs = mod.ID, true, map[string]string{"damage": "2"}
		cs, err = openContent(opts)
		if err != nil || cs.mod == nil {
			t.Fatalf("%+v ignored --mod: %v", opts, err)
		}
		_ = cs.Close()
		if m, err := resolveStartupMutators(opts); err != nil || m.String() != "damage=2" {
			t.Fatalf("%+v mutators %q, want the flag's damage=2: %v", opts, m.String(), err)
		}
	}
	cs, err := openContent(Options{Root: base})
	if err != nil || cs.mod == nil {
		t.Fatalf("the window did not take the saved mod: %v", err)
	}
	_ = cs.Close()
	if m, err := resolveStartupMutators(Options{Root: base}); err != nil || m.String() != "health=2" {
		t.Fatalf("the window mutators %q, want the saved health=2: %v", m.String(), err)
	}
}

// A battle restart remounts what the battle ran on: with no mod running that
// is explicitly no mod, never the saved choice (§4.3).
func TestBattleRestartRemountsTheRunningModNotTheSavedOne(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "saved", &modlibrary.Metadata{Schema: 1, ID: "saved", Name: "Saved", Version: "1"})
	cs, err := openContent(Options{Root: base, Mod: "none", ModSet: true})
	if err != nil || cs.mod != nil {
		t.Fatalf("no-mod start: %v", err)
	}
	saveModChoice(t, settings.ModSelection{ID: mod.ID}, nil, "")
	shell := &gameShell{cs: cs}
	defer func() { _ = shell.cs.Close() }()
	if !shell.prepareBattleRestartContent() {
		t.Fatal("restart remount failed")
	}
	if shell.cs.mod != nil {
		t.Fatalf("restart with no mod running mounted the saved mod %s", shell.cs.mod.ID)
	}
	running, err := openContent(Options{Root: base, Mod: mod.ID, ModSet: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = shell.cs.Close()
	shell.cs = running
	saveModChoice(t, settings.ModSelection{}, nil, "")
	if !shell.prepareBattleRestartContent() || shell.cs.mod == nil || shell.cs.mod.ID != mod.ID {
		t.Fatalf("restart did not keep the running mod: %+v", shell.cs.mod)
	}
}

// Start-up never fails because of the saved mod (§4.3): a saved mod that fails
// to open is recognised as such, and the start continues with no mod, a notice
// naming the mod and the reason, and the saved choice kept. The same mod named
// by --mod stays an error.
func TestSavedModThatFailsToOpenStartsWithoutIt(t *testing.T) {
	base, lib := modFixture(t)
	mod := installFixtureMod(t, lib, "broken", &modlibrary.Metadata{Schema: 1, ID: "broken", Name: "Broken", Version: "2", ContentProfile: "profiles/missing.json"})
	var saved *savedModError
	if _, err := openContent(Options{Root: base, Mod: mod.ID, ModSet: true}); err == nil || errors.As(err, &saved) {
		t.Fatalf("--mod naming a mod that does not open = %v, want a plain error", err)
	}
	saveModChoice(t, settings.ModSelection{ID: mod.ID, Version: mod.Version}, nil, "")
	_, err := openContent(Options{Root: base})
	if !errors.As(err, &saved) || saved.mod.ID != mod.ID {
		t.Fatalf("the saved mod's failure = %v, want a savedModError", err)
	}
	cs, err := startWithoutSavedMod(Options{Root: base}, saved.mod, saved.err)
	if err != nil {
		t.Fatalf("the start without the saved mod failed: %v", err)
	}
	defer cs.Close()
	if cs.mod != nil || cs.savedMod || !strings.Contains(cs.modNotice, "Broken 2") || !strings.Contains(cs.modNotice, "content profile") {
		t.Fatalf("fallback mod %v, notice %q", cs.mod, cs.modNotice)
	}
	if stored, err := settings.Load(); err != nil || stored.Mod.ID != mod.ID {
		t.Fatalf("the saved choice was not kept: %+v, %v", stored.Mod, err)
	}
}

// waitForJob waits until the download job has finished.
func waitForJob(t *testing.T, job *modDownloadJob) modDownloadView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v := job.view(); !v.running {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the download job did not finish")
	return modDownloadView{}
}

// One download and install runs per process (§8.2): while one runs a second
// cannot start, a cancel reaches only the network part, and the outcome
// stays for the next dialog until one has shown it. "Download cancelled"
// means the player cancelled; a stall, whose error also wraps a cancelled
// context, reports the download's own error.
func TestModDownloadJobRunsOneAtATime(t *testing.T) {
	var job modDownloadJob
	entry := func(id string) modfetch.Entry {
		return modfetch.Entry{Metadata: modlibrary.Metadata{Schema: 1, ID: id, Name: strings.ToUpper(id), Version: "1"}}
	}
	started, release, installing, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !job.start(entry("a"), func(context.Context, func(int64, int64)) error {
		close(started)
		<-release
		return nil
	}, func() error {
		close(installing)
		<-finish
		return nil
	}) {
		t.Fatal("the first download did not start")
	}
	<-started
	if job.start(entry("b"), func(context.Context, func(int64, int64)) error { return nil }, func() error { return nil }) {
		t.Fatal("a second download started while one was running")
	}
	close(release)
	<-installing
	if job.cancelNetwork() {
		t.Fatal("a cancel reached an install under way")
	}
	close(finish)
	if v := waitForJob(t, &job); v.outcome != "A 1 installed" || v.installs != 1 {
		t.Fatalf("outcome %q after %d installs", v.outcome, v.installs)
	}
	job.acknowledge()
	if v := job.view(); v.outcome != "" {
		t.Fatalf("an acknowledged outcome is still shown: %q", v.outcome)
	}

	if !job.start(entry("c"), func(ctx context.Context, _ func(int64, int64)) error {
		<-ctx.Done()
		return fmt.Errorf("mod download interrupted: %w", ctx.Err())
	}, func() error { t.Error("a cancelled download installed"); return nil }) {
		t.Fatal("the download after a finished one did not start")
	}
	if !job.cancelNetwork() {
		t.Fatal("the player's cancel found no network part")
	}
	if v := waitForJob(t, &job); v.outcome != "Download cancelled" {
		t.Fatalf("player cancel outcome %q", v.outcome)
	}

	stall := errors.New("mod download stalled for 1m0s; it resumes from x.part on the next attempt")
	job.start(entry("d"), func(context.Context, func(int64, int64)) error {
		return fmt.Errorf("%w: %w", stall, context.Canceled)
	}, func() error { return nil })
	if v := waitForJob(t, &job); !strings.HasPrefix(v.outcome, "Failed: ") || !strings.Contains(v.outcome, "resumes") {
		t.Fatalf("stall outcome %q, want the download's own error", v.outcome)
	}
}
