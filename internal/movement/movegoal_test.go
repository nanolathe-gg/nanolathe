package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestMoveGoalHandleIdentityAndLifetime locks the movement-goal handle's three
// rules [04 §8.3][04 §7.4]: a bound goal wins over the order's stored position,
// a handle belonging to a different node is ignored rather than obeyed, and
// ForgetUnit drops it with the rest of the per-handle movement state so a
// reused pool slot cannot inherit it.
func TestMoveGoalHandleIdentityAndLifetime(t *testing.T) {
	s := NewSystem(nil, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	h := pool.Handle(3)
	site := &orders.Node{GoalX: numeric.Fixed(100 << 16), GoalZ: numeric.Fixed(200 << 16)}
	other := &orders.Node{GoalX: numeric.Fixed(900 << 16), GoalZ: numeric.Fixed(900 << 16)}

	// With no binding the order's stored position is the goal — the ordinary
	// ground-move case [04 §8.3].
	if x, z, ok := s.MoveGoalFor(h, site); !ok || x != site.GoalX || z != site.GoalZ {
		t.Fatalf("unbound goal = (%d,%d,%v), want the node's stored position", x, z, ok)
	}

	approach := struct{ x, z numeric.Fixed }{numeric.Fixed(140 << 16), numeric.Fixed(210 << 16)}
	s.BindMoveGoal(h, site, approach.x, approach.z)
	if x, z, ok := s.MoveGoalFor(h, site); !ok || x != approach.x || z != approach.z {
		t.Fatalf("bound goal = (%d,%d,%v), want the approach point (%d,%d)", x, z, ok, approach.x, approach.z)
	}

	// A handle bound by one node must never steer another: the same staleness
	// rule the arrival handle applies [R-P0-01].
	if x, z, ok := s.MoveGoalFor(h, other); !ok || x != other.GoalX || z != other.GoalZ {
		t.Fatalf("stale handle leaked into another order: got (%d,%d,%v)", x, z, ok)
	}

	// Pool slots are reused by handle, so lifecycle cleanup must drop it.
	s.ForgetUnit(h)
	if x, z, _ := s.MoveGoalFor(h, site); x != site.GoalX || z != site.GoalZ {
		t.Fatalf("ForgetUnit left a movement goal behind: got (%d,%d)", x, z)
	}
}

// TestMoveGoalClear covers the explicit drop used when an owner abandons its
// approach without the unit dying.
func TestMoveGoalClear(t *testing.T) {
	s := NewSystem(nil, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	h := pool.Handle(1)
	n := &orders.Node{GoalX: numeric.Fixed(10 << 16), GoalZ: numeric.Fixed(20 << 16)}
	s.BindMoveGoal(h, n, numeric.Fixed(50<<16), numeric.Fixed(60<<16))
	s.ClearMoveGoal(h)
	if x, z, ok := s.MoveGoalFor(h, n); !ok || x != n.GoalX || z != n.GoalZ {
		t.Fatalf("ClearMoveGoal did not fall back to the order position: (%d,%d,%v)", x, z, ok)
	}
}
