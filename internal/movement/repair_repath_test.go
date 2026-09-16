package movement

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Repair refreshes its rectangle every 30..59 ticks [04 R-ORD-01 §5].
// Installing that goal must not postpone the scheduler's 60-tick poll:
// only an admitted poll stamps the current tick, and goal installation clears
// a timestamp older than ten ticks [04 R-MOV-01 §7][04 R-PATH-01 §8].
func TestRepairGoalRefreshAllowsPathAdmission(t *testing.T) {
	s := NewSystem(syntheticFlat(128, 128), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	s.BindWorld(w)
	s.ConfigurePath(1, 4, func(p int) bool { return p == 0 })
	def := wiringDef()
	profile := wiringProfile
	profile.FootPrintX, profile.FootPrintZ = 2, 2
	setScratchMovement(def, profile)
	h, err := w.Create(def, 0, numeric.Fixed(1656<<16), 0, numeric.Fixed(1719<<16))
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	s.EnsureUnit(u)
	blocker, err := w.Create(&content.UnitDef{UnitName: "repair-obstruction", FootprintX: 3, FootprintZ: 3, YardMap: "ooooooooo"}, 0, numeric.Fixed(1624<<16), 0, numeric.Fixed(1736<<16))
	if err != nil {
		t.Fatal(err)
	}
	s.EnsureUnit(w.Unit(blocker))
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("RepairUnit"), orders.Node{Owner: h, Phase: 1})
	n := q.Head()
	route := handleRow(s.Routes, h)

	install := func(tick uint32) {
		t.Helper()
		s.tick = tick
		if !s.InstallRectangleGoal(orders.RectangleGoalRequest{Owner: h, Node: n, CellX: 80, CellZ: 108, Width: 4, Depth: 4}) || !s.ActivateMove(u, n) {
			t.Fatal("repair goal did not activate")
		}
	}
	// The capture's geometry permits the ordinary synthetic line to cross the
	// artillery footprint; a real search must remain eligible to replace it.
	install(55188)
	if !route.WantsRepath || route.Count != 2 {
		t.Fatalf("initial repair route=%+v, want synthetic route awaiting search", route)
	}
	for tick := uint32(55189); tick < 55789; tick++ {
		// Exercise a legal 30-tick repair refresh without a session or RNG
		// fixture. Holding position isolates admission from steering.
		if (tick-55188)%30 == 0 {
			install(tick)
		}
		s.pathProvider.SetPathTick(tick)
		request, result := s.pathProvider.Poll(0)
		if result == path.PollRequest {
			if route.LastRequestTick != tick {
				t.Fatalf("admitted poll timestamp=%d, want %d", route.LastRequestTick, tick)
			}
			s.tick = tick
			for slice := 0; slice < 100; slice++ {
				work := s.searchFunc(request, 65536, 100)
				if !work.Done {
					continue
				}
				if work.Status != 0 || len(work.Points) < 3 {
					t.Fatalf("repair search did not route around the structure: %+v", work)
				}
				// Search reconstruction emits cardinal/diagonal segments [04
				// R-PATH-01 §7]. Walk their cell lattice to check that the route
				// avoids the occupied 3x3 rectangle, not merely its centre.
				for i := 1; i < len(work.Points); i++ {
					a, b := work.Points[i-1], work.Points[i]
					for a != b {
						if a.X >= 1600 && a.X < 1648 && a.Z >= 1712 && a.Z < 1760 {
							t.Fatalf("admitted path crosses artillery footprint at %+v: %+v", a, work.Points)
						}
						a.X += min(int32(16), max(int32(-16), b.X-a.X))
						a.Z += min(int32(16), max(int32(-16), b.Z-a.Z))
					}
				}
				s.publishFunc(request, work.Points, work.Status)
				if route.WantsRepath || !route.Active {
					t.Fatalf("search publication not adopted: %+v", route)
				}
				return
			}
			t.Fatal("repair search exceeded fixture work bound")
		}
	}
	t.Fatalf("repair refresh starved path admission for 600 ticks; last poll=%d", route.LastRequestTick)
}

// A replacement shortly after an actual poll keeps the throttle; an older
// timestamp is cleared by the goal installer, not replaced with install time.
func TestGoalReplacementPreservesRecentPollTick(t *testing.T) {
	for _, age := range []uint32{10, 11} {
		t.Run(fmt.Sprintf("age_%d", age), func(t *testing.T) {
			s, w, h := releaseFixture(t, wiringDef(), 2)
			u := w.Unit(h)
			n := &orders.Node{Owner: h, ID: orders.Lookup("Move_Ground"), GoalX: numeric.Fixed(96 << 16), GoalZ: numeric.Fixed(96 << 16), GoalSupplied: true}
			install := func() {
				s.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: n, X: n.GoalX, Z: n.GoalZ})
				s.ActivateMove(u, n)
			}
			s.tick = 100
			install()
			route := handleRow(s.Routes, h)
			route.LastRequestTick = 100 // a preceding successful follower poll
			s.tick = 100 + age
			install()
			want := uint32(100)
			if age > 10 {
				want = 0
			}
			if route.LastRequestTick != want {
				t.Fatalf("goal age %d: last poll=%d, want %d", age, route.LastRequestTick, want)
			}
		})
	}
}
