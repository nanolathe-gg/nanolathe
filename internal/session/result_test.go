package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func commanderHandles(s *Session) map[int][]int {
	// Returns map owner -> list of commander handles (alive non-dying only)
	m := make(map[int][]int)
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Dying || u.Def == nil || !u.Def.Commander {
			continue
		}
		m[int(u.Owner)] = append(m[int(u.Owner)], int(u.Handle))
	}
	return m
}

func killCommander(t *testing.T, s *Session, owner int) {
	t.Helper()
	handles := commanderHandles(s)
	list, ok := handles[owner]
	if !ok || len(list) == 0 {
		t.Fatalf("no commander for owner %d", owner)
	}
	h := list[0]
	// Ordinary damage path: health<=0 → world destroy path via Destroy [04 §2.4]
	// Use Destroy to latch Dying exactly once [08].
	s.Units.Destroy(pool.Handle(h), units.DeathKilled)
}

func init() {
	// Ensure rng seeded for deterministic tests
	rng.SeedGlobal(12345, 6789)
}

func TestResult_TwoPlayerHostileCommanderDeath(t *testing.T) {
	rng.SeedGlobal(1, 1)
	cat := minimalCatalogForStrict()
	// Ensure commander flag
	for _, u := range cat.Units {
		u.Commander = true
		u.MaxDamage = 1000
	}
	cat.Sides[0].Commander = "armcom"
	cat.Sides[1].Commander = "corcom"
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n")
	// Need to provide ai/default.txt to satisfy strict profile load if any computer player exists.
	// Use human-only to avoid AI requirement.
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	// Both human, hostile via per-owner fallback (5 sentinel)
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 0
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.RegisterAll()
	// Ensure state battle for EvaluateResult latching transition
	s.State = StateBattle
	// Find commanders
	if len(commanderHandles(s)) != 2 {
		t.Fatalf("expected 2 commanders, got %v", commanderHandles(s))
	}
	// Kill enemy commander (owner 1)
	handles := commanderHandles(s)
	h := handles[1][0]
	s.Units.Destroy(poolHandle(h), units.DeathKilled)
	if result := s.Units.FinalizeDeath(poolHandle(h), 0); !result.Freed {
		t.Fatal("enemy commander was not finalized")
	}
	// Evaluate until latched [RS-05][08 R-TRIG-01 §6] 4→-1 over ~150 ticks
	// (once per 30)
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		// Try more ticks with Step path as fallback (needs ~150 ticks for latch)
		for i := 0; i < 200; i++ {
			s.Step(int32(i + 100))
			if s.GetResult().Ended {
				latched = true
				break
			}
		}
	}
	res := s.GetResult()
	if !res.Ended {
		t.Fatalf("two-player: expected survivor wins latched, got %+v latch %+v", res, s.Latch)
	}
	if res.Draw {
		t.Fatalf("expected win not draw")
	}
	// Winner should be team of owner 0 (100+0 =100)
	wantWinner := s.teamForOwner(0)
	if res.WinnerTeam != wantWinner {
		t.Fatalf("winner team want %d got %d allies %v", wantWinner, res.WinnerTeam, res)
	}
	// Ensure latched once: further EvaluateResult should not change
	tickBefore := res.Tick
	for i := uint32(0); i < 5; i++ {
		s.EvaluateResult(100 + i)
	}
	res2 := s.GetResult()
	if res2.Tick != tickBefore {
		t.Fatalf("latched once: tick changed %d -> %d", tickBefore, res2.Tick)
	}
	if res2.WinnerTeam != res.WinnerTeam {
		t.Fatalf("latched once: winner changed")
	}
	// ResultView snapshot should expose ended state
	if s.Snapshot != nil {
		cur := s.Snapshot.Current()
		if cur == nil {
			// Publish a frame to ensure snapshot has result
			// Trigger a tick to publish
			s.Step(200)
			cur = s.Snapshot.Current()
		}
		if cur != nil && !cur.Result.Ended {
			t.Fatalf("ResultView should expose ended state via snapshot, got %+v", cur.Result)
		}
	}
}

