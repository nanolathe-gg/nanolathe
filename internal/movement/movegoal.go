package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The per-mover movement-goal handle [04 §8.3][R-P0-01][04 §7.4].
//
// Retail keeps the mover's goal in a handle of its own, separate from the
// order record: the path service writes its done/no-path bits into the order's
// satisfied word "through the goal-handle slot", and satisfied bit 0x80 is
// named for "goal-handle detach or rebind" [04 §8.3]. For an ordinary ground
// move the handle's cell is derived from the order's world goal, which is why
// the two were interchangeable here until now.
//
// They are not interchangeable for a build order. The MOBILEBUILD record's
// stored position is the footprint's centre [07 §9], while build-site
// generation "passes a selected point goal into path search" [04 §7.4] — a
// perimeter candidate outside the footprint. Reading the order position as the
// steering target walked the builder into its own site: any published route is
// consumed by the C15 prune once the mover is within five cells of its last
// waypoint [04 §7.3] C15, and every tick after that the mover steered at the
// order position again, so the last few cells of every approach ended on the
// centre no matter what the route said.
//
// ENGINE MECHANISM, not a traced retail claim: the binding below is Nanolathe's
// representation of that handle — a per-unit world point tagged with the order
// node that owns it. Retail's own layout is not recovered.
//
// TODO(question): which retail structure holds the mover's steering target,
// and whether the MOBILEBUILD handler binds its goal handle to the selected
// build-site candidate or leaves the handle on the order position and steers
// from the route alone, is untraced. What IS established is that a goal handle
// exists separately from the order record [04 §8.3] and that build-site
// generation selects a perimeter point goal for path search [04 §7.4]; binding
// that selected point as the mover's goal is the engine's way of honouring
// both. Tracing the MOBILEBUILD handler's goal-handle bind would settle it.
//
// Lifetime: this is derived state, like the arrival handle beside it. It is
// not written to a save box; the owner rebinds it on the first tick after a
// restore (construction's walk submission refreshes it before its idempotency
// guards, and ActivateMove/ReplanMove rebind it for ordinary moves).
// ForgetUnit drops it with the rest of the per-handle movement state.
type moveGoal struct {
	order *orders.Node  // the node this goal belongs to; identity gates its use
	x     numeric.Fixed // world point the mover steers at
	z     numeric.Fixed
	goal  path.Goal // optional shape payload; nil means the ordinary point goal
}

// BindMoveGoal binds the movement goal for handle h to the world point (x,z)
// on behalf of order node head [04 §8.3][04 §7.4]. Binding is idempotent and
// replaces any previous goal for that handle.
//
// Callers that steer at the order's own stored position do not need to call
// this; moveGoalFor falls back to the node's goal. It exists for the case
// research separates: an order whose stored position is not where the mover
// is supposed to stand.
func (s *System) BindMoveGoal(h pool.Handle, head *orders.Node, x, z numeric.Fixed) {
	if s == nil || head == nil {
		return
	}
	if s.moveGoals == nil {
		s.moveGoals = make(map[pool.Handle]*moveGoal)
	}
	goal := path.Goal(nil)
	if prior := s.moveGoals[h]; prior != nil && prior.order == head {
		// Rectangle steering refreshes the world point after the shape payload
		// has been installed. Keep that payload attached to the same node.
		goal = prior.goal
	}
	s.moveGoals[h] = &moveGoal{order: head, x: x, z: z, goal: goal}
}

// HasGroundGoal reports whether head currently owns this mover's ground goal
// payload. It is the identity-checked form of "this record asked for the
// mover": the four goal installers of [04 R-ORD-01 §1] bind the payload to the
// record they install for, and Release drops it again, so a record that owns
// one is by construction a record that is waiting on a movement outcome.
//
// The session's mover boundary uses it so that a family which installs a goal
// does not have to be named in a list to be driven. `Attack_Chase` is the case
// that exposed the gap: it installs a point or banded goal in phase 2 and then
// waits behind gates `0x13808`/`0x148E8`/`0x100E8` for the outcome
// [04 R-ORD-01 §3], exactly as the ground work family does, and being absent
// from the name list meant its goal was installed and never activated — an
// ordered attacker stood still forever.
func (s *System) HasGroundGoal(h pool.Handle, head *orders.Node) bool {
	if s == nil || s.moveGoals == nil || head == nil {
		return false
	}
	g := s.moveGoals[h]
	return g != nil && g.order == head
}

func (s *System) moveGoalPayload(h pool.Handle, head *orders.Node) path.Goal {
	if s == nil || s.moveGoals == nil || head == nil {
		return nil
	}
	if g := s.moveGoals[h]; g != nil && g.order == head {
		return g.goal
	}
	return nil
}

// ClearMoveGoal drops the movement goal for handle h.
func (s *System) ClearMoveGoal(h pool.Handle) {
	if s == nil || s.moveGoals == nil {
		return
	}
	delete(s.moveGoals, h)
}

// MoveGoalFor reports the world point the mover for handle h steers at while
// head is its active order [04 §8.3]. It is exported for wiring tests and
// diagnostics; the mover itself uses the unexported form.
func (s *System) MoveGoalFor(h pool.Handle, head *orders.Node) (x, z numeric.Fixed, ok bool) {
	return s.moveGoalFor(h, head)
}

// moveGoalFor returns the mover's steering target for head.
//
// A binding is used only while it still belongs to the node that made it, so a
// handle left over from a previous order can never steer the current one — the
// same staleness rule the arrival handle applies [R-P0-01]. With no live
// binding the order's stored position is the goal, which is the ordinary
// ground-move case [04 §8.3].
func (s *System) moveGoalFor(h pool.Handle, head *orders.Node) (x, z numeric.Fixed, ok bool) {
	if head == nil {
		return 0, 0, false
	}
	if s != nil && s.moveGoals != nil {
		if g := s.moveGoals[h]; g != nil && g.order == head {
			return g.x, g.z, true
		}
	}
	if head.GoalX != 0 || head.GoalZ != 0 {
		return head.GoalX, head.GoalZ, true
	}
	return 0, 0, false
}

// moveGoalForUnit resolves the steering target from a unit's primary head.
// It is the single accessor the mover's steering, threshold and arrival paths
// share, so those three can never disagree about where the unit is going.
func (s *System) moveGoalForUnit(u *units.Unit) (x, z numeric.Fixed, ok bool) {
	if u == nil {
		return 0, 0, false
	}
	q := orders.QueueForUnit(u)
	if q == nil {
		return 0, 0, false
	}
	var head *orders.Node
	if q.LenPrimary() > 0 {
		head = q.Primary()[0]
	} else {
		head = q.Head()
	}
	if head == nil {
		return 0, 0, false
	}
	if x, z, ok := s.moveGoalFor(u.Handle, head); ok {
		return x, z, true
	}
	// A target-tracking head with no resolved position yet still counts as
	// having a goal for the diagnostic/threshold path, matching the previous
	// behaviour of distSqToGoal.
	if head.GoalY != 0 || head.Target != 0 {
		return head.GoalX, head.GoalZ, true
	}
	return 0, 0, false
}
