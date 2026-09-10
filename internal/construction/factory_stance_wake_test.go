package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// The factory stance wait has no deadline; the ordinary pump consumes the
// script-touched event before StepUnit may poll its level again
// [04 §3.3][04 R-ORD-01 §1][04 R-ORD-01 §5][04 R-COB-06].
func TestFactoryStanceRequiresTheScriptEventBeforeAllocation(t *testing.T) {
	svc, factory, product, node := p28CompletionFixture(t, 2)
	product.Def.MinWaterDepth = -10000
	factory.InBuildStance = false
	node.Phase, node.DynamicGate, node.Deadline = uint8(State1), 0, -1
	node.BindTarget(0)
	q := orders.QueueForUnit(factory)
	svc.RegisterOrderHandlers(q)
	svc.StepUnit(TickContext{Tick: 100}, factory.Handle)
	if node.DynamicGate != InterruptCancel|units.PendingScriptTouched || node.Deadline != -1 {
		t.Fatalf("stance wait gate=%#x deadline=%d", node.DynamicGate, node.Deadline)
	}
	// A changed level alone is not an event. Both dispatch owners must leave
	// the parked record untouched until a script write wakes its gate.
	factory.InBuildStance = true
	q.Pump(factory, 101)
	svc.StepUnit(TickContext{Tick: 101}, factory.Handle)
	if node.Phase != uint8(State1) || node.Target != 0 {
		t.Fatalf("polled without event: phase=%d target=%d", node.Phase, node.Target)
	}
	factory.Pending |= units.PendingScriptTouched
	q.Pump(factory, 102)
	svc.StepUnit(TickContext{Tick: 102}, factory.Handle)
	if node.Phase != uint8(State3) || node.Target == 0 || factory.Pending&units.PendingScriptTouched != 0 {
		t.Fatalf("event did not allocate in its visit: phase=%d target=%d pending=%#x messages=%v", node.Phase, node.Target, factory.Pending, svc.Messages())
	}
}
