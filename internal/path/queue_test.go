package path

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

type testCandidateProvider struct {
	requests [10][]Request
	limit    int32
}
type testScheduler struct{ *Scheduler }

func (s *testScheduler) Submit(r Request) { s.provider.(*testCandidateProvider).Submit(r) }
func (s *testScheduler) Pending(p uint8) int {
	n := s.provider.(*testCandidateProvider).Pending(p)
	if s.active != nil && s.activePlayer == int(p) {
		n++
	}
	return n
}
func (s *testScheduler) TotalPending() int {
	return len(s.provider.(*testCandidateProvider).AllRequests())
}
func testPending(s *testScheduler, p uint8) int { return s.Pending(p) }

func (p *testCandidateProvider) PlayerCount() int { return 10 }
func (p *testCandidateProvider) UnitLimit() int32 { return p.limit }
func (p *testCandidateProvider) Eligible(player int) bool {
	return player >= 0 && player < 10 && len(p.requests[player]) > 0
}
func (p *testCandidateProvider) Poll(player int) (Request, PollResult) {
	if player < 0 || player >= 10 || len(p.requests[player]) == 0 {
		return Request{}, PollNoUnit
	}
	r := p.requests[player][0]
	p.requests[player] = p.requests[player][1:]
	return r, PollRequest
}
func (p *testCandidateProvider) Submit(r Request) {
	for player := range p.requests {
		for i := range p.requests[player] {
			if p.requests[player][i].Unit == r.Unit {
				p.requests[player] = append(p.requests[player][:i], p.requests[player][i+1:]...)
				break
			}
		}
	}
	q := p.requests[r.Player]
	for i := range q {
		if q[i].Unit == r.Unit {
			q[i] = r
			p.requests[r.Player] = q
			return
		}
	}
	idx := len(q)
	for i := range q {
		if r.Unit < q[i].Unit {
			idx = i
			break
		}
	}
	q = append(q, Request{})
	copy(q[idx+1:], q[idx:])
	q[idx] = r
	p.requests[r.Player] = q
}
func (p *testCandidateProvider) Cancel(unit pool.Handle) bool {
	for player := range p.requests {
		for i := range p.requests[player] {
			if p.requests[player][i].Unit == unit {
				p.requests[player] = append(p.requests[player][:i], p.requests[player][i+1:]...)
				return true
			}
		}
	}
	return false
}
func (p *testCandidateProvider) HasRequest(unit pool.Handle) bool {
	for player := range p.requests {
		for _, r := range p.requests[player] {
			if r.Unit == unit {
				return true
			}
		}
	}
	return false
}
func (p *testCandidateProvider) Pending(player uint8) int { return len(p.requests[player]) }
func (p *testCandidateProvider) AllRequests() []Request {
	var out []Request
	for player := range p.requests {
		out = append(out, p.requests[player]...)
	}
	return out
}

func newTestScheduler(search SearchFunc, publish PublishFunc) *testScheduler {
	s := NewScheduler(search, publish)
	s.SetCandidateProvider(&testCandidateProvider{limit: 1})
	return &testScheduler{s}
}

