//go:build retail

package headless

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	if scene.PassiveHuman != nil {
		t.Fatal("default scene relocated its passive human commander")
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

// 334 placed units in each of three armies, plus all four commanders, produce
// the optional thousand-unit workload without changing the default scene.
func TestSimBenchLargerArmyScene(t *testing.T) {
	catalog, fs := retailcat.Shared(t)
	opts := SimBenchOptions{Seed: 7, Difficulty: 1, ArmySize: 334, Gameplay: gameplay.Strict31}
	composed, scene, err := ComposeSimBenchBattle(opts, fs, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if scene.ArmySize != opts.ArmySize || rosterTotal(scene.MobileRoster) != opts.ArmySize-50 {
		t.Fatalf("larger scene provenance does not describe the requested roster: %+v", scene)
	}
	total := composed.Session.Units.LiveCountForPlayer(simBenchHumanSlot)
	for _, team := range scene.Teams {
		if team.Buildings != 50 || team.Mobiles != 284 {
			t.Fatalf("larger scene changed its building/mobile split: %+v", team)
		}
		total += composed.Session.Units.LiveCountForPlayer(team.Player)
	}
	if total != 1006 {
		t.Fatalf("larger scene starts with %d units, want 1006 including commanders", total)
	}
	human := composed.Session.Units.FirstLive(func(u *units.Unit) bool {
		return u.Owner == simBenchHumanSlot && u.Def != nil && u.Def.Commander
	})
	if human == nil || scene.PassiveHuman == nil || int32(human.X>>16) != scene.PassiveHuman.X || int32(human.Z>>16) != scene.PassiveHuman.Z {
		t.Fatalf("remote passive commander disagrees with scene provenance: %+v", scene.PassiveHuman)
	}
	t.Logf("passive human commander relocated to (%d, %d)", scene.PassiveHuman.X, scene.PassiveHuman.Z)
	anchor, fx, fz, ok := composed.Session.Movement.CommittedFootprint(human.Handle)
	if !ok || !composed.Session.Movement.Grid.RectOnMap(anchor, fx, fz) {
		t.Fatal("remote commander has no on-map committed footprint")
	}
	profile := composed.Session.Movement.ProfileFor(human.Handle)
	for dz := int32(0); dz < int32(fz); dz++ {
		for dx := int32(0); dx < int32(fx); dx++ {
			if !profile.IsPassableCommitCell(composed.Session.World, anchor.X+dx, anchor.Z+dz) {
				t.Fatal("remote commander occupies an invalid terrain footprint")
			}
		}
	}
	for _, other := range composed.Session.Units.Iter() {
		if other.Owner == simBenchHumanSlot {
			continue
		}
		dx, dz := int64(other.X>>16)-int64(human.X>>16), int64(other.Z>>16)-int64(human.Z>>16)
		if dx*dx+dz*dz < simBenchBuildingRadius*simBenchBuildingRadius {
			t.Fatalf("remote commander remains near opposing unit %d", other.Handle)
		}
	}
	before := *scene.PassiveHuman
	simDraws, crtDraws := composed.Session.SimRNG().Draws(), composed.Session.CrtRNG().Draws()
	if err := relocateSimBenchPassiveHuman(composed.Session, scene); err != nil {
		t.Fatal(err)
	}
	if *scene.PassiveHuman != before || composed.Session.SimRNG().Draws() != simDraws || composed.Session.CrtRNG().Draws() != crtDraws {
		t.Fatal("passive commander placement is not deterministic without random draws")
	}
	opts.UnitLimit = opts.ArmySize
	if _, _, err := ComposeSimBenchBattle(opts, fs, catalog); err == nil {
		t.Fatal("army that leaves no unit-limit slot for its commander was accepted")
	}
}
