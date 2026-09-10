package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// The payload's owner receives release and arrival even if it is no longer
// the head record [04 R-ORD-01 §9][04 R-AIR-01 §1]. Both private installers
// are used by the pump-driven air executors.
func TestPrivateAirInstallNotifiesDisplacedOwner(t *testing.T) {
	for _, velocity := range []bool{false, true} {
		sys, _, u := airFixture(t)
		old := &orders.Node{Owner: u.Handle}
		next := &orders.Node{Owner: u.Handle, Satisfied: 0x3E0}
		sys.installAirGoal(u, old, sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z}))
		sys.airStateFor(u, next)
		if velocity {
			sys.installAirPayload(u, next, &airVelocityMarker{unit: u})
		} else {
			sys.installAirGoal(u, next, sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z}))
		}
		if old.Satisfied != 0x80 || next.Satisfied != 0 {
			t.Fatalf("velocity=%v displaced=%#x installer=%#x; want release only on displaced owner [04 R-ORD-01 §9]", velocity, old.Satisfied, next.Satisfied)
		}
		if sys.ReleaseGoalPayload(old) {
			t.Fatal("old owner released the successor's payload")
		}
		if !sys.ReleaseGoalPayload(next) || next.Satisfied != 0x80 {
			t.Fatal("bound owner did not receive payload release")
		}
	}
}

func TestAirArrivalAndCleanupFollowPayloadAcrossHeadChange(t *testing.T) {
	for _, release := range []bool{false, true} {
		sys, w, u := airFixture(t)
		q := orders.QueueForUnit(u)
		q.Push(orders.Lookup("Wait"), orders.Node{Owner: u.Handle})
		owner := q.Head()
		sys.installAirGoal(u, owner, sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z}))
		q.PushHead(orders.Lookup("Wait"), orders.Node{Owner: u.Handle})
		next := q.Head()
		sys.airStateFor(u, next)
		if release {
			if !sys.ReleaseGoalPayload(owner) || owner.Satisfied != 0x80 {
				t.Fatal("cleanup lost the payload owner when the queue head changed [04 R-ORD-01 §9]")
			}
		} else {
			runMovementTick(sys, 1, w)
			if owner.Satisfied != 0xA0 {
				t.Fatalf("owner arrival=%#x, want arrival and release [04 R-AIR-01 §1]", owner.Satisfied)
			}
		}
		if next.Satisfied != 0 {
			t.Fatalf("new head received another record's payload bits %#x [04 R-ORD-01 §9]", next.Satisfied)
		}
	}
}

func TestMidairTakeOffKeepsExistingPayloadOwner(t *testing.T) {
	sys, w, u := airFixture(t)
	u.Move.Mode = 2
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Wait"), orders.Node{Owner: u.Handle})
	owner := q.Head()
	sys.installAirGoal(u, owner, sys.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z}))
	q.PushHead(orders.Lookup("Wait"), orders.Node{Owner: u.Handle})
	next := q.Head()
	// An airborne takeoff installs no new climb marker [04 R-AIR-01 §6],
	// but it still runs the command producer on the existing payload.
	if !sys.TakeOff(w, u.Handle) {
		t.Fatal("midair takeoff refused")
	}
	if owner.Satisfied != 0xA0 || next.Satisfied != 0 {
		t.Fatalf("old owner=%#x current head=%#x; want arrival/release only on payload owner [04 R-ORD-01 §9]", owner.Satisfied, next.Satisfied)
	}
}
