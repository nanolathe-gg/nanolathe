//go:build retail

package headless

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// TestSimBenchSceneComposesThreeFullArmies is the scene's contract against the
// real corpus: three computer armies of exactly the declared size, each with
// buildings, mobiles, queued factory production and scripted orders, and two
// composes of the same seed producing the same fingerprint. It runs a short
// window rather than the benchmark's own, because what it locks is the scene
// and its determinism, not a timing.
func TestSimBenchSceneComposesThreeFullArmies(t *testing.T) {
	catalog, fs := retailcat.Shared(t)
	opts := SimBenchOptions{Map: SimBenchDefaultMap, Seed: 7, Difficulty: 1}
	opts.applyDefaults()

	first, scene, err := ComposeSimBenchBattle(opts, fs, catalog)
	if err != nil {
		t.Fatalf("compose = %v", err)
	}
	if scene.Version != SimBenchSceneVersion || len(scene.Teams) != simBenchTeams {
		t.Fatalf("scene version %d with %d teams, want version %d with %d", scene.Version, len(scene.Teams), SimBenchSceneVersion, simBenchTeams)
	}
	wantBuildings := rosterTotal(simBenchBuildings)
	wantMobiles := rosterTotal(simBenchMobiles)
	for i, team := range scene.Teams {
		if team.Player != simBenchFirstAISlot+i {
			t.Fatalf("team %d is player %d, want %d", i, team.Player, simBenchFirstAISlot+i)
		}
		if team.Buildings != wantBuildings || team.Mobiles != wantMobiles {
			t.Fatalf("team %d placed %d buildings and %d mobiles, want %d and %d", i, team.Buildings, team.Mobiles, wantBuildings, wantMobiles)
		}
		if team.Factories == 0 || team.ScriptedMoves == 0 || team.ScriptedBuilds == 0 {
			t.Fatalf("team %d has no ongoing work: %+v", i, team)
		}
	}

	sess := first.Session
	// Every measured army must be a live computer slot with a bound manager,
	// or the scene is not the workload it claims [08 "Dispatch gates and order
	// sinks"].
	for i := range scene.Teams {
		slot := simBenchFirstAISlot + i
		if sess.AI[slot] == nil {
			t.Fatalf("player %d has no AI manager", slot)
		}
		// The commander battle entry stamps for each slot is on top of the
		// placed army.
		if live := sess.Units.LiveCountForPlayer(slot); live != simBenchUnitsPerTeam+1 {
			t.Fatalf("player %d has %d live units, want %d placed plus one commander", slot, live, simBenchUnitsPerTeam)
		}
	}

	for tick := 0; tick < 60; tick++ {
		simBenchStep(sess)
	}
	census := simBenchTakeCensus(sess, scene, "test", nil)
	if len(census.Teams) != simBenchTeams {
		t.Fatalf("census has %d team rows, want %d", len(census.Teams), simBenchTeams)
	}
	for _, row := range census.Teams {
		if row.LiveUnits < simBenchUnitsPerTeam {
			t.Fatalf("player %d census %+v", row.Player, row)
		}
		if row.FactoryQueued == 0 {
			t.Fatalf("player %d has no queued factory production: %+v", row.Player, row)
		}
	}

	second, _, err := ComposeSimBenchBattle(opts, fs, catalog)
	if err != nil {
		t.Fatalf("recompose = %v", err)
	}
	if first.InitialFingerprint != second.InitialFingerprint {
		t.Fatalf("two composes of seed %d disagree: %s vs %s", opts.Seed, first.InitialFingerprint, second.InitialFingerprint)
	}
}
