package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliTestRoot skips when the retail install is absent.
func cliTestRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory")
		}
		root = filepath.Join(home, "TotalAnnihilation")
	}
	if _, err := os.Stat(filepath.Join(root, "gamedata")); err != nil {
		t.Skip("retail assets not present")
	}
	return root
}

// captureRun executes a run writing into a temp file and returns its output.
func captureRun(t *testing.T, opts Options) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := run(opts, f); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestHeadlessSkirmishDeterministic locks PLAN_00 C4's dual-stream seed
// contract through the real session: same --seed ⇒ byte-identical summary
// including final stream states and draw counts.
func TestHeadlessSkirmishDeterministic(t *testing.T) {
	root := cliTestRoot(t)
	run := func() string {
		opts := Options{Root: root, Seed: -1}
		opts.Map = "ashap plateau"
		opts.Headless = true
		opts.Ticks = 15
		opts.Seed = 4242
		return captureRun(t, opts)
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("seeded headless runs diverge:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	if !strings.Contains(a, "skirmish unit") || !strings.Contains(a, "rng sim:") || !strings.Contains(a, "rng crt:") {
		t.Fatalf("summary missing skirmish/rng lines:\n%s", a)
	}
}

// TestSaveLoadContinueExact locks PLAN_14 C18's StateV1 promise at the CLI:
// N+M uninterrupted ticks == save-at-N then load-and-run-M, byte for byte.
func TestSaveLoadContinueExact(t *testing.T) {
	root := cliTestRoot(t)
	savePath := filepath.Join(t.TempDir(), "mid.sav")

	run := func(load, saveTo string, ticks int) string {
		opts := Options{Root: root, Seed: -1}
		opts.Map = "ashap plateau"
		opts.Headless = true
		opts.Seed = 777
		opts.Load = load
		opts.Save = saveTo
		opts.Ticks = ticks
		return captureRun(t, opts)
	}

	full := run("", "", 30)
	_ = run("", savePath, 20) // writes savePath at tick 20
	cont := run(savePath, "", 10)

	if full != cont {
		t.Fatalf("save→load→continue diverges from uninterrupted:\n--- full ---\n%s\n--- continued ---\n%s", full, cont)
	}
}
