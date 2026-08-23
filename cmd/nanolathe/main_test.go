package main

import (
	"bytes"
	"strings"
	"testing"
)

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

// TestSeedOverride: a fixed --seed must be reproducible, because RNG state is
// not saved and is reseeded on load [08 "Scheduler and random state in saves"].
func TestSeedOverride(t *testing.T) {
	if got := seedFor(Options{Seed: 7}); got != 7 {
		t.Fatalf("seedFor(7) = %d", got)
	}
	if seedFor(Options{Seed: -1}) == 0 {
		t.Fatal("clock-derived seed produced zero")
	}
}

// TestMissingRootDiagnostic locks the standard diagnostic shape from
// docs/ORCHESTRATION.md §7.
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
