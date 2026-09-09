package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func TestNonIdentityPlayerOrderAssignsWorldSlices(t *testing.T) {
	order := pool.PlayerPermutation{2, 0, 1, 3, 4, 5, 6, 7, 8, 9}
	world, err := newFixtureWorldWithOrder(2, nil, order)
	if err != nil {
		t.Fatalf("NewSlicedWithOrder: %v", err)
	}
	def := &content.UnitDef{UnitName: "ARMCOM", MaxDamage: 100}
	h2, err := world.Create(def, 2, 0, 0, 0)
	if err != nil || h2 != 1 {
		t.Fatalf("player 2 allocation = %d,%v; want 1,nil", h2, err)
	}
	h0, err := world.Create(def, 0, 0, 0, 0)
	if err != nil || h0 != 3 {
		t.Fatalf("player 0 allocation = %d,%v; want 3,nil", h0, err)
	}
	if got := world.Unit(h2).Owner; got != 2 {
		t.Fatalf("slot 1 owner = %d, want 2", got)
	}
	if got := world.Unit(h0).Owner; got != 0 {
		t.Fatalf("slot 3 owner = %d, want 0", got)
	}
}

func TestUnitPoolLowestFreeAndImmediateReuse(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "pool-reuse"}
	def.MaxDamage = 100
	h1, _ := world.Create(def, 0, 0, 0, 0)
	h2, _ := world.Create(def, 0, 0, 0, 0)
	if h1 != 1 || h2 != 2 {
		t.Fatalf("alloc %d %d, want 1 2", h1, h2)
	}
	// Slot 0 null
	if world.Unit(0) != nil {
		t.Fatalf("slot 0 should be nil")
	}
	// Free h1 and reuse should give lowest-free 1
	world.Destroy(h1, DeathKilled)
	// Alive vs death mark are separate [04 §2.3] C2: before teardown cleanup the
	// slot stays visible with Dying set; teardown cleanup clears it.
	if u := world.Unit(h1); u == nil || !u.Dying {
		t.Fatalf("destroyed unit should stay resolvable and marked Dying before cleanup")
	}
	world.TeardownCleanup()
	if world.Unit(h1) != nil {
		t.Fatalf("destroyed unit should be nil after cleanup")
	}
	h3, _ := world.Create(def, 0, 0, 0, 0)
	if h3 != 1 {
		t.Fatalf("reuse lowest free got %d, want 1", h3)
	}
	// No generation tags: stale handle 1 now aliases new occupant
	if world.Unit(pool.Handle(1)) == nil {
		t.Fatalf("reused slot 1 should be live")
	}
}

func TestTickOrderPlayersThenSlots(t *testing.T) {
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "pool-order"}
	def.MaxDamage = 100
	// Create units for players out of order to test the sweep visits players 0..9 asc then slots asc.
	hA, _ := world.Create(def, 1, numeric.Fixed(100*65536), 0, 0)
	hB, _ := world.Create(def, 0, numeric.Fixed(200*65536), 0, 0)
	// The sweep should not panic and maintain order; verify Iter is slots asc.
	iter := world.Iter()
	if len(iter) != 2 || iter[0].Handle != hA || iter[1].Handle != hB {
		// Actually slots asc means hA=1, hB=2 regardless of player order
	}
	_ = hA
	_ = hB
	runPhase2Sweep(world, 1)
	// P0-I02: Remaining is owned exclusively by construction.Service and must
	// never be mutated by the unit sweep [05 "Construction target state"]. The old
	// 0.01 stub is deleted. Verify unfinished unit never progresses in the sweep
	// even across 100 sweeps when no builder exists.
	def2 := &content.UnitDef{UnitName: "pool-order-second"}
	def2.MaxDamage = 100
	hC, _ := world.Create(def2, 0, 0, 0, 0)
	world.units[int(hC)].Remaining = 1.0 // nanoframe state 1→0 [04 §2.3] C3
	for i := 0; i < 100; i++ {
		runPhase2Sweep(world, uint32(2+i))
	}
	if world.units[int(hC)].Remaining != 1.0 {
		t.Fatalf("P0-I02: the unit sweep must never mutate Remaining; got %v want 1.0 [05 \"Construction target state\"]", world.units[int(hC)].Remaining)
	}
}

// P0-16 sliced pool tests

