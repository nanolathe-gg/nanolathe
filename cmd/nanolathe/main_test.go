package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
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

func TestWantsViewer(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want bool
	}{
		{name: "map", opts: Options{Map: "ashap plateau"}, want: true},
		{name: "no map", opts: Options{}, want: false},
		{name: "headless", opts: Options{Map: "ashap plateau", Headless: true}, want: false},
		{name: "dump", opts: Options{Map: "ashap plateau", Dump: "heightAt=1,1"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := wantsViewer(test.opts); got != test.want {
				t.Fatalf("wantsViewer(%+v) = %v, want %v", test.opts, got, test.want)
			}
		})
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

// TestDumpRNGIsReproducible is the falsifiable form of PLAN_03's exit gate:
// two runs at the same seed must agree on both stream states and both draw
// counts. It exercises seeding, the clock budget, the kernel phase order and
// the wind's two-stream call order end to end (R3).
func TestDumpRNGIsReproducible(t *testing.T) {
	run := func(seed int64) string {
		sim, crt := seedsFor(Options{Seed: seed})
		rng.SeedGlobal(sim, crt)
		var out strings.Builder
		if err := dumpRNG(Options{Seed: seed, Ticks: 600}, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first, second := run(7), run(7)
	if first != second {
		t.Fatalf("same seed diverged:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, "draws=") {
		t.Fatalf("dump does not report draw counts:\n%s", first)
	}
	// A wind change must actually have happened, or the gate proves nothing:
	// the deadline is 150..420 ticks out [01 §7.3] and we run 600.
	if strings.Contains(first, "changes=0") {
		t.Fatalf("no wind change in 600 ticks, so no simulation draws were exercised:\n%s", first)
	}
	if other := run(8); other == first {
		t.Fatal("different seeds produced an identical dump")
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
