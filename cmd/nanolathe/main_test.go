package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRetailRoot returns a usable retail install path for integration tests.
func findRetailRoot() string {
	if root := os.Getenv("NANOLATHE_TA_ROOT"); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, "TotalAnnihilation")
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	if info, err := os.Stat("/path/to/home/TotalAnnihilation"); err == nil && info.IsDir() {
		return "/path/to/home/TotalAnnihilation"
	}
	return ""
}

// probeRetail skips integration tests when the original assets are absent.
func probeRetail(t *testing.T) string {
	t.Helper()
	if root := findRetailRoot(); root != "" {
		if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err == nil {
			return root
		}
	}
	t.Skip("retail assets not available")
	return ""
}

func TestParseFlagsDefaults(t *testing.T) {
	var out bytes.Buffer
	opts, err := parseFlags(nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Seed >= 0 {
		t.Fatalf("default seed = %d, want a negative sentinel", opts.Seed)
	}
	if opts.Root == "" {
		t.Fatal("default root is empty")
	}
	if opts.Headless {
		t.Fatal("headless must default off")
	}
}

func TestParseFlagsHelp(t *testing.T) {
	var out bytes.Buffer
	if _, err := parseFlags([]string{"--help"}, &out); err != ErrHelp {
		t.Fatalf("err = %v, want ErrHelp", err)
	}
	if !strings.Contains(out.String(), "-map") {
		t.Fatalf("usage does not list the flags:\n%s", out.String())
	}
}

// TestSeedOverride: a fixed --seed must fix BOTH streams, because RNG state is
// not saved and is reseeded on load [08 "Scheduler and random state in saves"].
// PLAN_00 C4, PLAN_03 C10.
func TestSeedOverride(t *testing.T) {
	sim, crt := seedsFor(Options{Seed: 7})
	if sim != 7 || crt != 7 {
		t.Fatalf("seedsFor(7) = sim %d crt %d, want 7/7", sim, crt)
	}
	if sim, crt = seedsFor(Options{Seed: -1}); sim == 0 || crt == 0 {
		t.Fatalf("clock-derived seeds produced zero: sim %d crt %d", sim, crt)
	}
}

// TestMissingRootDiagnostic locks the standard diagnostic shape from
// AGENTS.md §Diagnostics.
func TestMissingRootDiagnostic(t *testing.T) {
	_, err := openContent(Options{Root: "/definitely/not/an/install"})
	if err == nil {
		t.Fatal("missing root was accepted")
	}
	message := err.Error()
	for _, want := range []string{"nanolathe:", "logical path", "providers searched", "expected"} {
		if !strings.Contains(message, want) {
			t.Fatalf("diagnostic %q is missing %q", message, want)
		}
	}
}

// mainTestRetailRoot skips when retail install is absent [AGENTS.md §Test policy].
func mainTestRetailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			root = filepath.Join(home, "TotalAnnihilation")
		} else {
			t.Skip("no home directory")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "gamedata")); err != nil {
		// Also accept loose HPI at root (real install has empty gamedata dir on disk
		// but HPI at root). Check for a required archive as fallback.
		if _, err2 := os.Stat(filepath.Join(root, "totala1.hpi")); err2 != nil {
			t.Skip("retail assets not present")
		}
	}
	return root
}

// TestHeadlessMissionRunsSession is the integrated headless-session check:
// --mission constructs a session and prints a summary.
func TestHeadlessMissionRunsSession(t *testing.T) {
	root := mainTestRetailRoot(t)
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	opts := Options{
		Root:     root,
		Headless: true,
		Mission:  "camps/arm campaign.tdf:MISSION0",
		Ticks:    300,
		Seed:     1,
	}
	if err := run(opts, out); err != nil {
		t.Fatalf("run headless mission: %v", err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "seed:") {
		t.Fatalf("mission run missing seed header: %q", s)
	}
	if !strings.Contains(s, "rng sim:") && !strings.Contains(s, "skirmish unit") {
		t.Fatalf("mission run did not produce session summary, got: %q", s)
	}
}
