package main

import (
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
// first run: no preferences file, so the established missing-value default
// reaches battle entry [08 R-SKIR-01 §6].
func TestUnitLimitDefaultsWithoutAStoredBlock(t *testing.T) {
	req, err := directMapBattleRequest(Options{Map: "Anteer Straight"}, &contentSet{fs: vfs.New()}, nil)
	if err != nil {
		t.Fatalf("directMapBattleRequest: %v", err)
	}
	if req.value.Skirmish.UnitLimit != session.SkirmishDefaultUnitLimit {
		t.Fatalf("direct request UnitLimit = %d, want %d", req.value.Skirmish.UnitLimit, session.SkirmishDefaultUnitLimit)
	}
}
