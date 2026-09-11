package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A saved standby resumes its phase and integer post rather than taking a new
// post at the aircraft's current position [04 R-AIR-01 §7]
// [08 R-SAVE-ORDER-01]. The marker is independently retained by the record.
func TestAirStandbyRestoreKeepsPhaseAndPost(t *testing.T) {
	src, _, u := airFixture(t)
	u.Move.Mode = 2
	n := pushAirOrder(t, u, "VTOL_Standby", 17<<16, 29<<16)
	n.Phase, n.GuardX, n.GuardY = 2, 73, 91
	n.Deadline, n.DynamicGate = 120, 1
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == u.Handle }
	images, err := orders.RetailOrderImagesWithPayload(u, resolve, nil, src.RetailOrderPayload)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary,
			Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype, DescriptorName: image.DescriptorName}
	}
	dst, _, loaded := airFixture(t)
	loaded.Move.Mode = 2
	loaded.X, loaded.Z = 180<<16, 210<<16
	loaded.Attachment.Cargo = []pool.Handle{loaded.Handle + 1}
	binding := orders.QueueForUnit(loaded).Binding()
	if err := orders.RetailRestoreOrdersAtTick(loaded, records, map[uint16]pool.Handle{uint16(u.Handle): loaded.Handle}, binding, 120); err != nil {
		t.Fatal(err)
	}
	if err := dst.RestoreHeadGoal(loaded); err != nil {
		t.Fatal(err)
	}
	dst.BindAirOrderLegs()
	q := orders.QueueForUnit(loaded)
	resumed := q.Head()
	sim := binding.SimRNG
	expected := *sim
	bearing := uint16(expected.Uint32n(0x10000))
	radius := numeric.Fixed(int64(8+expected.Uint32n(0x20)) << 16)
	delay := expected.Uint32n(15)
	ox, oz := offsetAtBearing(bearing, radius)
	before := sim.Draws()
	q.Pump(loaded, 120)
	if sim.Draws()-before != 3 {
		t.Fatalf("resumed phase 2 made %d draws, want bearing, radius, delay", sim.Draws()-before)
	}
	m := installedMarker(t, dst, loaded)
	if m.goal.X != numeric.Fixed(73<<16)-ox || m.goal.Z != numeric.Fixed(91<<16)-oz {
		t.Fatalf("restored loiter moved its post: marker %+v", m.goal)
	}
	if resumed.Phase != 1 || resumed.Deadline != int32(150+delay) || resumed.GuardX != 73 || resumed.GuardY != 91 {
		t.Fatalf("resumed record phase=%d deadline=%d post=(%d,%d)", resumed.Phase, resumed.Deadline, resumed.GuardX, resumed.GuardY)
	}
}

// The post is an integer pair on the order itself. A temporary primary head
// cannot reset it or the phase [04 R-AIR-01 §7][04 §3.3].
func TestAirStandbyInterruptionKeepsIntegerPost(t *testing.T) {
	sys, _, u := airFixture(t)
	u.Move.Mode = 2
	u.Attachment.Cargo = []pool.Handle{u.Handle + 1}
	u.X, u.Z = 73<<16|0x9000, 91<<16|0x4000
	n := pushAirOrder(t, u, "VTOL_Standby", 17<<16, 29<<16)
	sys.BindAirOrderLegs()
	q := orders.QueueForUnit(u)
	q.Pump(u, 100)
	if n.Phase != 1 || n.GuardX != 73 || n.GuardY != 91 {
		t.Fatalf("post not stored in the persistent record: phase=%d post=(%d,%d)", n.Phase, n.GuardX, n.GuardY)
	}
	blocker := &orders.Node{ID: orders.Lookup("Paralyze"), Owner: u.Handle, DynamicGate: 1, Deadline: 200}
	q.SetPrimary([]*orders.Node{blocker, n})
	u.X, u.Z = 180<<16, 210<<16
	q.RemoveHead()
	q.Pump(u, 101)
	if n.Phase != 1 || n.GuardX != 73 || n.GuardY != 91 || q.Binding().SimRNG.Draws() != 3 {
		t.Fatalf("temporary head restarted standby: phase=%d post=(%d,%d) draws=%d", n.Phase, n.GuardX, n.GuardY, q.Binding().SimRNG.Draws())
	}
}