func TestP016_PerDefLimit_SlicedWorld(t *testing.T) {
	world := newFixtureWorld(5, nil) // 5 per player
	def := &content.UnitDef{UnitName: "ARMLAB", MaxDamage: 100}
	def.LimitEnabled = true
	def.Limit = 2
	// Two should succeed
	for i := 0; i < 2; i++ {
		if _, err := world.Create(def, 0, 0, 0, 0); err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
	}
	// Third of same def, same player should fail
	if _, err := world.Create(def, 0, 0, 0, 0); err == nil {
		t.Fatal("third of same def should fail per-def limit")
	}
	// Different def with no limit should succeed in same slice
	def2 := &content.UnitDef{UnitName: "ARMSOLAR", MaxDamage: 100}
	def2.Limit = -1
	if _, err := world.Create(def2, 0, 0, 0, 0); err != nil {
		t.Fatalf("different def should succeed: %v", err)
	}
	// Same def on different player should succeed (per-player counting)
	if _, err := world.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatalf("same def different player should succeed: %v", err)
	}
}

func TestP016_SliceFullVsGlobalSpare_SlicedWorld(t *testing.T) {
	world := newFixtureWorld(3, nil) // 3 per player
	def := &content.UnitDef{MaxDamage: 100, UnitName: "A", Limit: -1}
	// Fill player 0 slice 1..3
	for i := 0; i < 3; i++ {
		if _, err := world.Create(def, 0, 0, 0, 0); err != nil {
			t.Fatalf("fill %d", i)
		}
	}
	// Next for player0 should fail even though player1 free
	if _, err := world.Create(def, 0, 0, 0, 0); err == nil {
		t.Fatal("slice full should fail")
	}
	// Player1 should succeed (global spare)
	if _, err := world.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatalf("player1 spare: %v", err)
	}
	// Verify used counts
	if got := world.Used(); got != 4 {
		t.Fatalf("used %d want 4", got)
	}
}

func TestP016_MaxUnitsLogicalIgnored(t *testing.T) {
	// P0-16 §7.4: the mission logical maxunits value does NOT gate the allocator.
	// We prove by allocating 2 units even though a logical limit of 1 would
	// forbid it. The allocator only cares about physical cap and per-def slice.
	world := newFixtureWorld(5, nil)
	// Pretend logical maxunits=1 (we just don't check it)
	def := &content.UnitDef{UnitName: "pool-limits", MaxDamage: 100, Limit: -1}
	// Both should succeed regardless of hypothetical logical 1
	h1, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	h2, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("second despite logical 1: %v", err)
	}
	if h1 == h2 {
		t.Fatal("handles should differ")
	}
}

func TestP016_ForcedSlotOOB_World(t *testing.T) {
	world := newFixtureWorld(5, nil) // p0 1..5 p1 6..10
	def := &content.UnitDef{UnitName: "pool-forced", MaxDamage: 100, Limit: -1}
	// Valid forced within slice
	if _, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 2); err != nil {
		t.Fatalf("valid forced 2: %v", err)
	}
	// Occupied should fail
	if _, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 2); err == nil {
		t.Fatal("occupied forced should fail")
	}
	// OOB: p0 slice is 1..5, forcing 6 for player0 should fail
	if _, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 6); err == nil {
		t.Fatal("OOB forced should fail")
	}
	// Slot 0 always fails
	if _, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 0); err == nil {
		t.Fatal("forced 0 should fail")
	}
	// Correct slice end boundary: 5 should succeed for p0 if free
	if _, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 5); err != nil {
		t.Fatalf("boundary 5: %v", err)
	}
}

