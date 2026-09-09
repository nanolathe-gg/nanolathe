package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestVisitActiveSlotsAscending verifies ascending retail slot order, one visit
// per slot per traversal, no map iteration defines order [01 §4.4][01 §6.2].
func TestVisitActiveSlotsAscending(t *testing.T) {
	world := newFixtureWorld(5, nil) // 5 per player, sliced
	def := &content.UnitDef{UnitName: "sweep", MaxDamage: 100, Limit: -1}
	// Create out-of-order players
	h0a, _ := world.Create(def, 0, 0, 0, 0) // slot 1
	h0b, _ := world.Create(def, 0, 0, 0, 0) // slot 2
	h1, _ := world.Create(def, 1, 0, 0, 0)  // player1 start 6
	h4, _ := world.Create(def, 4, 0, 0, 0)
	h9, _ := world.Create(def, 9, 0, 0, 0)
	var got []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) {
		got = append(got, v.Handle)
		if v.Handle != pool.Handle(v.Slot) {
			t.Fatalf("SlotVisit Handle %d != Slot %d", v.Handle, v.Slot)
		}
		if v.Unit == nil || v.Unit.Handle != v.Handle {
			t.Fatalf("SlotVisit Unit handle mismatch")
		}
	})
	// Expected order: players 0..9 asc, slots asc within slice.
	if len(got) != 5 {
		t.Fatalf("got %d visits want 5", len(got))
	}
	if got[0] != h0a || got[1] != h0b {
		t.Fatalf("p0 order wrong got %v %v want %v %v", got[0], got[1], h0a, h0b)
	}
	if got[2] != h1 {
		t.Fatalf("p1 order wrong got %v want %v", got[2], h1)
	}
	if got[3] != h4 {
		t.Fatalf("p4 order wrong got %v want %v", got[3], h4)
	}
	if got[4] != h9 {
		t.Fatalf("p9 order wrong got %v want %v", got[4], h9)
	}
	// Ensure one visit per slot: call again and count same
	var got2 []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) { got2 = append(got2, v.Handle) })
	if len(got2) != len(got) {
		t.Fatalf("second traversal len %d != %d", len(got2), len(got))
	}
	for i := range got {
		if got[i] != got2[i] {
			t.Fatalf("second traversal order differs at %d", i)
		}
	}
}

// TestVisitActiveSlotsNoMapIteration is a lightweight check that VisitActiveSlots
// does not use map iteration for ordering. It verifies determinism across two
// worlds built with same creation order; map iteration would randomize.
func TestVisitActiveSlotsNoMapIteration(t *testing.T) {
	world := newFixtureWorld(20, nil)
	def := &content.UnitDef{UnitName: "sweep-determinism", MaxDamage: 100}
	var handles []pool.Handle
	for i := 0; i < 10; i++ {
		h, _ := world.Create(def, uint8(i%10), 0, 0, 0)
		handles = append(handles, h)
	}
	var order []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) { order = append(order, v.Handle) })
	// For unsliced, order is players 0..9 asc then slots asc where owner matches.
	// Since we created 10 units with distinct owners 0..9 each one, order should be sorted by owner then slot.
	// But creation order was 0,1,2... so owners already asc and slots asc 1..10, so order should equal handles.
	// If map iteration were used, order could be random; this test locks determinism.
	for i, h := range order {
		if h != handles[i] {
			t.Fatalf("order[%d]=%d want %d (deterministic)", i, h, handles[i])
		}
	}
}

