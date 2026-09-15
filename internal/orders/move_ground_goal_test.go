package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// moveGoalFixture is a `Move_Ground`-capable queue whose movement adapter
// records every point install and release.
func moveGoalFixture() (*Queue, *units.Unit, *[]PointGoalRequest, *[]*Node) {
	rng.SeedGlobal(1, 0)
	installs := &[]PointGoalRequest{}
	releases := &[]*Node{}
	u := &units.Unit{
		Handle: 1,
		Alive:  true,
		Def:    &content.UnitDef{UnitName: "mover", BMCode: 1},
		X:      numeric.Fixed(70 << 16),
		Y:      numeric.Fixed(40 << 16),
		Z:      numeric.Fixed(90 << 16),
	}
	q := &Queue{binding: &QueueBinding{
		SimRNG: rng.Global.Sim,
		Movement: &MovementGoalAdapter{
			InstallPoint: func(req PointGoalRequest) bool {
				*installs = append(*installs, req)
				return true
			},
			Release: func(n *Node) bool { *releases = append(*releases, n); return true },
		},
	}}
	BindQueue(u, q)
	return q, u, installs, releases
}

// TestMoveGroundPhaseZeroInstallsItsPointGoal locks the half of
// [04 R-ORD-01 §4]'s `Move_Ground` row that phase 0 did not run before
// WU-19-97: "point goal at the record's goal with radius `int32(firstGeneralParameter) +
// 4`; gate = `0xE0`". Without the install the ordinary move was the one row of
// the table that owned no goal payload, so the follower had no object to ask
// for arrival [04 R-MOV-03 §2] and no other record's install could displace it
// from the controller's slot [04 R-ORD-01 §9].
//
// The full 32-bit argument and wrapping addition distinguish large positive
// values from negative values with the same low word.
func TestMoveGroundPhaseZeroInstallsItsPointGoal(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Skip("Move_Ground descriptor unavailable")
	}
	for _, tc := range []struct {
		name   string
		param1 uint32
		want   int32
	}{
		// The interface- and most AI-issued case: the word is 0, radius 4.
		{"interface move", 0, 4},
		// The AI wave task's gather broadcast forwards 160 [08 R-AI-01 §19].
		{"ai gather", 160, 164},
		{"full positive parameter", 0xFFFF, 65539},
		{"negative parameter", 0xFFFFFFFF, 3},
		{"wrapping addition", 0x7FFFFFFF, -2147483645},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, u, installs, _ := moveGoalFixture()
			goalX, goalZ := numeric.Fixed(300<<16), numeric.Fixed(420<<16)
			q.Push(id, Node{Owner: u.Handle, Param1: tc.param1, GoalX: goalX, GoalZ: goalZ})
			q.Pump(u, 40)

			if len(*installs) != 1 {
				t.Fatalf("phase 0 ran %d point installs, want 1 [04 R-ORD-01 §4]", len(*installs))
			}
			got := (*installs)[0]
			if got.Radius != tc.want {
				t.Fatalf("install radius = %d, want int32(%#x) + 4 = %d [04 R-ORD-01 §4]", got.Radius, tc.param1, tc.want)
			}
			if got.X != goalX || got.Z != goalZ {
				t.Fatalf("install point = (%d,%d), want the record's goal (%d,%d)", got.X, got.Z, goalX, goalZ)
			}
			n := q.Primary()[0]
			if got.Node != n || got.Owner != u.Handle {
				t.Fatalf("install carried node %p owner %d, want the pumped record %p owner %d", got.Node, got.Owner, n, u.Handle)
			}
			if n.Phase != 1 {
				t.Fatalf("phase = %d after the advance, want 1", n.Phase)
			}
			if n.DynamicGate != 0xE0 {
				t.Fatalf("gate = %#x, want the row's assigned 0xE0 [04 R-ORD-01 §4]", n.DynamicGate)
			}
			// The installer's closing clear of pending `0x20`-`0x200`
			// [04 R-ORD-01 §0]: a stale outcome from a previous leg must not
			// retire the new one on the tick it is armed.
			if n.Satisfied&0x3E0 != 0 {
				t.Fatalf("pending %#x survived the install, want 0x20..0x200 cleared [04 R-ORD-01 §0]", n.Satisfied)
			}
			// The read-back seam the movement layer uses agrees with what the
			// handler bound.
			if r, ok := MoveGroundGoalRadius(n); !ok || r != tc.want {
				t.Fatalf("MoveGroundGoalRadius = (%d,%v), want (%d,true)", r, ok, tc.want)
			}
		})
	}
}

// TestMoveGroundArrivalCompletesAfterTheInstall is the row end to end: the
// payload installed in phase 0 is the object whose arrival raises `0x20`, and
// phase 1 then completes with status 6 [04 R-ORD-01 §4][04 R-MOV-03 §2].
func TestMoveGroundArrivalCompletesAfterTheInstall(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Skip("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	var installs []PointGoalRequest
	q.binding.Movement = &MovementGoalAdapter{
		InstallPoint: func(req PointGoalRequest) bool { installs = append(installs, req); return true },
		Release:      func(*Node) bool { return true },
	}
	q.Push(id, Node{Owner: u.Handle, GoalX: numeric.Fixed(300 << 16), GoalZ: numeric.Fixed(420 << 16)})
	q.Pump(u, 40)
	if len(installs) != 1 {
		t.Fatalf("phase 0 ran %d installs, want 1", len(installs))
	}
	n := q.Primary()[0]

	n.Satisfied |= 0x20 // the follower observed arrival on the bound object
	q.pumpPrimary(u, 41)
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the arrived move completed [04 R-ORD-01 §4]", q.LenPrimary())
	}
	if got := spy.count(statusArrived); got != 1 {
		t.Fatalf("status 6 raised %d times on arrival, want 1 [04 R-ORD-01 §4]", got)
	}
}

// TestGroundMoveFamilyInstallsAPayload sweeps the ground rows [04 R-ORD-01 §4]
// gives a point goal to. Each must reach the shared installer, because the
// follower's repath arm and its arrival step both open with "with a payload
// installed" [04 R-MOV-03 §2] — a row that only writes its goal triple gets
// neither.
func TestGroundMoveFamilyInstallsAPayload(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase  uint8
		radius int32
	}{
		{"Move_Ground", 0, 4},
		{"Patrol", 1, patrolGoalRadius},
		{"RepairPatrol", 1, repairPatrolGoalRadius},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := Lookup(tc.name)
			if id == 0 {
				t.Skipf("%s descriptor unavailable", tc.name)
			}
			q, u, installs, _ := moveGoalFixture()
			u.Def.SightDistance = 200
			n := q.PushHead(id, Node{Owner: u.Handle, Phase: tc.phase, Deadline: -1,
				GoalX: numeric.Fixed(300 << 16), GoalZ: numeric.Fixed(420 << 16)})
			if n == nil {
				t.Fatal("push failed")
			}
			if code := DescriptorFor(id).Handler(u, n, 0, 40); code == 7 {
				t.Fatalf("%s cancelled its queue on the goal-installing phase", tc.name)
			}
			if len(*installs) != 1 {
				t.Fatalf("%s ran %d point installs, want 1 [04 R-ORD-01 §4]", tc.name, len(*installs))
			}
			if got := (*installs)[0].Radius; got != tc.radius {
				t.Fatalf("%s install radius = %d, want %d [04 R-ORD-01 §4]", tc.name, got, tc.radius)
			}
		})
	}
}
