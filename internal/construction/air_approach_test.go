package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The site marker is installed by the phase-1 order visit. Its arrival wakes
// phase 2; no ground rectangle belongs to this path [04 R-ORD-02 §2].
func TestAirApproachHoldsWithoutAGroundGoal(t *testing.T) {
	svc, builder, node := vtolBuildFixture(t)
	node.Phase = 1
	svc.Pump(builder, 1)
	if node.Phase != 2 || node.DynamicGate != orders.ApproachWakeGate || node.Target != 0 {
		t.Fatalf("site approach phase/gate/target = %d/%#x/%d", node.Phase, node.DynamicGate, node.Target)
	}
	if svc.Movement.HasGroundGoal(builder.Handle, node) {
		t.Fatal("an aircraft installed a ground rectangle goal")
	}
}

// TestAirApproachAbandonsOnNoRouteWithoutACaption: `VTOL_MobileBuild` phase 2
// "satisfied `0x40` → abandon" — result 8, no reach expression, no caption; the
// ground twin's status 7 `I can't reach the construction site` is not raised
// [04 R-ORD-02 §2].
func TestAirApproachAbandonsOnNoRouteWithoutACaption(t *testing.T) {
	svc, builder, node := vtolBuildFixture(t)
	node.Phase = 2
	var captions int
	q := orders.QueueOfUnit(builder)
	binding := q.Binding()
	binding.Presentation = &orders.PresentationAdapter{
		Status: func(*units.Unit, uint8, string) bool {
			captions++
			return true
		},
	}
	q.SetBinding(binding)

	code, applied := svc.vtolBuildVisit(builder, node, 0x40, 1)
	if !applied || code != 8 {
		t.Fatalf("no-route wake returned (%d, %v), want (8, true): abandon [04 R-ORD-02 §2]", code, applied)
	}
	if captions != 0 {
		t.Fatalf("%d captions raised on the air abandon, want none [04 R-ORD-02 §2]", captions)
	}
	if node.Target != 0 {
		t.Fatal("an abandoned air build must not create a product")
	}
}