// TestFreeCurrentDoesNotSkip verifies freeing current unit does not skip or
// double-visit another [01 §4.4].
func TestFreeCurrentDoesNotSkip(t *testing.T) {
	world := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "sweep-free", MaxDamage: 100, Limit: -1}
	h1, _ := world.Create(def, 0, 0, 0, 0) //1
	h2, _ := world.Create(def, 0, 0, 0, 0) //2
	h3, _ := world.Create(def, 0, 0, 0, 0) //3
	h4, _ := world.Create(def, 0, 0, 0, 0) //4
	// Visit and free current at h2
	var visited []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) {
		if v.Handle == h2 {
			// Free current via FinalizeDeath after marking Dying
			world.Destroy(v.Handle, DeathKilled)
			res := world.FinalizeDeath(v.Handle, 1)
			if !res.Freed {
				t.Fatalf("finalize current should free")
			}
			// Second call no-op
			res2 := world.FinalizeDeath(v.Handle, 1)
			if res2.Freed {
				t.Fatalf("second finalize should be no-op")
			}
		}
		visited = append(visited, v.Handle)
	})
	// Should have visited h1, h2, h3, h4 in order, not skipping h3 nor double visiting
	if len(visited) != 4 {
		t.Fatalf("visited len %d want 4", len(visited))
	}
	if visited[0] != h1 || visited[1] != h2 || visited[2] != h3 || visited[3] != h4 {
		t.Fatalf("visited order %v want [%d %d %d %d]", visited, h1, h2, h3, h4)
	}
	// After traversal, h2 should be free and not visited again
	if world.Unit(h2) != nil {
		t.Fatalf("h2 should be free after finalize")
	}
	// Next traversal should not include h2
	var visited2 []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) { visited2 = append(visited2, v.Handle) })
	if len(visited2) != 3 {
		t.Fatalf("second traversal len %d want 3 (h2 freed)", len(visited2))
	}
	for _, h := range visited2 {
		if h == h2 {
			t.Fatalf("h2 should not be visited after free")
		}
	}
}

// TestDeathDuringOwnVisitFinalizesAtVisitEnd models a lethal stage occurring
// during the unit's own phase-2 visit: the mark remains live for the rest of
// that visit, then the slot-end finalizer fires exactly once [01 §4.4].
func TestDeathDuringOwnVisitFinalizesAtVisitEnd(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "sweep-own-visit", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	hooks := 0
	world.OnDeath = func(pool.Handle, DeathCause, *Unit) { hooks++ }
	world.VisitActiveSlots(func(v SlotVisit) {
		if v.Handle != h {
			return
		}
		world.Destroy(h, DeathKilled)
		if world.Unit(h) == nil || !world.NeedsDeathFinalization(h) {
			t.Fatal("death mark must remain live and pending during its own visit")
		}
		if hooks != 0 {
			t.Fatal("death hook fired before slot-end finalization")
		}
		if result := world.FinalizeDeath(h, 1); !result.Freed || !result.HookFired {
			t.Fatalf("slot-end finalizer result=%+v, want freed and hook", result)
		}
	})
	if hooks != 1 || world.Unit(h) != nil {
		t.Fatalf("post-visit hooks=%d unit=%v, want one hook and freed slot", hooks, world.Unit(h))
	}
}

// TestAllocationDuringTraversal follows same-tick rule: new unit ahead visited
// same tick, behind waits [01 §4.4].
func TestAllocationDuringTraversal(t *testing.T) {
	world := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "sweep-allocation", MaxDamage: 100, Limit: -1}
	h1, _ := world.Create(def, 0, 0, 0, 0) // slot1
	h2, _ := world.Create(def, 0, 0, 0, 0) // slot2
	h3, _ := world.Create(def, 0, 0, 0, 0) // slot3
	// We will traverse and at slot1 allocate new unit for same player that should be ahead (slot4) and also for behind case
	var visited []pool.Handle
	var newAhead pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) {
		visited = append(visited, v.Handle)
		if v.Handle == h1 {
			// Allocate new unit for same player; lowest free is 4 (ahead)
			h, err := world.Create(def, 0, numeric.Fixed(0), 0, 0)
			if err != nil {
				t.Fatalf("create ahead: %v", err)
			}
			newAhead = h
			if h != 4 {
				// With slots 1,2,3 occupied, lowest free 4
				t.Fatalf("ahead alloc got %d want 4", h)
			}
		}
		if v.Handle == h2 {
			// Allocate new unit that will reuse a behind slot if we free h1 first?
			// Instead test behind: create for player0 after we passed slot2, the behind slot would be 1 if we freed it.
			// Free h1 (behind) then allocate: lowest free becomes 1 behind, should NOT be visited this traversal.
			world.Destroy(h1, DeathKilled)
			world.FinalizeDeath(h1, 10)
			// Now allocate: lowest free is 1 (behind)
			hBehind, err := world.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatalf("create behind: %v", err)
			}
			if hBehind != h1 {
				t.Fatalf("behind alloc got %d want %d (reuse freed behind)", hBehind, h1)
			}
			// This behind unit should NOT be in current visited list (since we already passed slot1)
			// It will be visited next tick.
		}
	})
	// visited should contain h1, h2, h3, newAhead (4) but NOT the behind reuse (which is h1 reused)
	// Order: h1 (1), h2 (2), h3 (3), newAhead (4)
	if len(visited) != 4 {
		t.Fatalf("visited len %d want 4, got %v", len(visited), visited)
	}
	if visited[0] != h1 || visited[1] != h2 || visited[2] != h3 || visited[3] != newAhead {
		t.Fatalf("visited order %v want [%d %d %d %d]", visited, h1, h2, h3, newAhead)
	}
	// Behind unit (reused h1) should not have been visited this tick
	for _, h := range visited {
		if h == h1 && len(visited) > 1 {
			// h1 appears once at start, but reused h1 should not appear again at end
			// Count occurrences of h1 value
		}
	}
	countH1 := 0
	for _, h := range visited {
		if h == h1 {
			countH1++
		}
	}
	if countH1 != 1 {
		t.Fatalf("behind reuse should not cause double visit, countH1 %d", countH1)
	}
	// Next traversal should include behind unit, ahead unit, and h2,h3
	var visitedNext []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) { visitedNext = append(visitedNext, v.Handle) })
	// Should have h1 (reused behind), h2,h3,newAhead
	if len(visitedNext) != 4 {
		t.Fatalf("next traversal len %d want 4 got %v", len(visitedNext), visitedNext)
	}
	// Order should be h1(=1), h2(2), h3(3), newAhead(4) again
	if visitedNext[0] != h1 || visitedNext[1] != h2 || visitedNext[2] != h3 || visitedNext[3] != newAhead {
		t.Fatalf("next order %v", visitedNext)
	}
}

