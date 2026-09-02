package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestParseBuildsExplicitSeedPair(t *testing.T) {
	request, _, _, err := parse([]string{"-root", "/tmp/assets", "-map", "test", "-seed", "23", "-ticks", "7"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if request.SimulationSeed != 23 || request.CRTSeed != 23 || request.TickLimit != 7 {
		t.Fatalf("request = %+v", request)
	}
}

// TestHeadlessSkirmishTakesTheDefaultUnitLimit checks the one thing this
// command can get wrong about the unit pool: it composes a skirmish with no
// persisted preferences at all, so the pool must be sized from the established
// missing-value limit — `limit × 10 + 1` records, `limit` per slot
// [05 R-SHARE-01 §7][08 R-SKIR-01 §6] — and not from the catalog's definition
// count. Skipped when the retail install is absent.
func TestHeadlessSkirmishTakesTheDefaultUnitLimit(t *testing.T) {
	root := defaultRoot()
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail install: %v", err)
	}
	defer fs.Close()

	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioDirectOTA, Map: "ashap plateau",
		LocalOwner: -1, SimulationSeed: 7, CRTSeed: 7, FS: fs,
	})
	if err != nil {
		t.Fatalf("ComposeFreshBattle: %v", err)
	}
	limit := session.SkirmishDefaultUnitLimit
	if got := battle.Session.Units.TotalRecords(); got != limit*10+1 {
		t.Fatalf("TotalRecords = %d, want %d", got, limit*10+1)
	}
	if defs := len(battle.Session.Catalog.Units); defs == limit {
		t.Fatalf("catalog definition count %d equals the limit, so this test cannot tell the two apart", defs)
	}
	start, end, ok := battle.Session.Units.SliceForPlayer(1)
	if !ok || start != limit+1 || end != 2*limit {
		t.Fatalf("player 1 slice = %d..%d (ok=%v), want %d..%d", start, end, ok, limit+1, 2*limit)
	}
}
