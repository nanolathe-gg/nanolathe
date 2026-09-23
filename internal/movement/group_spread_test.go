package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// spreadFixture stages n first requests for player 0 (and m for player 1) on
// one tick, all aimed at the far corner of the synthetic map, from starts at
// different distances. It returns the handles in creation order.
func spreadFixture(t *testing.T, rules Rules, n, m int) (*System, []pool.Handle) {
	t.Helper()
	sys := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	sys.Rules = rules
	w := newMovementFixtureWorld(24)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 24, func(player int) bool { return player == 0 || player == 1 })
	sys.Scheduler.SetStepAllowance(66650)
	var handles []pool.Handle
	for i := 0; i < n+m; i++ {
		owner := uint8(0)
		if i >= n {
			owner = 1
		}
		x, z := int32(i%10), int32(i/10*4)
		h, err := w.Create(wiringDef(), owner, world.CellToWorld(x), 0, world.CellToWorld(z))
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		sys.SubmitMove(h, owner, path.Cell{X: x, Z: z}, path.Cell{X: 19, Z: 19})
		handles = append(handles, h)
	}
	return sys, handles
}

// admissionTicks runs the scheduler from tick first until every handle has
// been admitted and returns each one's admission tick.
func admissionTicks(t *testing.T, sys *System, handles []pool.Handle, first uint32) []uint32 {
	t.Helper()
	got := make([]uint32, len(handles))
	for tick := first; tick < first+10; tick++ {
		sys.Scheduler.Tick(tick)
		for i, h := range handles {
			if got[i] == 0 && handleRow(sys.Routes, h).LastRequestTick != 0 {
				got[i] = handleRow(sys.Routes, h).LastRequestTick
			}
		}
	}
	for i, tick := range got {
		if tick == 0 {
			t.Fatalf("handle %d never admitted", handles[i])
		}
	}
	return got
}

// Strict 3.1 and Community never spread: the rule is off, nothing is recorded,
// and a twenty-unit group is admitted on the tick it comes due, as retail's
// throttle admits it [04 R-MOV-01 §7][04 R-PATH-01 §6].
func TestStrictFirstRequestSpreadIsOff(t *testing.T) {
	for _, rules := range []Rules{nil, StrictRules{}, &CommunityRules{}} {
		if rules != nil {
			if minGroup, ticks := rules.FirstRequestSpread(nil); minGroup != 0 || ticks != 0 {
				t.Fatalf("%T spread = (%d, %d), want off", rules, minGroup, ticks)
			}
		}
		sys, handles := spreadFixture(t, rules, 20, 0)
		if len(sys.firstRequests) != 0 {
			t.Fatalf("%T recorded %d first requests", rules, len(sys.firstRequests))
		}
		for i, tick := range admissionTicks(t, sys, handles, 100) {
			if tick != 100 {
				t.Fatalf("%T: handle %d admitted on %d, want every unit on 100", rules, handles[i], tick)
			}
		}
	}
}

// Modern spreads a group of at least sixteen over three ticks, nearest goal
// first, and leaves a smaller group, or two players' groups that are each
// small, on the due tick (DESIGN_MOVEMENT_PATH "Modern group-order spreading").
func TestModernFirstRequestSpreadOverThreeTicks(t *testing.T) {
	sys, handles := spreadFixture(t, &ModernRules{}, 20, 0)
	got := admissionTicks(t, sys, handles, 100)
	var perTick [3]int
	for i, tick := range got {
		if tick < 100 || tick > 102 {
			t.Fatalf("handle %d admitted on %d, want 100..102", handles[i], tick)
		}
		perTick[tick-100]++
	}
	if perTick[0] == 0 || perTick[1] == 0 || perTick[2] == 0 {
		t.Fatalf("admissions per tick %v, want all three ticks used", perTick)
	}
	// Nearest first: a unit admitted later is never nearer its goal than one
	// admitted earlier. The fixture's goal is the far corner, so distance is
	// the octile distance from the creation cell.
	dist := func(i int) int32 {
		dx, dz := 19-int32(i%10), 19-int32(i/10*4)
		return 18*max(dx, dz) + 7*min(dx, dz)
	}
	for i := range got {
		for j := range got {
			if got[i] < got[j] && dist(i) > dist(j) {
				t.Fatalf("handle %d (distance %d) admitted on %d before nearer handle %d (distance %d) on %d",
					handles[i], dist(i), got[i], handles[j], dist(j), got[j])
			}
		}
	}

	for _, sizes := range [][2]int{{15, 0}, {12, 12}} {
		sys, handles := spreadFixture(t, &ModernRules{}, sizes[0], sizes[1])
		for i, tick := range admissionTicks(t, sys, handles, 100) {
			if tick != 100 {
				t.Fatalf("groups %v: handle %d admitted on %d, want 100", sizes, handles[i], tick)
			}
		}
	}
}

