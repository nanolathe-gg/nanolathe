package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// These tests lock the installer host policy in DESIGN_CONTENT_VFS §5 and
// DESIGN_SESSIONS_AI_SAVE §5; their fixtures contain no retail assets.
func installerRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var parseOut bytes.Buffer
	opts, err := parseFlags(args, &parseOut)
	if err != nil {
		t.Fatalf("parse installer flags: %v: %s", err, &parseOut)
	}
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	errOut, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()
	code := runOptions(opts, out, errOut)
	stdout, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(errOut.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(stdout), string(stderr)
}

func TestInstallerListDoesNotMountContent(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	// With no required products this candidate fails openContent. Listing
	// must only resolve roots and leave stdout suitable for a shell loop.
	if err := os.WriteFile(filepath.Join(first, "totala1.hpi"), []byte("authored invalid archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NANOLATHE_TA_ROOT", " ")
	code, out, errOut := installerRun(t, "--list-installs", "--root", first, "--root", second)
	if code != 0 || out != first+"\n"+second+"\n" || errOut != "" {
		t.Fatalf("listing = %d, stdout %q, stderr %q", code, out, errOut)
	}
	t.Setenv("NANOLATHE_TA_ROOT", second)
	code, out, errOut = installerRun(t, "--list-installs")
	if code != 0 || out != second+"\n" || errOut != "" {
		t.Fatalf("environment listing = %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestInstallerListFailureUsesStderr(t *testing.T) {
	// A resolver rejection is deterministic even on hosts with installed data.
	t.Setenv("NANOLATHE_TA_ROOT", " ")
	code, out, errOut := installerRun(t, "--list-installs")
	if code != 1 || out != "" || !strings.Contains(errOut, "empty install root") {
		t.Fatalf("failed listing = %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestInstallerCheckUsesStartupValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NANOLATHE_TA_ROOT", " ")
	code, out, errOut := installerRun(t, "--check-install", "--root", root)
	if code != 1 || out != "" || !strings.Contains(errOut, "gamedata/moveinfo.tdf") {
		t.Fatalf("empty install = %d, stdout %q, stderr %q", code, out, errOut)
	}
	if err := os.Mkdir(filepath.Join(root, "gamedata"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"moveinfo.tdf", "sidedata.tdf"} {
		if err := os.WriteFile(filepath.Join(root, "gamedata", name), []byte("// authored startup fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut = installerRun(t, "--check-install", "--root", root)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("valid startup products = %d, stdout %q, stderr %q", code, out, errOut)
	}
	// The restart remount must keep content roots and save storage independent.
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	saves := filepath.Join(t.TempDir(), "chosen saves")
	shell := &gameShell{opts: Options{Root: t.TempDir(), SaveDir: saves}, cs: cs}
	if !shell.prepareBattleRestartContent() {
		t.Fatal("restart could not remount the original content root")
	}
	defer shell.cs.Close()
	if shell.cs.root != root || shell.saveLoadDir() != saves {
		t.Fatalf("restart changed content/save directories: %q, %q", shell.cs.root, shell.saveLoadDir())
	}
	// Both startup products are required, even with a readable root.
	if err := os.Remove(filepath.Join(root, "gamedata", "sidedata.tdf")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = installerRun(t, "--check-install", "--root", root)
	if code != 1 || out != "" || !strings.Contains(errOut, "gamedata/sidedata.tdf") {
		t.Fatalf("missing side definitions = %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestInstallerDiagnosticsRejectGameModes(t *testing.T) {
	for _, args := range [][]string{
		{"--list-installs", "--check-install"},
		{"--list-installs", "--headless"},
		{"--check-install", "--map", "ashap plateau"},
		{"--check-install", "--load-save", "explicit.sav"},
	} {
		code, out, errOut := installerRun(t, args...)
		if code != 1 || out != "" || !strings.Contains(errOut, "incompatible installation diagnostic flags") {
			t.Fatalf("%v = %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestInstallerSaveDirectoryOverride(t *testing.T) {
	var out bytes.Buffer
	root, saves := t.TempDir(), filepath.Join(t.TempDir(), "chosen saves")
	opts, err := parseFlags([]string{"--root", root, "--save-dir", saves, "--load-save", "explicit.sav"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{opts: opts, cs: &contentSet{root: t.TempDir()}}
	if got := shell.saveLoadDir(); got != saves || opts.LoadSave != "explicit.sav" {
		t.Fatalf("save directory = %q, explicit load = %q", got, opts.LoadSave)
	}
	// The override also applies when discovery supplies the content root.
	shell.opts.Root = ""
	if got := shell.saveLoadDir(); got != saves {
		t.Fatalf("discovered-root save override = %q, want %q", got, saves)
	}
	shell.opts.SaveDir = ""
	if got, want := shell.saveLoadDir(), retailSaveDir(shell.cs.root); got != want {
		t.Fatalf("default discovered save directory = %q, want %q", got, want)
	}
	shell.opts.Root = root
	if got, want := shell.saveLoadDir(), retailSaveDir(root); got != want {
		t.Fatalf("default explicit-root save directory = %q, want %q", got, want)
	}
	if _, err := os.Stat(saves); !os.IsNotExist(err) {
		t.Fatalf("resolving the save directory created it: %v", err)
	}
}

func TestInstallerSavePathKeepsDottedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".local", "share", "nanolathe", "savegame")
	screen := newSaveLoadScreen(saveScreenMode, dir, saveLoadFromResults)
	screen.hostSaveDir = true
	for _, tc := range []struct{ name, leaf string }{
		{"slot", "slot.SAV"},
		{"v1.2 final", "v1.SAV"},
		{"", ""},
		{" ", ""},
		{"../outside", ""},
		{`..\outside`, ""},
		{"/absolute", ""},
		{`C:\absolute`, ""},
	} {
		screen.SetName(tc.name)
		want := ""
		if tc.leaf != "" {
			want = filepath.Join(dir, tc.leaf)
		}
		if got := screen.CommitPath(); got != want {
			t.Errorf("name %q: path %q, want %q", tc.name, got, want)
		}
	}
	// The optional host routing must not change callers without --save-dir.
	screen.hostSaveDir = false
	screen.SetName("slot")
	if got, want := screen.CommitPath(), session.RetailSavePath(dir, "slot"); got != want {
		t.Fatalf("default path = %q, want retail path %q", got, want)
	}
}

func TestInstallerSaveDialogsUseExactDirectory(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	shell.opts.SaveDir = filepath.Join(t.TempDir(), ".local", "share", "saved games")
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromResults); err != nil {
		t.Fatal(err)
	}
	saveLoadUI.SetName("slot")
	path := saveLoadUI.CommitPath()
	if want := filepath.Join(shell.opts.SaveDir, "slot.SAV"); path != want {
		t.Fatalf("save dialog path = %q, want %q", path, want)
	}
	summary := session.ContinuationSummary(session.PostBattleSummary{Mission: "fixture", BetweenMissions: true}, session.ContinuationSaveMetadata{Description: "slot", Players: 1})
	if err := session.WriteRetailContinuationSave(path, summary); err != nil {
		t.Fatal(err)
	}
	shell.closeSaveLoadScreen()
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
		t.Fatal(err)
	}
	defer shell.closeSaveLoadScreen()
	if saveLoadUI == nil || len(saveLoadUI.Entries()) != 1 || saveLoadUI.Entries()[0].Path != path {
		t.Fatal("load dialog did not find the save in the exact host directory")
	}
}
