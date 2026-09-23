package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// singlePollProvider hides the provider's idle-run boundary, so the scheduler
// polls every candidate one at a time.
type singlePollProvider struct{ p *pathProvider }

func (s singlePollProvider) PlayerCount() int                                { return s.p.PlayerCount() }
func (s singlePollProvider) UnitLimit() int32                                { return s.p.UnitLimit() }
func (s singlePollProvider) Eligible(player int) bool                        { return s.p.Eligible(player) }
func (s singlePollProvider) Poll(player int) (path.Request, path.PollResult) { return s.p.Poll(player) }
func (s singlePollProvider) SetPathTick(tick uint32)                         { s.p.SetPathTick(tick) }
func (s singlePollProvider) Cancel(unit pool.Handle) bool                    { return s.p.Cancel(unit) }
func (s singlePollProvider) HasRequest(unit pool.Handle) bool                { return s.p.HasRequest(unit) }

func idleBatchFixture(t *testing.T, batch bool) (*System, []pool.Handle) {
	t.Helper()
	sys := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 10, func(player int) bool { return player == 0 || player == 1 })
	sys.Scheduler.SetStepAllowance(66650)
	if !batch {
		sys.Scheduler.SetCandidateProvider(singlePollProvider{sys.pathProvider})
	}
	var handles []pool.Handle
	for i := 0; i < 6; i++ {
		owner := uint8(i & 1)
		h, err := w.Create(wiringDef(), owner, world.CellToWorld(int32(1+i*3)), 0, world.CellToWorld(int32(1+i)))
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		handles = append(handles, h)
	}
	return sys, handles
}

// The real provider's idle runs are a cost change only. With the large step
// allowance that makes almost every poll idle, a scheduler that batches them
// must publish the same routes and leave the provider and scheduler in the
// same state as one that polls every candidate [04 R-PATH-01 §6].
func TestPathProviderIdleBatchingMatchesSinglePolls(t *testing.T) {
	run := func(batch bool) (routes []Route, cursors [10]int, trace path.SchedulerTraceState) {
		sys, handles := idleBatchFixture(t, batch)
		for tick := uint32(1); tick <= 200; tick++ {
			sys.BeginTick(tick)
			if tick%23 == 1 {
				for i, h := range handles {
					goal := path.Cell{X: int32(18 - (int(tick)+i*5)%17), Z: int32(2 + (int(tick)/23+i)%15)}
					start := path.Cell{X: int32(1 + i*3), Z: int32(1 + i)}
					sys.SubmitMove(h, uint8(i&1), start, goal)
				}
			}
			sys.Scheduler.Tick(tick)
		}
		for _, h := range handles {
			routes = append(routes, *handleRow(sys.Routes, h))
		}
		return routes, sys.pathProvider.cursor, sys.Scheduler.TraceState()
	}
	singleRoutes, singleCursors, singleTrace := run(false)
	batchRoutes, batchCursors, batchTrace := run(true)
	if !reflect.DeepEqual(batchRoutes, singleRoutes) {
		t.Fatalf("batched routes differ:\nbatched %+v\nsingle  %+v", batchRoutes, singleRoutes)
	}
	if batchCursors != singleCursors {
		t.Fatalf("batched cursors %v, single-poll cursors %v", batchCursors, singleCursors)
	}
	if !reflect.DeepEqual(batchTrace, singleTrace) {
		t.Fatalf("batched scheduler state %+v, single-poll %+v", batchTrace, singleTrace)
	}
	published := 0
	for _, r := range singleRoutes {
		if r.Count > 0 {
			published++
		}
	}
	if published == 0 {
		t.Fatal("no route was published; the comparison proves nothing")
	}
}

// SkipIdle moves the cursor exactly as the same number of idle polls does,
// including the first visit's start and the wrap at the slice's end.
func TestPathProviderSkipIdleMatchesPolls(t *testing.T) {
	for _, n := range []int32{1, 2, 7, 63, 64, 65, 1000, 22216} {
		polled, _ := idleBatchFixture(t, true)
		skipped, _ := idleBatchFixture(t, true)
		for i := int32(0); i < n; i++ {
			if _, result := polled.pathProvider.Poll(1); result == path.PollRequest {
				t.Fatal("an idle fixture admitted a request")
			}
		}
		if run := skipped.pathProvider.IdleRun(1, n); run != n {
			t.Fatalf("IdleRun(%d) = %d with nothing staged", n, run)
		}
		skipped.pathProvider.SkipIdle(1, n)
		if polled.pathProvider.cursor != skipped.pathProvider.cursor || polled.pathProvider.started != skipped.pathProvider.started {
			t.Fatalf("n=%d: skipped cursor %v, polled %v", n, skipped.pathProvider.cursor, polled.pathProvider.cursor)
		}
	}
}

// A staged slot ends the idle run exactly where the polls would reach it, on
// either side of the slice's wrap.
func TestPathProviderIdleRunStopsAtStagedSlot(t *testing.T) {
	for _, lead := range []int32{0, 1, 5, 300} {
		sys, handles := idleBatchFixture(t, true)
		p := sys.pathProvider
		h := handles[3] // owned by player 1
		p.Submit(path.Request{Unit: h, Player: 1})
		p.SkipIdle(1, lead)
		run := p.IdleRun(1, 1<<20)
		polls := int32(0)
		for {
			c := p.cursor[1]
			if !p.started[1] {
				start, _, _ := p.world.SliceForPlayer(1)
				c = start - 1
			}
			start, end, _ := p.world.SliceForPlayer(1)
			if nextSlot(c, start, end) == int(h) {
				break
			}
			p.SkipIdle(1, 1)
			polls++
		}
		if run != polls {
			t.Fatalf("lead %d: IdleRun = %d, the staged slot is %d polls away", lead, run, polls)
		}
	}
}
