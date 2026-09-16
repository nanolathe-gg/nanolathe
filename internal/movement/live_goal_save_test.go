package movement

import (
	"testing"

	"bytes"
	"encoding/binary"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func saveGoalOrders(t *testing.T, s *System, u *units.Unit) []save.OrderRecord {
	t.Helper()
	imgs, err := orders.RetailOrderImagesWithPayload(u, func(h pool.Handle) (uint16, bool) { return uint16(h), true }, nil, s.RetailOrderPayload)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]save.OrderRecord, len(imgs))
	for i, x := range imgs {
		out[i] = save.OrderRecord{ParentStableID: x.ParentStableID, Sequence: x.Sequence, Secondary: x.Secondary, Main: x.Main, SubtypeCode: x.SubtypeCode, Subtype: x.Subtype, DescriptorName: x.DescriptorName}
	}
	return out
}

// A normal terrain descent must retain the goal that produces its wake after
// saving. No synthetic satisfaction or extra handler dispatch repairs the load
// [08 R-SAVE-02 §10, §11][04 R-AIR-01 §6].
func TestLiveTerrainLandingSaveResumes(t *testing.T) {
	s, w, u := airFixture(t)
	s.SetMoverMode(u, 2)
	u.Y += 100 << 16
	n := pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
	runLandingTick(s, 1, w)
	runLandingTick(s, 2, w)
	if n.Phase != 2 || s.AirGoalPayload(u.Handle) == nil {
		t.Fatal("descent not armed")
	}
	records := saveGoalOrders(t, s, u)
	if records[0].SubtypeCode != 2 {
		t.Fatal("descent goal not saved")
	}
	next, nw, nu := airFixture(t)
	next.SetMoverMode(nu, 2)
	nu.X, nu.Y, nu.Z = u.X, u.Y, u.Z
	binding := orders.QueueOfUnit(nu).Binding()
	if err := orders.RetailRestoreOrdersAtTick(nu, records, map[uint16]pool.Handle{uint16(nu.Handle): nu.Handle}, binding, 2); err != nil {
		t.Fatal(err)
	}
	if err := next.RestoreHeadGoal(nu); err != nil {
		t.Fatal(err)
	}
	for tick := uint32(3); tick < 500 && nu.Move.Mode != 1; tick++ {
		runLandingTick(next, tick, nw)
	}
	if nu.Move.Mode != 1 {
		t.Fatal("restored landing remained stuck waiting for movement")
	}
}

// Restored displaced objects retain their separate lifetime. Releasing one
// unbinds the controller; destroying it only destroys itself [04 R-ORD-01 §9].
func TestRestoredDisplacedGoalOwnsReleaseAndSave(t *testing.T) {
	for _, destroy := range []bool{false, true} {
		s, w, h := releaseFixture(t, wiringDef(), 2)
		u := w.Unit(h)
		head := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: h, Satisfied: 0x400}
		tail := &orders.Node{ID: orders.Lookup("Patrol"), Owner: h, Satisfied: 0x800}
		orders.BindQueue(u, orders.NewQueueWith([]*orders.Node{head, tail}, nil))
		s.installGroundPayload(h, tail, path.AnnulusGoalRestored(path.Cell{X: -3, Z: 7}, 64, 128, 4, 9), 0, 0)
		s.installGroundPayload(h, head, path.PointGoalRestored(path.Cell{X: 2, Z: -4}, 17, 123), 0, 0)
		records := saveGoalOrders(t, s, u)
		next := NewSystem(s.Terrain, Profile{}, NewOccupancyGrid())
		next.BindWorld(w)
		if err := orders.RetailRestoreOrdersAtTick(u, records, map[uint16]pool.Handle{uint16(h): h}, nil, 0); err != nil {
			t.Fatal(err)
		}
		if err := next.RestoreHeadGoal(u); err != nil {
			t.Fatal(err)
		}
		q := orders.QueueOfUnit(u)
		head, tail = q.Primary()[0], q.Primary()[1]
		again := saveGoalOrders(t, next, u)
		for i := range records {
			if !bytes.Equal(records[i].Subtype, again[i].Subtype) || !bytes.Equal(records[i].Main, again[i].Main) {
				t.Fatal("load/save changed independent radius thresholds or pending state")
			}
		}
		prior := head.Satisfied
		if destroy {
			next.ReleaseGoal(tail)
		} else if !next.ReleaseGoalPayload(tail) {
			t.Fatal("restored displaced object was lost")
		}
		if destroy {
			if head.Satisfied != prior || !next.HasGroundGoal(h, head) {
				t.Fatal("tail destructor disturbed head")
			}
		} else if head.Satisfied != prior|0x80 || next.HasGroundGoal(h, head) {
			t.Fatal("tail explicit release did not unbind head")
		}
		if saveGoalOrders(t, next, u)[1].SubtypeCode != 0 {
			t.Fatal("released staged payload reappeared")
		}
	}
}

// The velocity object mutates independently of the order's stored position.
// Unknown words belong only to that restored object [08 R-SAVE-02 §10].
func TestVelocityGoalSaveUsesLiveStateAndObjectScratch(t *testing.T) {
	s, _, u := airFixture(t)
	n := &orders.Node{Owner: u.Handle, RetailSubtypeCode: 3, RetailSubtypeWords16: []uint16{0x100, 0x1234, 0x5678, 0xabcd}, RetailSubtypeWords32: []uint32{1, 2, 3, 4, 5, 6}}
	orders.BindQueue(u, orders.NewQueueWith([]*orders.Node{n}, nil))
	if err := s.RestoreHeadGoal(u); err != nil {
		t.Fatal(err)
	}
	m := s.AirGoalPayload(u.Handle).(*airVelocityMarker)
	var dst Vec3
	m.UpdateGoal(u, &dst)
	image, err := s.RetailOrderPayload(n)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(image.Data[0xc:]) != 5 || binary.LittleEndian.Uint16(image.Data[0xa:]) != 0x100 || binary.LittleEndian.Uint16(image.Data[0x24:]) != 0x1234 || binary.LittleEndian.Uint16(image.Data[0x28:]) != 0xabcd {
		t.Fatal("live velocity state or retained unknown words were lost")
	}
	s.installAirPayload(u, n, &airVelocityMarker{sys: s, unit: u})
	image, err = s.RetailOrderPayload(n)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(image.Data[0x24:]) != 0 || binary.LittleEndian.Uint16(image.Data[0x28:]) != 0 {
		t.Fatal("new marker inherited old object's scratch")
	}
}

func TestSecondaryGoalRestoreDoesNotBindController(t *testing.T) {
	s, w, h := releaseFixture(t, wiringDef(), 2)
	u := w.Unit(h)
	n := &orders.Node{ID: orders.Lookup("QMove"), Owner: h, RetailSubtypeCode: 6, RetailSubtypeWords32: []uint32{1, 4, 2, 7}}
	orders.BindQueue(u, orders.NewQueueWith(nil, []*orders.Node{n}))
	if err := s.RestoreHeadGoal(u); err != nil {
		t.Fatal(err)
	}
	image, err := s.RetailOrderPayload(n)
	if err != nil || image.Code != 6 || s.HasGroundGoal(h, n) {
		t.Fatalf("secondary record must retain its object without binding: %+v, %v", image, err)
	}
	s.ReleaseGoal(n)
	s.BindMoveGoal(h, n, 32<<16, 64<<16)
	image, err = s.RetailOrderPayload(n)
	if err != nil || image.Code != 0 {
		t.Fatalf("steering-only binding fabricated a saved object: %+v, %v", image, err)
	}
}
