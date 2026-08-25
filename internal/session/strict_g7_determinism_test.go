package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestStrictSkirmish_TwoRunsMatchTraceAndStateHash implements G7 [ON-10 §11 G7].
func TestStrictSkirmish_TwoRunsMatchTraceAndStateHash(t *testing.T) {
	const simSeed, crtSeed uint32 = 123, 456
	run := func() (string, string, map[string]uint32, uint32, int, []string) {
		rng.SeedGlobal(simSeed, crtSeed)
		cat := strictMinimalCatalog()
		terrain := strictMinimalTerrain()
		m := strictSyntheticMission()
		s := &Session{Catalog: cat, World: terrain, Mission: m, Skirmish: SkirmishConfig{NumPlayers: 2, CommanderDeath: 1}}
		s.Skirmish.Players[0].AllyGroup = 1
		s.Skirmish.Players[1].AllyGroup = 2
		w, _ := newSlicedWorld(cat)
		s.Units = w
		s.Econ = strictEconomyForTest()
		for i := 0; i < 2; i++ {
			s.Econ.Players[i].Exists = true
			s.Econ.Players[i].ControllerState = uint8(i + 1)
			s.Econ.Players[i].StatusHalfwordAt144 = 1
		}
		s.Econ.SeedDeadlines(0)
		var crt rng.CRT = rng.NewCRT(crtSeed)
		s.InitWindForSession(&crt, 0)
		_ = createAndBindServices(s)
		s.RegisterAll()
		s.State = StateBattle
		// Add two units
		def := cat.Units["armcom"]
		def.Commander = true
		for i := 0; i < 2; i++ {
			h, _ := s.Units.Create(def, uint8(i), strictCellToWorld(int32(10+i*10)), 0, strictCellToWorld(10))
			publishOne(s, s.Units.Unit(h))
			s.Movement.EnsureUnit(s.Units.Unit(h))
		}
		s.SetTraceEnabled(true)
		s.ClearTrace()
		s.Clock.ScaledAnchor = 0
		for tick := 1; tick <= 50; tick++ {
			s.Step(int32(tick))
		}
		traceHash := HashTrace(s.TraceEvents())
		stateHash := HashState(s)
		milestones := map[string]uint32{"final_tick": s.Clock.GlobalTick}
		winner := s.GetResult().WinnerTeam
		finalTick := s.Clock.GlobalTick
		poolCounts := []string{stateHash}
		return traceHash, stateHash, milestones, finalTick, winner, poolCounts
	}
	trace1, state1, miles1, finalTick1, winner1, _ := run()
	trace2, state2, miles2, finalTick2, winner2, _ := run()
	if trace1 != trace2 {
		t.Fatalf("G7 determinism: trace hash mismatch %s vs %s", trace1, trace2)
	}
	if state1 != state2 {
		t.Fatalf("G7 determinism: state hash mismatch %s vs %s", state1, state2)
	}
	if finalTick1 != finalTick2 {
		t.Fatalf("G7 determinism: final tick mismatch %d vs %d", finalTick1, finalTick2)
	}
	if winner1 != winner2 {
		t.Fatalf("G7 determinism: winner mismatch %d vs %d", winner1, winner2)
	}
	if miles1["final_tick"] != miles2["final_tick"] {
		t.Fatalf("G7 determinism: milestone tick mismatch")
	}
	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(strictMinimalCatalog()), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer"}},
		MaxTick: 50, Milestones: miles1, Winner: winner1, Reason: "G7 determinism",
		FinalTick: finalTick1, FinalStateHash: state1, TraceHash: trace1,
	}
	t.Logf("G7 evidence: %s", FormatEvidence(ev))
}