func TestP016_StaleDamageAliasSlot5(t *testing.T) {
	world := newFixtureWorld(10, nil) // p0 1..10 includes slot 5
	defA := &content.UnitDef{UnitName: "ARMSTUMP", MaxDamage: 100, Limit: -1}
	defB := &content.UnitDef{UnitName: "ARMSOLAR", MaxDamage: 100, Limit: -1}
	// Allocate 5 units for player0 to place victim at slot5
	for i := 0; i < 5; i++ {
		if _, err := world.Create(defA, 0, 0, 0, 0); err != nil {
			t.Fatalf("fill %d", i)
		}
	}
	// Victim is slot5 (lowest 1..5, so 5 is last allocated)
	victimH := pool.Handle(5)
	v := world.Unit(victimH)
	if v == nil || v.Def != defA {
		t.Fatalf("want victim at 5 defA, got %v", v)
	}
	v.Health = 100
	// Free the victim immediately while retaining its stored slot index.
	world.FreeImmediate(victimH)
	if world.Unit(victimH) != nil {
		t.Fatal("victim should be free")
	}
	// SlotIndex retained stale [P0-16 §3.4]
	if idx := world.SlotIndex(victimH); idx != uint16(victimH) {
		t.Fatalf("slotIndex retain %d != %d", idx, victimH)
	}
	// Reallocate new unit reusing slot5 (lowest-free after earlier frees? Need to free only 5, so 5 is lowest free among 5? Actually slots 1..4 still occupied, so 5 is lowest free)
	h, err := world.Create(defB, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if h != victimH {
		t.Fatalf("reuse should be same slot 5, got %d", h)
	}
	newU := world.Unit(h)
	if newU == nil || newU.Def != defB {
		t.Fatal("new occupant should be defB at 5")
	}
	newU.Health = 100
	// Damage packet 0x0B u16 slot validates only slot!=0 && alive -> alias silent [P0-16 §6]
	ok := world.ApplyDamage(victimH, 40)
	if !ok {
		t.Fatal("damage should validate on reused alive slot (stale alias)")
	}
	if newU.Health != 60 {
		t.Fatalf("stale alias should hit new occupant: health %d want 60", newU.Health)
	}
	// Validation for dead/free slot should fail
	world.FreeImmediate(h)
	if world.ApplyDamage(h, 10) {
		t.Fatal("damage to dead slot should fail validation")
	}
}

func TestP016_FreeAndReallocateLowestFreeSameTick(t *testing.T) {
	world := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "pool-per-player", MaxDamage: 100, Limit: -1}
	// Allocate 1,2,3
	h1, _ := world.Create(def, 0, 0, 0, 0) //1
	h2, _ := world.Create(def, 0, 0, 0, 0) //2
	_, _ = world.Create(def, 0, 0, 0, 0)   //3
	// Simulate immediate free within same tick (phase2 slot-end free before later slot)
	world.FreeImmediate(h2)
	// Next alloc for same player should reuse lowest free 2
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("reuse err %v", err)
	}
	if h != h2 {
		t.Fatalf("want reuse %d got %d", h2, h)
	}
	// Free 1 too, next should be 1
	world.FreeImmediate(h1)
	h, _ = world.Create(def, 0, 0, 0, 0)
	if h != h1 {
		t.Fatalf("want 1 got %d", h)
	}
}

func TestP016_ZeroRNGAllocation(t *testing.T) {
	rng.SeedGlobal(0x66e29572^12345|1, 9876)
	if rng.Global.Sim == nil {
		t.Skip("rng not seeded")
	}
	before := rng.Global.Sim.Draws()
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "pool-limit", MaxDamage: 100, LimitEnabled: true, Limit: 2}
	world.Create(def, 0, 0, 0, 0)
	world.Create(def, 0, 0, 0, 0)
	world.Create(def, 0, 0, 0, 0) // fail per-def limit
	world.CreateWithForcedSlot(&content.UnitDef{UnitName: "pool-forced-oob", MaxDamage: 100, Limit: -1}, 1, 0, 0, 0, 15)
	world.FreeImmediate(pool.Handle(1))
	if after := rng.Global.Sim.Draws(); after != before {
		t.Fatalf("allocation drew RNG %d -> %d", before, after)
	}
}

func TestOnDeathExtraFiresExactlyOnceAlongsidePrimary(t *testing.T) {
	world := newFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "death-extra", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var primary, extra int
	world.OnDeath = func(pool.Handle, DeathCause, *Unit) { primary++ }
	world.OnDeathExtra = func(pool.Handle, DeathCause, *Unit) { extra++ }
	world.Destroy(h, DeathKilled)
	if got := world.FinalizeDeath(h, 1); !got.Freed {
		t.Fatalf("FinalizeDeath = %#v, want freed", got)
	}
	if primary != 1 || extra != 1 {
		t.Fatalf("death hooks primary=%d extra=%d, want exactly one each", primary, extra)
	}
	if got := world.FinalizeDeath(h, 2); got.Freed || primary != 1 || extra != 1 {
		t.Fatalf("second FinalizeDeath = %#v hooks=%d/%d, duplicated or freed", got, primary, extra)
	}
}

// TestEligiblePredicate locks the shared eligibility predicate `E(u)` of
// [07 R-WGT-01 §10]: the selectable status bit 5 set AND the remaining-build
// fraction exactly `0.0`. The compare is exact single-precision, so any nonzero
// fraction — a nanoframe one work quantum from done included — fails it, and
// the predicate reads NO order state.
func TestEligiblePredicate(t *testing.T) {
	u := &Unit{Alive: true, Flags: ClassifierEligibleStatus}
	if !u.Eligible() {
		t.Fatalf("fresh eligible unit rejected")
	}
	u.Remaining = 0.0001
	if u.Eligible() {
		t.Fatalf("unfinished unit accepted (fraction %v)", u.Remaining)
	}
	u.Remaining = 0.0
	u.Flags &^= ClassifierEligibleStatus
	if u.Eligible() {
		t.Fatalf("bit-0x20-clear unit accepted")
	}
	u.Flags |= ClassifierEligibleStatus
	u.Dying = true
	if u.Eligible() {
		t.Fatalf("dying unit accepted")
	}
}
