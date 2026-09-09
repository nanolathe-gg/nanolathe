package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// stubEconomy is a queue-owned economy binding stand-in. Only its
// identity matters to this test.
type stubEconomy struct {
	buckets [2]economy.Bucket
}

func (s *stubEconomy) UnitBuckets(pool.Handle) *[2]economy.Bucket { return &s.buckets }

// TestRemoveHead_PreservesQueueOwnedServices locks the queue-subtraction
// contract that a construction removal must not replace the unit's queue
// [04 §3.3][05 C21]. The queue owns its concrete binding and diagnostics;
// rebuilding the segment into a fresh
// orders.Queue silently dropped all of them, so the successor order lost
// target lookup and hostility and secondary stockpile admission lost the
// economy buckets after the first factory product completed.
func TestRemoveHead_PreservesQueueOwnedServices(t *testing.T) {
	svc := NewService(nil, nil, nil, nil)
	factory := &units.Unit{Handle: 1, Owner: 0, Alive: true}
	target := &units.Unit{Handle: 7, Owner: 1, Alive: true}

	q := orders.QueueForUnit(factory)
	econ := &stubEconomy{}
	hostilityCalls := 0
	lookupCalls := 0
	q.SetBinding(&orders.QueueBinding{
		Hostility: func(actor, other *units.Unit) bool { hostilityCalls++; return true },
		Lookup: func(h pool.Handle) *units.Unit {
			lookupCalls++
			if h == target.Handle {
				return target
			}
			return nil
		},
		Economy: econ,
	})

	build := orders.Node{Param2: 1}
	q.Push(orders.Lookup("BuildingBuild"), build)
	successor := orders.Node{Target: target.Handle}
	q.Push(orders.Lookup("Attack_Chase"), successor)
	q.PushSecondary(orders.Lookup("BuildWeapon"), orders.Node{})

	if got := q.LenPrimary(); got != 2 {
		t.Fatalf("primary length before removal = %d, want 2", got)
	}
	head := q.Head()

	svc.removeHead(factory, head)

	after := orders.QueueForUnit(factory)
	if after != q {
		t.Fatalf("removeHead replaced the unit's queue; queue identity must survive subtraction [05 C21]")
	}
	if after.Binding() == nil || after.Binding().Hostility == nil {
		t.Errorf("Hostility hook lost across removal")
	}
	if after.Binding() == nil || after.Binding().Lookup == nil {
		t.Errorf("Lookup hook lost across removal")
	}
	if after.Binding() == nil || after.Binding().Economy != econ {
		t.Errorf("Economy binding lost across removal: got %v want %v", after.Binding(), econ)
	}
	if got := after.LenSecondary(); got != 1 {
		t.Errorf("secondary segment length after removal = %d, want 1", got)
	}

	// The successor must still resolve its target through the queue's own
	// lookup, which is the concrete failure the replacement queue caused.
	if after.Binding().Hostility(factory, after.Binding().Lookup(target.Handle)) != true || lookupCalls == 0 || hostilityCalls == 0 {
		t.Errorf("successor order could not resolve target/hostility after removal")
	}

	// Exactly one primary node owns the active marker, and it is the new head.
	prim := after.Primary()
	if len(prim) != 1 {
		t.Fatalf("primary length after removal = %d, want 1", len(prim))
	}
	active := 0
	for _, n := range prim {
		if n.Flags&orders.FlagActive != 0 {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active marker count after removal = %d, want 1", active)
	}
	if prim[0].Flags&orders.FlagActive == 0 {
		t.Errorf("active marker was not handed to the removed node's successor [04 §3.3]")
	}
	if head.Flags&orders.FlagActive != 0 {
		t.Errorf("removed node still carries the active marker")
	}
}

// TestRemoveHead_PreservesDiagnostics locks that queue diagnostics recorded
// before a construction removal are still readable afterwards.
func TestRemoveHead_PreservesDiagnostics(t *testing.T) {
	svc := NewService(nil, nil, nil, nil)
	factory := &units.Unit{Handle: 1, Owner: 0, Alive: true}
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{Param2: 1})

	// Drive a real dispatch failure so the diagnostic comes from the queue's
	// own recorder rather than a test-only write.
	q.Push(orders.Lookup(""), orders.Node{})
	before := len(q.Diagnostics())

	svc.removeHead(factory, q.Head())

	after := orders.QueueForUnit(factory)
	if after != q {
		t.Fatalf("removeHead replaced the unit's queue")
	}
	if len(after.Diagnostics()) != before {
		t.Errorf("diagnostics changed across removal: got %d want %d", len(after.Diagnostics()), before)
	}
}
