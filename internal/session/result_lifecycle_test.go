package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestResult_StateTransitionAndNoTick verifies 6→7→2 and no tick after terminal [RS-05][08].
func TestResult_StateTransitionAndNoTick(t *testing.T) {
	rng.SeedGlobal(100, 200)
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
	s.RegisterAll()
	s.State = StateBattle
	// Kill enemy to trigger result
	handles := commanderHandles(s)
	h := handles[1][0]
	s.Units.Destroy(poolHandle(h), 1)
	var latchedTick uint32
	for tick := uint32(1); tick < 300; tick++ {
		s.Step(int32(tick))
		if s.GetResult().Ended {
			latchedTick = s.GetResult().Tick
			break
		}
	}
	if !s.GetResult().Ended {
		t.Fatalf("should have latched")
	}
	t.Logf("after latch State=%v latch=%+v result=%+v", s.State, s.Latch, s.GetResult())
	if s.State != StatePostBattle {
		t.Fatalf("after latch, want StatePostBattle(7) got %v", s.State)
	}
	tickAfterLatch := s.Clock.GlobalTick
	t.Logf("tickAfterLatch=%d", tickAfterLatch)
	// Further Step should not advance GlobalTick while in PostBattle [RS-05] no hidden ticks
	prev := s.Clock.GlobalTick
	s.Step(int32(latchedTick) + 10)
	t.Logf("after one postbattle step State=%v GlobalTick=%d", s.State, s.Clock.GlobalTick)
	if s.Clock.GlobalTick != prev {
		t.Fatalf("tick after terminal advanced %d -> %d, expected no tick [RS-05]", prev, s.Clock.GlobalTick)
	}
	if s.State != StateRouter {
		// After one Step in PostBattle, handlePostBattle should have moved 7→2
		t.Logf("postbattle step State=%v (expected Router)", s.State)
	}
	// Ensure 7→2 transition happened and no tick occurred
	if s.State != StateRouter && s.State != StatePostBattle {
		t.Fatalf("expected PostBattle→Router, got %v", s.State)
	}
	// One more Advance should go 7→2 if still in PostBattle, but we already transitioned
	// Verify that we can explicitly transition 7→2 and still no tick
	if s.State == StatePostBattle {
		s.Advance()
		if s.State != StateRouter {
			t.Fatalf("7→2 want Router got %v", s.State)
		}
		if s.Clock.GlobalTick != tickAfterLatch {
			t.Fatalf("GlobalTick changed after 7→2")
		}
	}
}

// TestResult_RetryResetsAuthoritativeState verifies retry clears the terminal
// result and latch so the next battle starts from a clean attempt [08
// "Session states"].
func TestResult_RetryResetsAuthoritativeState(t *testing.T) {
	rng.SeedGlobal(101, 201)
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
	s.RegisterAll()
	s.State = StateBattle
	// First match: kill enemy
	s.Units.Destroy(poolHandle(commanderHandles(s)[1][0]), 1)
	for tick := uint32(1); tick < 300; tick++ {
		s.Step(int32(tick))
		if s.GetResult().Ended {
			break
		}
	}
	if !s.GetResult().Ended {
		t.Fatalf("first match did not reach terminal result")
	}
	// Retry via clean session recreation (simulates shell retry) [RS-05]
	// Use NewSyntheticSkirmishForTest to ensure clean terrain without VFS TNT dependency
	s2, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("retry NewSyntheticSkirmishForTest: %v", err)
	}
	s2.RegisterAll()
	s2.State = StateBattle
	// Kill enemy again in new session
	s2.Units.Destroy(poolHandle(commanderHandles(s2)[1][0]), 1)
	for tick := uint32(1); tick < 300; tick++ {
		s2.Step(int32(tick))
		if s2.GetResult().Ended {
			break
		}
	}
	if !s2.GetResult().Ended {
		t.Fatalf("second match did not reach terminal result")
	}
	// Also test Session.Retry on same object resets cleanly
	s3, _ := NewSyntheticSkirmishForTest(fs, cat, cfg)
	s3.RegisterAll()
	s3.State = StatePostBattle
	s3.Latch.Countdown = -1
	s3.Latch.Bits = LatchBitEnding | LatchBitWin1
	s3.result = Result{Ended: true, Tick: 100}
	// Retry should clear and allow new latch
	_ = s3.Retry()
	if s3.GetResult().Ended {
		t.Fatalf("after Retry, result should be cleared")
	}
	if s3.Latch.IsEnding() {
		t.Fatalf("after Retry latch should be reset")
	}
	if s3.State != StateLoading {
		t.Fatalf("after Retry want Loading got %v", s3.State)
	}
}

