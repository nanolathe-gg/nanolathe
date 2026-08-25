package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
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
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
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
	// Evaluate until latched [RS-05][RR-04] 4→-1 over ~150 ticks (once per 30)
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
		_, cur, ok := s.Snapshot.Read()
		if !ok {
			// Publish a frame to ensure snapshot has result
			// Trigger a tick to publish
			s.Step(200)
			_, cur, ok = s.Snapshot.Read()
		}
		if ok && !cur.Result.Ended {
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
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Ensure 3 teams
	if len(commanderHandles(s)) != 3 {
		t.Fatalf("expected 3 commanders")
	}
	// Kill one enemy (owner 1) – should NOT end
	s.Units.Destroy(poolHandle(commanderHandles(s)[1][0]), units.DeathKilled)
	for tick := uint32(0); tick < 200; tick++ {
		s.EvaluateResult(tick)
	}
	if s.GetResult().Ended {
		t.Fatalf("FFA: killing one of three should not end match, got %+v", s.GetResult())
	}
	// Kill second enemy (owner 2) – now only owner 0 remains, should end with winner 0
	s.Units.Destroy(poolHandle(commanderHandles(s)[2][0]), units.DeathKilled)
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
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Kill enemy commander (owner 2)
	s.Units.Destroy(poolHandle(commanderHandles(s)[2][0]), units.DeathKilled)
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("allied pair vs enemy: enemy death should latch allies win, got %+v latch %+v", s.GetResult(), s.Latch)
	}
	res := s.GetResult()
	// Winner should be team 1 (allied pair)
	if res.WinnerTeam != 1 {
		t.Fatalf("allied pair winner want 1 got %d", res.WinnerTeam)
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
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// LocalOwner is 0 by default (first human)
	// Kill local commander
	s.Units.Destroy(poolHandle(commanderHandles(s)[0][0]), units.DeathKilled)
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

func TestResult_MutualDestructionDraw(t *testing.T) {
	rng.SeedGlobal(5, 5)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Kill both commanders before evaluation (same evaluation)
	handles := commanderHandles(s)
	s.Units.Destroy(poolHandle(handles[0][0]), units.DeathKilled)
	s.Units.Destroy(poolHandle(handles[1][0]), units.DeathKilled)
	var latched bool
	for tick := uint32(0); tick < 200; tick++ {
		if s.EvaluateResult(tick) {
			latched = true
			break
		}
	}
	if !latched {
		t.Fatalf("mutual destruction should latch draw")
	}
	res := s.GetResult()
	if !res.Draw {
		t.Fatalf("expected draw on mutual destruction, got %+v", res)
	}
	if res.WinnerTeam != -1 {
		t.Fatalf("draw winner should be -1, got %d", res.WinnerTeam)
	}
}

func TestResult_CallbackFiresOnce(t *testing.T) {
	rng.SeedGlobal(6, 6)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	count := 0
	s.SetResultCallback(func(r Result) { count++ })
	// Kill enemy
	s.Units.Destroy(poolHandle(commanderHandles(s)[1][0]), units.DeathKilled)
	for tick := uint32(0); tick < 200; tick++ {
		s.EvaluateResult(tick)
	}
	if count != 1 {
		t.Fatalf("callback should fire once, got %d", count)
	}
	// Further evaluations should not fire again (200..300 covers next due windows)
	for tick := uint32(200); tick < 310; tick++ {
		s.EvaluateResult(tick)
	}
	if count != 1 {
		t.Fatalf("callback fired more than once: %d", count)
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
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	// Initially not ended
	if s.Snapshot != nil {
		if view := s.Snapshot.GetResultView(); view.Ended {
			t.Fatalf("initial ResultView should not be ended")
		}
	}
	s.Units.Destroy(poolHandle(commanderHandles(s)[1][0]), units.DeathKilled)
	for tick := uint32(0); tick < 200; tick++ {
		s.EvaluateResult(tick)
	}
	// After latch, snapshot view should be ended
	if s.Snapshot != nil {
		view := s.Snapshot.GetResultView()
		if !view.Ended {
			t.Fatalf("ResultView should be ended after latch, got %+v", view)
		}
		if view.WinnerTeam != s.GetResult().WinnerTeam {
			t.Fatalf("ResultView winner mismatch")
		}
		// Also check via Read()
		_, cur, ok := s.Snapshot.Read()
		if ok && !cur.Result.Ended {
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
