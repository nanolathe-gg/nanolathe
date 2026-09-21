package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func vtolBuildFixture(t *testing.T) (*Service, *units.Unit, *orders.Node) {
	t.Helper()
	svc, builder, node := approachFixture(t, 10, 10)
	builder.Def.CanFly = true
	builder.Def.CruiseAlt = 80
	svc.Economy = &economy.Service{}
	node.ID = orders.Lookup(VTOLMobileBuildOrder)
	node.Phase, node.DynamicGate, node.Deadline = 0, 0, -1
	svc.Movement = movement.NewSystem(svc.Terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	svc.Movement.SetClasses(svc.Catalog.Movement)
	svc.Movement.BindWorld(svc.World)
	svc.Movement.EnsureUnit(builder)
	svc.Movement.ProductFootprint = func(uint32) (int32, int32, bool) { return 6, 6, true }
	return svc, builder, node
}

func restoreVTOLBuildOrder(t *testing.T, svc *Service, builder *units.Unit) *orders.Node {
	t.Helper()
	stable := make(map[uint16]pool.Handle)
	for _, u := range svc.World.Iter() {
		stable[uint16(u.Handle)] = u.Handle
	}
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), svc.World.Unit(h) != nil }
	images, err := orders.RetailOrderImagesWithPayload(builder, resolve, nil, svc.Movement.RetailOrderPayload)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{
			ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary,
			Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype,
			DescriptorName: image.DescriptorName, BuildTypeName: image.BuildTypeName,
		}
	}
	if err := orders.RetailRestoreOrdersAtTick(builder, records, stable, orders.QueueOfUnit(builder).Binding(), 0); err != nil {
		t.Fatal(err)
	}
	svc.Movement = movement.NewSystem(svc.Terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	svc.Movement.BindWorld(svc.World)
	svc.Movement.EnsureUnit(builder)
	svc.Movement.ProductFootprint = func(uint32) (int32, int32, bool) { return 6, 6, true }
	if err := svc.Movement.RestoreHeadGoal(builder); err != nil {
		t.Fatal(err)
	}
	return orders.QueueOfUnit(builder).Head()
}

// The saved phase distinguishes the climb from the site approach. A newly
// constructed movement system must honor that phase without private executor
// state [04 R-ORD-02 §2][08 R-SAVE-ORDER-01].
func TestVTOLBuildSavedApproachPhaseDistinguishesMovementWakes(t *testing.T) {
	svc, builder, node := vtolBuildFixture(t)
	svc.Pump(builder, 1)
	if node.Phase != 1 || node.DynamicGate != orders.ApproachWakeGate {
		t.Fatalf("takeoff phase/gate = %d/%#x, want 1/%#x", node.Phase, node.DynamicGate, orders.ApproachWakeGate)
	}
	node = restoreVTOLBuildOrder(t, svc, builder)
	node.Satisfied |= 0x20
	pumpApproach(svc, builder, 2)
	if node.Phase != 2 || node.Target != 0 || node.DynamicGate != orders.ApproachWakeGate {
		t.Fatalf("climb wake phase/target/gate = %d/%d/%#x, want site wait at phase 2", node.Phase, node.Target, node.DynamicGate)
	}
	node = restoreVTOLBuildOrder(t, svc, builder)
	// No second outcome means no placement, even though the aircraft has
	// already consumed a movement wake on this same record.
	pumpApproach(svc, builder, 3)
	if node.Phase != 2 || node.Target != 0 {
		t.Fatalf("unawakened site phase/target = %d/%d", node.Phase, node.Target)
	}
	node.Satisfied |= 0x40
	pumpApproach(svc, builder, 4)
	if orders.QueueOfUnit(builder).Head() == node || node.Target != 0 {
		t.Fatal("site no-route wake must abandon before allocating a nanoframe")
	}
}

