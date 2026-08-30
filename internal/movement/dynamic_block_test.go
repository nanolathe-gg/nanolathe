package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestStepUnitBlockedCommitKeepsOrderAndRequest(t *testing.T) {
	system := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	def := &content.UnitDef{
		UnitName: "dynamic-block-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: int32(worldUnitsPerCell), TurnRate: 65535,
	}
	blockerHandle, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	// The mover is deliberately the higher pool slot: this was the case in
	// which the removed lower-slot-priority policy would have replanned it.
	moverHandle, err := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	system.BindWorld(w)
	system.EnsureUnit(w.Unit(moverHandle))
	system.EnsureUnit(w.Unit(blockerHandle))

	moveID := orders.Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground order is unavailable")
	}
	queue := orders.QueueForUnit(w.Unit(moverHandle))
	queue.Push(moveID, orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(0)})
	head := queue.Head()
	system.Routes[moverHandle].Publish([]Point{{X: 0, Z: 0}, {X: 3, Z: 0}})
	system.activeOrders[moverHandle] = &activeMove{order: head, token: 41}
	system.nextActivation = 41
	// Keep one pre-existing request so a collision-triggered Cancel/Submit pair
	// would be observable in its activation and start fields. StepUnit must
	// leave this scheduler state and the active-order token untouched.
	system.pathProvider.Submit(path.Request{
		Unit: moverHandle, Player: 0, Start: path.Cell{X: 0, Z: 0},
		Goal: path.PointGoal(path.Cell{X: 3, Z: 0}, 0), Activation: 41,
	})
	requestsBefore := system.pathProvider.allRequests()
	activeBefore := *system.activeOrders[moverHandle]
	nextBefore := system.nextActivation
	result := system.StepUnit(moverHandle, 1)
	if !result.Blocked {
		t.Fatal("active route proposal into blocker was not rejected")
	}
	if requestsAfter := system.pathProvider.allRequests(); !reflect.DeepEqual(requestsAfter, requestsBefore) || !system.HasPathRequest(moverHandle) {
		t.Fatalf("blocked commit changed scheduler requests: before=%#v after=%#v has=%v", requestsBefore, requestsAfter, system.HasPathRequest(moverHandle))
	}
	if got := *system.activeOrders[moverHandle]; !reflect.DeepEqual(got, activeBefore) || system.nextActivation != nextBefore {
		t.Fatalf("blocked commit changed active-order token: before=%#v/%d after=%#v/%d", activeBefore, nextBefore, got, system.nextActivation)
	}
	if queue.Head() != head {
		t.Fatal("blocked commit changed the active order head")
	}
	if head.Satisfied&arrivalSatisfiedBit != 0 {
		t.Fatal("blocked commit marked the order satisfied")
	}
	if !system.Collisions[moverHandle].Blocked {
		t.Fatal("collision state did not retain blocked result")
	}
	if got, want := system.Collisions[moverHandle].Speed, int32(worldUnitsPerCell)/2; got != want {
		t.Fatalf("blocked commit speed=%d want half max velocity %d", got, want)
	}
	if got, ok := system.Grid.OccupantAt(Cell{X: 0, Z: 0}); !ok || got != int(moverHandle) {
		t.Fatalf("blocked commit changed mover occupancy: (%d,%t)", got, ok)
	}
	if got, ok := system.Grid.OccupantAt(Cell{X: 1, Z: 0}); !ok || got != int(blockerHandle) {
		t.Fatalf("blocked commit changed blocker occupancy: (%d,%t)", got, ok)
	}
	// The no-replan implementation path contains no RNG call; the test only
	// observes scheduler/order state because no RNG source is injectable here.
}