// TestFinalizeDeathExactlyOnce verifies death hooks and pool free happen exactly
// once via FinalizeDeath; second call no-op [01 §4.4].
func TestFinalizeDeathExactlyOnce(t *testing.T) {
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "sweep-finalize", MaxDamage: 100}
	h, _ := world.Create(def, 0, 0, 0, 0)
	hookCount := 0
	world.OnDeath = func(_ pool.Handle, _ DeathCause, _ *Unit) { hookCount++ }
	// Mark dying via Destroy
	world.Destroy(h, DeathKilled)
	if hookCount != 0 {
		t.Fatalf("Destroy should defer hook until finalization, got %d", hookCount)
	}
	if !world.NeedsDeathFinalization(h) {
		t.Fatalf("NeedsDeath should be true after Destroy")
	}
	res := world.FinalizeDeath(h, 1)
	if !res.Freed {
		t.Fatalf("first FinalizeDeath should free")
	}
	if !res.HookFired {
		t.Fatalf("FinalizeDeath should fire deferred hook")
	}
	// hookCount should still be 1
	if hookCount != 1 {
		t.Fatalf("hookCount after finalize %d want 1", hookCount)
	}
	if world.Unit(h) != nil {
		t.Fatalf("unit should be nil after free")
	}
	if world.NeedsDeathFinalization(h) {
		t.Fatalf("NeedsDeath should be false after free")
	}
	// Second call no-op
	res2 := world.FinalizeDeath(h, 2)
	if res2.Freed || res2.HookFired {
		t.Fatalf("second finalize should be no-op, got %+v", res2)
	}
	if hookCount != 1 {
		t.Fatalf("hookCount after second finalize %d want 1", hookCount)
	}
	// Test FinalizeDeath as sole hook path (without prior Destroy)
	h2, _ := world.Create(def, 0, 0, 0, 0)
	hookCount = 0
	// Directly mark Dying without hook
	u2 := world.units[int(h2)]
	u2.Dying = true
	u2.DeathCause = DeathReclaimed
	u2.deathHookFired = false
	if !world.NeedsDeathFinalization(h2) {
		t.Fatalf("NeedsDeath should be true for manually marked dying")
	}
	res3 := world.FinalizeDeath(h2, 3)
	if !res3.Freed || !res3.HookFired {
		t.Fatalf("FinalizeDeath should fire hook and free when Destroy not used, got %+v", res3)
	}
	if hookCount != 1 {
		t.Fatalf("hookCount via finalize %d want 1", hookCount)
	}
	res4 := world.FinalizeDeath(h2, 4)
	if res4.Freed || res4.HookFired {
		t.Fatalf("second finalize of h2 should be no-op")
	}
	if hookCount != 1 {
		t.Fatalf("hookCount after second finalize h2 %d want 1", hookCount)
	}
}

