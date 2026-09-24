package path

import "testing"

// boundProvider is a provider whose players are always eligible and whose
// polls find nothing unless a request is queued, so every poll it answers is
// a visited candidate. It can answer the Modern bounded-work question.
type boundProvider struct {
	polls     [10]int
	requests  [10][]Request
	eligible  [10]bool
	sweep     int
	carry     int32
	sweepStop bool
}

func (p *boundProvider) PlayerCount() int         { return 2 }
func (p *boundProvider) UnitLimit() int32         { return 1 }
func (p *boundProvider) Eligible(player int) bool { return p.eligible[player] }
func (p *boundProvider) Poll(player int) (Request, PollResult) {
	p.polls[player]++
	if q := p.requests[player]; len(q) > 0 {
		p.requests[player] = q[1:]
		return q[0], PollRequest
	}
	return Request{}, PollVisited
}
func (p *boundProvider) PathWorkBound() (int32, bool) { return p.carry, p.sweepStop }
func (p *boundProvider) SweepLen(int) int             { return p.sweep }

// retailProvider hides the bounded-work answer, as a provider that does not
// implement it (every standalone scheduler) does.
type retailProvider struct{ *boundProvider }

func (retailProvider) PathWorkBound() {}

func newBoundScheduler(p CandidateProvider, search SearchFunc) *Scheduler {
	s := NewScheduler(search, nil)
	s.SetCandidateProvider(p)
	s.SetUnitLimit(1)
	s.SetPlayerCount(2)
	s.SetStepAllowance(66650)
	return s
}

// A player waiting behind another player's long search banks its share every
// call. Retail carries it without bound [04 R-PATH-01 §6]; the Modern bound
// keeps four shares and credits the rest to the poll count, so the heuristic
// tier the next replenish derives is unchanged.
func TestModernWorkBoundCapsCarryAndCreditsPolls(t *testing.T) {
	longSearch := func(Request, int32, int) WorkResult { return WorkResult{Pops: 100} }
	run := func(carry int32) (acc, service int32) {
		p := &boundProvider{carry: carry, sweep: 8}
		p.eligible[0], p.eligible[1] = true, true
		p.requests[0] = []Request{{Unit: 1, Player: 0, Goal: PointGoal(Cell{X: 40, Z: 40}, 0)}}
		s := newBoundScheduler(p, longSearch)
		s.Tick(1)
		if s.active == nil || s.activePlayer != 0 {
			t.Fatal("player 0's request was not admitted first")
		}
		// Player 0's search never finishes, so player 1 never gets a poll.
		p.eligible[0] = false
		for tick := uint32(2); tick <= 21; tick++ {
			s.Tick(tick)
		}
		return s.accumulator[1], s.serviceCount[1]
	}
	share := int32(66650 / 2)
	retailAcc, retailService := run(0)
	if retailAcc < 19*share {
		t.Fatalf("retail carry = %d, want at least 19 shares (%d)", retailAcc, 19*share)
	}
	acc, service := run(modernCarryTestShares)
	if acc != modernCarryTestShares*share {
		t.Fatalf("bounded carry = %d, want %d", acc, modernCarryTestShares*share)
	}
	if acc+service != retailAcc+retailService {
		t.Fatalf("bounded carry %d + credited polls %d = %d, retail %d + %d = %d: the heuristic tier would see a different load",
			acc, service, acc+service, retailAcc, retailService, retailAcc+retailService)
	}
}

// modernCarryTestShares mirrors movement's Modern answer; the scheduler only
// sees the number a provider returns.
const modernCarryTestShares = 4

// With nothing admissible, retail polls until the whole share is spent. The
// sweep stop ends the player's call after one full sweep and credits the
// rest as polls; an admission restarts the sweep.
func TestModernWorkBoundSweepStop(t *testing.T) {
	search := func(Request, int32, int) WorkResult { return WorkResult{Done: true} }
	share := 66650 / 2

	retail := &boundProvider{sweep: 10}
	retail.eligible[0] = true
	rs := newBoundScheduler(retailProvider{retail}, search)
	rs.Tick(1)
	if retail.polls[0] != share {
		t.Fatalf("retail polls = %d, want the whole share %d", retail.polls[0], share)
	}

	strict := &boundProvider{sweep: 10}
	strict.eligible[0] = true
	ss := newBoundScheduler(strict, search)
	ss.Tick(1)
	if strict.polls[0] != retail.polls[0] || ss.serviceCount != rs.serviceCount || ss.accumulator != rs.accumulator {
		t.Fatalf("a (0,false) answer must equal a provider without the policy: polls %d vs %d", strict.polls[0], retail.polls[0])
	}

	modern := &boundProvider{sweep: 10, carry: 4, sweepStop: true}
	modern.eligible[0] = true
	ms := newBoundScheduler(modern, search)
	ms.Tick(1)
	if modern.polls[0] != 11 {
		t.Fatalf("modern polls = %d, want one sweep of 10 plus the poll that proves it (11)", modern.polls[0])
	}
	if ms.serviceCount[0] != rs.serviceCount[0] || ms.accumulator[0] != 0 {
		t.Fatalf("modern service count %d (retail %d), accumulator %d: the dropped work must be credited as polls",
			ms.serviceCount[0], rs.serviceCount[0], ms.accumulator[0])
	}

	// An admission part-way restarts the sweep: five visited polls, one
	// request, then a full sweep of ten more before the stop.
	again := &boundProvider{sweep: 10, carry: 4, sweepStop: true}
	again.eligible[0] = true
	as := newBoundScheduler(again, search)
	again.requests[0] = nil
	calls := 0
	as.provider = &countingInsert{boundProvider: again, at: 6, calls: &calls}
	as.Tick(1)
	if again.polls[0] != 6+11 {
		t.Fatalf("polls = %d, want 6 before and 11 after the admission", again.polls[0])
	}
}

// countingInsert returns one request on its at'th poll.
type countingInsert struct {
	*boundProvider
	at    int
	calls *int
}

func (c *countingInsert) Poll(player int) (Request, PollResult) {
	*c.calls++
	if *c.calls == c.at {
		c.polls[player]++
		return Request{Unit: 2, Player: uint8(player), Goal: PointGoal(Cell{X: 4, Z: 4}, 0)}, PollRequest
	}
	return c.boundProvider.Poll(player)
}
