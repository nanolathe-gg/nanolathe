package economy

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

func activePlayer(p *Player) {
	p.Exists = true
	p.ControllerState = 1 // settling state 1 ∈ both active and settling sets
	p.IsObserver = false
	p.StatusHalfwordAt144 = 1 // nonzero so predicate true regardless of StatusWord
	p.StatusWordAt140 = 0
	p.GameEnded = false
	p.EndGameCountdown = -1
	p.Helper1Deadline = 0
	p.Helper2Deadline = 0
	// sensible thresholds/capacities for sharing tests
	p.Capacity[Metal] = 1000
	p.Capacity[Energy] = 1000
	p.Allies = [10]bool{}
}

// TestSettlementCadence verifies C2 basic deadline advance and C3 ordering.
func TestSettlementCadence(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 0
	svc.Players[0].WinLoseTime = 0
	svc.Players[0].DisplayTimer = 0
	// Ensure helpers not interfering: set deadlines far future so they don't run at tick 0? But helpers at 0 would run at tick 0.
	// For this test we set helper deadlines to 0 so they run at tick 0, but we care about settlement.
	svc.Players[0].Helper1Deadline = 0
	svc.Players[0].Helper2Deadline = 0
	svc.Players[0].Mirror[Metal].Production = 1
	svc.Players[0].StorageBonusEnabled = true
	svc.Players[0].StorageBonus[Metal] = 1000
	w := units.New(10, nil)
	// Tick 0 should settle (deadline 0 <=0) and advance to 30
	svc.TickPlayer(0, 0, w, nil)
	if svc.Players[0].UpdateTime != 30 {
		t.Fatalf("C2: after tick 0 deadline should be 30, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 1 {
		t.Fatalf("C2: due pass must consume/archive production, live=%v pass=%v", svc.Players[0].Mirror[Metal].Production, svc.Players[0].PassProduced[Metal])
	}
	// Ticks 1..29 should not settle
	svc.Players[0].Mirror[Metal].Production = 2
	for tick := uint32(1); tick < 30; tick++ {
		svc.TickPlayer(0, tick, w, nil)
		if svc.Players[0].UpdateTime != 30 {
			t.Fatalf("C2: deadline should remain 30 at tick %d, got %d", tick, svc.Players[0].UpdateTime)
		}
		if svc.Players[0].Mirror[Metal].Production != 2 {
			t.Fatalf("C2: future deadline must leave live production pending at tick %d", tick)
		}
	}
	// Tick 30 should settle again and advance to 60
	svc.Players[0].Stock[Metal] = 1
	svc.TickPlayer(0, 30, w, nil)
	if svc.Players[0].UpdateTime != 60 {
		t.Fatalf("C2: after tick 30 deadline should be 60, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 2 || svc.Players[0].Stock[Metal] != 3 {
		t.Fatalf("C2: second due pass did not settle live production, stock=%v live=%v pass=%v", svc.Players[0].Stock[Metal], svc.Players[0].Mirror[Metal].Production, svc.Players[0].PassProduced[Metal])
	}
}

// TestCatchUpEdge verifies C2 catch-up: deadline 100 behind settles once per tick advancing exactly 30 each tick.
func TestCatchUpEdge(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	// Global tick 200, deadline 100 (100 behind)
	var tick uint32 = 200
	svc.Players[0].UpdateTime = 100
	svc.Players[0].Helper1Deadline = 1<<32 - 1 // far future to avoid helper noise? Set to tick to make helper due each time? Keep helpers aligned to not affect settlement counts.
	svc.Players[0].Helper2Deadline = 1<<32 - 1
	// Override helpers to not increment? But fine.
	w := units.New(10, nil)
	// Simulate ticks 200.. until caught up
	expectedDeadlines := []uint32{130, 160, 190, 220}
	for i, wantDeadline := range expectedDeadlines {
		svc.Players[0].Mirror[Metal].Production = float32(i + 1)
		curTick := tick + uint32(i)
		svc.TickPlayer(0, curTick, w, nil)
		if svc.Players[0].UpdateTime != wantDeadline {
			t.Fatalf("C2 catch-up: after tick %d deadline want %d got %d", curTick, wantDeadline, svc.Players[0].UpdateTime)
		}
		if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != float32(i+1) {
			t.Fatalf("C2 catch-up: due pass at tick %d did not consume/archive production", curTick)
		}
	}
	// Next tick after catch-up: deadline 220 at tick 204? 220 >204 should not settle? Wait we advanced tick linearly, but deadline after 4 ticks is 220, which is > tick 203? Let's continue ticks until deadline exceeds tick.
	// At tick 204, deadline 220 >204 so should NOT settle until tick 220.
	svc.Players[0].Mirror[Metal].Production = 9
	svc.TickPlayer(0, 204, w, nil)
	if svc.Players[0].UpdateTime != 220 {
		t.Fatalf("deadline should stay 220 at tick 204, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 9 {
		t.Fatalf("C2 catch-up: future deadline must leave production pending")
	}
}

// TestCatchUp100TicksBehind verifies explicit 100 behind settles once per tick.
func TestCatchUp100TicksBehind(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 0
	var curTick uint32 = 100
	svc.Players[0].Helper1Deadline = curTick + 1000 // prevent helper interference with settlement counts? Actually helpers at future won't run
	svc.Players[0].Helper2Deadline = curTick + 1000
	w := units.New(10, nil)
	// Deadline 0 at tick 100 is 100 behind, should settle once per tick until caught up (single add per tick, not loop).
	// Ticks 100,101,102,103 should settle (deadlines 0->30->60->90->120), tick 104 should NOT settle as deadline 120 >104.
	for i := 0; i < 4; i++ {
		svc.Players[0].Mirror[Metal].Production = float32(i + 1)
		tk := curTick + uint32(i)
		wantDeadline := svc.Players[0].UpdateTime
		svc.TickPlayer(0, tk, w, nil)
		if svc.Players[0].UpdateTime != wantDeadline+30 {
			t.Fatalf("deadline should advance exactly 30: was %d now %d", wantDeadline, svc.Players[0].UpdateTime)
		}
		if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != float32(i+1) {
			t.Fatalf("100 behind: due pass at tick %d did not consume/archive production", tk)
		}
	}
	// Next tick 104 deadline 120 >104 should not settle, single add ensures catch-up per tick.
	svc.Players[0].Mirror[Metal].Production = 9
	svc.TickPlayer(0, curTick+4, w, nil)
	if svc.Players[0].UpdateTime != 120 {
		t.Fatalf("deadline should stay 120 at tick 104, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 9 {
		t.Fatalf("future deadline must leave production pending")
	}
}

// TestUnsignedComparisonWrap verifies C2 unsigned comparison across 2^32 wrap.
func TestUnsignedComparisonWrap(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	w := units.New(10, nil)
	// Case A: deadline near max, tick small (0): deadline > tick unsigned => not due => no settle, deadline frozen.
	svc.Players[0].Mirror[Metal].Production = 3
	svc.Players[0].UpdateTime = 0xFFFFFFF0 // 4294967280
	svc.Players[0].Helper1Deadline = 0xFFFFFFFF
	svc.Players[0].Helper2Deadline = 0xFFFFFFFF
	svc.TickPlayer(0, 5, w, nil)
	if svc.Players[0].UpdateTime != 0xFFFFFFF0 {
		t.Fatalf("unsigned wrap A: deadline should stay 0xFFFFFFF0, got %08x", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 3 {
		t.Fatalf("unsigned wrap A: future deadline must leave production pending")
	}
	// Case B: deadline small, tick near max: deadline (5) not > tick (0xFFFFFFFE) => due => settle and advance.
	svc.Players[0].UpdateTime = 5
	svc.Players[0].Mirror[Metal].Production = 4
	svc.TickPlayer(0, 0xFFFFFFFE, w, nil) // 4294967294
	if svc.Players[0].UpdateTime != 35 {  // 5+30
		t.Fatalf("wrap B: deadline should be 35, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 4 {
		t.Fatalf("unsigned wrap B: due pass must consume/archive production")
	}
	// Case C: deadline = 0xFFFFFFFF, tick =0 => deadline > tick true => not due
	svc.Players[0].UpdateTime = 0xFFFFFFFF
	svc.Players[0].Mirror[Metal].Production = 5
	svc.TickPlayer(0, 0, w, nil)
	if svc.Players[0].Mirror[Metal].Production != 5 {
		t.Fatalf("unsigned wrap C: future deadline must leave production pending")
	}
	// Case D: deadline =0, tick=0xFFFFFFFF => deadline <= tick => due (wrap single add)
	svc.Players[0].UpdateTime = 0
	svc.Players[0].Mirror[Metal].Production = 6
	svc.TickPlayer(0, 0xFFFFFFFF, w, nil)
	if svc.Players[0].UpdateTime != 30 {
		t.Fatalf("wrap D deadline advance to 30, got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 6 {
		t.Fatalf("unsigned wrap D: due pass must consume/archive production")
	}
	// Ensure helpers also use unsigned; not critical but check helper wrap similarly
	svc.Players[0].Helper1Deadline = 0xFFFFFFF0
	svc.Players[0].Helper1Calls = 0
	// Tick with helper deadline future: should not run helper
	svc.Players[0].UpdateTime = 0xFFFFFFF0 // set deadline future again to avoid settlement interference
	svc.TickPlayer(0, 5, w, nil)
	if svc.Players[0].Helper1Calls != 0 {
		t.Fatalf("helper unsigned wrap should not have run")
	}
}

// TestSkippedSlotNothingAdvances verifies C3: while skipped, nothing advances including deadline.
func TestSkippedSlotNothingAdvances(t *testing.T) {
	var svc Service
	w := units.New(10, nil)
	// Player 0 inactive (Exists false) with deadline 100
	svc.Players[0].Exists = false
	svc.Players[0].UpdateTime = 100
	svc.Players[0].Helper1Deadline = 100
	svc.Players[0].Helper2Deadline = 100
	svc.Players[0].ControllerState = 1
	svc.Players[0].Mirror[Metal].Production = 3
	var beforeCalled bool
	svc.TickPlayer(0, 200, w, func() { beforeCalled = true })
	if beforeCalled {
		t.Fatalf("skipped slot beforeDeadline should not be called")
	}
	if svc.Players[0].UpdateTime != 100 {
		t.Fatalf("skipped slot deadline frozen: want 100 got %d", svc.Players[0].UpdateTime)
	}
	if svc.Players[0].Helper1Deadline != 100 || svc.Players[0].Helper2Deadline != 100 {
		t.Fatalf("skipped slot helper deadlines frozen")
	}
	if svc.Players[0].WeaponRefreshCalls != 0 {
		t.Fatalf("skipped slot weapon refresh should not run")
	}
	if svc.Players[0].Mirror[Metal].Production != 3 {
		t.Fatalf("skipped slot must leave production pending")
	}
	// Also test observer skip freezes deadline
	svc.Players[1].Exists = true
	svc.Players[1].ControllerState = 1
	svc.Players[1].IsObserver = true
	svc.Players[1].UpdateTime = 50
	svc.Players[1].Helper1Deadline = 50
	svc.Players[1].Helper2Deadline = 50
	svc.Players[1].Mirror[Metal].Production = 4
	beforeCalled = false
	svc.TickPlayer(1, 100, w, func() { beforeCalled = true })
	if beforeCalled || svc.Players[1].UpdateTime != 50 {
		t.Fatalf("observer skipped should freeze all, got before %v deadline %d", beforeCalled, svc.Players[1].UpdateTime)
	}
	if svc.Players[1].Mirror[Metal].Production != 4 {
		t.Fatalf("observer skipped must leave production pending")
	}
	// Active state skip (state 0 not in {1,2,3})
	svc.Players[2].Exists = true
	svc.Players[2].ControllerState = 0
	svc.Players[2].UpdateTime = 70
	svc.Players[2].Helper1Deadline = 70
	svc.TickPlayer(2, 100, w, nil)
	if svc.Players[2].UpdateTime != 70 {
		t.Fatalf("inactive state should freeze deadline")
	}
}

// TestGateChainIndependentlyBlocks verifies C4 each condition independently blocks settlement but deadline still advances when gate blocks.
func TestGateChainIndependentlyBlocks(t *testing.T) {
	w := units.New(10, nil)
	cases := []struct {
		name   string
		mutate func(*Player)
	}{
		{
			name: "statusPair false",
			mutate: func(p *Player) {
				activePlayer(p)
				p.StatusHalfwordAt144 = 0 // halfword zero
				p.StatusWordAt140 = 1     // neighbour non-zero => predicate false (0==0? 0!=0 false, 1==0 false => false)
			},
		},
		{
			name: "settlingState third active but not settling (3)",
			mutate: func(p *Player) {
				activePlayer(p)
				p.ControllerState = 3 // active but not settling
			},
		},
		{
			name: "gameEnded true",
			mutate: func(p *Player) {
				activePlayer(p)
				p.GameEnded = true
			},
		},
		{
			name: "countdown non-negative 0",
			mutate: func(p *Player) {
				activePlayer(p)
				p.EndGameCountdown = 0
			},
		},
		{
			name: "countdown positive",
			mutate: func(p *Player) {
				activePlayer(p)
				p.EndGameCountdown = 5
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			p := &svc.Players[0]
			tc.mutate(p)
			p.Mirror[Metal].Production = 4
			// Ensure deadline due
			p.UpdateTime = 100
			p.Helper1Deadline = 200 // far future to avoid extra helper increments affecting test clarity
			p.Helper2Deadline = 200
			p.WeaponRefreshCalls = 0
			var beforeCalls int
			svc.TickPlayer(0, 100, w, func() { beforeCalls++ })
			if p.UpdateTime != 130 {
				t.Fatalf("%s: gate block should still advance deadline by 30 (100->130), got %d", tc.name, p.UpdateTime)
			}
			// Per-tick helpers and beforeDeadline should have run even when gate blocks, because they are before deadline compare
			if p.Helper1Calls != 0 && tc.name != "helper" { // helper deadline was future, so 0 expected; but we should check beforeCalls
			}
			if beforeCalls != 1 {
				t.Fatalf("%s: beforeDeadline should have run despite gate block", tc.name)
			}
			if p.WeaponRefreshCalls != 1 {
				t.Fatalf("%s: weapon refresh should have run despite gate block, got %d", tc.name, p.WeaponRefreshCalls)
			}
			if p.Mirror[Metal].Production != 4 {
				t.Fatalf("%s: blocked gate must leave production pending", tc.name)
			}
		})
	}
	// Also verify that observer/status etc when early skipped does NOT advance deadline — already tested in skipped test
	// Test each gate's success allows settlement
	t.Run("all gates pass settles", func(t *testing.T) {
		var svc Service
		p := &svc.Players[0]
		activePlayer(p)
		p.UpdateTime = 100
		p.Helper1Deadline = 200
		p.Helper2Deadline = 200
		p.Mirror[Metal].Production = 4
		svc.TickPlayer(0, 100, w, nil)
		if p.UpdateTime != 130 {
			t.Fatalf("deadline advance 100->130")
		}
		if p.Mirror[Metal].Production != 0 || p.PassProduced[Metal] != 4 {
			t.Fatalf("all gates pass must consume/archive production")
		}
	})
	// Verify isObserver gate also blocks at gate level but early skip already would have frozen deadline,
	// so to test gate observer blocking while deadline still advances we need to see that early skip would have frozen.
	// Instead we test that if we bypass early skip by making player active then mutate to observer after early check? No.
	// The spec's gate observer is redundant with early skip, but we test early skip freezing already.
}

// TestBeforeDeadlineOrdering verifies C3 beforeDeadline invoked after helpers, before deadline compare.
func TestBeforeDeadlineOrdering(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	w := units.New(10, nil)
	// Set helper deadlines to be due at tick 100 so helpers will run at 100
	svc.Players[0].UpdateTime = 100
	svc.Players[0].Helper1Deadline = 100
	svc.Players[0].Helper2Deadline = 100
	svc.Players[0].WeaponRefreshCalls = 0
	svc.Players[0].Mirror[Metal].Production = 5
	// Capture helper calls at beforeDeadline time
	var helper1AtBefore, helper2AtBefore, weaponAtBefore int
	var productionAtBefore float32
	svc.TickPlayer(0, 100, w, func() {
		helper1AtBefore = svc.Players[0].Helper1Calls
		helper2AtBefore = svc.Players[0].Helper2Calls
		weaponAtBefore = svc.Players[0].WeaponRefreshCalls
		productionAtBefore = svc.Players[0].Mirror[Metal].Production
		// helpers should have already incremented
	})
	// The callback observed all helper effects, and the deadline advanced only
	// after the callback returned, which locks the phase ordering without a
	// notification hook.
	if helper1AtBefore != 1 || helper2AtBefore != 1 {
		t.Fatalf("helpers should have run before beforeDeadline: h1=%d h2=%d", helper1AtBefore, helper2AtBefore)
	}
	if weaponAtBefore != 1 {
		t.Fatalf("weapon refresh should have run before beforeDeadline")
	}
	if productionAtBefore != 5 || svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 5 {
		t.Fatalf("beforeDeadline must precede settlement: callback production=%v live=%v pass=%v", productionAtBefore, svc.Players[0].Mirror[Metal].Production, svc.Players[0].PassProduced[Metal])
	}
	// Verify beforeDeadline not called when early skipped
	var svc2 Service
	svc2.Players[0].Exists = false
	svc2.Players[0].UpdateTime = 100
	called := false
	svc2.TickPlayer(0, 100, w, func() { called = true })
	if called {
		t.Fatalf("beforeDeadline should NOT be called when early skipped")
	}
	// Verify beforeDeadline called even when deadline not due (helpers run, but settlement skipped)
	var svc3 Service
	activePlayer(&svc3.Players[0])
	svc3.Players[0].UpdateTime = 200 // future, not due at tick 100
	svc3.Players[0].Helper1Deadline = 100
	svc3.Players[0].Helper2Deadline = 100
	svc3.Players[0].Mirror[Metal].Production = 6
	called = false
	var pendingAtBefore float32
	svc3.TickPlayer(0, 100, w, func() { called = true; pendingAtBefore = svc3.Players[0].Mirror[Metal].Production })
	if !called {
		t.Fatalf("beforeDeadline should be called even when deadline not due (helpers before compare)")
	}
	if svc3.Players[0].UpdateTime != 200 {
		t.Fatalf("deadline future should stay 200, got %d", svc3.Players[0].UpdateTime)
	}
	if pendingAtBefore != 6 || svc3.Players[0].Mirror[Metal].Production != 6 {
		t.Fatalf("future deadline must not settle production, callback=%v live=%v", pendingAtBefore, svc3.Players[0].Mirror[Metal].Production)
	}
	// Helpers should have run even when deadline future
	if svc3.Players[0].Helper1Calls != 1 {
		t.Fatalf("helpers should run even when deadline future")
	}
}

// TestPerTickHelpersNeverTouchStock verifies helpers don't mutate stock.
func TestPerTickHelpersNeverTouchStock(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].Stock[Metal] = 100
	svc.Players[0].Stock[Energy] = 200
	svc.Players[0].UpdateTime = 1000 // far future so settlement never runs
	svc.Players[0].Helper1Deadline = 0
	svc.Players[0].Helper2Deadline = 0
	w := units.New(10, nil)
	def := &content.UnitDef{}
	def.MaxDamage = 100
	_, _ = w.Create(def, 0, 0, 0, 0)
	svc.TickPlayer(0, 0, w, nil)
	if svc.Players[0].Stock[Metal] != 100 || svc.Players[0].Stock[Energy] != 200 {
		t.Fatalf("helpers must never touch stock, got %v %v", svc.Players[0].Stock[Metal], svc.Players[0].Stock[Energy])
	}
	if svc.Players[0].Helper1Calls != 1 || svc.Players[0].Helper2Calls != 1 || svc.Players[0].WeaponRefreshCalls != 1 {
		t.Fatalf("helpers and sweep should have run")
	}
}

// TestSeedingSemantics verifies C5 seeding.
func TestSeedingSemantics(t *testing.T) {
	var svc Service
	// Setup two active players, one inactive
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 999
	svc.Players[0].WinLoseTime = 999
	svc.Players[0].DisplayTimer = 999
	svc.Players[0].Helper1Deadline = 999
	svc.Players[0].Helper2Deadline = 999

	activePlayer(&svc.Players[1])
	svc.Players[1].UpdateTime = 888
	svc.Players[1].WinLoseTime = 888
	svc.Players[1].DisplayTimer = 888
	svc.Players[1].ControllerState = 2
	svc.Players[1].Helper1Deadline = 888

	// Inactive player 2
	svc.Players[2].Exists = false
	svc.Players[2].UpdateTime = 777

	svc.SeedDeadlines(42)
	if svc.Players[0].UpdateTime != 42 || svc.Players[0].WinLoseTime != 42 || svc.Players[0].DisplayTimer != 42 {
		t.Fatalf("seed active player 0: want 42 got %d %d %d", svc.Players[0].UpdateTime, svc.Players[0].WinLoseTime, svc.Players[0].DisplayTimer)
	}
	if svc.Players[1].UpdateTime != 42 || svc.Players[1].WinLoseTime != 42 || svc.Players[1].DisplayTimer != 42 {
		t.Fatalf("seed active player 1")
	}
	if svc.Players[2].UpdateTime != 777 {
		t.Fatalf("inactive player should not be seeded, got %d", svc.Players[2].UpdateTime)
	}
	if svc.Players[0].Helper1Deadline != 42 || svc.Players[0].Helper2Deadline != 42 {
		t.Fatalf("helpers should be seeded as well")
	}
	// Mission setup: one full player-phase pass before starting resources
	// Seed then run Tick at same tick should settle for all active players once and advance deadlines by 30.
	var svc2 Service
	activePlayer(&svc2.Players[0])
	activePlayer(&svc2.Players[1])
	svc2.Players[0].Mirror[Metal] = Bucket{Production: 2, Requested: 1, Carry: 4}
	svc2.Players[1].Mirror[Metal] = Bucket{Production: 3, Requested: 2, Carry: 5}
	svc2.SeedDeadlines(10)
	w := units.New(10, nil)
	svc2.Tick(10, w) // full pass at tick 10: deadlines 10 -> 40 and settle
	if svc2.Players[0].UpdateTime != 40 || svc2.Players[1].UpdateTime != 40 {
		t.Fatalf("after initial pass deadlines should be 40 (10+30), got %d %d", svc2.Players[0].UpdateTime, svc2.Players[1].UpdateTime)
	}
	for i, want := range []float32{2, 3} {
		p := &svc2.Players[i]
		if p.Mirror[Metal].Production != 0 || p.Mirror[Metal].Requested != 0 || p.PassProduced[Metal] != want || p.PassConsumed[Metal] != want-1 || p.Mirror[Metal].Carry == 0 {
			t.Fatalf("initial seeded pass player %d did not consume/archive production and preserve debt: mirror=%+v pass=(%v,%v)", i, p.Mirror[Metal], p.PassProduced[Metal], p.PassConsumed[Metal])
		}
	}
	// Spawn credits outside ledger
	CreditSpawn(&svc2.Players[0], Metal, 1000)
	if svc2.Players[0].Stock[Metal] != 1000 {
		t.Fatalf("spawn credit outside ledger")
	}
	// Save/Load verbatim: after the pass deadlines are 40, saved state should contain 40, not re-seeded to 10.
	saved := svc2.SaveState()
	if saved[0].UpdateTime != 40 {
		t.Fatalf("SaveState after pass should be 40, got %d", saved[0].UpdateTime)
	}
	// mutate and restore
	svc2.Players[0].UpdateTime = 9999
	svc2.LoadState(saved)
	if svc2.Players[0].UpdateTime != 40 {
		t.Fatalf("LoadState should restore verbatim 40, got %d", svc2.Players[0].UpdateTime)
	}
	// Load should not re-seed; it restores exactly saved values.
	// Note: only UpdateTime advances via settlement cadence; sibling deadlines remain at seed value 10 until their own systems advance them.
	saved2 := svc2.SaveState()
	if saved2[0].UpdateTime != 40 || saved2[0].WinLoseTime != 10 || saved2[0].DisplayTimer != 10 {
		t.Fatalf("SaveState verbatim after load, got %v", saved2[0])
	}
	// Also verify seeding directly without a pass persists verbatim seed value.
	var svc3 Service
	activePlayer(&svc3.Players[0])
	svc3.SeedDeadlines(99)
	saved3 := svc3.SaveState()
	if saved3[0].UpdateTime != 99 || saved3[0].WinLoseTime != 99 || saved3[0].DisplayTimer != 99 {
		t.Fatalf("SaveState after seed without pass should be 99")
	}
}

// TestShareCadences verifies C12 cadences 60 and 450.
func TestShareCadences(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	activePlayer(&svc.Players[1])
	svc.ReferencePlayer = 0
	svc.Players[0].AutoShareMetal = true
	svc.Players[0].AutoShareEnergy = true
	svc.Players[0].AutoShareSensor = true
	svc.Players[0].MetalShareThreshold = 50
	svc.Players[0].EnergyShareThreshold = 50
	svc.Players[0].Stock[Metal] = 200
	svc.Players[0].Stock[Energy] = 200
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Capacity[Energy] = 1000
	svc.Players[0].Allies[1] = true
	svc.Players[1].Stock[Metal] = 10
	svc.Players[1].Stock[Energy] = 10
	svc.Players[1].Capacity[Metal] = 1000
	svc.Players[1].Capacity[Energy] = 1000

	// tick 60 should transfer (60%60==0)
	svc.ShareTick(60)
	if svc.Players[0].Stock[Metal] >= 200 {
		t.Fatalf("metal share at tick 60 should have transferred, stock %v", svc.Players[0].Stock[Metal])
	}
	if svc.Players[1].Stock[Metal] <= 10 {
		t.Fatalf("metal share dst should have increased")
	}
	// Reset stocks
	svc.Players[0].Stock[Metal] = 200
	svc.Players[1].Stock[Metal] = 10
	svc.Players[0].Stock[Energy] = 200
	svc.Players[1].Stock[Energy] = 10
	svc.SensorShareCalls = 0
	// tick 61 should NOT transfer
	svc.ShareTick(61)
	if svc.Players[0].Stock[Metal] != 200 {
		t.Fatalf("tick 61 should not transfer metal")
	}
	if svc.SensorShareCalls != 0 {
		t.Fatalf("tick 61 should not sensor share")
	}
	// tick 450 should do sensor only (450%60=30 so not metal, 450%450=0 sensor)
	svc.Players[0].Stock[Metal] = 200
	svc.Players[1].Stock[Metal] = 10
	svc.Players[0].Stock[Energy] = 200
	svc.Players[1].Stock[Energy] = 10
	svc.SensorShareCalls = 0
	svc.ShareTick(450)
	if svc.Players[0].Stock[Metal] != 200 {
		t.Fatalf("450 should NOT trigger metal share (450%%60=30), stock %v", svc.Players[0].Stock[Metal])
	}
	if svc.SensorShareCalls != 1 {
		t.Fatalf("450 should trigger sensor share, got %d", svc.SensorShareCalls)
	}
	// tick 900 should trigger both (900%60==0 and 900%450==0)
	svc.Players[0].Stock[Metal] = 200
	svc.Players[1].Stock[Metal] = 10
	svc.Players[0].Stock[Energy] = 200
	svc.Players[1].Stock[Energy] = 10
	svc.SensorShareCalls = 0
	svc.ShareTick(900)
	if svc.Players[0].Stock[Metal] >= 200 {
		t.Fatalf("900 should trigger metal share")
	}
	if svc.SensorShareCalls != 1 {
		t.Fatalf("900 should trigger sensor share, got %d", svc.SensorShareCalls)
	}
	// tick 120 should transfer (multiple of 60) but not sensor (120%450 !=0)
	svc.Players[0].Stock[Metal] = 200
	svc.Players[1].Stock[Metal] = 10
	svc.SensorShareCalls = 0
	svc.ShareTick(120)
	if svc.Players[0].Stock[Metal] >= 200 {
		t.Fatalf("120 should transfer")
	}
	if svc.SensorShareCalls != 0 {
		t.Fatalf("120 should not sensor share")
	}
	// tick 0 should transfer (0%60==0, 0%450==0) => both
	svc.Players[0].Stock[Metal] = 200
	svc.Players[1].Stock[Metal] = 10
	svc.Players[0].Stock[Energy] = 200
	svc.Players[1].Stock[Energy] = 10
	svc.SensorShareCalls = 0
	svc.ShareTick(0)
	if svc.Players[0].Stock[Metal] >= 200 {
		t.Fatalf("0 should transfer")
	}
	if svc.SensorShareCalls != 1 {
		t.Fatalf("0 should sensor share")
	}
}

// TestShareTransferUsesLedger verifies ShareTick uses ShareTransfer (live stock between passes)
func TestShareTransferUsesLedger(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	activePlayer(&svc.Players[1])
	svc.ReferencePlayer = 0
	svc.Players[0].AutoShareMetal = true
	svc.Players[0].MetalShareThreshold = 0
	svc.Players[0].Stock[Metal] = 100
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Allies[1] = true
	svc.Players[1].Stock[Metal] = 0
	svc.Players[1].Capacity[Metal] = 10 // small gap limits transfer
	// excess =100-0=100, ratio 0.333 =>33.33, gap=10 => transfer 10
	svc.ShareTick(60)
	if svc.Players[0].Stock[Metal] != 90 {
		t.Fatalf("transfer should be min(gap, excess*ratio)=10, src stock 90 got %v", svc.Players[0].Stock[Metal])
	}
	if svc.Players[1].Stock[Metal] != 10 {
		t.Fatalf("dst stock 10 got %v", svc.Players[1].Stock[Metal])
	}
}

// TestTickLoopsPlayersAscending verifies I1 deterministic iteration.
func TestTickLoopsPlayersAscending(t *testing.T) {
	var svc Service
	for i := 0; i < 10; i++ {
		activePlayer(&svc.Players[i])
		svc.Players[i].UpdateTime = 0
		svc.Players[i].Mirror[Metal].Production = float32(i + 1)
	}
	w := units.New(10, nil)
	svc.Tick(0, w)
	for i := 0; i < 10; i++ {
		if svc.Players[i].UpdateTime != 30 {
			t.Fatalf("Tick should visit active players 0..9, player %d deadline=%d", i, svc.Players[i].UpdateTime)
		}
		if svc.Players[i].Mirror[Metal].Production != 0 || svc.Players[i].PassProduced[Metal] != float32(i+1) {
			t.Fatalf("Tick must settle player %d in the ascending player pass", i)
		}
	}
}

// TestTickWithNilWorld validates sweep still increments even with nil world (no stock touch).
func TestTickWithNilWorld(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 0
	svc.Players[0].Stock[Metal] = 50
	svc.Players[0].Mirror[Metal] = Bucket{Production: 7, Requested: 3, Carry: 2}
	svc.TickPlayer(0, 0, nil, nil)
	if svc.Players[0].WeaponRefreshCalls != 1 {
		t.Fatalf("weapon refresh should run even with nil world")
	}
	if svc.Players[0].Stock[Metal] != 55 {
		t.Fatalf("nil world settlement should apply production and debt, stock=%v want 55", svc.Players[0].Stock[Metal])
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[0].Mirror[Metal].Requested != 0 || svc.Players[0].Mirror[Metal].Carry != 0 || svc.Players[0].PassProduced[Metal] != 7 || svc.Players[0].PassConsumed[Metal] != 3 {
		t.Fatalf("nil world settlement must consume/archive pending ledger state: mirror=%+v pass=(%v,%v)", svc.Players[0].Mirror[Metal], svc.Players[0].PassProduced[Metal], svc.Players[0].PassConsumed[Metal])
	}
}
