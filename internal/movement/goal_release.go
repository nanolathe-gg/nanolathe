package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// goalReleasedPending is the controller's payload-release notification
// [04 R-ORD-01 §0]. It does not imply destruction of the record's object.
const goalReleasedPending uint32 = 0x80

// recordGoal is our representation of a record's independently owned object
// [04 R-ORD-01 §9]. The per-unit slice preserves installation order for final
// cleanup and needs no allocation on lookup. Only one family is populated.
type recordGoal struct {
	node   *orders.Node
	ground *moveGoal
	air    GoalPayload
}

func (s *System) storeRecordGoal(h pool.Handle, owned recordGoal) {
	rows := handleRow(s.recordGoals, h)
	for i := range rows {
		if rows[i].node == owned.node {
			rows[i] = owned
			return
		}
	}
	setHandleRow(&s.recordGoals, h, append(rows, owned))
}

// detachControllerGoal gives the controller a null goal. Notification follows
// the currently bound object, which can belong to another record entirely.
// No retained record object is destroyed [04 R-ORD-01 §9].
func (s *System) detachControllerGoal(h pool.Handle) {
	s.CancelPathRequest(h)
	s.displaceControllerGoal(h)
	if route := handleRow(s.Routes, h); route != nil {
		route.Active = false
		route.WantsRepath = false
	}
}

// displaceControllerGoal replaces only the payload binding. Ground installers
// leave route acceptance to activation, including adoption of a restored route
// whose record binding is absent [04 R-PATH-01 §8]. An explicit null-goal
// release additionally clears the route through detachControllerGoal above.
func (s *System) displaceControllerGoal(h pool.Handle) {
	s.raiseEvictedGoalRelease(h)
	setHandleRow(&s.moveGoals, h, nil)
	if fl := handleRow(s.Flights, h); fl != nil && fl.Command != nil {
		c := fl.Command
		if c.Payload != nil && c.payloadOwner == nil {
			c.Payload.Release()
		}
		c.Payload = nil
		c.payloadOwner = nil
	}
}

// releaseRecordGoal is the record helper's release arm: if this record owns
// an object, unbind the controller, then destroy only this record's object.
// An empty record leaves another record's binding alone [04 R-ORD-01 §9].
func (s *System) releaseRecordGoal(n *orders.Node) bool {
	return s.deleteRecordGoal(n, true)
}

// The destructor checks binding identity; explicit handler release does not.
// Both destroy the record's object [04 R-ORD-01 §9].
func (s *System) deleteRecordGoal(n *orders.Node, explicit bool) bool {
	if s == nil || n == nil {
		return false
	}
	rows := handleRow(s.recordGoals, n.Owner)
	for i, owned := range rows {
		if owned.node != n {
			continue
		}
		if explicit || s.airPayloadOwner(n.Owner) == n || handleRow(s.moveGoals, n.Owner) == owned.ground && owned.ground != nil {
			s.detachControllerGoal(n.Owner)
		}
		if owned.air != nil {
			owned.air.Release()
		}
		copy(rows[i:], rows[i+1:])
		rows[len(rows)-1] = recordGoal{}
		setHandleRow(&s.recordGoals, n.Owner, rows[:len(rows)-1])
		return true
	}
	return false
}

// forgetRecordGoals destroys all retained objects in stable installation order
// after detaching the controller. It also clears the row before slot reuse.
func (s *System) forgetRecordGoals(h pool.Handle) {
	s.detachControllerGoal(h)
	for _, owned := range handleRow(s.recordGoals, h) {
		if owned.air != nil {
			owned.air.Release()
		}
	}
	setHandleRow(&s.recordGoals, h, nil)
}

// ReleaseGoalPayload implements the record-level release helper. A moverless
// unit is a no-op; a record with an object unbinds the controller regardless of
// which record owns the current binding, then destroys its own object. This
// release form leaves all pending movement bits intact [04 R-ORD-01 §1, §9].
func (s *System) ReleaseGoalPayload(n *orders.Node) bool {
	if s == nil || n == nil {
		return false
	}
	if u := s.unitFor(n.Owner); u != nil && u.Flags&units.BuildingClassStatus != 0 {
		return false
	}
	s.discardModernClearance(n)
	s.discardRepairLanding(n)
	return s.releaseRecordGoal(n)
}

// TargetRemoved clears the non-notifying weak link in every retained air
// marker, including displaced records. It preserves cached goal/control state
// and emits no order event [04 R-AIR-01 §4]. Final removal must call this before
// the target slot can be reused.
func (s *System) TargetRemoved(target pool.Handle) {
	if s == nil || target == 0 {
		return
	}
	for _, e := range s.repairLandings {
		if e.pad != nil && e.pad.Handle == target {
			e.pad, e.reserved = nil, false
		}
	}
	for _, row := range s.recordGoals {
		for _, owned := range row {
			if m, ok := owned.air.(*airMarker); ok && m.target == target {
				m.target = 0
			}
		}
	}
}