func TestResult_ThreePlayerFFA(t *testing.T) {
	rng.SeedGlobal(2, 2)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
		u.MaxDamage = 500
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=20;\nZPos=20;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 3}
	cfg.ApplyDefaults()
	for i := 0; i < 3; i++ {
		cfg.Players[i].Controller = 0
	}
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Ensure 3 teams
	if len(commanderHandles(s)) != 3 {
		t.Fatalf("expected 3 commanders")
	}
	// Kill one enemy (owner 1) – should NOT end
	h1 := poolHandle(commanderHandles(s)[1][0])
	s.Units.Destroy(h1, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h1, 0); !result.Freed {
		t.Fatal("first FFA commander was not finalized")
	}
	for tick := uint32(0); tick < 200; tick++ {
		s.EvaluateResult(tick)
	}
	if s.GetResult().Ended {
		t.Fatalf("FFA: killing one of three should not end match, got %+v", s.GetResult())
	}
	// Kill second enemy (owner 2) – now only owner 0 remains, should end with winner 0
	h2 := poolHandle(commanderHandles(s)[2][0])
	s.Units.Destroy(h2, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h2, 0); !result.Freed {
		t.Fatal("second FFA commander was not finalized")
	}
	var latched bool
	for tick := uint32(60); tick < 260; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("FFA second kill should latch win, got %+v latch %+v", s.GetResult(), s.Latch)
	}
	res := s.GetResult()
	if res.WinnerTeam != s.teamForOwner(0) {
		t.Fatalf("FFA winner want team %d got %d", s.teamForOwner(0), res.WinnerTeam)
	}
}

// TestResult_AlliedPairVsEnemy pins the ally skip of the kind-2 victory sweep
// [08 R-TRIG-01 §6] "The kind-2 victory sweep": the walk skips any slot whose
// byte in the LOCAL player's first alliance row is non-zero, so an allied
// peer's survival is not a reason to keep playing. Killing the only
// non-allied player wins the battle there and then.
//
// **Correction.** This test previously asserted the opposite — that the enemy's
// death "must not end while allied peer lives", and that victory came only
// once the ally was killed too. That followed [08 R-SKIR-01 §3] "Victory
// detection", which describes the kind-3 sweep and closes with "allies
// included"; [08 R-TRIG-01 §6] states that sentence is wrong for kind 2 and
// gives the alliance-row skip instead.
func TestResult_AlliedPairVsEnemy(t *testing.T) {
	rng.SeedGlobal(3, 3)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=20;\nZPos=20;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 3}
	cfg.ApplyDefaults()
	cfg.Players[0].AllyGroup = 1
	cfg.Players[1].AllyGroup = 1
	cfg.Players[2].AllyGroup = 2
	for i := 0; i < 3; i++ {
		cfg.Players[i].Controller = 0
	}
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Kill enemy commander (owner 2)
	h := poolHandle(commanderHandles(s)[2][0])
	s.Units.Destroy(h, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h, 0); !result.Freed {
		t.Fatal("enemy commander was not finalized")
	}
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("allied pair vs enemy: the only non-allied player's death must latch victory, got %+v latch %+v", s.GetResult(), s.Latch)
	}
	// The ally is still alive and still owns a commander: victory did not wait
	// for it [08 R-TRIG-01 §6].
	if s.Units.LiveCountForPlayer(1) == 0 {
		t.Fatalf("the allied peer must still be alive when victory latches")
	}
	res := s.GetResult()
	// The winner is the surviving local owner's team, and the ally shares it:
	// a team is named by the lowest slot in the alliance ROW the row-to-player
	// conversion built, not by the setup row's ally-group ordinal, which does
	// not survive a load [08 R-SKIR-01 §2].
	if res.WinnerTeam != s.teamForOwner(0) || s.teamForOwner(1) != s.teamForOwner(0) {
		t.Fatalf("allied pair winner want %d (shared with the ally at %d) got %d", s.teamForOwner(0), s.teamForOwner(1), res.WinnerTeam)
	}
	if res.Draw {
		t.Fatalf("expected win not draw")
	}
}

