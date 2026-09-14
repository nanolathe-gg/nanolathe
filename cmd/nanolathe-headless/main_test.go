package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestParseBuildsExplicitSeedPair(t *testing.T) {
	request, _, _, _, err := parse([]string{"-root", "/tmp/assets", "-map", "test", "-seed", "23", "-ticks", "7"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if request.SimulationSeed != 23 || request.CRTSeed != 23 || request.TickLimit != 7 {
		t.Fatalf("request = %+v", request)
	}
}

// TestParseRejectsDifficultyOutsideTheVocabulary locks the flag's range check:
// the battle difficulty word is 0 easy, 1 medium, 2 hard, and nothing else
// [08 R-AI-01 §12][05 R-ECO-01 §3], so a value outside that range must fail
// parse with a nanolathe-shaped diagnostic rather than reach the session.
func TestParseRejectsDifficultyOutsideTheVocabulary(t *testing.T) {
	for _, difficulty := range []string{"-1", "3"} {
		if _, _, _, _, err := parse([]string{"-map", "test", "-difficulty", difficulty}, &bytes.Buffer{}); err == nil {
			t.Fatalf("parse with -difficulty %s unexpectedly succeeded", difficulty)
		} else if !strings.HasPrefix(err.Error(), "nanolathe: ") {
			t.Fatalf("parse with -difficulty %s error = %q, want a nanolathe-shaped diagnostic", difficulty, err)
		}
	}
	if _, _, _, _, err := parse([]string{"-map", "test", "-difficulty", "2"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("parse with -difficulty 2 = %v, want no error", err)
	}
}

// TestHeadlessSkirmishTakesTheDefaultUnitLimit checks the one thing this
// command can get wrong about the unit pool: it composes a skirmish with no
// persisted preferences at all, so the pool must be sized from the established
// missing-value limit — `limit × 10 + 1` records, `limit` per slot
// [05 R-SHARE-01 §7][08 R-SKIR-01 §6] — and not from the catalog's definition
// count. Skipped when the retail install is absent.
func TestHeadlessSkirmishTakesTheDefaultUnitLimit(t *testing.T) {
	root := testsupport.RetailRoot(t)
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

func TestRepeatedRootFlags(t *testing.T) {
	t.Setenv("NANOLATHE_TA_ROOT", "/ignored")
	request, _, _, _, err := parse([]string{"--root", "base", "--root=mod"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if request.Root != "base" || len(request.Roots) != 2 || request.Roots[0] != "base" || request.Roots[1] != "mod" {
		t.Fatalf("request roots = %q / %v", request.Root, request.Roots)
	}
	if _, _, _, _, err := parse([]string{"--root="}, &bytes.Buffer{}); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestParseUnitLimit(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	stored := settings.Defaults()
	stored.UnitLimit = 1500
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args        []string
		want, bench int
	}{
		{nil, 1500, headless.SimBenchDefaultUnitLimit},
		{[]string{"--unit-limit", "2000"}, 2000, 2000},
		{[]string{"--sim-benchmark", "/tmp/unused-benchmark"}, 0, headless.SimBenchDefaultUnitLimit},
	} {
		req, _, _, bench, err := parse(tc.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if req.UnitLimit != tc.want || bench.UnitLimit != tc.bench {
			t.Fatalf("limits = %d/%d, want %d/%d", req.UnitLimit, bench.UnitLimit, tc.want, tc.bench)
		}
	}
	for _, value := range []string{"-1", "0", "19", "3277"} {
		if _, _, _, _, err := parse([]string{"--unit-limit", value}, io.Discard); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