func TestSchedulerReplenishCadence(t *testing.T) {
	var captured []int32
	search := func(r Request, scale int32, budget int) WorkResult {
		captured = append(captured, scale)
		if budget != 0 && budget != 100 {
			t.Fatalf("budget want setup or 100 got %d", budget)
		}
		return WorkResult{}
	}
	s := newTestScheduler(search, nil)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	s.SetBase(65536)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(captured) != 2 || captured[0] != 65536*6 || captured[1] != 65536*6 {
		t.Fatalf("tick 0 scale want %d got %v", 65536*6, captured)
	}
	captured = nil
	for i := 2; i <= 12; i++ {
		s.Submit(Request{Unit: pool.Handle(i), Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	}
	s.Tick(1)
	if len(captured) != 1 {
		t.Fatalf("tick 1 continues only the active request, got %d calls", len(captured))
	}
	if captured[0] != 65536*6 {
		t.Fatalf("active request scale changed before replenish: got %d", captured[0])
	}
	captured = nil
	s.Tick(150)
	if len(captured) != 1 {
		t.Fatalf("tick 150 continues only active request, got %d calls", len(captured))
	}
	for _, sc := range captured {
		if sc != 65536*6 {
			t.Fatalf("active request must retain admission scale got %d", sc)
		}
	}
	captured = nil
	s.Tick(151)
	for _, sc := range captured {
		if sc != 65536*6 {
			t.Fatalf("tick 151 active request retains 6x got %d", sc)
		}
	}
	captured = nil
	s.Tick(299)
	for _, sc := range captured {
		if sc != 65536*6 {
			t.Fatalf("tick 299 active request retains 6x got %d", sc)
		}
	}
	captured = nil
	s.Tick(300)
	for _, sc := range captured {
		if sc != 65536*6 {
			t.Fatalf("tick 300 active request retains 6x got %d", sc)
		}
	}
}

func TestSchedulerQuantumWeighting(t *testing.T) {
	s := newTestScheduler(nil, nil)
	s.SetBase(1000)
	s.SetUnitLimit(10)
	s.serviceCount[0], s.serviceCount[1], s.serviceCount[2] = 0, 10, 20
	s.replenish()
	if s.scales[0] != 6000 || s.scales[1] != 3000 || s.scales[2] != 1000 {
		t.Fatalf("quantum tiers want [6000 3000 1000] got [%d %d %d]", s.scales[0], s.scales[1], s.scales[2])
	}
}

func TestSchedulerPopBudgetEnforcement(t *testing.T) {
	var budgets []int
	calls := 0
	search := func(r Request, scale int32, budget int) WorkResult {
		budgets = append(budgets, budget)
		if budget == 0 {
			return WorkResult{SetupSteps: 2}
		}
		calls++
		if calls == 1 {
			if scale == 0 {
				t.Fatalf("scale should be non-zero")
			}
			return WorkResult{Pops: 100}
		}
		return WorkResult{Points: []Point{{X: 1, Z: 1}, {X: 2, Z: 2}}, Done: true, Pops: 1}
	}
	var published [][]Point
	publish := func(r Request, pts []Point, st Status) {
		published = append(published, pts)
	}
	s := newTestScheduler(search, publish)
	s.SetUnitLimit(1)
	// The budget under test is the SLICE budget of 100 pops, not a per-call
	// one: a call keeps taking slices until the player's accumulator goes
	// non-positive [04 R-PATH-01 §6]. Ten players make the share small enough
	// that the admission charge plus one slice overdraws it, so the call ends
	// on a real budget boundary with the request latched.
	s.SetPlayerCount(10)
	s.SetBase(DefaultBase)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(budgets) != 2 || budgets[0] != 0 || budgets[1] != 100 {
		t.Fatalf("first call budgets want [0 100] got %v", budgets)
	}
	if len(published) != 0 {
		t.Fatalf("should not publish on budget exhaustion, got %d publishes", len(published))
	}
	if testPending(s, 0) != 1 {
		t.Fatalf("request should remain ACTIVE after budget exhaustion, pending %d", s.Pending(0))
	}
	s.Tick(1)
	if len(budgets) != 3 || budgets[2] != 100 {
		t.Fatalf("second call budget want 100 got %v", budgets)
	}
	if len(published) != 1 {
		t.Fatalf("second call should publish, got %d", len(published))
	}
	if len(published[0]) != 2 {
		t.Fatalf("published points want 2 got %d", len(published[0]))
	}
	if testPending(s, 0) != 0 {
		t.Fatalf("request should be removed after completion, pending %d", s.Pending(0))
	}
}

func TestSchedulerDuplicateGoalOverwrite(t *testing.T) {
	s := newTestScheduler(nil, nil)
	g1 := PointGoal(Cell{10, 10}, 0)
	g2 := PointGoal(Cell{99, 99}, 0)
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{0, 0}, Goal: g1})
	if testPending(s, 2) != 1 {
		t.Fatalf("pending want 1 got %d", s.Pending(2))
	}
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{0, 0}, Goal: g2})
	if testPending(s, 2) != 1 {
		t.Fatalf("duplicate should overwrite, not duplicate: pending %d", s.Pending(2))
	}
	if len(s.provider.(*testCandidateProvider).AllRequests()) != 1 {
		t.Fatalf("total pending want 1 got %d", s.TotalPending())
	}
	var got Goal
	search := func(r Request, scale int32, budget int) WorkResult {
		got = r.Goal
		return WorkResult{Points: []Point{{X: 1}}, Done: true}
	}
	s.SetSearch(search)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	s.Tick(0)
	if got != g2 {
		t.Fatalf("duplicate goal should overwrite: got %v want %v", got, g2)
	}
	if testPending(s, 2) != 0 {
		t.Fatalf("after tick pending should be 0 got %d", s.Pending(2))
	}
	s.Submit(Request{Unit: 5, Player: 2, Start: Cell{1, 1}, Goal: g1})
	s.Submit(Request{Unit: 5, Player: 3, Start: Cell{2, 2}, Goal: g2})
	if s.Pending(2) != 0 {
		t.Fatalf("move should remove from old player, pending2 %d", s.Pending(2))
	}
	if testPending(s, 3) != 1 {
		t.Fatalf("move should add to new player, pending3 %d", s.Pending(3))
	}
}

