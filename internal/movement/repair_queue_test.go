package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func repairQueueFixture(t *testing.T, count int) (*System, *units.World, *units.Unit, []*units.Unit) {
	t.Helper()
	s, w, first := airFixture(t)
	s.Rules = &ModernRules{}
	pad := spawnAirBasePad(t, w, s.Terrain, "queuepad", 256<<16, 256<<16)
	s.EnsureUnit(pad)
	aircraft := []*units.Unit{first}
	for i := 1; i < count; i++ {
		h, err := w.Create(first.Def, 0, numeric.Fixed((64+i*32)<<16), first.Y, 64<<16)
		if err != nil {
			t.Fatal(err)
		}
		u := w.Unit(h)
		u.Remaining = 0
		s.EnsureUnit(u)
		aircraft = append(aircraft, u)
	}
	b := orders.QueueForUnit(first).Binding()
	b.Movement = &orders.MovementGoalAdapter{RunAir: s.AirLegRunner(), Release: s.ReleaseGoalPayload}
	for _, u := range aircraft {
		u.Health, u.Activated = 50, true
		q := orders.QueueForUnit(u)
		q.SetBinding(b)
		q.Push(orders.Lookup("VTOL_Landing"), orders.Node{Owner: u.Handle, Target: pad.Handle})
	}
	return s, w, pad, aircraft
}

func enterRepairQueue(s *System, u *units.Unit, tick uint32) {
	n := orders.QueueForUnit(u).Head()
	n.Phase = 1
	s.legVTOLLanding(u, n, 0, tick)
}

func TestModernRepairQueueReservesAuthoredPiecesInOrder(t *testing.T) {
	s, _, pad, planes := repairQueueFixture(t, 4)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	if len(s.repairLandings) != 4 || !s.repairLandings[0].reserved || !s.repairLandings[1].reserved ||
		s.repairLandings[0].piece != 0 || s.repairLandings[1].piece != 1 {
		t.Fatal("the first two aircraft must reserve the two authored pieces")
	}
	for i := 2; i < 4; i++ {
		e := s.repairLandings[i]
		if e.reserved || !e.holding || e.node.Phase != 1 || e.node.Deadline != 31 || e.node.DynamicGate != 1 {
			t.Fatalf("waiter %d lost its landing or retry deadline: %+v", i, e)
		}
		m := s.AirMarkerState(planes[i].Handle)
		if m.AltOffset != int16(planes[i].Def.CruiseAlt) || airPlanarDistance(m.Goal.X, m.Goal.Z, pad.X, pad.Z) < 64<<16 {
			t.Fatal("even an unarmed aircraft must wait airborne away from the landing pieces")
		}
		if planes[i].Health != 50 || planes[i].Attachment.Carrier != 0 {
			t.Fatal("waiting healed or attached an aircraft")
		}
	}
	if airPlanarDistance(s.repairLandings[2].post.X, s.repairLandings[2].post.Z, s.repairLandings[3].post.X, s.repairLandings[3].post.Z) < 64<<16 {
		t.Fatal("waiting aircraft share a holding station")
	}
	if orders.QueueForUnit(planes[0]).Binding().SimRNG.Draws() != 0 {
		t.Fatal("reservation and holding consumed randomness")
	}
	// Marker replacement must not release a reservation. Cancellation must.
	s.installAirGoal(planes[0], s.repairLandings[0].node, s.newPointMarker(planes[0], Vec3{}))
	if !s.repairLandings[0].reserved {
		t.Fatal("marker replacement lost a live reservation")
	}
	orders.QueueForUnit(planes[0]).RemoveHead()
	// Visit the later waiter first: the older waiter still gets the free piece.
	enterRepairQueue(s, planes[3], 31)
	if !s.repairLandings[1].reserved || s.repairLandings[1].unit != planes[2] || s.repairLandings[2].reserved {
		t.Fatal("a later waiter overtook the older repair request")
	}
}