// The cut points balance summed cost, not count: each bucket starts where the
// running cost before it reaches a third of the total, ties go to the lower
// slot, and the answer depends on nothing else.
func TestHoldGroupBalancesByCost(t *testing.T) {
	sys := &System{}
	costs := []int64{1, 1, 1, 1, 2, 2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 30}
	group := make([]firstRequest, len(costs))
	for i, c := range costs {
		h := pool.Handle(i + 1)
		setHandleRow(&sys.Routes, h, &Route{})
		group[i] = firstRequest{slot: h, cost: c}
	}
	sys.holdGroup(group, 500, 3)
	// Total 121. The first twelve sum to 44, the first time the running sum
	// reaches a third (40.3), so the second tick starts at the thirteenth; the
	// running sum first reaches two thirds (80.7) at 91, after fifteen, so the
	// third tick holds only the last. Per tick: 44, 47, 30.
	want := []uint32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 501, 501, 501, 502}
	var load [3]int64
	for i, c := range group {
		hold := handleRow(sys.Routes, c.slot).firstHold
		if hold != want[i] {
			t.Fatalf("request %d (cost %d) hold %d, want %d", i, c.cost, hold, want[i])
		}
		k := 0
		if hold != 0 {
			k = int(hold - 500)
		}
		load[k] += c.cost
	}
	t.Logf("cost per tick %v of 121", load)
}

// A held first request feeds the ordinary re-route throttle: once admitted on
// its hold tick, its stamp is that tick and the next admission comes
// RepathDelay later, exactly as for an unheld request.
func TestFirstRequestHoldComposesWithRepathDelay(t *testing.T) {
	m := &ModernRules{}
	sys := &System{Rules: m}
	route := &Route{WantsRepath: true, firstHold: 102}
	if sys.repathDue(route, 7, 101) || !sys.repathDue(route, 7, 102) {
		t.Fatal("held first request: want refused before its hold and due on it")
	}
	// Admission stamps the tick and clears the hold, as Poll does.
	route.LastRequestTick, route.firstHold = 102, 0
	delay := m.RepathDelay(sys, 7, 102)
	if sys.repathDue(route, 7, 102+delay-1) || !sys.repathDue(route, 7, 102+delay) {
		t.Fatalf("after the held admission: want the %d-tick Modern throttle from the admission tick", delay)
	}
}

// Assigning a group's holds allocates nothing once the scratch is warm.
func TestAssignFirstRequestHoldsDoesNotAllocate(t *testing.T) {
	sys, handles := spreadFixture(t, &ModernRules{}, 20, 0)
	restage := func() {
		for _, h := range handles {
			route := handleRow(sys.Routes, h)
			route.LastRequestTick, route.firstHold = 0, 0
			sys.noteFirstRequest(h)
		}
		sys.assignFirstRequestHolds(200)
	}
	restage()
	if allocs := testing.AllocsPerRun(50, restage); allocs != 0 {
		t.Fatalf("group assignment allocated %v per call", allocs)
	}
}
