package path

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// slotProvider walks each player's slice of slots with a cursor, the way the
// movement follower's provider does: most visits find nothing, and a slot
// with a staged request admits it.
type slotProvider struct {
	slices   [10][2]int
	eligible [10]bool
	cursor   [10]int
	started  [10]bool
	staged   map[int]Request
	visits   int
}

func (p *slotProvider) PlayerCount() int { return 10 }
func (p *slotProvider) UnitLimit() int32 { return 50 }
func (p *slotProvider) Eligible(player int) bool {
	return player >= 0 && player < 10 && p.eligible[player]
}
func (p *slotProvider) Poll(player int) (Request, PollResult) {
	p.visits++
	if !p.Eligible(player) {
		return Request{}, PollNoUnit
	}
	start, end := p.slices[player][0], p.slices[player][1]
	if !p.started[player] {
		p.cursor[player], p.started[player] = start-1, true
	}
	p.cursor[player]++
	if p.cursor[player] > end {
		p.cursor[player] = start
	}
	r, ok := p.staged[p.cursor[player]]
	if !ok {
		return Request{}, PollVisited
	}
	delete(p.staged, p.cursor[player])
	return r, PollRequest
}

// batchingSlotProvider is the same provider with the idle-run boundary.
type batchingSlotProvider struct{ *slotProvider }

func (p batchingSlotProvider) IdleRun(player int, limit int32) int32 {
	if !p.Eligible(player) {
		return limit
	}
	start, end := p.slices[player][0], p.slices[player][1]
	c := p.cursor[player]
	if !p.started[player] {
		c = start - 1
	}
	for i := int32(0); i < limit; i++ {
		if c++; c > end {
			c = start
		}
		if _, ok := p.staged[c]; ok {
			return i
		}
	}
	return limit
}

func (p batchingSlotProvider) SkipIdle(player int, n int32) {
	for ; n > 0; n-- {
		p.Poll(player)
	}
}

// Batching idle polls is a cost change only: under a large allowance, with
// requests staged between ticks and searches that span several slices, the
// batching scheduler must make exactly the searches, publications and
// counter changes the one-poll-at-a-time scheduler makes [04 R-PATH-01 §6].
func TestSchedulerIdleBatchingMatchesSinglePolls(t *testing.T) {
	for _, allowance := range []int{1333, 66650} {
		t.Run(fmt.Sprint(allowance), func(t *testing.T) {
			run := func(batch bool) ([]string, *Scheduler) {
				plain := &slotProvider{staged: map[int]Request{}}
				for p := 0; p < 10; p++ {
					plain.slices[p] = [2]int{1 + p*40, 40 + p*40}
				}
				plain.eligible[0], plain.eligible[2], plain.eligible[3], plain.eligible[7] = true, true, true, true
				var log []string
				remaining := map[pool.Handle]int{}
				search := func(r Request, scale int32, budget int) WorkResult {
					log = append(log, fmt.Sprintf("search %d scale %d budget %d", r.Unit, scale, budget))
					if budget == 0 {
						remaining[r.Unit] = int(r.Unit%7) * 90
						return WorkResult{SetupSteps: int(r.Unit % 5)}
					}
					pops := min(budget, remaining[r.Unit])
					remaining[r.Unit] -= pops
					return WorkResult{Done: remaining[r.Unit] == 0, Pops: pops, Points: []Point{{X: int32(r.Unit)}}}
				}
				publish := func(r Request, points []Point, status Status) {
					log = append(log, fmt.Sprintf("publish %d %v", r.Unit, points))
				}
				s := NewScheduler(search, publish)
				if batch {
					s.SetCandidateProvider(batchingSlotProvider{plain})
				} else {
					s.SetCandidateProvider(plain)
				}
				s.SetStepAllowance(allowance)
				seed := uint32(12345)
				for tick := uint32(1); tick <= 400; tick++ {
					for k := 0; k < 3; k++ {
						seed = seed*1103515245 + 12345
						if seed>>28 < 5 {
							player := []int{0, 2, 3, 7, 5}[(seed>>8)%5]
							slot := plain.slices[player][0] + int((seed>>12)%40)
							plain.staged[slot] = Request{Unit: pool.Handle(slot), Player: uint8(player)}
						}
					}
					s.Tick(tick)
					log = append(log, fmt.Sprintf("tick %d acc %v svc %v cursor %d %v", tick, s.accumulator, s.serviceCount, s.playerCursor, plain.cursor))
				}
				return log, s
			}
			single, a := run(false)
			batched, b := run(true)
			if len(single) != len(batched) {
				t.Fatalf("batched log has %d entries, single-poll log %d", len(batched), len(single))
			}
			for i := range single {
				if single[i] != batched[i] {
					t.Fatalf("entry %d: batched %q, single-poll %q", i, batched[i], single[i])
				}
			}
			if !reflect.DeepEqual(a.scales, b.scales) || a.callCount != b.callCount {
				t.Fatal("batched scheduler quanta differ from the single-poll scheduler")
			}
		})
	}
}