func TestResult_LocalDefeat(t *testing.T) {
	rng.SeedGlobal(4, 4)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 0
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// LocalOwner is 0 by default (first human)
	// Kill local commander
	h := poolHandle(commanderHandles(s)[0][0])
	s.Units.Destroy(h, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h, 0); !result.Freed {
		t.Fatal("local commander was not finalized")
	}
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("local defeat should latch")
	}
	res := s.GetResult()
	wantWinner := s.teamForOwner(1)
	if res.WinnerTeam != wantWinner {
		t.Fatalf("local defeat winner want %d got %d", wantWinner, res.WinnerTeam)
	}
	if !s.Latch.IsLose() {
		t.Fatalf("local defeat latch should be lose bit")
	}
}

// TestResult_MutualDestructionIsALocalDefeat pins the predicate order of
// [08 R-TRIG-01 §6]: for session kinds 2/3 the defeat predicate is evaluated
// FIRST and, if true, steps the countdown on the lost path; only when it is
// false does the victory sweep run. Defeat therefore wins a tie, and a wipe
// that leaves nobody standing latches the local defeat.
//
// **Correction.** This test previously asserted a draw with WinnerTeam -1,
// citing an "RR-04" mutual-destruction rule that no research section carries.
// Retail's kind-2 end has no draw outcome at all: the lost path is taken
// whenever the local live count is zero, whatever else survives. The -1 winner
// remains, because there is no surviving opponent whose team could be named,
// but the kind is a defeat and the latch carries the lost bit.
func TestResult_MutualDestructionIsALocalDefeat(t *testing.T) {
	rng.SeedGlobal(5, 5)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Kill both commanders before evaluation (same evaluation)
	handles := commanderHandles(s)
	h0 := poolHandle(handles[0][0])
	h1 := poolHandle(handles[1][0])
	s.Units.Destroy(h0, units.DeathKilled)
	s.Units.Destroy(h1, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h0, 0); !result.Freed {
		t.Fatal("first mutual-destruction commander was not finalized")
	}
	if result := s.Units.FinalizeDeath(h1, 0); !result.Freed {
		t.Fatal("second mutual-destruction commander was not finalized")
	}
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("mutual destruction should latch the local defeat")
	}
	res := s.GetResult()
	if res.Draw {
		t.Fatalf("kind 2 has no draw outcome, got %+v", res)
	}
	if res.Kind != "defeat" {
		t.Fatalf("mutual destruction kind = %q, want defeat", res.Kind)
	}
	if res.WinnerTeam != -1 || len(res.Winners) != 0 {
		t.Fatalf("no opponent survives, so no winner may be named: winner=%d winners=%v", res.WinnerTeam, res.Winners)
	}
	if !s.Latch.IsLose() {
		t.Fatalf("mutual destruction must take the lost path, latch %+v", s.Latch)
	}
}

func TestResult_ResultViewExposesEnded(t *testing.T) {
	rng.SeedGlobal(7, 7)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Initially not ended
	if s.Snapshot != nil {
		if cur := s.Snapshot.Current(); cur != nil && cur.Result.Ended {
			t.Fatalf("initial ResultView should not be ended")
		}
	}
	h := poolHandle(commanderHandles(s)[1][0])
	s.Units.Destroy(h, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h, 0); !result.Freed {
		t.Fatal("result-view commander was not finalized")
	}
	for tick := uint32(0); tick < 200; tick++ {
		s.EvaluateResult(tick)
	}
	// A committed frame is the sole result publication path; direct evaluator
	// calls in this fixture need one explicit frame commit before inspection.
	s.publishSnapshot(199)
	// After latch, snapshot view should be ended
	if s.Snapshot != nil {
		view := s.Snapshot.Current().Result
		if !view.Ended {
			t.Fatalf("ResultView should be ended after latch, got %+v", view)
		}
		if view.WinnerTeam != s.GetResult().WinnerTeam {
			t.Fatalf("ResultView winner mismatch")
		}
		// Also check via Read()
		cur := s.Snapshot.Current()
		if cur != nil && !cur.Result.Ended {
			t.Fatalf("Frame Result should be ended")
		}
	}
}

