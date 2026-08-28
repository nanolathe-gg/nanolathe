package path

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestSchedulerReplenishCadence(t *testing.T) {
	var captured []int32
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		captured = append(captured, scale)
		if budget != 100 {
			t.Fatalf("budget want 100 got %d", budget)
		}
		return nil, 0, false
	}
	s := NewScheduler(search, nil)
	s.SetBase(65536)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(captured) != 1 || captured[0] != 65536*6 {
		t.Fatalf("tick 0 scale want %d got %v", 65536*6, captured)
	}
	captured = nil
	for i := 2; i <= 12; i++ {
		s.Submit(Request{Unit: pool.Handle(i), Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	}
	s.Tick(1)
	if len(captured) != 12 {
		t.Fatalf("tick 1 want 12 calls got %d", len(captured))
	}
	for _, sc := range captured {
		if sc != 65536*6 {
			t.Fatalf("before replenish scale want 6x got %d", sc)
		}
	}
	captured = nil
	s.Tick(150)
	if len(captured) != 12 {
		t.Fatalf("tick 150 want 12 calls got %d", len(captured))
	}
	for _, sc := range captured {
		if sc != 65536*3 {
			t.Fatalf("after replenish scale want 3x (tier 1) got %d", sc)
		}
	}
	captured = nil
	s.Tick(151)
	for _, sc := range captured {
		if sc != 65536*3 {
			t.Fatalf("tick 151 should keep 3x got %d", sc)
		}
	}
	captured = nil
	s.Tick(299)
	for _, sc := range captured {
		if sc != 65536*3 {
			t.Fatalf("tick 299 should keep 3x got %d", sc)
		}
	}
	captured = nil
	s.Tick(300)
	for _, sc := range captured {
		if sc != 65536*3 {
			t.Fatalf("tick 300 still 3x got %d", sc)
		}
	}
}

func TestSchedulerQuantumWeighting(t *testing.T) {
	s := NewScheduler(nil, nil)
	s.SetBase(1000)
	for i := 1; i <= 5; i++ {
		s.Submit(Request{Unit: pool.Handle(i), Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	}
	for i := 6; i <= 20; i++ {
		s.Submit(Request{Unit: pool.Handle(i), Player: 1, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	}
	for i := 21; i <= 45; i++ {
		s.Submit(Request{Unit: pool.Handle(i), Player: 2, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	}
	scales := make(map[uint8]int32)
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		if budget != 100 {
			t.Fatalf("budget want 100 got %d", budget)
		}
		scales[r.Player] = scale
		return nil, 0, false
	}
	s.SetSearch(search)
	s.Tick(0)
	if scales[0] != 6000 {
		t.Fatalf("player 0 low pressure want 6000 got %d", scales[0])
	}
	if scales[1] != 3000 {
		t.Fatalf("player 1 medium pressure want 3000 got %d", scales[1])
	}
	if scales[2] != 1000 {
		t.Fatalf("player 2 high pressure want 1000 got %d", scales[2])
	}
}

func TestSchedulerPopBudgetEnforcement(t *testing.T) {
	var budgets []int
	calls := 0
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		budgets = append(budgets, budget)
		calls++
		if calls == 1 {
			if scale == 0 {
				t.Fatalf("scale should be non-zero")
			}
			return nil, 0, false
		}
		return []Point{{X: 1, Z: 1}, {X: 2, Z: 2}}, 0, true
	}
	var published [][]Point
	publish := func(r Request, pts []Point, st Status) {
		published = append(published, pts)
	}
	s := NewScheduler(search, publish)
	s.SetBase(DefaultBase)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(budgets) != 1 || budgets[0] != 100 {
		t.Fatalf("first call budget want 100 got %v", budgets)
	}
	if len(published) != 0 {
		t.Fatalf("should not publish on budget exhaustion, got %d publishes", len(published))
	}
	if s.Pending(0) != 1 {
		t.Fatalf("request should remain ACTIVE after budget exhaustion, pending %d", s.Pending(0))
	}
	s.Tick(1)
	if len(budgets) != 2 || budgets[1] != 100 {
		t.Fatalf("second call budget want 100 got %v", budgets)
	}
	if len(published) != 1 {
		t.Fatalf("second call should publish, got %d", len(published))
	}
	if len(published[0]) != 2 {
		t.Fatalf("published points want 2 got %d", len(published[0]))
	}
	if s.Pending(0) != 0 {
		t.Fatalf("request should be removed after completion, pending %d", s.Pending(0))
	}
}

func TestSchedulerDuplicateGoalOverwrite(t *testing.T) {
	s := NewScheduler(nil, nil)
	g1 := PointGoal(Cell{10, 10}, 0)
	g2 := PointGoal(Cell{99, 99}, 0)
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{0, 0}, Goal: g1})
	if s.Pending(2) != 1 {
		t.Fatalf("pending want 1 got %d", s.Pending(2))
	}
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{0, 0}, Goal: g2})
	if s.Pending(2) != 1 {
		t.Fatalf("duplicate should overwrite, not duplicate: pending %d", s.Pending(2))
	}
	if s.TotalPending() != 1 {
		t.Fatalf("total pending want 1 got %d", s.TotalPending())
	}
	var got Goal
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		got = r.Goal
		return []Point{{X: 1}}, 0, true
	}
	s.SetSearch(search)
	s.Tick(0)
	if got != g2 {
		t.Fatalf("duplicate goal should overwrite: got %v want %v", got, g2)
	}
	if s.Pending(2) != 0 {
		t.Fatalf("after tick pending should be 0 got %d", s.Pending(2))
	}
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{1, 1}, Goal: g1})
	s.Submit(Request{Unit: 5, Player: 3, Start: Cell{2, 2}, Goal: g2})
	if s.Pending(2) != 0 {
		t.Fatalf("move should remove from old player, pending2 %d", s.Pending(2))
	}
	if s.Pending(3) != 1 {
		t.Fatalf("move should add to new player, pending3 %d", s.Pending(3))
	}
}

