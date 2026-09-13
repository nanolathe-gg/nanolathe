package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// The real secondary pump must expose both the completion and the next round's
// first admitted step in the completion tick [06 §11.1][06 R-WPN-05 §2].
func TestBuildWeaponCompletionStartsNextRoundImmediately(t *testing.T) {
	u, q, econ := armsFixture(t)
	weapon := weaponDefForBuild(true, 12)
	weapon.EnergyPerShot, weapon.MetalPerShot = 7, 11
	u.Slots[0].Weapon = weapon
	q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 2})
	for _, tick := range []uint32{0, 5, 10} {
		q.Pump(u, tick)
	}
	n := q.Secondary()[0]
	if n.Param2 != 1 || n.Param3 != 5 || n.Deadline != 15 || n.DynamicGate != 1 || u.Slots[0].Ammo != 1 {
		t.Fatalf("after completion: node=%+v ammo=%d; want one queued round at progress 5, deadline 15 and ammo 1", n, u.Slots[0].Ammo)
	}
	buckets := econ.UnitBuckets(u.Handle)
	// One complete round costs 7/11; the next first step costs trunc(35/12)
	// and trunc(55/12), so this tick has already admitted another 2/4.
	for res, want := range [2]float32{economy.Energy: 9, economy.Metal: 15} {
		if b := buckets[res]; b.Requested != want || b.Accepted != want {
			t.Fatalf("resource %d: requested=%v accepted=%v, want %v", res, b.Requested, b.Accepted, want)
		}
	}
	q.Pump(u, 14)
	if n.Param3 != 5 || buckets[economy.Energy].Requested != 9 {
		t.Fatal("work advanced before its accepted-incomplete deadline")
	}
	q.Pump(u, 15)
	q.Pump(u, 20)
	if q.LenSecondary() != 0 || u.Slots[0].Ammo != 2 {
		t.Fatalf("second completion: queued=%d ammo=%d, want empty queue and ammo 2 at tick 20", q.LenSecondary(), u.Slots[0].Ammo)
	}
	if buckets[economy.Energy].Requested != 14 || buckets[economy.Metal].Requested != 22 {
		t.Fatalf("two-round requests %v, want exactly two per-round costs", buckets)
	}
}

// Restart enters the full-slot gate before progress reset, and resuming after
// a launch must start a fresh round rather than crediting it free [06 R-WPN-05 §2].
func TestBuildWeaponCompletionAtCapRetainsProgressUntilRestart(t *testing.T) {
	u, q, econ := armsFixture(t)
	weapon := weaponDefForBuild(true, 12)
	weapon.EnergyPerShot = 12
	u.Slots[0].Weapon, u.Slots[0].Ammo = weapon, 199
	q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 2})
	for _, tick := range []uint32{0, 5, 10} {
		q.Pump(u, tick)
	}
	n := q.Secondary()[0]
	if n.Param2 != 1 || n.Param3 != 12 || n.Deadline != 310 || n.DynamicGate != 1 || u.Slots[0].Ammo != 200 {
		t.Fatalf("full-slot restart: node=%+v ammo=%d; want progress 12 and deadline 310", n, u.Slots[0].Ammo)
	}
	u.Slots[0].Ammo-- // model a successful launch opening one place
	q.Pump(u, 309)
	if n.Param3 != 12 {
		t.Fatal("full-slot wait expired early")
	}
	q.Pump(u, 310)
	if n.Param3 != 5 || n.Param2 != 1 || n.Deadline != 315 || u.Slots[0].Ammo != 199 {
		t.Fatalf("reopened slot: node=%+v ammo=%d; want fresh progress 5 without completion", n, u.Slots[0].Ammo)
	}
	if got := econ.UnitBuckets(u.Handle)[economy.Energy].Requested; got != 17 {
		t.Fatalf("requested energy=%v, want complete round 12 plus new first step 5", got)
	}
}

func TestBuildWeaponCompletionPreservesWrappedCapDeadline(t *testing.T) {
	u, q, _ := armsFixture(t)
	u.Slots[0].Weapon = weaponDefForBuild(true, 12)
	u.Slots[0].Ammo = 199
	q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 2, Param3: 10})
	n := q.Secondary()[0]
	// Inspect the handler result directly: zero is a valid absolute deadline,
	// and the pump's unsigned deadline ordering is a separate contract [04 §3.3].
	if code := buildWeaponHandler(u, n, 0, ^uint32(299)); code != 2 || n.Deadline != 0 || n.DynamicGate != 1 || n.Param3 != 12 {
		t.Fatalf("code=%d node=%+v, want a completed round held at the cap with wrapped deadline zero", code, n)
	}
}
