package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// probeRetail skips integration tests when the retail assets are not opted
// in. It is the shared helper several test files in this package call;
// routing it through testsupport.RetailRoot here fixes the opt-in for every
// caller without each of them resolving ~/TotalAnnihilation on its own.
func probeRetail(t *testing.T) string {
	t.Helper()
	return testsupport.RetailRoot(t)
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
	if opts.LoadSave != "" {
		t.Fatalf("default LoadSave = %q, want empty", opts.LoadSave)
	}
}

func TestParseFlagsLoadSave(t *testing.T) {
	var out bytes.Buffer
	opts, err := parseFlags([]string{"--load-save", "/tmp/slot.sav"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if opts.LoadSave != "/tmp/slot.sav" {
		t.Fatalf("LoadSave = %q", opts.LoadSave)
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
	// --shot-select is off by default, so an ordinary capture composes the
	// empty-selection battle screen the composer would show a player who has
	// clicked nothing [07 §6][07 R-HUD-03 §1].
	if opts.ShotSelect {
		t.Error("default ShotSelect = true, want false")
	}
	opts, err = parseFlags([]string{"-shot", "a.png", "-shot-select"}, &out)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.ShotSelect {
		t.Error("-shot-select did not set ShotSelect")
	}
}