func TestResult_AIProfileLoadFailure(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	// Note: fs has no ai/default.txt, so strict should error when computer player exists
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[1].Controller = 1 // computer
	_, err := NewSkirmishWithFS(fs, cat, cfg)
	if err == nil {
		t.Fatalf("expected AI profile load failure error for computer player without profile")
	}
}

func newLobbyEndRuleSession(t *testing.T, commanderDeath int, addEnemyUnit bool) (*Session, pool.Handle) {
	t.Helper()
	cat := minimalCatalogForStrict()
	ordinary := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "corllt"},
		UnitName:         "corllt",
		MaxDamage:        500,
		FootprintX:       1,
		FootprintZ:       1,
	}
	cat.Units[ordinary.CanonicalKey] = ordinary
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.CommanderDeath = commanderDeath
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 0
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	if !addEnemyUnit {
		return s, 0
	}
	h, err := s.Units.Create(ordinary, 1, numeric.Fixed(120<<16), 0, numeric.Fixed(120<<16))
	if err != nil {
		t.Fatalf("create enemy ordinary unit: %v", err)
	}
	return s, h
}

func stepLobbyThrough(t *testing.T, s *Session, first, last int32) {
	t.Helper()
	for now := first; now <= last; now++ {
		s.Step(now)
		// A terminal result is latched before the post-battle dispatch. Stop at
		// that boundary so this helper does not drive the state machine back
		// through Router and Loading while the result remains authoritative [08
		// "Session states"].
		if s.GetResult().Ended {
			return
		}
	}
}

func TestSkirmishLobby_DefaultMissionTriggersDoNotEndLiveMatch(t *testing.T) {
	for _, commanderDeath := range []int{0, 1} {
		t.Run(string(rune('0'+commanderDeath)), func(t *testing.T) {
			s, _ := newLobbyEndRuleSession(t, commanderDeath, false)
			if len(s.Mission.Victory) == 0 || len(s.Mission.Defeat) == 0 {
				t.Fatalf("fixture must contain injected default mission triggers")
			}
			stepLobbyThrough(t, s, 0, 220)
			if s.Clock.GlobalTick <= 180 {
				t.Fatalf("advanced only %d ticks, want beyond 180", s.Clock.GlobalTick)
			}
			if s.State != StateBattle || s.GetResult().Ended || s.Latch.Countdown != -1 {
				t.Fatalf("live lobby match ended: state=%v result=%+v latch=%+v", s.State, s.GetResult(), s.Latch)
			}
			if s.Mission.Victory[0].Completed || s.Mission.Defeat[0].Completed {
				t.Fatalf("lobby polled OTA default triggers: victory=%v defeat=%v", s.Mission.Victory[0].Completed, s.Mission.Defeat[0].Completed)
			}
		})
	}
}

func TestSkirmishLobby_CommanderDeathModeSweepsOwnerUnits(t *testing.T) {
	s, ordinary := newLobbyEndRuleSession(t, 1, true)
	killCommander(t, s, 1)
	stepLobbyThrough(t, s, 0, 220)
	if u := s.Units.Unit(ordinary); u != nil && u.Alive {
		t.Fatalf("ordinary enemy unit survived the commander owner sweep")
	}
	res := s.GetResult()
	if s.State != StatePostBattle || !res.Ended || res.Reason != ReasonCommanderDeath {
		t.Fatalf("commander-death mode did not end on commander loss: state=%v result=%+v", s.State, res)
	}
}