// Both air work phases survive in a save; phase 4 remains a work body, unlike
// the ground/factory completion phase with the same number. Phase 3's stance
// poll does not gate the quantum [04 R-ORD-02 §2].
func TestVTOLBuildWorkPhasesResumeWithoutCompletionOrReveal(t *testing.T) {
	for _, phase := range []uint8{3, 4} {
		t.Run(string(rune('0'+phase)), func(t *testing.T) {
			svc, builder, node := vtolBuildFixture(t)
			product, err := svc.World.Create(svc.Catalog.Units["corlab"], builder.Owner, node.GoalX, 0, node.GoalZ)
			if err != nil {
				t.Fatal(err)
			}
			cargo := svc.World.Unit(product)
			cargo.Remaining = 1
			node.BindTarget(product)
			node.Phase = phase
			node = restoreVTOLBuildOrder(t, svc, builder)
			builder.InBuildStance = false
			builder.RevealDeadline = 77
			svc.Pump(builder, 31)
			if !(cargo.Remaining < 1 && cargo.Remaining > 0) || node.Phase != phase || orders.QueueOfUnit(builder).Head() != node {
				t.Fatalf("phase %d work remaining/phase = %v/%d", phase, cargo.Remaining, node.Phase)
			}
			if builder.RevealDeadline != 77 {
				t.Fatalf("air work changed reveal deadline to %d", builder.RevealDeadline)
			}
			wantGate := uint32(0xB)
			if phase == 3 {
				wantGate |= 4
			}
			if node.Deadline != 32 || node.DynamicGate != wantGate {
				t.Fatalf("work deadline/gate = %d/%#x, want 32/%#x", node.Deadline, node.DynamicGate, wantGate)
			}
		})
	}
}

// A due orbit is part of the work visit even when that quantum finishes the
// product. Completion then belongs to phase 5, through the ordinary epilogue
// [04 §10.3][04 R-ORD-02 §2].
func TestVTOLBuildCompletingWorkInstallsOrbitBeforePhaseFive(t *testing.T) {
	svc, builder, node := vtolBuildFixture(t)
	h, err := svc.World.Create(svc.Catalog.Units["corlab"], builder.Owner, node.GoalX, 0, node.GoalZ)
	if err != nil {
		t.Fatal(err)
	}
	product := svc.World.Unit(h)
	product.Remaining = 0.01
	node.BindTarget(h)
	node.Phase = 4
	code, applied := svc.vtolBuildVisit(builder, node, 0, 150)
	if code != 1 || !applied || product.Remaining != 0 || node.Phase != 4 {
		t.Fatalf("finishing visit code/applied/remaining/phase = %d/%v/%v/%d", code, applied, product.Remaining, node.Phase)
	}
	payload, err := svc.Movement.RetailOrderPayload(node)
	if err != nil || payload.Code != 2 {
		t.Fatalf("finishing visit installed no orbit: payload=%+v err=%v", payload, err)
	}
	// Apply the advance the ordinary pump owns, then visit the saved completion
	// phase; it must not be mistaken for an invalid phase and reset to takeoff.
	node.Phase++
	var completed int
	orders.QueueOfUnit(builder).Binding().Presentation = &orders.PresentationAdapter{
		Status: func(_ *units.Unit, _ uint8, text string) bool {
			if text == "Building complete" {
				completed++
			}
			return true
		},
	}
	svc.Pump(builder, 150)
	if orders.QueueOfUnit(builder).Head() == node || completed != 1 {
		t.Fatalf("completion kept record or omitted caption: retained=%v captions=%d", orders.QueueOfUnit(builder).Head() == node, completed)
	}
}

// A `VTOL_MobileBuild` record whose product resolves to nothing must not hand
// the primary pump a hold with no gate: the pump reloads the head on a hold
// [04 R-ORD-01 §10], so that pair never leaves the tick. The ground row answers
// the same condition with retention from the per-unit step; this row reports
// the record as not advanced, which is the same retention seen from the pump.
func TestVTOLBuildPlacementWithoutAProductDoesNotHoldThePump(t *testing.T) {
	svc, builder, node := vtolBuildFixture(t)
	node.Phase = 2
	node.BuildDefKey, node.Param1 = "", 0
	if svc.getProductDefForNode(node) != nil {
		t.Fatal("the fixture's record still resolves a product; the test proves nothing")
	}
	code, advanced := svc.vtolBuildVisit(builder, node, 0, 150)
	if advanced {
		t.Fatalf("visit returned code %d as a pump result; with gate %#x and deadline %d the pump would reload it forever", code, node.DynamicGate, node.Deadline)
	}
	if node.Phase != 2 {
		t.Fatalf("phase = %d, want the record retained at placement", node.Phase)
	}
}
