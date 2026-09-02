package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// parkInstallFixture is parkFixture with the queue binding's movement port
// wired to the system's own installers, which is what internal/session
// composes [04 R-ORD-01 §1]. Without the rectangle installer bound a `Park`
// record could only publish its geometry on its parameter words.
func parkInstallFixture(t *testing.T) (*System, *units.Unit, *orders.Queue) {
	t.Helper()
	sys, w, u := parkFixture(t)
	sim := rng.NewSimulation(0x12345677)
	q := orders.QueueForUnit(u)
	q.SetBinding(&orders.QueueBinding{
		SimRNG: &sim,
		Lookup: w.Unit,
		Movement: &orders.MovementGoalAdapter{
			Ready:            func() bool { return true },
			InstallPoint:     sys.InstallPointGoal,
			InstallRectangle: sys.InstallRectangleGoal,
			Release:          sys.ReleaseGoalPayload,
		},
	})
	return sys, u, q
}

// TestParkPhase0InstallsThroughTheRectangleInstaller locks the record-level
// half of the `Park` row's "install a rectangle goal" [04 R-ORD-01 §2]: the
// install goes through the shared installer, so it displaces whatever object
// the movement controller's one payload slot held and raises `0x80` on THAT
// object's own record [04 R-ORD-01 §9], and it finishes by clearing pending
// `0x20`-`0x200` on the record it installed for [04 R-ORD-01 §1].
//
// Before WU-19-100 phase 0 wrote the rectangle to the record's parameter words
// and installed nothing, so the slot stayed empty for a parking product: no
// record could ever be displaced by a `Park`, and the closing clear never ran.
func TestParkPhase0InstallsThroughTheRectangleInstaller(t *testing.T) {
	sys, u, q := parkInstallFixture(t)

	// A different record already owns the controller's payload slot.
	displaced := &orders.Node{Owner: u.Handle, ID: orders.Lookup("Move_Ground"), Deadline: -1}
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: u.Handle, Node: displaced, X: u.X, Y: u.Y, Z: u.Z, Radius: 4}) {
		t.Fatal("fixture failed to seat the displaced record's payload")
	}
	if displaced.Satisfied&0x80 != 0 {
		t.Fatal("the seating install left 0x80 standing on its own record; the closing clear should cancel the self-raise")
	}

	q.Push(orders.Lookup("Park"), orders.Node{Owner: u.Handle, Deadline: -1})
	head := q.Head()
	if head == nil {
		t.Fatal("no head record")
	}
	// Pre-load every pending bit so the closing clear is observable.
	head.Satisfied |= 0x3E0

	q.Pump(u, 1)

	if head.Phase != 1 {
		t.Fatalf("phase %d after the first visit, want phase 1 [04 R-ORD-01 §2]", head.Phase)
	}
	if head.DynamicGate != 0xE0 {
		t.Fatalf("gate=%#x, want 0xe0 [04 R-ORD-01 §2]", head.DynamicGate)
	}
	if !sys.HasGroundGoal(u.Handle, head) {
		t.Fatal("Park phase 0 installed no ground payload [04 R-ORD-01 §2]")
	}
	if head.Satisfied&0x3E0 != 0 {
		t.Fatalf("pending word %#x after the install, want 0x20-0x200 cleared [04 R-ORD-01 §1]", head.Satisfied)
	}
	if displaced.Satisfied&0x80 == 0 {
		t.Fatal("the displaced record did not receive the 0x80 rebind raise [04 R-ORD-01 §9]")
	}

	// The installed payload is the rectangle the row authors, grown by the
	// product's own footprint [04 R-PATH-01 §12], and it is what the arrival
	// handle tests — so a product standing on the border raises `0x20`.
	minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(head)
	if !ok {
		t.Fatal("Park phase 0 published no rectangle on its parameter words")
	}
	sys.ActivateMove(u, head)
	ah := sys.arrivalHandles[u.Handle]
	if ah == nil || ah.payload == nil {
		t.Fatal("the arrival handle carries no installed payload")
	}
	coll := sys.Collisions[u.Handle]
	if coll == nil {
		t.Fatal("no collision state for the product")
	}
	// A corner of the grown rectangle's border, which is a goal cell whichever
	// border cell the steering point picked.
	coll.CachedAnchor = Cell{X: minX - 1, Z: minZ - 1}
	if !sys.finalGoalReached(u, true) {
		t.Fatalf("a product on the grown border of [%d,%d]x[%d,%d] did not arrive [04 §7.2]", minX, maxX, minZ, maxZ)
	}
	if head.Satisfied&0x20 == 0 {
		t.Fatal("arrival did not raise the record's 0x20 bit [R-P0-01]")
	}
}