func TestRepairQueueStrictBypassAndSwitches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules Rules
		queue bool
	}{{"unbound", nil, false}, {"strict", StrictRules{}, false}, {"community", CommunityRules{}, false}, {"modern", &ModernRules{}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, planes := repairQueueFixture(t, 2)
			s.Rules = tc.rules
			for _, u := range planes {
				if code := s.legVTOLLanding(u, orders.QueueForUnit(u).Head(), 0, 1); code != 1 {
					t.Fatalf("takeoff returned %d", code)
				}
				enterRepairQueue(s, u, 2)
			}
			if got := orders.QueueForUnit(planes[0]).Binding().SimRNG.Draws(); got != 2 {
				t.Fatalf("takeoff draw count = %d, want one per landing", got)
			}
			if (len(s.repairLandings) != 0) != tc.queue {
				t.Fatal("queue state escaped its rule-set selection")
			}
			if !tc.queue {
				for _, u := range planes {
					n := orders.QueueForUnit(u).Head()
					n.Phase = 3
					s.legVTOLLanding(u, n, 0, 3)
					if n.Param1 != 0 {
						t.Fatal("retail must still offer the first free piece to both approaches")
					}
				}
			}
		})
	}
	s, _, _, planes := repairQueueFixture(t, 2)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	s.Rules = StrictRules{}
	s.BeginTick(2)
	if len(s.repairLandings) != 0 {
		t.Fatal("switching to Strict retained reservations")
	}
	s.Rules = &ModernRules{}
	for _, u := range planes {
		enterRepairQueue(s, u, 3)
	}
	if len(s.repairLandings) != 2 || s.repairLandings[0].piece == s.repairLandings[1].piece {
		t.Fatal("switching back did not reacquire distinct pieces")
	}
}

func TestModernRepairQueueLostPadAndAircraftReuse(t *testing.T) {
	s, w, pad, planes := repairQueueFixture(t, 3)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	first := planes[0]
	s.ForgetUnit(first.Handle)
	w.FreeImmediate(first.Handle)
	h, err := w.Create(first.Def, 0, 80<<16, 10<<16, 80<<16)
	if err != nil {
		t.Fatal(err)
	}
	if h != first.Handle {
		t.Fatal("fixture did not reuse the freed slot")
	}
	enterRepairQueue(s, planes[2], 31)
	if len(s.repairLandings) != 2 || !s.repairLandings[1].reserved {
		t.Fatal("a freed aircraft blocked the next patient")
	}
	orders.TargetRemoved(w, pad.Handle)
	s.TargetRemoved(pad.Handle)
	pad.Alive = false
	for _, u := range planes[1:] {
		enterRepairQueue(s, u, 32)
	}
	anchor := s.repairLandings[0].anchor
	for tick := uint32(62); tick < 180; tick += 30 {
		for _, u := range planes[1:] {
			enterRepairQueue(s, u, tick)
		}
	}
	if s.repairLandings[0].anchor != anchor || !s.repairLandings[0].holding {
		t.Fatal("loss of the last pad must hold near its last position")
	}
	replacement := spawnAirBasePad(t, w, s.Terrain, "replacement", 350<<16, 350<<16)
	enterRepairQueue(s, planes[1], 181)
	if orders.QueueForUnit(planes[1]).Head().Target != replacement.Handle || !s.repairLandings[0].reserved {
		t.Fatal("waiting aircraft did not find the replacement friendly pad")
	}
}

func TestModernRepairHoldingUsesVisibleThreatsOnly(t *testing.T) {
	s, w, _, planes := repairQueueFixture(t, 3)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	u := planes[2]
	e := s.repairLandings[2]
	threat := spawnAirBasePadFor(t, w, s.Terrain, "threat", 1, 480<<16, 256<<16)
	threat.SlotAt(0).Weapon = &content.WeaponDef{Range: 200}
	b := orders.QueueForUnit(u).Binding()
	b.DangerVisible = func(_, target *units.Unit) bool { return target == threat }
	point := s.repairHoldingPoint(e)
	if point.X >= e.pad.X {
		t.Fatal("holding did not move to the side away from the visible threat")
	}
	b.DangerVisible = func(_, _ *units.Unit) bool { return false }
	hidden := s.repairHoldingPoint(e)
	threat.X = 64 << 16
	if moved := s.repairHoldingPoint(e); hidden != moved {
		t.Fatal("an unseen enemy position changed the waiting station")
	}
}

