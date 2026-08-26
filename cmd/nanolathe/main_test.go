package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
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

// TestDispatchOrdering_ShotPrecedesHeadlessMap locks P5: --headless --map
// must not shadow --shot/--shot-menu .
func TestDispatchOrdering_ShotPrecedesHeadlessMap(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "headless map alone -> headless",
			opts: Options{Headless: true, Map: "Ashap Plateau"},
			want: "headless",
		},
		{
			name: "headless map + shot -> shot",
			opts: Options{Headless: true, Map: "Ashap Plateau", Shot: "/tmp/out.png"},
			want: "shot",
		},
		{
			name: "headless map + shot-menu -> shot",
			opts: Options{Headless: true, Map: "Ashap Plateau", Shot: "/tmp/out.png", ShotMenu: "main"},
			want: "shot",
		},
		{
			name: "headless mission alone -> headless",
			opts: Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0"},
			want: "headless",
		},
		{
			name: "headless mission + shot -> shot",
			opts: Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0", Shot: "/tmp/out.png"},
			want: "shot",
		},
		{
			name: "headless map+mission + shot -> shot",
			opts: Options{Headless: true, Map: "Ashap Plateau", Mission: "camps/arm campaign.tdf:MISSION0", Shot: "/tmp/out.png"},
			want: "shot",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dispatchKind(tc.opts); got != tc.want {
				t.Fatalf("dispatchKind(%+v) = %q, want %q", tc.opts, got, tc.want)
			}
		})
	}
	// Direct helper mirrors.
	if !shouldRunShot(Options{Shot: "/tmp/a.png"}) {
		t.Fatal("shouldRunShot false for Shot set")
	}
	if shouldRunHeadlessSession(Options{Headless: true, Map: "Ashap Plateau", Shot: "/tmp/a.png"}) {
		t.Fatal("shouldRunHeadlessSession must be false when Shot set [P5]")
	}
}

// TestDispatchOrdering_MissionHeadless locks P6: --headless --mission must
// dispatch to session construction like --headless --map .
func TestDispatchOrdering_MissionHeadless(t *testing.T) {
	if !shouldRunHeadlessSession(Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0"}) {
		t.Fatalf("shouldRunHeadlessSession false for --headless --mission, want true [P6]")
	}
	if shouldRunHeadlessSession(Options{Headless: true}) {
		t.Fatalf("shouldRunHeadlessSession true for headless alone, want false (falls through to report)")
	}
	if shouldRunHeadlessSession(Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0", Dump: "manifest"}) {
		t.Fatalf("shouldRunHeadlessSession true when Dump set, want false")
	}
	if shouldRunHeadlessSession(Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0", Load: "/tmp/sav"}) {
		t.Fatalf("shouldRunHeadlessSession true when Load set, want false (load wins)")
	}
	if got := dispatchKind(Options{Headless: true, Mission: "camps/arm campaign.tdf:MISSION0"}); got != "headless" {
		t.Fatalf("dispatchKind mission = %q, want headless", got)
	}
	if got := dispatchKind(Options{Headless: true, Map: "Ashap Plateau"}); got != "headless" {
		t.Fatalf("dispatchKind map = %q, want headless", got)
	}
	if got := dispatchKind(Options{Headless: true}); got == "headless" {
		t.Fatalf("dispatchKind headless alone = headless, want report")
	}
}

// TestHeadlessMapShotWritesPNG is the P5 integration check: go run
// --headless --map --shot writes a PNG (skip if no retail).
func TestHeadlessMapShotWritesPNG(t *testing.T) {
	root := mainTestRetailRoot(t)
	tmp := filepath.Join(t.TempDir(), "p5.png")
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	opts := Options{
		Root:     root,
		Headless: true,
		Map:      "Ashap Plateau",
		Shot:     tmp,
		Frames:   30,
		Seed:     1,
	}
	if err := run(opts, out); err != nil {
		t.Fatalf("run headless map+shot: %v", err)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("shot PNG not written: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("shot PNG is empty")
	}
	f, err := os.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("shot PNG decode: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Fatalf("shot PNG has zero bounds %v", img.Bounds())
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(data)
	if !strings.Contains(outStr, "seed:") {
		t.Fatalf("shot run output missing seed header: %q", outStr)
	}
	if !strings.Contains(outStr, "shot") {
		t.Fatalf("shot run output missing shot line: %q", outStr)
	}
}

// TestHeadlessMissionRunsSession is the P6 integration check: --headless
// --mission must construct a session and print summary, not just manifest.
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
	// Must have run the session (units + rng), not just the manifest report.
	if !strings.Contains(s, "rng sim:") && !strings.Contains(s, "skirmish unit") {
		t.Fatalf("mission run did not produce session summary (expected rng/ unit lines), got: %q", s)
	}
	// Ensure it did NOT just fall through to report's provider list as the sole output.
	// A real session run may still contain notes, but should not be *only* manifest.
	if strings.Contains(s, "providers:") && !strings.Contains(s, "rng") {
		t.Fatalf("mission run appears to be manifest-only, not session: %q", s)
	}
}
