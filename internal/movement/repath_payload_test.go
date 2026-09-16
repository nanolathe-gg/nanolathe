package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestFollowerRepathArmNeedsAnInstalledPayload locks step 3 of the follower's
// per-tick service: "**With a payload installed**, when the mover's blocked bit
// is set **or** fewer than two points remain, arm the repath bit"
// [04 R-MOV-03 §2 "The follower's per-tick service"].
//
// "A payload installed" is the controller's single slot [04 R-ORD-01 §9], not
// the record's own field: only the bound object arms the repath bit. With the
// slot empty the follower has nothing to re-path toward, and arming the bit
// anyway made every unbound record submit a request on the 60-tick cadence.
//
// The arm was unconditional until WU-19-97 for one reason: `Move_Ground` phase
// 0 installed no payload in this build, so gating it as the section writes it
// would have stopped the ordinary move from re-pathing at all. Phase 0 now
// installs its point goal [04 R-ORD-01 §4], so both halves are testable.
func TestFollowerRepathArmNeedsAnInstalledPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		install bool
		want    bool
	}{
		{"no payload in the controller slot", false, false},
		{"payload installed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
			w := newMovementFixtureWorld(10)
			system.BindWorld(w)
			system.ConfigurePath(1, 10, func(player int) bool { return player >= 0 && player < 1 })
			def := &content.UnitDef{
				UnitName: "repath-gate", FootprintX: 1, FootprintZ: 1,
				MaxVelocity: int32(worldUnitsPerCell), Acceleration: int32(worldUnitsPerCell),
				BrakeRate: int32(worldUnitsPerCell), TurnRate: 65535,
			}
			h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
			if err != nil {
				t.Fatalf("create mover: %v", err)
			}
			u := w.Unit(h)
			system.EnsureUnit(u)
			moveID := orders.Lookup("Move_Ground")
			if moveID == 0 {
				t.Skip("Move_Ground order is unavailable")
			}
			q := orders.QueueForUnit(u)
			q.Push(moveID, orders.Node{Owner: h, GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(1), GoalSupplied: true})
			head := q.Head()
			if tc.install {
				// Exactly what `Move_Ground` phase 0 hands the controller
				// [04 R-ORD-01 §4]: a point goal at the record's goal with the
				// radius its argument word names.
				radius, ok := orders.MoveGroundGoalRadius(head)
				if !ok {
					t.Fatal("Move_Ground did not report its goal radius")
				}
				system.InstallPointGoal(orders.PointGoalRequest{
					Owner: h, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: radius,
				})
			}
			setHandleRow(&system.activeOrders, h, &activeMove{order: head, token: 5})

			route := handleRow(system.Routes, h)
			route.Active = true
			route.Count = 1 // the published prefix is exhausted: fewer than two points
			route.WantsRepath = false
			route.LastRequestTick = 0

			system.serviceGroundFollower(u, head, route, 60)
			if route.WantsRepath != tc.want {
				t.Fatalf("wants-repath = %v, want %v [04 R-MOV-03 §2 step 3]", route.WantsRepath, tc.want)
			}
			if got := system.HasPathRequest(h); got != tc.want {
				t.Fatalf("path request submitted = %v, want %v", got, tc.want)
			}
		})
	}
}
