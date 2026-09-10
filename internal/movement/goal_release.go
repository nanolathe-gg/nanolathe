package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// goalReleasedPending is pending bit `0x80`, "a previous goal object is
// released", of the five movement bits census [04 R-ORD-01 §0].
const goalReleasedPending uint32 = 0x80

// ReleaseGoalPayload is the release form of the record-level install/release
// helper that sits behind all four goal installers of [04 R-ORD-01 §1].
//
// The helper "runs entirely through the owner's mover: a unit without a mover
// (a building) is a no-op — nothing is released, nothing installed". A
// definition whose `bmcode` is 0 is the building class and never owns a mover —
// the allocator constructs one only for `bmcode 1` [04 R-FAC-02 §5] — and the
// same authored byte raises status-word bit 29 at creation [04 §3.4], which is
// the runtime mirror this package reads.
//
// With a mover, and only when the record actually holds a payload, the release
// is steps 1 through 4 of the ground follower's route-acceptance rule
// [04 R-PATH-01 §8] run with a null goal:
//
//  1. cancel any in-flight search belonging to this follower;
//  2. OR `0x80` into the pending word of the record that owned the previous
//     payload — which is this record, because the payload is a field OF the
//     record and an installer never reaches another record's field
//     [04 R-ORD-01 §0];
//  3. clear has-waypoint and adopt the goal (null here);
//  4. the new object being null, also clear wants-repath and STOP — step 5's
//     acceptance gates and step 6's last-request reset are the install path's,
//     not the release's.
//
// Then the payload object is virtually deleted and the record's payload field
// cleared. For a flight block the release is "the `0x80` raise and the null
// store" alone [04 R-ORD-01 §1] — no search to cancel and no route to clear,
// because aircraft never enter the ground scheduler [04 R-PATH-01 §8].
//
// The release form deliberately does NOT clear pending `0x20`–`0x200`. That
// clear belongs to step (2) of the helper, which runs only when a new object is
// given; "the release form (no new object) is step (1) alone, which is why it
// leaves `0x80` visible" [04 R-ORD-01 §1]. The arrival release of
// [04 R-MOV-03 §2] and the queue teardown of [04 R-MOV-03 §9] reach this same
// helper through the record.
//
// The identity test on every arm is what keeps a late teardown from detaching a
// successor's payload: a record that no longer owns the mover's payload
// releases nothing and raises nothing. The returned bool reports whether a
// payload was actually released.
func (s *System) ReleaseGoalPayload(n *orders.Node) bool {
	if s == nil || n == nil {
		return false
	}
	owner := n.Owner
	// The mover-less no-op. A unit the world can no longer resolve (a death or
	// transport teardown that runs after the record) is not evidence of a
	// building, and refusing to release there would strand the payload, so the
	// no-op is taken only on a unit positively known to be building class.
	if u := s.unitFor(owner); u != nil && u.Flags&units.BuildingClassStatus != 0 {
		return false
	}
	released := false
	if g := handleRow(s.moveGoals, owner); g != nil && g.order == n {
		s.CancelPathRequest(owner)         // step 1
		n.Satisfied |= goalReleasedPending // step 2
		if route := handleRow(s.Routes, owner); route != nil {
			route.Active = false      // step 3: clear has-waypoint, adopt null
			route.WantsRepath = false // step 4: null goal, so also wants-repath
		}
		setHandleRow(&s.moveGoals, owner, nil) // virtual delete; the record's field is cleared
		released = true
	}
	if st := handleRow(s.airOrders, owner); st != nil && st.order == n {
		n.Satisfied |= goalReleasedPending
		s.releaseAirGoalForNode(owner, n)
		released = true
	}
	return released
}