func TestSchedulerFullOrEmpty(t *testing.T) {
	calls := 0
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		calls++
		if calls == 1 {
			return []Point{{X: 1, Z: 1}, {X: 2, Z: 2}}, 0, false
		}
		return []Point{{X: 10, Z: 10}, {X: 20, Z: 20}, {X: 30, Z: 30}}, 0, true
	}
	var publishes [][]Point
	publish := func(r Request, pts []Point, st Status) {
		cp := make([]Point, len(pts))
		copy(cp, pts)
		publishes = append(publishes, cp)
	}
	s := NewScheduler(search, publish)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(publishes) != 0 {
		t.Fatalf("full-or-empty: budget exhaustion must not publish partial, got %d publishes", len(publishes))
	}
	if s.Pending(0) != 1 {
		t.Fatalf("full-or-empty: request must stay ACTIVE after budget exhaustion")
	}
	if calls != 1 {
		t.Fatalf("calls want 1 got %d", calls)
	}
	s.Tick(1)
	if len(publishes) != 1 {
		t.Fatalf("second tick should publish exactly once got %d", len(publishes))
	}
	if len(publishes[0]) != 3 {
		t.Fatalf("published should be full 3 points, got %d", len(publishes[0]))
	}
	if publishes[0][0] != (Point{X: 10, Z: 10}) {
		t.Fatalf("published should be final full route, not partial prefix, got %v", publishes[0])
	}
	if s.Pending(0) != 0 {
		t.Fatalf("after completion pending should be 0")
	}
}

func TestSchedulerHeapExhaustionEmptyPublication(t *testing.T) {
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		if budget != 100 {
			t.Fatalf("budget want 100 got %d", budget)
		}
		return nil, StatusRejected, true
	}
	var published []struct {
		r  Request
		pt []Point
		st Status
	}
	publish := func(r Request, pts []Point, st Status) {
		published = append(published, struct {
			r  Request
			pt []Point
			st Status
		}{r, pts, st})
	}
	s := NewScheduler(search, publish)
	s.Submit(Request{Unit: 7, Player: 1, Start: Cell{0, 0}, Goal: PointGoal(Cell{50, 50}, 0)})
	s.Tick(10)
	if len(published) != 1 {
		t.Fatalf("heap exhaustion should publish once got %d", len(published))
	}
	if len(published[0].pt) != 0 {
		t.Fatalf("heap exhaustion should publish empty route, got %d points", len(published[0].pt))
	}
	if published[0].st != StatusRejected {
		t.Fatalf("heap exhaustion status want 0x200 got %#x", published[0].st)
	}
	if s.Pending(1) != 0 {
		t.Fatalf("heap exhaustion should remove request, pending %d", s.Pending(1))
	}
}

func TestSchedulerDefaultAndSetBase(t *testing.T) {
	s := NewScheduler(nil, nil)
	if s.ScaleFor(0) != DefaultBase*6 {
		t.Fatalf("scheduler should use DefaultBase when not set, got %d want %d", s.ScaleFor(0), DefaultBase*6)
	}
	s.SetBase(1000)
	if s.ScaleFor(0) != 6000 {
		t.Fatalf("scheduler SetBase should override, got %d", s.ScaleFor(0))
	}
}

func TestSchedulerHeapExhaustionViaEmptyHeap(t *testing.T) {
	// Verify that heap exhaustion path is distinct from budget exhaustion.
	// Budget exhaustion leaves active; heap exhaustion publishes empty.
	calls := 0
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		calls++
		// Simulate heap exhaustion after consuming budget fully but finding no path.
		return []Point{}, StatusRejected, true
	}
	var pubs int
	s := NewScheduler(search, func(r Request, pts []Point, st Status) { pubs++ })
	s.Submit(Request{Unit: 9, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{100, 100}, 0)})
	s.Tick(0)
	if pubs != 1 || calls != 1 {
		t.Fatalf("heap exhaustion should publish empty in one tick: pubs %d calls %d", pubs, calls)
	}
}

func TestSchedulerDeterministicPlayerOrder(t *testing.T) {
	var order []pool.Handle
	search := func(r Request, scale int32, budget int) ([]Point, Status, bool) {
		order = append(order, r.Unit)
		return []Point{{X: 1}}, 0, true
	}
	s := NewScheduler(search, func(r Request, pts []Point, st Status) {})
	// Submit out of order across players; Tick should process player 0..9 asc, and within player Unit asc.
	s.Submit(Request{Unit: 20, Player: 5, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 10, Player: 2, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 30, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 15, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Tick(0)
	// Expected order: player0 units 15,30 then player2 unit10 then player5 unit20 (sorted within player)
	want := []pool.Handle{15, 30, 10, 20}
	if len(order) != len(want) {
		t.Fatalf("order len want %d got %d %v", len(want), len(order), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("order[%d] want %d got %d (full %v)", i, w, order[i], order)
		}
	}
}
