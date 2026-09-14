package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestUnitLimitReachesTheBattleRequest follows the configured per-player unit
// limit from the persisted block to the composed battle request. Nothing in
// the frontend edits it — retail's skirmish lobby has no gadget for it
// [08 R-SKIR-01 §6] — so the only thing that can go wrong is the value being
// dropped, which would silently resize the unit pool [05 R-SHARE-01 §7].
func TestUnitLimitReachesTheBattleRequest(t *testing.T) {
	maps := []string{"Anteer Straight"}
	stored := settings.Defaults()
	stored.UnitLimit = 320

	shell := &gameShell{maps: maps}
	shell.setup = newSkirmishMenuConfig(maps[0])
	shell.applySettings(stored)
	if shell.setup.UnitLimit != 320 {
		t.Fatalf("shell setup UnitLimit = %d, want 320", shell.setup.UnitLimit)
	}

	// The start config is a copy of the setup record, so the limit rides
	// along with the rest of the setup.
	cfg := shell.skirmishConfigForStart(maps[0])
	if cfg.UnitLimit != 320 {
		t.Fatalf("start config UnitLimit = %d, want 320", cfg.UnitLimit)
	}

	// And the whole block is written back with the value intact rather than
	// re-defaulted on every save.
	if got := shell.captureSettings().UnitLimit; got != 320 {
		t.Fatalf("captureSettings UnitLimit = %d, want 320", got)
	}

	req, err := skirmishBattleRequest(Options{Map: maps[0]}, &contentSet{fs: vfs.New()}, cfg, headlessScenarioSkirmish, nil, nil)
	if err != nil {
		t.Fatalf("skirmishBattleRequest: %v", err)
	}
	if req.value.Skirmish.UnitLimit != 320 {
		t.Fatalf("battle request UnitLimit = %d, want 320", req.value.Skirmish.UnitLimit)
	}
}

// TestUnitLimitDefaultsWithoutAStoredBlock is the direct `-map` path and a
// first run: no preferences file, so Nanolathe's default reaches battle entry.
func TestUnitLimitDefaultsWithoutAStoredBlock(t *testing.T) {
	req, err := directMapBattleRequest(Options{Map: "Anteer Straight"}, &contentSet{fs: vfs.New()}, nil)
	if err != nil {
		t.Fatalf("directMapBattleRequest: %v", err)
	}
	if req.value.Skirmish.UnitLimit != session.SkirmishDefaultUnitLimit {
		t.Fatalf("direct request UnitLimit = %d, want %d", req.value.Skirmish.UnitLimit, session.SkirmishDefaultUnitLimit)
	}
}

// CLI precedence must survive settings load and both menu/direct composition.
func TestUnitLimitCLIAndConfigPrecedence(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	stored := settings.Defaults()
	stored.UnitLimit = 1500
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"--unit-limit", "2000"}} {
		opts, err := parseFlags(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		opts.Map = "Anteer Straight"
		want := 1500
		if len(args) > 0 {
			want = 2000
		}
		shell := &gameShell{opts: opts, maps: []string{opts.Map}}
		shell.setup = newSkirmishMenuConfig(opts.Map)
		shell.applySettings(stored)
		if shell.setup.UnitLimit != want || shell.captureSettings().UnitLimit != want {
			t.Fatalf("shell/captured limit = %d/%d, want %d", shell.setup.UnitLimit, shell.captureSettings().UnitLimit, want)
		}
		req, err := directMapBattleRequest(opts, &contentSet{fs: vfs.New()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.value.Skirmish.UnitLimit != want {
			t.Fatalf("direct limit = %d, want %d", req.value.Skirmish.UnitLimit, want)
		}
		req, err = skirmishBattleRequest(opts, &contentSet{fs: vfs.New()}, shell.setup, headlessScenarioSkirmish, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.value.Skirmish.UnitLimit != want {
			t.Fatalf("menu limit = %d, want %d", req.value.Skirmish.UnitLimit, want)
		}
	}
}

func TestUnitLimitCLIRejectsOutOfRange(t *testing.T) {
	for _, value := range []string{"-1", "0", "19", "3277", "1000000000"} {
		var output bytes.Buffer
		_, status, run := mainOptions([]string{"--unit-limit", value}, &output)
		if run || status == 0 || !strings.Contains(output.String(), "expected 20..3276") {
			t.Fatalf("invalid limit %s: run=%v status=%d diagnostic=%q", value, run, status, output.String())
		}
	}
	for _, value := range []string{"20", "1000", "3276"} {
		if _, err := parseFlags([]string{"--unit-limit", value}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if settings.DefaultUnitLimit != 1000 || session.SkirmishDefaultUnitLimit != settings.DefaultUnitLimit {
		t.Fatal("unit-limit defaults disagree")
	}
}