// TestDynamicBlockCommitScenarios locks the established final-commit contract:
// occupancy is checked synchronously in pool-slot order, a foreign occupant is
// a hard blocker, and a rejected proposal leaves the old stamp in place. The
// outer yield/replan policy remains Unknown [R-MOV-02A][04 §8.2].
func TestDynamicBlockCommitScenarios(t *testing.T) {
	t.Run("stationary blocker", func(t *testing.T) {
		grid := NewOccupancyGrid()
		if !grid.Stamp(Cell{0, 0}, 1, 1, 1) || !grid.Stamp(Cell{1, 0}, 1, 1, 2) {
			t.Fatal("failed to seed stationary-blocker occupancy")
		}
		mover := &CollisionState{
			ID: 1, X: 0, Z: 0, VX: int32(worldUnitsPerCell), FootPrintX: 1,
			FootPrintZ: 1, Mode: 2, CachedAnchor: Cell{0, 0},
			CachedMode: 2, OldAnchor: Cell{0, 0}, MaxVelocity: 65536,
		}
		CommitSweep([]*CollisionState{mover}, grid, occupancyValidator(grid), nil)
		if !mover.Blocked {
			t.Fatal("proposal into stationary occupant was not blocked")
		}
		assertOccupant(t, grid, Cell{0, 0}, 1)
		assertOccupant(t, grid, Cell{1, 0}, 2)
	})

	t.Run("same destination claim first", func(t *testing.T) {
		grid := NewOccupancyGrid()
		if !grid.Stamp(Cell{0, 0}, 1, 1, 1) || !grid.Stamp(Cell{2, 0}, 1, 1, 2) {
			t.Fatal("failed to seed same-destination occupancy")
		}
		first := dynamicMover(1, Cell{0, 0}, int32(worldUnitsPerCell))
		second := dynamicMover(2, Cell{2, 0}, -int32(worldUnitsPerCell))
		CommitSweep([]*CollisionState{second, first}, grid, occupancyValidator(grid), nil)
		if first.Blocked {
			t.Fatal("lower-slot first claimant was blocked")
		}
		if !second.Blocked {
			t.Fatal("later same-destination claimant was not blocked")
		}
		assertOccupant(t, grid, Cell{1, 0}, 1)
		assertOccupant(t, grid, Cell{2, 0}, 2)
	})

	t.Run("head-on swap", func(t *testing.T) {
		grid := NewOccupancyGrid()
		if !grid.Stamp(Cell{0, 0}, 1, 1, 1) || !grid.Stamp(Cell{1, 0}, 1, 1, 2) {
			t.Fatal("failed to seed head-on occupancy")
		}
		first := dynamicMover(1, Cell{0, 0}, int32(worldUnitsPerCell))
		second := dynamicMover(2, Cell{1, 0}, -int32(worldUnitsPerCell))
		CommitSweep([]*CollisionState{first, second}, grid, occupancyValidator(grid), nil)
		if !first.Blocked || !second.Blocked {
			t.Fatal("head-on swap did not block both proposals")
		}
		assertOccupant(t, grid, Cell{0, 0}, 1)
		assertOccupant(t, grid, Cell{1, 0}, 2)
	})
}

func dynamicMover(id int, anchor Cell, vx int32) *CollisionState {
	return &CollisionState{
		ID: id, X: int32(anchor.X) * int32(worldUnitsPerCell), Z: int32(anchor.Z) * int32(worldUnitsPerCell),
		VX: vx, FootPrintX: 1, FootPrintZ: 1, Mode: 2,
		CachedAnchor: anchor, CachedMode: 2, OldAnchor: anchor, MaxVelocity: 65536,
	}
}

func occupancyValidator(grid *OccupancyGrid) func(*CollisionState) func(Cell) bool {
	return func(mover *CollisionState) func(Cell) bool {
		return func(cell Cell) bool {
			occupant, ok := grid.OccupantAt(cell)
			return !ok || occupant == mover.ID
		}
	}
}

func assertOccupant(t *testing.T, grid *OccupancyGrid, cell Cell, want int) {
	t.Helper()
	if got, ok := grid.OccupantAt(cell); !ok || got != want {
		t.Fatalf("cell %v occupant=(%d,%t), want %d", cell, got, ok, want)
	}
}
