package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestProductRemovalStopUsesTheOrdinaryPumpHandoff covers the real visit
// boundary: the order pump runs before construction StepUnit. Product removal
// delivers bit 3 through the pump's satisfied argument, where the construction
// handler consumes it once; StepUnit then finds a surviving restarted request
// instead of its null-product fallback dropping production [04 §3.3]
// [04 R-ORD-01 §6][05 "Cancel-current and stop interrupts"].
func TestProductRemovalStopUsesTheOrdinaryPumpHandoff(t *testing.T) {
	svc, factory, product, node := factoryWithAttachedProduct(t)
	q := orders.QueueForUnit(factory)
	svc.RegisterOrderHandlers(q)

	refreshes := 0
	svc.OnRefresh = func(*units.Unit) { refreshes++ }
	if !svc.NotifyProductRemoved(product.Handle) {
		t.Fatal("product removal did not notify the builder")
	}

	// This is the session's order-pump-before-StepUnit boundary, not the old
	// direct Service.Pump shortcut.
	(&orders.Pump{World: svc.World}).PumpUnit(factory.Handle, 100)
	if node.Param2 != 2 || node.Phase != uint8(State0) {
		t.Fatalf("pump stop left count=%d phase=%d, want count 2 and restart", node.Param2, node.Phase)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("pump stop removed production queue, primary length %d", q.LenPrimary())
	}
	if factory.Pending&InterruptStop != 0 {
		t.Fatalf("stop remained pending after its one handler visit: %#x", factory.Pending)
	}
	if product.Dying || svc.LastKill().Damage != 0 {
		t.Fatalf("stop must not take cancel/death path: dying=%v packet=%+v", product.Dying, svc.LastKill())
	}

	res := svc.StepUnit(TickContext{Tick: 100, World: svc.World, Economy: svc.Economy, Catalog: svc.Catalog}, factory.Handle)
	if res.Err != nil {
		t.Fatalf("StepUnit after ordinary stop handoff: %v", res.Err)
	}
	if q.LenPrimary() != 1 || node.Param2 != 2 {
		t.Fatalf("StepUnit discarded or decremented surviving production: len=%d count=%d", q.LenPrimary(), node.Param2)
	}
	if refreshes != 1 {
		t.Fatalf("stop refreshes=%d, want one", refreshes)
	}
}

// TestProductRemovalStopBeatsAnArmedRetryDeadline locks the same handoff when
// state 3 is parked for a future retry. A stop notification is already a
// satisfied gate bit, so the ordinary pump delivers it now; it is not deferred
// until the retry deadline and it is not discarded by an external no-op.
func TestProductRemovalStopBeatsAnArmedRetryDeadline(t *testing.T) {
	svc, factory, product, node := factoryWithAttachedProduct(t)
	q := orders.QueueForUnit(factory)
	svc.RegisterOrderHandlers(q)
	node.Deadline = 200

	if !svc.NotifyProductRemoved(product.Handle) {
		t.Fatal("product removal did not notify the builder")
	}
	(&orders.Pump{World: svc.World}).PumpUnit(factory.Handle, 100)
	if node.Param2 != 2 || node.Phase != uint8(State0) || node.Deadline != -1 {
		t.Fatalf("early stop = count %d phase %d deadline %d, want 2, 0, -1", node.Param2, node.Phase, node.Deadline)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("early stop removed production queue, primary length %d", q.LenPrimary())
	}
}

// TestOrdinaryPumpCancelDoesNotLeakACompletionResult covers the other
// construction interrupt received through this handler. Cancel-current applies
// its required posture before its kill packet, but that dying product is not a
// completed product for a later StepUnit/session completion hook [05
// "Cancel-current and stop interrupts"].
func TestOrdinaryPumpCancelDoesNotLeakACompletionResult(t *testing.T) {
	svc, factory, product, _ := factoryWithAttachedProduct(t)
	q := orders.QueueForUnit(factory)
	svc.RegisterOrderHandlers(q)
	factory.Pending = InterruptCancel

	(&orders.Pump{World: svc.World}).PumpUnit(factory.Handle, 100)
	if q.LenPrimary() != 0 || !product.Dying {
		t.Fatalf("ordinary cancel queue=%d dying=%v, want removed and dying", q.LenPrimary(), product.Dying)
	}
	if got := svc.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production; got != 50 {
		t.Fatalf("cancel refund=%v, want one 50 refund", got)
	}
	if svc.LastKill().Damage != Kind9Damage {
		t.Fatalf("cancel packet=%+v, want one kind-9 packet", svc.LastKill())
	}
	if svc.completedInPump != 0 {
		t.Fatalf("ordinary cancel left completion handle %d for another builder", svc.completedInPump)
	}
	res := svc.StepUnit(TickContext{Tick: 100, World: svc.World, Economy: svc.Economy, Catalog: svc.Catalog}, factory.Handle)
	if res.Completed || res.Product != 0 {
		t.Fatalf("cancelled product reported as completion: %+v", res)
	}
	if got := svc.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production; got != 50 {
		t.Fatalf("StepUnit repeated cancellation refund: %v", got)
	}
}
