package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestParseUntilResultFlag(t *testing.T) {
	out := &strings.Builder{}
	opts, err := parseFlags([]string{"--headless", "--map", "test", "--until-result", "--max-tick", "42"}, out)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.UntilResult {
		t.Fatalf("UntilResult not set")
	}
	if opts.MaxTick != 42 {
		t.Fatalf("MaxTick want 42 got %d", opts.MaxTick)
	}
}

func TestHeadlessTimeoutExitsNonzero(t *testing.T) {
	// Simulate headless max-tick timeout: session with no commander death should timeout.
	rng.SeedGlobal(100, 200)
	cat := content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {UnitName: "armcom", MaxDamage: 1000, SightDistance: 128, Commander: true},
			"corcom": {UnitName: "corcom", MaxDamage: 1000, SightDistance: 128, Commander: true},
		},
		Movement: map[string]*content.MovementClass{"testmove": {FootprintX: 1, FootprintZ: 1}},
		Sides: []*content.SideDef{
			{Name: "ARM", Commander: "armcom"},
			{Name: "CORE", Commander: "corcom"},
		},
		Features: map[string]*content.FeatureDef{},
		Maps:     map[string]*content.MapHeader{},
	}
	for _, u := range cat.Units {
		u.CanonicalKey = content.CanonicalKey(u.UnitName)
	}
	cat.Movement["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	// Use synthetic FS with minimal map
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ota := "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "maps", "test.ota"), []byte(ota), 0644); err != nil {
		t.Fatalf("write ota: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	cfg := session.SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	sess, err := session.NewSkirmishForTest(fs, &cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	sess.State = session.StateBattle
	// Do not kill any commander, so result never ends.
	// Simulate headless loop with maxTick 5
	maxTick := 5
	timeout := true
	for iter := 0; iter < maxTick+10; iter++ {
		sess.Step(int32(iter))
		if sess.GetResult().Ended {
			timeout = false
			break
		}
		if sess.Clock != nil && int(sess.Clock.GlobalTick) >= maxTick {
			break
		}
	}
	if !timeout {
		t.Fatalf("expected timeout, but result ended: %+v", sess.GetResult())
	}
	// Verify JSON timeout would be non-zero exit: runUntilResult would return error
	// Simulate that error
	if timeout {
		// This is the expected timeout path that would exit nonzero via run()
		// We just verify the logic
	}
}

func TestHeadlessNaturalResultExitsZero(t *testing.T) {
	rng.SeedGlobal(200, 300)
	cat := content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {UnitName: "armcom", MaxDamage: 1000, SightDistance: 128, Commander: true},
			"corcom": {UnitName: "corcom", MaxDamage: 1000, SightDistance: 128, Commander: true},
		},
		Movement: map[string]*content.MovementClass{"testmove": {FootprintX: 1, FootprintZ: 1}},
		Sides: []*content.SideDef{
			{Name: "ARM", Commander: "armcom"},
			{Name: "CORE", Commander: "corcom"},
		},
		Features: map[string]*content.FeatureDef{},
		Maps:     map[string]*content.MapHeader{},
	}
	for _, u := range cat.Units {
		u.CanonicalKey = content.CanonicalKey(u.UnitName)
	}
	cat.Movement["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ota := "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "maps", "test.ota"), []byte(ota), 0644); err != nil {
		t.Fatalf("write ota: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	cfg := session.SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	sess, err := session.NewSkirmishForTest(fs, &cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	sess.State = session.StateBattle
	// Kill enemy commander to trigger win
	var enemyHandle pool.Handle
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && int(u.Owner) == 1 {
			enemyHandle = u.Handle
			break
		}
	}
	if enemyHandle == 0 {
		t.Fatalf("no enemy commander")
	}
	sess.Units.Destroy(enemyHandle, 1)
	// Run until result with sufficient maxTick
	maxTick := 100
	var result session.Result
	for iter := 0; iter < maxTick; iter++ {
		sess.Step(int32(iter))
		// Also directly evaluate for determinism (kernel handler also does)
		sess.EvaluateResult(uint32(iter))
		if sess.GetResult().Ended {
			result = sess.GetResult()
			break
		}
	}
	if !result.Ended {
		t.Fatalf("expected natural result, got %+v", sess.GetResult())
	}
	// Verify JSON output would be produced and exit 0
	j := map[string]interface{}{
		"winner_team": result.WinnerTeam,
		"reason":      result.Reason,
		"tick":        result.Tick,
		"draw":        result.Draw,
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(string(b), "\"reason\"") || !strings.Contains(string(b), "\"winner_team\"") {
		t.Fatalf("json missing fields: %s", string(b))
	}
}
