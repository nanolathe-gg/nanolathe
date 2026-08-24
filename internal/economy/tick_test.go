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
	var settleCalls []uint32
	svc.OnSettle = func(p int, tick uint32) {
		settleCalls = append(settleCalls, tick)
	}
	w := units.New(10, nil)
	// Tick 0 should settle (deadline 0 <=0) and advance to 30
	svc.TickPlayer(0, 0, w, nil)
	if len(settleCalls) != 1 || settleCalls[0] != 0 {
		t.Fatalf("C2: at tick 0 deadline 0 should settle once, got %v", settleCalls)
	}
	if svc.Players[0].UpdateTime != 30 {
		t.Fatalf("C2: after tick 0 deadline should be 30, got %d", svc.Players[0].UpdateTime)
	}
	// Ticks 1..29 should not settle
	settleCalls = nil
	for tick := uint32(1); tick < 30; tick++ {
		svc.TickPlayer(0, tick, w, nil)
		if len(settleCalls) != 0 {
			t.Fatalf("C2: tick %d should not settle, got %v", tick, settleCalls)
		}
		if svc.Players[0].UpdateTime != 30 {
			t.Fatalf("C2: deadline should remain 30 at tick %d, got %d", tick, svc.Players[0].UpdateTime)
		}
	}
	// Tick 30 should settle again and advance to 60
	settleCalls = nil
	svc.TickPlayer(0, 30, w, nil)
	if len(settleCalls) != 1 {
		t.Fatalf("C2: tick 30 should settle")
	}
	if svc.Players[0].UpdateTime != 60 {
		t.Fatalf("C2: after tick 30 deadline should be 60, got %d", svc.Players[0].UpdateTime)
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
	var settles int
	svc.OnSettle = func(p int, tk uint32) { settles++ }
	w := units.New(10, nil)
	// Simulate ticks 200.. until caught up
	expectedDeadlines := []uint32{130, 160, 190, 220}
	for i, wantDeadline := range expectedDeadlines {
		settles = 0
		curTick := tick + uint32(i)
		svc.TickPlayer(0, curTick, w, nil)
		if settles != 1 {
			t.Fatalf("C2 catch-up: tick %d deadline 100 behind should settle once per tick, got %d settles at iteration %d", curTick, settles, i)
		}
		if svc.Players[0].UpdateTime != wantDeadline {
			t.Fatalf("C2 catch-up: after tick %d deadline want %d got %d", curTick, wantDeadline, svc.Players[0].UpdateTime)
		}
	}
	// Next tick after catch-up: deadline 220 at tick 204? 220 >204 should not settle? Wait we advanced tick linearly, but deadline after 4 ticks is 220, which is > tick 203? Let's continue ticks until deadline exceeds tick.
	// At tick 204, deadline 220 >204 so should NOT settle until tick 220.
	settles = 0
	svc.TickPlayer(0, 204, w, nil)
	if settles != 0 {
		t.Fatalf("C2 catch-up: tick 204 deadline 220 should not settle")
	}
	if svc.Players[0].UpdateTime != 220 {
		t.Fatalf("deadline should stay 220 at tick 204, got %d", svc.Players[0].UpdateTime)
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
	settles := 0
	svc.OnSettle = func(p int, tk uint32) { settles++ }
	w := units.New(10, nil)
	// Deadline 0 at tick 100 is 100 behind, should settle once per tick until caught up (single add per tick, not loop).
	// Ticks 100,101,102,103 should settle (deadlines 0->30->60->90->120), tick 104 should NOT settle as deadline 120 >104.
	for i := 0; i < 4; i++ {
		settles = 0
		tk := curTick + uint32(i)
		wantDeadline := svc.Players[0].UpdateTime
		svc.TickPlayer(0, tk, w, nil)
		if settles != 1 {
			t.Fatalf("100 behind: tick %d should settle once, got %d", tk, settles)
		}
		if svc.Players[0].UpdateTime != wantDeadline+30 {
			t.Fatalf("deadline should advance exactly 30: was %d now %d", wantDeadline, svc.Players[0].UpdateTime)
		}
	}
	// Next tick 104 deadline 120 >104 should not settle, single add ensures catch-up per tick.
	settles = 0
	svc.TickPlayer(0, curTick+4, w, nil)
	if settles != 0 {
		t.Fatalf("tick %d deadline 120 should NOT settle", curTick+4)
	}
	if svc.Players[0].UpdateTime != 120 {
		t.Fatalf("deadline should stay 120 at tick 104, got %d", svc.Players[0].UpdateTime)
	}
}

// TestUnsignedComparisonWrap verifies C2 unsigned comparison across 2^32 wrap.
func TestUnsignedComparisonWrap(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	w := units.New(10, nil)
	var settles int
	svc.OnSettle = func(p int, tk uint32) { settles++ }
	// Case A: deadline near max, tick small (0): deadline > tick unsigned => not due => no settle, deadline frozen.
	svc.Players[0].UpdateTime = 0xFFFFFFF0 // 4294967280
	svc.Players[0].Helper1Deadline = 0xFFFFFFFF
	svc.Players[0].Helper2Deadline = 0xFFFFFFFF
	settles = 0
	svc.TickPlayer(0, 5, w, nil)
	if settles != 0 {
		t.Fatalf("unsigned wrap A: deadline 0xFFFFFFF0 > tick 5 should NOT settle (unsigned), got settle")
	}
	if svc.Players[0].UpdateTime != 0xFFFFFFF0 {
		t.Fatalf("unsigned wrap A: deadline should stay 0xFFFFFFF0, got %08x", svc.Players[0].UpdateTime)
	}
	// Case B: deadline small, tick near max: deadline (5) not > tick (0xFFFFFFFE) => due => settle and advance.
	svc.Players[0].UpdateTime = 5
	settles = 0
	svc.TickPlayer(0, 0xFFFFFFFE, w, nil) // 4294967294
	if settles != 1 {
		t.Fatalf("unsigned wrap B: deadline 5 <= tick 0xFFFFFFFE unsigned => should settle, got %d", settles)
	}
	if svc.Players[0].UpdateTime != 35 { // 5+30
		t.Fatalf("wrap B: deadline should be 35, got %d", svc.Players[0].UpdateTime)
	}
	// Case C: deadline = 0xFFFFFFFF, tick =0 => deadline > tick true => not due
	svc.Players[0].UpdateTime = 0xFFFFFFFF
	settles = 0
	svc.TickPlayer(0, 0, w, nil)
	if settles != 0 {
		t.Fatalf("wrap C: deadline 0xFFFFFFFF >0 should not settle")
	}
	// Case D: deadline =0, tick=0xFFFFFFFF => deadline <= tick => due (wrap single add)
	svc.Players[0].UpdateTime = 0
	settles = 0
	svc.TickPlayer(0, 0xFFFFFFFF, w, nil)
	if settles != 1 {
		t.Fatalf("wrap D: deadline 0 <= 0xFFFFFFFF should settle")
	}
	if svc.Players[0].UpdateTime != 30 {
		t.Fatalf("wrap D deadline advance to 30, got %d", svc.Players[0].UpdateTime)
	}
	// Ensure helpers also use unsigned; not critical but check helper wrap similarly
	svc.Players[0].Helper1Deadline = 0xFFFFFFF0
	svc.Players[0].Helper1Calls = 0
	settles = 0
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
	var settles int
	svc.OnSettle = func(p int, tk uint32) { settles++ }
	var beforeCalled bool
	svc.TickPlayer(0, 200, w, func() { beforeCalled = true })
	if settles != 0 {
		t.Fatalf("skipped slot should not settle")
	}
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
	// Also test observer skip freezes deadline
	svc.Players[1].Exists = true
	svc.Players[1].ControllerState = 1
	svc.Players[1].IsObserver = true
	svc.Players[1].UpdateTime = 50
	svc.Players[1].Helper1Deadline = 50
	svc.Players[1].Helper2Deadline = 50
	settles = 0
	beforeCalled = false
	svc.TickPlayer(1, 100, w, func() { beforeCalled = true })
	if settles != 0 || beforeCalled || svc.Players[1].UpdateTime != 50 {
		t.Fatalf("observer skipped should freeze all, got settles %d before %v deadline %d", settles, beforeCalled, svc.Players[1].UpdateTime)
	}
	// Active state skip (state 0 not in {1,2,3})
	svc.Players[2].Exists = true
	svc.Players[2].ControllerState = 0
	svc.Players[2].UpdateTime = 70
	svc.Players[2].Helper1Deadline = 70
	settles = 0
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
			// Ensure deadline due
			p.UpdateTime = 100
			p.Helper1Deadline = 200 // far future to avoid extra helper increments affecting test clarity
			p.Helper2Deadline = 200
			p.WeaponRefreshCalls = 0
			var settles int
			svc.OnSettle = func(int, uint32) { settles++ }
			var beforeCalls int
			svc.TickPlayer(0, 100, w, func() { beforeCalls++ })
			if settles != 0 {
				t.Fatalf("%s: should block settlement, got settle", tc.name)
			}
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
		var settles int
		svc.OnSettle = func(int, uint32) { settles++ }
		svc.TickPlayer(0, 100, w, nil)
		if settles != 1 {
			t.Fatalf("all gates pass should settle")
		}
		if p.UpdateTime != 130 {
			t.Fatalf("deadline advance 100->130")
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
	var order []string
	svc.OnSettle = func(p int, tick uint32) { order = append(order, "settle") }
	// Capture helper calls at beforeDeadline time
	var helper1AtBefore, helper2AtBefore, weaponAtBefore int
	svc.TickPlayer(0, 100, w, func() {
		order = append(order, "beforeDeadline")
		helper1AtBefore = svc.Players[0].Helper1Calls
		helper2AtBefore = svc.Players[0].Helper2Calls
		weaponAtBefore = svc.Players[0].WeaponRefreshCalls
		// helpers should have already incremented
	})
	// Check ordering: helpers before beforeDeadline, beforeDeadline before settle
	if len(order) != 2 || order[0] != "beforeDeadline" || order[1] != "settle" {
		t.Fatalf("ordering: beforeDeadline should be after helpers and before settle, got %v", order)
	}
	if helper1AtBefore != 1 || helper2AtBefore != 1 {
		t.Fatalf("helpers should have run before beforeDeadline: h1=%d h2=%d", helper1AtBefore, helper2AtBefore)
	}
	if weaponAtBefore != 1 {
		t.Fatalf("weapon refresh should have run before beforeDeadline")
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
	var settle3 int
	svc3.OnSettle = func(int, uint32) { settle3++ }
	called = false
	svc3.TickPlayer(0, 100, w, func() { called = true })
	if !called {
		t.Fatalf("beforeDeadline should be called even when deadline not due (helpers before compare)")
	}
	if settle3 != 0 {
		t.Fatalf("should not settle when deadline future")
	}
	if svc3.Players[0].UpdateTime != 200 {
		t.Fatalf("deadline future should stay 200, got %d", svc3.Players[0].UpdateTime)
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
	svc2.SeedDeadlines(10)
	w := units.New(10, nil)
	var calls []int
	svc2.OnSettle = func(p int, tick uint32) { calls = append(calls, p) }
	svc2.Tick(10, w) // full pass at tick 10: deadlines 10 -> 40 and settle
	if len(calls) != 2 {
		t.Fatalf("initial pass after seeding should settle active players, got %v", calls)
	}
	if svc2.Players[0].UpdateTime != 40 || svc2.Players[1].UpdateTime != 40 {
		t.Fatalf("after initial pass deadlines should be 40 (10+30), got %d %d", svc2.Players[0].UpdateTime, svc2.Players[1].UpdateTime)
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
	}
	w := units.New(10, nil)
	var order []int
	svc.OnSettle = func(p int, tick uint32) { order = append(order, p) }
	svc.Tick(0, w)
	for i := 0; i < 10; i++ {
		if order[i] != i {
			t.Fatalf("Tick order should be 0..9 ascending, got %v", order)
		}
	}
}

// TestSettleHookDefaultNoop verifies Settle without hook is no-op and doesn't panic.
func TestSettleHookDefaultNoop(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 0
	svc.OnSettle = nil
	w := units.New(10, nil)
	// Should not panic
	svc.TickPlayer(0, 0, w, nil)
	// Deadline should have advanced even with no-op settle
	if svc.Players[0].UpdateTime != 30 {
		t.Fatalf("deadline advance without settle hook")
	}
}

// TestTickWithNilWorld validates sweep still increments even with nil world (no stock touch).
func TestTickWithNilWorld(t *testing.T) {
	var svc Service
	activePlayer(&svc.Players[0])
	svc.Players[0].UpdateTime = 0
	svc.Players[0].Stock[Metal] = 50
	var settles int
	svc.OnSettle = func(int, uint32) { settles++ }
	svc.TickPlayer(0, 0, nil, nil)
	if settles != 1 {
		t.Fatalf("nil world should still settle")
	}
	if svc.Players[0].WeaponRefreshCalls != 1 {
		t.Fatalf("weapon refresh should run even with nil world")
	}
	if svc.Players[0].Stock[Metal] != 50 {
		t.Fatalf("nil world sweep must not touch stock")
	}
}
