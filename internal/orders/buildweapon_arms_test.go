package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// armsFixture builds one unit with a bound queue and an economy ledger, so the
// stockpile handler's admission has somewhere to record its request.
func armsFixture(t *testing.T) (*units.Unit, *Queue, *economy.Service) {
	t.Helper()
	w := newOrdersFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 100}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	econ := &economy.Service{}
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Economy: econ})
	return u, q, econ
}

// TestUnarmedSlotCompletesEveryRoundFreeInOneVisit locks the unarmed-slot arm of
// [06 R-WPN-05 §2]. A slot with no weapon holds weapon record 0 — reload 0, both
// per-shot costs 0 — so phase 1 forms `next = min(0 + 5, 0) = 0`, both cost
// deltas are 0, `0 < 0` is false and the round completes; the pump then
// re-dispatches the head in the same visit, so the WHOLE queued count completes
// at once, at no cost, into that slot's byte.
func TestUnarmedSlotCompletesEveryRoundFreeInOneVisit(t *testing.T) {
	u, q, econ := armsFixture(t)
	u.Slots[0].Weapon = nil // weapon record 0, the `[noweapon]` sentinel
	u.Slots[0].Ammo = 0

	bid := Lookup("BuildWeapon")
	if bid == 0 {
		t.Fatal("BuildWeapon not found")
	}
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 3, Param3: 0})
	q.Pump(u, 0)

	if got := u.Slots[0].Ammo; got != 3 {
		t.Fatalf("slot byte %d after one visit, want all 3 rounds [06 R-WPN-05 §2]", got)
	}
	if q.LenSecondary() != 0 {
		t.Fatalf("%d nodes left; the count reached 0 so the record completes [06 R-WPN-05 §2]", q.LenSecondary())
	}
	buckets := econ.UnitBuckets(u.Handle)
	if buckets == nil {
		t.Fatal("no economy buckets for the fixture unit")
	}
	for i, b := range buckets {
		if b.Requested != 0 {
			t.Fatalf("resource %d requested %v, want 0 — record 0's rounds are free [06 R-WPN-05 §2]", i, b.Requested)
		}
	}
}

// TestUnarmedSlotStopsAtTheByteGate is the other end of the same visit: the free
// chain stops when the byte passes 199, and the record then holds rather than
// spinning [06 R-WPN-05 §2].
func TestUnarmedSlotStopsAtTheByteGate(t *testing.T) {
	u, q, _ := armsFixture(t)
	u.Slots[0].Weapon = nil
	u.Slots[0].Ammo = 198

	bid := Lookup("BuildWeapon")
	q.CoalesceTail(bid, Node{Param1: 0, Param2: 10, Param3: 0})
	q.Pump(u, 0)

	if got := u.Slots[0].Ammo; got != 200 {
		t.Fatalf("slot byte %d, want 200 — the ordinary path reaches 200 and starts nothing beyond it [06 R-WPN-05 §2]", got)
	}
	if q.LenSecondary() != 1 {
		t.Fatalf("%d nodes left, want the blocked record still linked", q.LenSecondary())
	}
	n := q.Secondary()[0]
	if n.Param2 != 8 {
		t.Fatalf("remaining count %d, want 8 (two rounds completed)", n.Param2)
	}
	if n.Deadline == 0 || n.DynamicGate == 0 {
		t.Fatalf("blocked hold left gate %d deadline %d; every hold arms a deadline [06 R-WPN-05 §2]", n.DynamicGate, n.Deadline)
	}
}

// TestHandlerUsesTheNodesSlotVerbatim locks the correction of [06 R-WPN-05 §2]:
// the node's slot index selects the slot with no search and no stockpile test.
// The handler used to hunt for the first `stockpile` slot when the named one did
// not qualify, which credited a slot the producer never named.
func TestHandlerUsesTheNodesSlotVerbatim(t *testing.T) {
	u, q, _ := armsFixture(t)
	u.Slots[0].Weapon = nil                         // the node names this one
	u.Slots[1].Weapon = weaponDefForBuild(true, 30) // the old fallback's target
	u.Slots[1].Ammo, u.Slots[0].Ammo = 0, 0
	q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 2}) // Param1 = 0

	q.Pump(u, 0)

	if got := u.Slots[0].Ammo; got != 2 {
		t.Fatalf("named slot 0 byte %d, want 2 [06 R-WPN-05 §2]", got)
	}
	if got := u.Slots[1].Ammo; got != 0 {
		t.Fatalf("slot 1 byte %d, want 0 — no search redirects the node [06 R-WPN-05 §2]", got)
	}
}

// TestStockpileEnqueueRefusesAnUnarmedOrOrdinarySlot locks the enqueue guard
// [06 R-WPN-05 §2] asks an implementation for: a node is refused when the named
// slot holds weapon record 0 or a weapon without `stockpile`. It is the one
// behavior both safe and indistinguishable from retail on shipped content,
// where a `MAKENUKE`/`MAKEANTI` button only ever names a stockpile slot.
func TestStockpileEnqueueRefusesAnUnarmedOrOrdinarySlot(t *testing.T) {
	u, _, _ := armsFixture(t)
	u.Slots[0].Weapon = nil                          // record 0
	u.Slots[1].Weapon = weaponDefForBuild(false, 30) // armed, no `stockpile`
	u.Slots[2].Weapon = weaponDefForBuild(true, 30)  // the real thing

	for _, tc := range []struct {
		name string
		slot int
		want bool
	}{
		{"weapon record 0", 0, false},
		{"weapon without stockpile", 1, false},
		{"stockpile weapon", 2, true},
		{"index past the three slots", 3, false},
		{"negative index", -1, false},
	} {
		if got := StockpileSlotAcceptsBuildWeapon(u, tc.slot); got != tc.want {
			t.Fatalf("%s: accepts = %v, want %v [06 R-WPN-05 §2]", tc.name, got, tc.want)
		}
	}
	if StockpileSlotAcceptsBuildWeapon(nil, 0) {
		t.Fatal("a nil unit accepts a node [06 R-WPN-05 §2]")
	}
}