func TestModernRepairQueueRestoreReacquiresBeforeTouchdown(t *testing.T) {
	for _, phase := range []uint8{1, 3, 5, 6} {
		t.Run(string(rune('0'+phase)), func(t *testing.T) {
			src, _, pad, planes := repairQueueFixture(t, 1)
			u := planes[0]
			enterRepairQueue(src, u, 1)
			n := orders.QueueForUnit(u).Head()
			n.Phase, n.DynamicGate, n.Deadline = phase, 1, 30
			resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == u.Handle || h == pad.Handle }
			images, err := orders.RetailOrderImagesWithPayload(u, resolve, nil, src.RetailOrderPayload)
			if err != nil {
				t.Fatal(err)
			}
			im := images[0]
			record := save.OrderRecord{ParentStableID: im.ParentStableID, Main: im.Main, SubtypeCode: im.SubtypeCode, Subtype: im.Subtype, DescriptorName: im.DescriptorName}
			dst, _, loadedPad, loaded := repairQueueFixture(t, 2)
			enterRepairQueue(dst, loaded[1], 1)
			b := orders.QueueForUnit(loaded[0]).Binding()
			if err := orders.RetailRestoreOrdersAtTick(loaded[0], []save.OrderRecord{record}, map[uint16]pool.Handle{uint16(u.Handle): loaded[0].Handle, uint16(pad.Handle): loadedPad.Handle}, b, 30); err != nil {
				t.Fatal(err)
			}
			if err := dst.RestoreHeadGoal(loaded[0]); err != nil {
				t.Fatal(err)
			}
			dst.legVTOLLanding(loaded[0], orders.QueueForUnit(loaded[0]).Head(), 0x20, 31)
			if loaded[0].Attachment.Carrier != 0 || len(dst.repairLandings) != 2 || dst.repairLandings[0].piece == dst.repairLandings[1].piece {
				t.Fatal("restored landing skipped reservation and fresh approach")
			}
		})
	}
}

func TestModernRepairQueueSuspensionKeepsItsApproachReserved(t *testing.T) {
	s, _, _, planes := repairQueueFixture(t, 3)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	first := s.repairLandings[0]
	q := orders.QueueForUnit(planes[0])
	marker := s.AirGoalPayload(planes[0].Handle)
	q.PushHead(orders.Lookup("Paralyze"), orders.Node{Owner: planes[0].Handle, DynamicGate: 1, Deadline: 100})
	s.BeginTick(2)
	enterRepairQueue(s, planes[2], 31)
	if len(s.repairLandings) != 3 || !first.reserved || s.repairLandings[2].reserved || s.AirGoalPayload(planes[0].Handle) != marker {
		t.Fatal("a temporary head released a piece while the suspended marker still approached it")
	}
	q.RemoveHead()
	q.RemoveHead()
	enterRepairQueue(s, planes[2], 32)
	if !s.repairLandings[1].reserved {
		t.Fatal("removing the landing did not release its reserved piece")
	}
}

func TestModernRepairHoldingAvoidsOccupiedAirFootprint(t *testing.T) {
	s, w, _, planes := repairQueueFixture(t, 3)
	for _, u := range planes {
		enterRepairQueue(s, u, 1)
	}
	e := s.repairLandings[2]
	preferred := s.repairHoldingPoint(e)
	h, err := w.Create(planes[0].Def, 0, preferred.X, preferred.Y, preferred.Z)
	if err != nil {
		t.Fatal(err)
	}
	blocker := w.Unit(h)
	blocker.Move.Mode = 2
	s.EnsureUnit(blocker)
	c := handleRow(s.Collisions, h)
	bx, bz := c.HalfBias()
	at := QuantizedAnchor(int32(preferred.X), int32(preferred.Z), bx, bz)
	s.Grid.StampPlane(PlaneAir, at, c.FootPrintX, c.FootPrintZ, int(h))
	if point := s.repairHoldingPoint(e); point == preferred || !s.repairStationFree(e.unit, point) {
		t.Fatal("holding station ignored an unrelated aircraft's air footprint")
	}
}

func TestModernRepairQueueLoadedTransportBypassesReservations(t *testing.T) {
	s, w, pad, planes := repairQueueFixture(t, 1)
	u := planes[0]
	h, err := w.Create(defForCargo("carried", 1), 0, u.X, u.Y, u.Z)
	if err != nil {
		t.Fatal(err)
	}
	if !AttachCargo(w, u.Handle, h, 0) {
		t.Fatal("load cargo")
	}
	n := orders.QueueForUnit(u).Head()
	n.Phase = 6
	if code := s.legVTOLLanding(u, n, 0x20, 1); code != 5 || w.Unit(h).Attachment.Carrier != pad.Handle || len(s.repairLandings) != 0 {
		t.Fatal("loaded transport did not retain ordinary cargo landing")
	}
}
