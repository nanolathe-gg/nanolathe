//go:build retail

package headless

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// A Survival battle with an idle commander (docs/DESIGN_SURVIVAL.md §12):
// waves arrive and the battle ends only in defeat, the attacker never takes a
// result row, equal seeds replay the same wave plans, and Strict 3.1 runs it
// too.
func TestSurvivalBattleEndsInDefeatAndReplays(t *testing.T) {
	if testing.Short() {
		t.Skip("long Survival trajectory: run tools/check-retail --full")
	}
	catalog, fs := retailcat.Shared(t)
	run := func(mode gameplay.Mode) Report {
		t.Helper()
		report, err := RunWithContent(Request{
			Gameplay: mode, Map: "ashap plateau", Difficulty: 1,
			SimulationSeed: 7, CRTSeed: 7, TickLimit: 60000,
			Survival: session.SurvivalOptions{Enabled: true, Pace: 2},
		}, fs, catalog)
		if err != nil && !errors.Is(err, ErrTickLimit) {
			t.Fatalf("%s survival: %v", mode, err)
		}
		return report
	}
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		a := run(mode)
		if a.ScenarioKind != ScenarioSurvival || a.Survival == nil {
			t.Fatalf("%s: scenario %q survival %v", mode, a.ScenarioKind, a.Survival)
		}
		if a.Result == "won" {
			t.Fatalf("%s: a Survival battle was won", mode)
		}
		if len(a.Survival.Waves) < 2 || a.Players[1].UnitsCreated == 0 {
			t.Fatalf("%s: %d waves planned, attacker created %d units", mode, len(a.Survival.Waves), a.Players[1].UnitsCreated)
		}
		b := run(mode)
		if !reflect.DeepEqual(a.Survival.Waves, b.Survival.Waves) || a.StateHash != b.StateHash {
			t.Fatalf("%s: equal seeds did not replay", mode)
		}
		t.Logf("%s: tick %d result %q waves %d outcome %+v", mode, a.Tick, a.Result, len(a.Survival.Waves), *a.Survival.Outcome)
	}
}

// Extra deposits are the map's own deposit feature, and the load-time deposit
// pass seeds their metal like any authored patch (DESIGN_SURVIVAL §4.5).
func TestSurvivalDepositsCarryMetal(t *testing.T) {
	catalog, fs := retailcat.Shared(t)
	battle, err := ComposeFreshBattle(FreshBattleRequest{
		Kind: ScenarioSurvival, Map: "painted desert", LocalOwner: -1,
		Skirmish:       session.SurvivalSkirmishConfig("painted desert", 2, session.SurvivalOptions{}),
		SimulationSeed: 3, CRTSeed: 3, FS: fs, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	cells, metal := battle.Session.SurvivalDeposits()
	if len(cells) == 0 {
		t.Fatalf("no extra deposits on a map whose starts have them")
	}
	for i, m := range metal {
		if m == 0 {
			t.Fatalf("extra deposit at %v carries no metal", cells[i])
		}
	}
}
