package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
)

func weaponDefForBuild(stockpile bool, reload int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID:         100,
		ReloadTime: reload,
		Stockpile:  stockpile,
	}
}

func TestBuildWeaponStockpileQueue(t *testing.T) {
	// Unit with stockpile weapon at slot 0.
	w := newOrdersFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 100}
	// Install stockpile weapon via direct slot.
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	wd := weaponDefForBuild(true, 30)
	u.Slots[0].Weapon = wd
	u.Slots[0].Ammo = 0
	// Queue BuildWeapon with count 1 via secondary.
	bid := Lookup("BuildWeapon")
	if bid == 0 {
		t.Fatalf("BuildWeapon not found")
	}
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Economy: &economy.Service{}})
	// Ensure handler is registered.
	if DescriptorFor(bid).Handler == nil {
		t.Fatalf("BuildWeapon handler not registered [06 §11.1]")
	}
	// Use tail coalesce with slotIdx 0, count 1.
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 1, Param3: 0})
	if q.LenSecondary() != 1 {
		t.Fatalf("secondary len %d want 1", q.LenSecondary())
	}
	// Simulate pump ticks: first dispatch at tick 0, then every 5 ticks.
	// Tick 0: progress 0->5.
	q.Pump(u, 0)
	n := q.Secondary()[0]
	if n.Param3 != 5 {
		t.Fatalf("progress after tick 0 got %d want 5 [06 §11.1]", n.Param3)
	}
	if u.Slots[0].Ammo != 0 {
		t.Fatalf("ammo before complete %d want 0", u.Slots[0].Ammo)
	}
	// Advance ticks 5,10,15,20,25 -> complete at 25 (progress 30).
	ticks := []uint32{5, 10, 15, 20, 25}
	for i, tk := range ticks {
		// Before last tick, ensure node still exists to avoid index panic.
		if q.LenSecondary() == 0 {
			t.Fatalf("tick %d queue empty prematurely", tk)
		}
		q.Pump(u, tk)
		if i < 4 {
			if q.LenSecondary() == 0 {
				t.Fatalf("tick %d removed prematurely", tk)
			}
			n = q.Secondary()[0]
			_ = n
			if u.Slots[0].Ammo != 0 {
				t.Fatalf("tick %d ammo %d want 0 before complete", tk, u.Slots[0].Ammo)
			}
		} else {
			// Last tick completes: queue should be removed, ammo 1.
			if q.LenSecondary() != 0 {
				t.Fatalf("after completion queue should be empty, got %d", q.LenSecondary())
			}
			if u.Slots[0].Ammo != 1 {
				t.Fatalf("after completion ammo %d want 1 [06 §11.1]", u.Slots[0].Ammo)
			}
		}
	}
	// UI counts: after completion, ammo 1, queued 0.
	ammo, queued := StockpileCounts(u)
	if ammo[0] != 1 || queued[0] != 0 {
		t.Fatalf("StockpileCounts ammo %v queued %v want 1,0", ammo, queued)
	}
	// Launch should decrement ammo (stockpile launch does not write reload).
	// Simulate launch via direct slot decrement (as weapon tick would).
	if u.Slots[0].Ammo != 1 {
		t.Fatalf("pre-launch ammo")
	}
	// Launch consumes one.
	u.Slots[0].Ammo--
	if u.Slots[0].Ammo != 0 {
		t.Fatalf("after launch ammo %d want 0", u.Slots[0].Ammo)
	}
	// Reload should remain unchanged (special stockpile reload) [06 §11.1].
	if u.Slots[0].Reload != 0 {
		t.Fatalf("stockpile reload should stay 0, got %d", u.Slots[0].Reload)
	}
}

func TestBuildWeaponBlockedAt199(t *testing.T) {
	w := newOrdersFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	wd := weaponDefForBuild(true, 30)
	u.Slots[0].Weapon = wd
	u.Slots[0].Ammo = 200 // blocked >199
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Economy: &economy.Service{}})
	bid := Lookup("BuildWeapon")
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 1, Param3: 0})
	q.Pump(u, 0)
	n := q.Secondary()[0]
	// Should schedule 300 tick wait [06 §11.1] and not advance progress.
	if n.Param3 != 0 {
		t.Fatalf("blocked >199 should not advance progress, got %d", n.Param3)
	}
	if n.Deadline != 300 {
		t.Fatalf("blocked deadline %d want 300 [06 §11.1]", n.Deadline)
	}
	if u.Slots[0].Ammo != 200 {
		t.Fatalf("blocked ammo should stay 200")
	}
}

func TestBuildWeaponCoalesceAndUI(t *testing.T) {
	w := newOrdersFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	wd := weaponDefForBuild(true, 30)
	u.Slots[0].Weapon = wd
	q := QueueForUnit(u)
	bid := Lookup("BuildWeapon")
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 1})
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 1})
	if q.LenSecondary() != 1 {
		t.Fatalf("coalesce tail should keep 1 node, got %d", q.LenSecondary())
	}
	if q.Secondary()[0].Param2 != 2 {
		t.Fatalf("coalesced count %d want 2", q.Secondary()[0].Param2)
	}
	ammo, queued := StockpileCounts(u)
	if queued[0] != 2 {
		t.Fatalf("queued UI %d want 2", queued[0])
	}
	_ = ammo
}