func TestSchedulerFullOrEmpty(t *testing.T) {
	calls := 0
	search := func(r Request, scale int32, budget int) WorkResult {
		if budget == 0 {
			return WorkResult{SetupSteps: 1}
		}
		calls++
		if calls == 1 {
			return WorkResult{Points: []Point{{X: 1, Z: 1}, {X: 2, Z: 2}}, Pops: 100}
		}
		return WorkResult{Points: []Point{{X: 10, Z: 10}, {X: 20, Z: 20}, {X: 30, Z: 30}}, Done: true, Pops: 1}
	}
	var publishes [][]Point
	publish := func(r Request, pts []Point, st Status) {
		cp := make([]Point, len(pts))
		copy(cp, pts)
		publishes = append(publishes, cp)
	}
	s := newTestScheduler(search, publish)
	s.SetUnitLimit(1)
	// Ten players share the step allowance, so one call buys 133 steps: the
	// 100-step admission charge plus its setup step leave room for exactly one
	// 100-pop slice before the accumulator goes non-positive and the call ends
	// with the request latched [04 R-PATH-01 §6]. With a single player the
	// call would spend the whole 1333-step share and run both slices, which is
	// correct behaviour but puts the budget boundary out of this test's reach.
	s.SetPlayerCount(10)
	s.Submit(Request{Unit: 1, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{10, 10}, 0)})
	s.Tick(0)
	if len(publishes) != 0 {
		t.Fatalf("full-or-empty: budget exhaustion must not publish partial, got %d publishes", len(publishes))
	}
	if testPending(s, 0) != 1 {
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
	if testPending(s, 0) != 0 {
		t.Fatalf("after completion pending should be 0")
	}
}

