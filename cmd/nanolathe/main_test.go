package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
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

type scriptedBattleSeedSource struct {
	pairs []BattleSeeds
	n     int
}

func (s *scriptedBattleSeedSource) NextBattleSeeds() BattleSeeds {
	pair := s.pairs[s.n]
	s.n++
	return pair
}

func TestBattleSeedSourceCopiesOneExplicitPairIntoSkirmishConfig(t *testing.T) {
	source := &scriptedBattleSeedSource{pairs: []BattleSeeds{{Simulation: 17, CRT: 29}}}
	cfg := configWithBattleSeeds(session.SkirmishConfig{RNGSimSeed: 101, RNGCrtSeed: 103}, source)
	if cfg.RNGSimSeed != 17 || cfg.RNGCrtSeed != 29 {
		t.Fatalf("config seeds = %d/%d, want 17/29", cfg.RNGSimSeed, cfg.RNGCrtSeed)
	}
	if source.n != 1 {
		t.Fatalf("source calls = %d, want exactly one", source.n)
	}
}

func TestDeterministicBattleSeedSourceSelectsBothStreams(t *testing.T) {
	first := newBattleSeedSource(Options{Seed: 0x1234}).NextBattleSeeds()
	second := newBattleSeedSource(Options{Seed: 0x1234}).NextBattleSeeds()
	if first != second {
		t.Fatalf("same --seed selected different pairs: %#v vs %#v", first, second)
	}
	if first.Simulation != 0x1234 || first.CRT != 0x1234 {
		t.Fatalf("pair = %#v, want both streams set to 0x1234", first)
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

// TestShotFlagDefaults locks the --shot surface: the flag is empty by default,
// so an ordinary run is unaffected, and --shot-ticks carries a positive default
// so a capture without one still advances past the entry tail rather than
// photographing tick zero [01 §4.1].
func TestShotFlagDefaults(t *testing.T) {
	var out bytes.Buffer
	opts, err := parseFlags(nil, &out)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.Shot != "" {
		t.Errorf("default Shot = %q, want empty", opts.Shot)
	}
	if opts.ShotTicks <= 0 {
		t.Errorf("default ShotTicks = %d, want positive", opts.ShotTicks)
	}
	opts, err = parseFlags([]string{"-shot", "a.png", "-shot-ticks", "5"}, &out)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.Shot != "a.png" || opts.ShotTicks != 5 {
		t.Errorf("parsed Shot=%q ShotTicks=%d, want a.png/5", opts.Shot, opts.ShotTicks)
	}
}