// Completing standby passes its cached goal to the landing child and retires
// the parent through the pump [04 R-AIR-01 §7][04 §3.3].
func TestAirStandbyLandingKeepsCachedGoal(t *testing.T) {
	sys, _, u := airFixture(t)
	u.Move.Mode = 2
	n := pushAirOrder(t, u, "VTOL_Standby", 17<<16, 29<<16)
	n.GoalY, n.Phase = 31<<16, 2
	sys.BindAirOrderLegs()
	q := orders.QueueForUnit(u)
	q.SetExternallyDrivenHandler(orders.Lookup("VTOL_LandIfCan"))
	q.Pump(u, 100)
	if q.LenPrimary() != 1 || q.Head() == n || q.Head().ID != orders.Lookup("VTOL_LandIfCan") {
		t.Fatalf("standby did not complete into one landing record: %+v", q.Primary())
	}
	if q.Head().GoalX != 17<<16 || q.Head().GoalY != 31<<16 || q.Head().GoalZ != 29<<16 {
		t.Fatalf("landing lost cached goal: %+v", q.Head())
	}
}

// HelpBuild's order handler already owns the takeoff and site legs. A restored
// work-phase record must retain its existing marker between orbit deadlines
// [04 R-ORD-01 §7][04 §10.3][08 R-SAVE-ORDER-01].
func TestAirHelpBuildDoesNotRestartApproach(t *testing.T) {
	sys, _, u := airFixture(t)
	u.Move.Mode = 2
	n := pushAirOrder(t, u, "VTOL_HelpBuild", 300<<16, 350<<16)
	n.Phase = 3
	marker := sys.newPointMarker(u, Vec3{X: 150 << 16, Z: 180 << 16})
	sys.installAirGoal(u, n, marker)
	for tick := uint32(41); tick <= 42; tick++ {
		sys.tick = tick
		sys.runAirOrderLeg(u, n, 0, sys.tick)
	}
	if sys.AirGoalPayload(u.Handle) != marker {
		t.Fatal("resumed work replaced its retained marker with the approach")
	}
	sys.tick = 150
	sys.runAirOrderLeg(u, n, 0, sys.tick)
	if sys.AirGoalPayload(u.Handle) == marker {
		t.Fatal("work phase omitted the due orbit station")
	}
}

// The saved move is already in its arrival phase. Its marker can differ from
// the original requested point; neither restore nor the following mover visits
// may rerun phase 1 and replace it [04 R-ORD-02 §2][08 R-SAVE-02 §10, §11].
func TestAirMoveRestoreRetainsArrivalPhasePayload(t *testing.T) {
	src, _, u := airFixture(t)
	n := pushAirOrder(t, u, "VTOL_Move", 300<<16, 350<<16)
	n.Phase, n.DynamicGate = 2, airLegGate
	src.installAirGoal(u, n, src.newPointMarker(u, Vec3{X: 400 << 16, Z: 450 << 16}))
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == u.Handle }
	images, err := orders.RetailOrderImagesWithPayload(u, resolve, nil, src.RetailOrderPayload)
	if err != nil {
		t.Fatal(err)
	}
	image := images[0]
	record := save.OrderRecord{ParentStableID: image.ParentStableID, Main: image.Main,
		SubtypeCode: image.SubtypeCode, Subtype: image.Subtype, DescriptorName: image.DescriptorName}
	dst, w, loaded := airFixture(t)
	loaded.Move.Mode = 2
	if err := orders.RetailRestoreOrdersAtTick(loaded, []save.OrderRecord{record}, map[uint16]pool.Handle{uint16(u.Handle): loaded.Handle}, orders.QueueForUnit(loaded).Binding(), 120); err != nil {
		t.Fatal(err)
	}
	if err := dst.RestoreHeadGoal(loaded); err != nil {
		t.Fatal(err)
	}
	marker := dst.AirGoalPayload(loaded.Handle)
	for tick := uint32(121); tick <= 122; tick++ {
		runLandingTick(dst, tick, w)
	}
	if dst.AirGoalPayload(loaded.Handle) != marker || orders.QueueForUnit(loaded).Head().Phase != 2 {
		t.Fatal("restored arrival phase restarted its move installer")
	}
	if m := installedMarker(t, dst, loaded); m.goal.X != 400<<16 || m.goal.Z != 450<<16 {
		t.Fatalf("retained move marker lost its saved point: %+v", m.goal)
	}
}