// TestResult_SimultaneousFinalCommanders drives the same tie through the full
// Session.Step path rather than direct evaluator calls. The predicate order of
// [08 R-TRIG-01 §6] makes it a local defeat: the defeat predicate is evaluated
// first for session kinds 2/3 and takes the lost path.
//
// **Correction.** It previously asserted a draw, citing an "RR-04" rule that no
// research section carries; retail's kind-2 end has no draw outcome. See
// TestResult_MutualDestructionIsALocalDefeat for the evaluator-level form.
func TestResult_SimultaneousFinalCommanders(t *testing.T) {
	rng.SeedGlobal(102, 202)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	s, _ := NewSyntheticSkirmishForTest(fs, cat, cfg)
	s.RegisterAll()
	s.State = StateBattle
	// Kill both simultaneously before evaluation
	handles := commanderHandles(s)
	s.Units.Destroy(poolHandle(handles[0][0]), 1)
	s.Units.Destroy(poolHandle(handles[1][0]), 1)
	for tick := uint32(1); tick < 300; tick++ {
		s.Step(int32(tick))
		if s.GetResult().Ended {
			break
		}
	}
	res := s.GetResult()
	if !res.Ended || res.Draw || res.Kind != "defeat" {
		t.Fatalf("simultaneous should latch the local defeat, got %+v", res)
	}
	if !s.Latch.IsLose() {
		t.Fatalf("simultaneous should take the lost path, latch %+v", s.Latch)
	}
}

// TestResult_LocalWinLossViaSnapshot verifies snapshot carries kind, winners/losers, countdown, scores [RS-05].
func TestResult_LocalWinLossViaSnapshot(t *testing.T) {
	rng.SeedGlobal(103, 203)
	cat := minimalCatalogForStrict()
	for _, u := range cat.Units {
		u.Commander = true
	}
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	// Local win: kill enemy
	s, _ := NewSyntheticSkirmishForTest(fs, cat, cfg)
	s.RegisterAll()
	s.State = StateBattle
	s.LocalOwner = 0
	s.EnemyOwner = 1
	s.Units.Destroy(poolHandle(commanderHandles(s)[1][0]), 1)
	for tick := uint32(1); tick < 300; tick++ {
		s.Step(int32(tick))
		if s.GetResult().Ended {
			break
		}
	}
	res := s.GetResult()
	if res.Kind != "victory" {
		t.Fatalf("local win kind want victory got %s", res.Kind)
	}
	if len(res.Winners) == 0 || len(res.Losers) == 0 {
		t.Fatalf("winners/losers missing win %+v", res)
	}
	if view := s.Snapshot.Current().Result; !view.Ended || view.Kind != "victory" {
		t.Fatalf("snapshot victory not published %+v", view)
	}
	if s.Latch.Countdown != -1 {
		t.Fatalf("countdown after latch want -1 got %d", s.Latch.Countdown)
	}
	// Local loss: kill local
	s2, _ := NewSyntheticSkirmishForTest(fs, cat, cfg)
	s2.RegisterAll()
	s2.State = StateBattle
	s2.LocalOwner = 0
	s2.EnemyOwner = 1
	s2.Units.Destroy(poolHandle(commanderHandles(s2)[0][0]), 1)
	for tick := uint32(1); tick < 300; tick++ {
		s2.Step(int32(tick))
		if s2.GetResult().Ended {
			break
		}
	}
	res2 := s2.GetResult()
	if res2.Kind != "defeat" {
		t.Fatalf("local loss kind want defeat got %s", res2.Kind)
	}
	if view := s2.Snapshot.Current().Result; !view.Ended || view.Kind != "defeat" {
		t.Fatalf("snapshot defeat not published %+v", view)
	}
}
