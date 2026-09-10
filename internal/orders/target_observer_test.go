package orders

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func observerFixture(t *testing.T) (*units.World, *units.Unit, *units.Unit) {
	t.Helper()
	w := newOrdersFixtureWorld(8, &content.Catalog{})
	def := &content.UnitDef{UnitName: "observer", MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	target, _ := w.Create(def, 1, 0, 0, 0)
	return w, w.Unit(h), w.Unit(target)
}

// The guard starts without a target, then explicitly binds its acquired unit.
// Constructor static admission must not suppress that later observer link
// [04 R-ORD-01 §3][04 R-MOV-03 §7].
func TestStationaryGuardBindsTargetObserver(t *testing.T) {
	w, guard, target := observerFixture(t)
	guard.InstallWeapon(0, &content.WeaponDef{Range: 100})
	guard.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	q := QueueForUnit(guard)
	q.SetBinding(&QueueBinding{Lookup: w.Unit})
	q.Push(Lookup("Guard_NoMove"), Node{Owner: guard.Handle})
	n := q.Primary()[0]
	if n.StaticGate&staticTargetObserver != 0 || n.Target != 0 {
		t.Fatal("stationary guard did not begin with an unlinked target")
	}
	n.Phase = 1
	if code := guardNoMoveHandler(guard, n, 0, 30); code != 1 || n.Target != target.Handle {
		t.Fatal("guard did not bind its acquired target")
	}
	n.Phase, n.DynamicGate = 2, 0x7008
	TargetRemoved(w, target.Handle)
	if n.Target != 0 || n.Satisfied&pendTargetRemoved == 0 || guard.Pending != 0 {
		t.Fatal("target removal did not unlink and wake the guard record")
	}
	if code := guardNoMoveHandler(guard, n, n.Satisfied&n.DynamicGate, 31); code != 2 || n.Phase != 3 {
		t.Fatal("target removal did not move the guard to its rescan")
	}
	// A repeated event for a reused handle cannot reach the unlinked record.
	n.Satisfied = 0
	TargetRemoved(w, target.Handle)
	TargetCloaked(w, target.Handle)
	if n.Satisfied != 0 {
		t.Fatal("a removed target handle revived its observer")
	}
}

// Every registered record receives its own pending word, including records
// behind a different head and records on the other segment [04 R-MOV-03 §7].
func TestTargetObserverEventsBelongToEachRecord(t *testing.T) {
	w, watcher, target := observerFixture(t)
	q := QueueForUnit(watcher)
	front := newNode(Lookup("Attack_NoMove"), Node{Owner: watcher.Handle, Target: target.Handle})
	behind := newNode(Lookup("Attack_Chase"), Node{Owner: watcher.Handle, Target: target.Handle})
	rear := newNode(Lookup("BuildWeapon"), Node{Owner: watcher.Handle})
	rear.BindTarget(target.Handle)
	q.SetPrimary([]*Node{front, behind})
	q.SetSecondary([]*Node{rear})
	ObserverNotice(w, target)
	TargetCloaked(w, target.Handle)
	for _, n := range []*Node{front, behind, rear} {
		if n.Satisfied != observerNotice|pendTargetCloaked || n.Target != target.Handle {
			t.Fatal("damage/cloak did not notify each record while preserving its link")
		}
	}
	front.Satisfied = 0 // the front record consumes only its own notice
	if behind.Satisfied == 0 || rear.Satisfied == 0 || watcher.Pending != 0 {
		t.Fatal("the front record consumed another observer's event")
	}
	TargetRemoved(w, target.Handle)
	for _, n := range []*Node{front, behind, rear} {
		if n.Target != 0 || n.Satisfied&pendTargetRemoved == 0 {
			t.Fatal("removal did not notify and unlink every observing record")
		}
	}
}

// The save reader relinks the main target independently of the restored static
// issued-target bit [08 R-SAVE-ORDER-01]. No extra saved linkage state exists.
func TestRestoredGuardTargetObserver(t *testing.T) {
	w, guard, target := observerFixture(t)
	data := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(data, 1)
	binary.LittleEndian.PutUint16(data[2:], 2)
	data[8], data[9] = byte(Lookup("Guard_NoMove")), 2
	binary.LittleEndian.PutUint32(data[0x0a:], 0x7008)
	binary.LittleEndian.PutUint32(data[0x32:], DescriptorFor(Lookup("Guard_NoMove")).StaticGate)
	err := RetailRestoreOrders(guard, []save.OrderRecord{{ParentStableID: 1, Main: data}}, map[uint16]pool.Handle{1: guard.Handle, 2: target.Handle}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := QueueOfUnit(guard).Primary()[0]
	if n.StaticGate&staticTargetObserver != 0 {
		t.Fatal("restore changed the saved issued-target bit")
	}
	TargetRemoved(w, target.Handle)
	if n.Target != 0 || n.Satisfied != pendTargetRemoved {
		t.Fatal("restored target did not participate in removal notification")
	}
}