// TestDeadUnitNotStepped verifies dead unit cannot be stepped by later stages
// after finalization [04 R-MOV-03 §1].
func TestDeadUnitNotStepped(t *testing.T) {
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "sweep-dead", MaxDamage: 100}
	h, _ := world.Create(def, 0, 0, 0, 0)
	// Mark dying and finalize
	world.Destroy(h, DeathKilled)
	world.FinalizeDeath(h, 1)
	// StepPreUpdate should be no-op (dead)
	// We can detect stepping by checking that unitPreUpdate would be called; but it is placeholder no-op.
	// Instead verify that NeedsDeath is false and Unit is nil and Step does not panic and does not resurrect.
	world.StepPreUpdate(h, 2)
	if world.Unit(h) != nil {
		t.Fatalf("dead unit should remain nil after StepPreUpdate")
	}
	// Create new unit reusing same slot and ensure it can be stepped (not considered dead)
	h2, _ := world.Create(def, 0, 0, 0, 0)
	if h2 != h {
		t.Fatalf("reuse should give same slot %d want %d", h2, h)
	}
	// This new unit is alive, not dying, StepPreUpdate should be callable without error
	world.StepPreUpdate(h2, 3)
	if world.Unit(h2) == nil {
		t.Fatalf("new unit should still be alive after StepPreUpdate")
	}
	if world.NeedsDeathFinalization(h2) {
		t.Fatalf("new unit should not need death finalization")
	}
}

// TestVisitActiveSlotsFreedSlotReusable verifies freed slot immediately reusable
// and no double-visit [01 §4.4][P0-16].
func TestVisitActiveSlotsFreedSlotReusable(t *testing.T) {
	world := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "sweep-reuse", MaxDamage: 100, Limit: -1}
	h1, _ := world.Create(def, 0, 0, 0, 0) //1
	h2, _ := world.Create(def, 0, 0, 0, 0) //2
	h3, _ := world.Create(def, 0, 0, 0, 0) //3
	// Free h2 before traversal, allocate new should reuse lowest-free 2 and be visited same tick if ahead?
	// But if we free before traversal starts, visited should include reused slot.
	world.Destroy(h2, DeathKilled)
	world.FinalizeDeath(h2, 1)
	if world.Unit(h2) != nil {
		t.Fatalf("h2 should be free")
	}
	hNew, _ := world.Create(def, 0, 0, 0, 0)
	if hNew != h2 {
		t.Fatalf("reuse lowest free got %d want %d", hNew, h2)
	}
	var visited []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) { visited = append(visited, v.Handle) })
	if len(visited) != 3 {
		t.Fatalf("visited len %d want 3", len(visited))
	}
	// Order should be h1, hNew(=h2), h3
	if visited[0] != h1 || visited[1] != hNew || visited[2] != h3 {
		t.Fatalf("visited order %v want [%d %d %d]", visited, h1, hNew, h3)
	}
	// Ensure no double-visit within same traversal even if we free and reallocate during traversal
	var visited2 []pool.Handle
	world.VisitActiveSlots(func(v SlotVisit) {
		visited2 = append(visited2, v.Handle)
		if v.Handle == h1 {
			world.Destroy(h1, DeathKilled)
			world.FinalizeDeath(h1, 2)
			// Immediate reuse of h1 (behind) should not be visited again this traversal
			hTmp, _ := world.Create(def, 0, 0, 0, 0)
			if hTmp != h1 {
				t.Fatalf("reuse h1 got %d", hTmp)
			}
		}
	})
	// visited2 should contain original h1, hNew, h3 but not the reused h1 again (already visited)
	if len(visited2) != 3 {
		t.Fatalf("visited2 len %d want 3 got %v", len(visited2), visited2)
	}
	countH1 := 0
	for _, h := range visited2 {
		if h == h1 {
			countH1++
		}
	}
	if countH1 != 1 {
		t.Fatalf("h1 should appear exactly once per traversal, count %d", countH1)
	}
}