func TestSchedulerHeapExhaustionEmptyPublication(t *testing.T) {
	search := func(r Request, scale int32, budget int) WorkResult {
		return WorkResult{Status: StatusRejected, Done: true}
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
	s := newTestScheduler(search, publish)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
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
	if testPending(s, 1) != 0 {
		t.Fatalf("heap exhaustion should remove request, pending %d", s.Pending(1))
	}
}

func TestSchedulerDefaultAndSetBase(t *testing.T) {
	s := newTestScheduler(nil, nil)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	if s.ScaleFor(0) != DefaultBase*6 {
		t.Fatalf("scheduler should use DefaultBase when not set, got %d want %d", s.ScaleFor(0), DefaultBase*6)
	}
	s.SetBase(1000)
	if s.ScaleFor(0) != 6000 {
		t.Fatalf("scheduler SetBase should override, got %d", s.ScaleFor(0))
	}
}

func TestSchedulerUnsetUnitLimitIsInert(t *testing.T) {
	calls := 0
	s := &testScheduler{NewScheduler(func(Request, int32, int) WorkResult {
		calls++
		return WorkResult{Status: StatusRejected, Done: true}
	}, nil)}
	s.SetCandidateProvider(&testCandidateProvider{limit: 0})
	s.Submit(Request{Unit: 1, Player: 0, Goal: PointGoal(Cell{1, 1}, 0)})
	s.Tick(0)
	if calls != 0 || s.Pending(0) != 1 || s.ScaleFor(0) != 0 {
		t.Fatalf("unset unit limit must leave scheduler inert: calls=%d pending=%d scale=%d", calls, s.Pending(0), s.ScaleFor(0))
	}
}

func TestSchedulerZeroPlayersDoesNotAdvanceCadence(t *testing.T) {
	s := newTestScheduler(nil, nil)
	s.SetUnitLimit(1)
	s.SetPlayerCount(0)
	s.callCount = replenishInterval - 1
	s.Tick(0)
	if s.callCount != replenishInterval-1 {
		t.Fatalf("zero-player tick changed call counter to %d", s.callCount)
	}
}

func TestSchedulerActiveRequestKeepsReceivingPlayerShare(t *testing.T) {
	calls := 0
	s := newTestScheduler(func(Request, int32, int) WorkResult {
		calls++
		return WorkResult{Done: false, Pops: 100}
	}, nil)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	s.Submit(Request{Unit: 1, Player: 0, Goal: PointGoal(Cell{20, 20}, 0)})
	s.Tick(0)
	if s.active == nil {
		t.Fatal("request did not become active")
	}
	if s.provider.(*testCandidateProvider).Eligible(0) {
		t.Fatal("test provider still eligible after admission")
	}
	// Cross the regression boundary directly: the admitted request no longer
	// exists in the provider and its previously accumulated share is spent.
	s.accumulator[0] = 0
	beforeCalls := calls
	s.Tick(1)
	// With the accumulator forced to zero, the ONLY thing that lets the active
	// request take a slice is the accrual term that credits the active
	// player's share even though its request has left the provider: without
	// it the loop's `accumulator <= 0` guard breaks before the first slice.
	if calls <= beforeCalls {
		t.Fatalf("active request did not resume with a new player share: calls=%d beforeCalls=%d", calls, beforeCalls)
	}
	// Corrected 2026-09-04: this used to also require the accumulator to end
	// the tick POSITIVE, which only held while the call stopped after a single
	// 100-pop slice. A call spends the share in slices "until that accumulator
	// goes non-positive" [04 R-PATH-01 §6], so ending non-positive is the
	// contract, and the observable for the accrual is how many slices the
	// share bought.
	if got := calls - beforeCalls; got < 2 {
		t.Fatalf("one tick's share bought %d slices of 100 pops, want the several the equal share covers [04 R-PATH-01 §6]", got)
	}
}

func TestSchedulerCancelReleasesAdmittedRequest(t *testing.T) {
	s := newTestScheduler(func(Request, int32, int) WorkResult {
		return WorkResult{Done: false, Pops: 100}
	}, nil)
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	s.Submit(Request{Unit: 7, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{30, 30}, 0)})
	s.Tick(0)
	if !s.HasRequest(7) || s.active == nil {
		t.Fatal("request was not admitted")
	}
	if !s.Cancel(7) {
		t.Fatal("active request was not canceled")
	}
	if s.HasRequest(7) || s.active != nil {
		t.Fatal("canceled request still owns scheduler state")
	}
}

func TestSchedulerHeapExhaustionViaEmptyHeap(t *testing.T) {
	// Verify that heap exhaustion path is distinct from budget exhaustion.
	// Budget exhaustion leaves active; heap exhaustion publishes empty.
	calls := 0
	search := func(r Request, scale int32, budget int) WorkResult {
		calls++
		// Simulate heap exhaustion after consuming budget fully but finding no path.
		return WorkResult{Status: StatusRejected, Done: true}
	}
	var pubs int
	s := newTestScheduler(search, func(r Request, pts []Point, st Status) { pubs++ })
	s.SetUnitLimit(1)
	s.SetPlayerCount(1)
	s.Submit(Request{Unit: 9, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{100, 100}, 0)})
	s.Tick(0)
	if pubs != 1 || calls != 1 {
		t.Fatalf("heap exhaustion should publish empty in one tick: pubs %d calls %d", pubs, calls)
	}
}

func TestSchedulerDeterministicPlayerOrder(t *testing.T) {
	var order []pool.Handle
	search := func(r Request, scale int32, budget int) WorkResult {
		order = append(order, r.Unit)
		return WorkResult{Points: []Point{{X: 1}}, Done: true}
	}
	s := newTestScheduler(search, func(r Request, pts []Point, st Status) {})
	s.SetUnitLimit(1)
	s.SetPlayerCount(10)
	// The fixture provider receives requests out of order across players and
	// remains deterministic by round-robin player cursor and sorted unit queues.
	s.Submit(Request{Unit: 20, Player: 5, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 10, Player: 2, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 30, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Submit(Request{Unit: 15, Player: 0, Start: Cell{0, 0}, Goal: PointGoal(Cell{0, 0}, 0)})
	s.Tick(0)
	// Expected order: player0 units 15,30 then player2 unit10 then player5 unit20 (sorted within player)
	want := []pool.Handle{15, 10, 20, 30}
	if len(order) != len(want) {
		t.Fatalf("order len want %d got %d %v", len(want), len(order), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("order[%d] want %d got %d (full %v)", i, w, order[i], order)
		}
	}
}
