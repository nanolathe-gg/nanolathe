package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Binding the derived steering point after restore must preserve the route
// awaiting adoption; it is not a null-goal release [04 R-PATH-01 §8].
func TestDerivedGoalBindingPreservesRestoredRoute(t *testing.T) {
	sys, _, h := releaseFixture(t, wiringDef(), 2)
	n := &orders.Node{Owner: h}
	route := &Route{Active: true, Count: 2, WantsRepath: true, LastRequestTick: 100}
	route.Points[0], route.Points[1] = Point{X: 17, Z: 19}, Point{X: 23, Z: 29}
	setHandleRow(&sys.Routes, h, route)
	before := *route
	sys.BindMoveGoal(h, n, 23<<16, 29<<16)
	if *route != before {
		t.Fatal("derived steering bind altered restored route or request-poll state")
	}
	if !sys.ReleaseGoalPayload(n) || route.Active || route.WantsRepath {
		t.Fatal("explicit release failed to clear the route after derived binding")
	}
}

// Ground arrival unbinds just like flight arrival. A later explicit release
// still finds the arrived record's object and can unbind a successor
// [04 R-MOV-03 §2][04 R-ORD-01 §9].
func TestGroundArrivalRetainsTheRecordObject(t *testing.T) {
	sys, w, h := releaseFixture(t, wiringDef(), 2)
	u := w.Unit(h)
	a, b := &orders.Node{Owner: h}, &orders.Node{Owner: h}
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: a, X: u.X, Z: u.Z})
	sys.raiseArrival(u, &arrivalHandle{order: a})
	if a.Satisfied != 0xA0 || sys.HasGroundGoal(h, a) {
		t.Fatal("ground arrival did not release its binding with both outcomes")
	}
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: b, X: u.X, Z: u.Z})
	if !sys.ReleaseGoalPayload(a) || b.Satisfied != 0x80 || sys.HasGroundGoal(h, b) {
		t.Fatal("ground arrival destroyed its object before a later explicit release")
	}
	if !sys.ReleaseGoalPayload(b) {
		t.Fatal("displaced ground object did not remain owned by its record")
	}
}

type retainedPayloadProbe struct {
	headingProbePayload
	arrived   bool
	destroyed int
}

func (p *retainedPayloadProbe) Arrived(*units.Unit) bool { return p.arrived }
func (p *retainedPayloadProbe) Persistent() bool         { return false }
func (p *retainedPayloadProbe) Release()                 { p.destroyed++ }

// Controller arrival and replacement release a binding; only record cleanup
// destroys its object. Explicitly releasing the displaced owner unbinds the current owner
// but leaves the latter's object alive [04 R-ORD-01 §9][04 R-AIR-01 §1].
func TestRecordPayloadOutlivesItsControllerBinding(t *testing.T) {
	for _, arrived := range []bool{false, true} {
		sys, _, u := airFixture(t)
		a, b := &orders.Node{Owner: u.Handle}, &orders.Node{Owner: u.Handle}
		pa, pb := &retainedPayloadProbe{arrived: arrived}, &retainedPayloadProbe{}
		sys.installAirPayload(u, a, pa)
		if arrived {
			sys.StepFlightCommand(u, a, sys.AirSectors)
			if a.Satisfied != 0xA0 || sys.AirGoalPayload(u.Handle) != nil {
				t.Fatal("arrival did not detach with both movement bits")
			}
		}
		sys.installAirPayload(u, b, pb)
		if pa.destroyed != 0 || pb.destroyed != 0 {
			t.Fatal("controller replacement or arrival destroyed a retained object")
		}
		fresh := &orders.Node{Owner: u.Handle}
		if sys.ReleaseGoalPayload(fresh) || sys.AirGoalPayload(u.Handle) != pb {
			t.Fatal("a fresh record's empty release disturbed the bound object")
		}
		if !sys.ReleaseGoalPayload(a) || pa.destroyed != 1 || pb.destroyed != 0 {
			t.Fatal("explicit release did not destroy exactly the old record's object")
		}
		if b.Satisfied != 0x80 || sys.AirGoalPayload(u.Handle) != nil {
			t.Fatal("explicit release did not deliver release alone to the current owner")
		}
		if sys.ReleaseGoalPayload(a) || !sys.ReleaseGoalPayload(b) || pb.destroyed != 1 {
			t.Fatal("retained objects were not each destroyed exactly once")
		}
	}
}

// Unlike an explicit handler release, destroying a displaced record deletes
// only its object and leaves the current binding untouched [04 R-ORD-01 §9].
func TestRecordDestructorPreservesAnotherRecordsBinding(t *testing.T) {
	sys, _, u := airFixture(t)
	a, b := &orders.Node{Owner: u.Handle}, &orders.Node{Owner: u.Handle}
	pa, pb := &retainedPayloadProbe{}, &retainedPayloadProbe{}
	sys.installAirPayload(u, a, pa)
	sys.installAirPayload(u, b, pb)
	sys.ReleaseGoal(a)
	if pa.destroyed != 1 || pb.destroyed != 0 || b.Satisfied != 0 || sys.AirGoalPayload(u.Handle) != pb {
		t.Fatal("record destructor failed to distinguish its object from the current binding")
	}
	if !sys.ReleaseGoalPayload(b) || pb.destroyed != 1 {
		t.Fatal("remaining owner's explicit release lost its object")
	}
}

// Mover-only restore leaves live record ownership alone. Unit retirement
// destroys even the objects no longer bound to its controller; full session
// restoration creates a new system [04 R-ORD-01 §9][08 R-SAVE-02 §8].
func TestRetainedPayloadsSurviveMoverRestoreAndClearOnForget(t *testing.T) {
	for _, restore := range []bool{false, true} {
		sys, _, u := airFixture(t)
		a, b := &orders.Node{Owner: u.Handle}, &orders.Node{Owner: u.Handle}
		pa, pb := &retainedPayloadProbe{}, &retainedPayloadProbe{}
		sys.installAirPayload(u, a, pa)
		sys.installAirPayload(u, b, pb)
		if restore {
			if err := sys.RestoreMover(u.Handle, make([]byte, 35)); err != nil {
				t.Fatal(err)
			}
			if pa.destroyed != 0 || pb.destroyed != 0 || sys.AirGoalPayload(u.Handle) != pb {
				t.Fatal("mover-only restore changed live order ownership")
			}
		}
		sys.ForgetUnit(u.Handle)
		if pa.destroyed != 1 || pb.destroyed != 1 || len(handleRow(sys.recordGoals, u.Handle)) != 0 {
			t.Fatal("retirement retained or duplicated payload ownership")
		}
		if sys.AirGoalPayload(u.Handle) != nil || sys.ReleaseGoalPayload(a) || sys.ReleaseGoalPayload(b) {
			t.Fatal("retired records still control payload cleanup")
		}
	}
}
