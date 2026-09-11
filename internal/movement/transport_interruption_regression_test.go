package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Script attachment shares the local-child re-arm and exact survivor purge
// with air pickup [04 R-UNIT-06 §3][04 R-AIR-01 §9][04 R-AIR-01 §10].
func TestScriptAttachmentRearmsOnlyLocalCargoOutsideAirbases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		control uint8
		airbase bool
		want    bool
	}{{"local human", 1, false, true}, {"local computer", 2, false, true}, {"remote", 3, false, false}, {"airbase", 1, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			sys, w, carrier, cargo, _, _ := transportFixture(t)
			carrier.Def.IsAirBase = tc.airbase
			cargo.Owner = 1 // the child controller, independently of the carrier
			econ := &economy.Service{}
			econ.Players[0].ControllerState = 1
			econ.Players[1].ControllerState = tc.control
			q := orders.QueueForUnit(cargo)
			q.Binding().Economy = econ
			wait, move, rear := orders.Lookup("Wait"), orders.Lookup("Move_Ground"), orders.Lookup("SelfDestruct")
			q.Push(wait, orders.NewNodeForOrder(wait, 0, 0, 0, 0, 0, cargo.Handle, true))
			q.Push(move, orders.NewNodeForOrder(move, 0, 0, 0, 0, 0, cargo.Handle, true))
			q.Push(rear, orders.NewNodeForOrder(rear, 0, 0, 0, 0, 0, cargo.Handle, true))
			waitNode, moveNode, rearNode := q.Primary()[0], q.Primary()[1], q.Secondary()[0]
			if !sys.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, -1, 0) {
				t.Fatal("script attachment failed")
			}
			if tc.want {
				if len(q.Primary()) != 2 || q.Head().ID != orders.Lookup("BeCarried") || q.Primary()[1] != waitNode {
					t.Errorf("attachment did not replace Move with BeCarried while preserving Wait: %+v", q.Primary())
				}
			} else if len(q.Primary()) != 2 || q.Primary()[0] != waitNode || q.Primary()[1] != moveNode {
				t.Error("attachment changed the exempt child's orders")
			}
			if len(q.Secondary()) != 1 || q.Secondary()[0] != rearNode {
				t.Error("attachment changed the rear segment")
			}
		})
	}
}

// The retained observer supplies validation while the live list head supplies
// lowering height and the released child [04 R-AIR-01 §10 item 4].
func TestAirUnloadSeparatesObservedCargoFromCurrentListHead(t *testing.T) {
	sys, w, carrier, observed, _, _ := transportFixture(t)
	def := *observed.Def
	def.ModelTop, def.ModelTopFixed = 19, 19<<16
	def.FootprintX, def.FootprintZ = 3, 3
	h, err := w.Create(&def, 0, world.CellToWorld(40), observed.Y, observed.Z)
	if err != nil {
		t.Fatal(err)
	}
	head := w.Unit(h)
	sys.EnsureUnit(head)
	profile := sys.ProfileFor(head.Handle)
	profile.FootPrintX, profile.FootPrintZ = 3, 3
	setHandleRow(&sys.profiles, head.Handle, &profile)
	if !AttachCargo(w, carrier.Handle, head.Handle, -1) || !AttachCargo(w, carrier.Handle, observed.Handle, -1) {
		t.Fatal("initial cargo attachment failed")
	}
	q := orders.QueueForUnit(carrier)
	id := orders.Lookup("VTOL_Unload")
	q.Push(id, orders.NewNodeForOrder(id, 0, world.CellToWorld(44), 0, world.CellToWorld(20), 0, carrier.Handle, false))
	sys.Terrain.PlotAt(43, 19).SetFeature(0xFFFB)
	n := q.Head()
	if got := sys.legVTOLUnload(carrier, n, 0, 1); got != 1 {
		t.Fatalf("phase 0: %d", got)
	}
	if n.Target != observed.Handle {
		t.Error("phase 0 did not bind its cargo observer")
	}
	if !sys.ScriptAttachCargo(w, carrier.Handle, head.Handle, -1, 0) {
		t.Fatal("same-carrier reattachment failed")
	}
	// The small observed cargo fits here, but the large new head does not.
	if !sys.ValidateUnloadSite(w, observed.Handle, n.GoalX, n.GoalZ, sys.Terrain) || sys.ValidateUnloadSite(w, head.Handle, n.GoalX, n.GoalZ, sys.Terrain) {
		t.Fatal("fixture must distinguish the two validation footprints")
	}
	n.Phase = 1
	if got := sys.legVTOLUnload(carrier, n, 0, 2); got != 1 {
		t.Fatalf("phase 1: %d", got)
	}
	m, ok := sys.AirGoalPayload(carrier.Handle).(*airMarker)
	if !ok || m.altOffset != 19 {
		t.Errorf("lowering marker = %+v, want current-head altitude 19", m)
	}
	n.Phase = 2
	if got := sys.legVTOLUnload(carrier, n, 0, 3); got != 1 {
		t.Fatalf("phase 2: %d", got)
	}
	if head.Attachment.Carrier != 0 || observed.Attachment.Carrier != carrier.Handle {
		t.Error("unload did not release the current cargo-list head")
	}
	orders.TargetRemoved(w, observed.Handle)
	if n.Target != 0 || n.Satisfied&8 == 0 {
		t.Error("cargo removal did not notify the unload observer")
	}
}