func TestSkirmishDeathmatchRespawnDrawsXThenZ(t *testing.T) {
	s, _ := newLobbyEndRuleSession(t, int(CommanderDeathDeathmatch), false)
	s.World = minimalTerrain()
	// This test isolates commander placement and draw order; production
	// composition supplies movement for occupancy and rebinding.
	s.Movement = nil
	h := poolHandle(commanderHandles(s)[0][0])
	simState := s.SimRNG().State
	simDraws := s.SimRNG().Draws()
	s.Units.Destroy(h, units.DeathKilled)
	if result := s.Units.FinalizeDeath(h, 0); !result.Freed {
		t.Fatal("commander was not finalized")
	}
	s.NotifyDeathFinalized(0, 0)
	// **Correction (WU-19-116).** This used to assert that the commander's death
	// armed a deathmatch countdown of 4 on the spot. It does not: rule 2 runs
	// the same owner sweep as rule 1 and arms nothing. The respawn rides the one
	// shared countdown, which the local slot's first settlement due arms once
	// the sweep has driven the live count to zero, and the rule word is read
	// again at the due that takes that countdown below zero
	// [08 R-SKIR-01 §3] "Defeat detection"[08 R-TRIG-01 §6] "Countdown and
	// latch". Nothing is armed until the first due, so the sixth due — 150 ticks
	// after the first, not 120 — is the one that respawns.
	if active, countdown, _, exhausted := s.DeathmatchStatus(); active || countdown != -1 || exhausted {
		t.Fatalf("commander death armed a countdown of its own: active=%v countdown=%d exhausted=%v", active, countdown, exhausted)
	}
	// Predict the first candidate from a copy of the stream. The candidate
	// rectangle is W/D minus one tenth on each side, with X sampled before Z
	// [01 §7.1][08 R-SKIR-01 §3].
	predict := rng.SimulationFromState(simState)
	mapW := uint32(s.World.CellW * 16)
	mapH := uint32(s.World.CellH * 16)
	insetW, insetH := mapW/10, mapH/10
	rx := predict.Uint32n(mapW - 2*insetW)
	rz := predict.Uint32n(mapH - 2*insetH)
	wantX := numeric.Fixed(int64(insetW+rx) << 16)
	wantZ := numeric.Fixed(int64(insetH+rz) << 16)
	// Six dues: the first arms the shared countdown to 4, the next five step it
	// to -1, and that sixth due is where the rule word selects the respawn
	// [08 R-TRIG-01 §6] "Countdown and latch".
	for tick := uint32(30); tick <= 180; tick += 30 {
		s.EvaluateResult(tick)
	}
	var commander *units.Unit
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && s.isCommanderForOwner(u) {
			commander = u
			break
		}
	}
	if commander == nil {
		t.Fatal("deathmatch did not create a commander")
	}
	if commander.X != wantX || commander.Z != wantZ {
		t.Fatalf("respawn position = (%v,%v), want (%v,%v)", commander.X, commander.Z, wantX, wantZ)
	}
	// This synthetic constructor intentionally leaves the unit world's
	// allocation RNG unbound; only the two candidate draws belong to this
	// fixture's stream. Production composition binds the allocator stream
	// before any creation and accounts for its common-initializer draws.
	wantDraws := uint64(2)
	if got := s.SimRNG().Draws() - simDraws; got != wantDraws {
		t.Fatalf("respawn consumed %d simulation draws, want %d candidate draws", got, wantDraws)
	}
}

func TestSkirmishLobby_AllUnitsModeWaitsForFinalUnit(t *testing.T) {
	s, ordinary := newLobbyEndRuleSession(t, 0, true)
	killCommander(t, s, 1)
	stepLobbyThrough(t, s, 0, 220)
	if s.State != StateBattle || s.GetResult().Ended || s.Latch.Countdown != -1 {
		t.Fatalf("all-units mode ended while enemy unit survived: state=%v result=%+v latch=%+v", s.State, s.GetResult(), s.Latch)
	}
	s.Units.Destroy(ordinary, units.DeathKilled)
	stepLobbyThrough(t, s, 221, 450)
	res := s.GetResult()
	if s.State != StatePostBattle || !res.Ended || res.Reason != ReasonAllUnits {
		t.Fatalf("all-units mode did not end after final unit: state=%v result=%+v", s.State, res)
	}
}

// Helper to convert int handle to pool.Handle
func poolHandle(h int) pool.Handle {
	return pool.Handle(h)
}

// Ensure imports used
var _ = content.Catalog{}
var _ = vfs.FS{}

func init() {
	// Ensure commander flag for helper
}
