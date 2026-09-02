package pool

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func TestP016_PlayerPermutationComparatorAndModeGate(t *testing.T) {
	var keys [PlayerCount]uint32
	for i := range keys {
		keys[i] = uint32(100 + i)
	}
	keys[0], keys[1], keys[2] = 20, 5, 10
	got := PlayerPermutationForMode(3, keys)
	want := PlayerPermutation{1, 2, 0, 3, 4, 5, 6, 7, 8, 9}
	if got != want {
		t.Fatalf("mode-3 player order = %v, want %v", got, want)
	}
	// The comparator is gated by mission mode: non-mode-3 setup retains the
	// fixed player-record order, regardless of sort-key contents [R-P0-16-A].
	if got := PlayerPermutationForMode(2, keys); got != IdentityPlayerPermutation() {
		t.Fatalf("mode-2 player order = %v, want identity", got)
	}
	// Equal keys preserve the fixed slot tie order used by the retail
	// insertion-sort input. A non-strict comparison would move player 1 ahead
	// of player 0, so assert the complete identity result [R-P0-16-A].
	keys[0], keys[1] = 7, 7
	if got := PlayerPermutationForMode(3, keys); got != IdentityPlayerPermutation() {
		t.Fatalf("equal-key order = %v, want identity slot order", got)
	}
}

func TestP016_NonIdentitySlicesAndPermutationValidation(t *testing.T) {
	order := PlayerPermutation{2, 0, 1, 3, 4, 5, 6, 7, 8, 9}
	p, err := NewUnitsSlicedWithOrder(2, order)
	if err != nil {
		t.Fatalf("NewUnitsSlicedWithOrder: %v", err)
	}
	if start, end, ok := p.SliceForPlayer(2); !ok || start != 1 || end != 2 {
		t.Fatalf("player 2 slice = %d..%d,%v; want 1..2,true", start, end, ok)
	}
	if start, end, ok := p.SliceForPlayer(0); !ok || start != 3 || end != 4 {
		t.Fatalf("player 0 slice = %d..%d,%v; want 3..4,true", start, end, ok)
	}
	if start, end, ok := p.SliceForPlayer(1); !ok || start != 5 || end != 6 {
		t.Fatalf("player 1 slice = %d..%d,%v; want 5..6,true", start, end, ok)
	}
	if h, ok := p.AllocForPlayerWithDef(0, 1, false, 0); !ok || h != 3 {
		t.Fatalf("player 0 allocation = %d,%v; want 3,true", h, ok)
	}
	if _, ok := p.AllocForcedWithDef(0, 1, 1, false, 0); ok {
		t.Fatal("cross-slice forced allocation succeeded")
	}

	invalid := PlayerPermutation{0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	if err := ValidatePlayerPermutation(invalid); err == nil {
		t.Fatal("duplicate permutation accepted")
	}
	invalid[1] = 10
	if err := ValidatePlayerPermutation(invalid); err == nil {
		t.Fatal("out-of-range permutation accepted")
	}
	beforeStart, beforeEnd, _ := p.SliceForPlayer(0)
	if err := p.InitSlicedWithOrder(2, invalid); err == nil {
		t.Fatal("invalid reinitialization accepted")
	}
	afterStart, afterEnd, _ := p.SliceForPlayer(0)
	if beforeStart != afterStart || beforeEnd != afterEnd || !p.Alive(3) {
		t.Fatalf("invalid initialization mutated pool: before %d..%d after %d..%d alive=%v", beforeStart, beforeEnd, afterStart, afterEnd, p.Alive(3))
	}
}

// TestProjectileAppendDeadCompaction locks the [01 §6.1]/[06 §5.1] projectile
// lifecycle: allocation appends at the active-span tail and never fills holes;
// retirement flags without decrementing the count; a full span of dead records
// refuses further reservations; stable compaction preserves survivor order and
// repairs the follow link.
func TestProjectileAppendDeadCompaction(t *testing.T) {
	var p Projectiles

	// Fill the span: 300 reserves, handles 1..300.
	for i := 0; i < ProjectileCapacity; i++ {
		h, ok := p.Reserve()
		if !ok || h == 0 {
			t.Fatalf("reserve %d failed", i)
		}
		p.SetPayload(h, i) // tag with creation order for stability checks
	}
	if h, ok := p.Reserve(); ok || h != 0 {
		t.Fatalf("reserve past capacity succeeded: handle %d", h)
	}
	if p.Count() != ProjectileCapacity {
		t.Fatalf("count = %d, want %d", p.Count(), ProjectileCapacity)
	}

	// Dead records still consume capacity until compaction.
	p.MarkDead(1)
	p.MarkDead(2)
	if h, ok := p.Reserve(); ok {
		t.Fatalf("reserve over dead-but-compacted slot succeeded: handle %d", h)
	}
	if p.Count() != ProjectileCapacity {
		t.Fatalf("MarkDead changed count to %d", p.Count())
	}
	if !p.IsDead(1) || !p.IsDead(2) {
		t.Fatal("dead flags not set")
	}

	// Follow camera tracks record 3 (payload 2); records 1, 2, 4 and 5 die.
	const followTarget = Handle(3)
	follow := followTarget
	p.MarkDead(4)
	p.MarkDead(5)
	p.MarkDead(1)
	p.MarkDead(2)
	p.Compact(&follow)

	if got := p.Count(); got != ProjectileCapacity-4 {
		t.Fatalf("count after compact = %d, want %d", got, ProjectileCapacity-4)
	}
	// Record 3 survived and slid from slot 3 to slot 1; its payload moved too.
	payload, ok := p.Payload(follow)
	if !ok || payload != 2 || follow != Handle(1) {
		t.Fatalf("follow repair: handle %d payload %d (found %v), want handle 1 payload 2", follow, payload, ok)
	}
	// Survivors preserve relative order: payloads are strictly ascending.
	last := -1
	for h := Handle(1); int(h) <= p.Count(); h++ {
		payload, _ := p.Payload(h)
		if payload <= last {
			t.Fatalf("order violated at handle %d: payload %d after %d", h, payload, last)
		}
		last = payload
	}
	// A removed target clears the follow link: kill the current tail and
	// follow it into compaction.
	gone := Handle(p.Count())
	p.MarkDead(gone)
	p.Compact(&gone)
	if gone != 0 {
		t.Fatalf("follow to removed record = %d, want 0", gone)
	}
	// Compaction with no holes is a no-op that must not move anything.
	before := p.Count()
	stable := Handle(2)
	p.Compact(&stable)
	if p.Count() != before || stable != Handle(2) {
		t.Fatalf("hole-free compact changed state: count %d->%d handle %d", before, p.Count(), stable)
	}
}

// TestP016_CapacityFormula validates the physical cap = maxDefs*10+1
// [P0-16 §3.1] [01 §6.1]. Stock capacity is roughly 2000–5000, not 500.
func TestP016_CapacityFormula(t *testing.T) {
	if got := CapacityForDefs(200); got != 2001 {
		t.Fatalf("CapacityForDefs(200)=%d want 2001", got)
	}
	if got := UsableCapacityForDefs(200); got != 2000 {
		t.Fatalf("Usable 200=%d want 2000", got)
	}
	// Stock maxunits folklore 500 vs sliced: 500 usable would be maxDefs=50 total 501
	if got := CapacityForDefs(50); got != 501 {
		t.Fatalf("50 defs cap %d", got)
	}
	p := NewUnitsSliced(200)
	if p.Capacity() != 2000 {
		t.Fatalf("sliced capacity %d want 2000", p.Capacity())
	}
	if p.TotalRecords() != 2001 {
		t.Fatalf("total %d", p.TotalRecords())
	}
	if p.SlotIndex(0) != 0 || p.SlotIndex(1) != 1 {
		t.Fatalf("slotIndex retain")
	}
	// Slot 0 null sentinel: allocation should never return 0
	if h, ok := p.AllocForPlayerWithDef(0, 1, false, 0); !ok || h == 0 {
		t.Fatalf("alloc for player 0 failed %d %v", h, ok)
	}
	if p.SlotIndex(1) != 1 {
		t.Fatalf("slot 1 index should be 1")
	}
	// Verify slices: player 0 holds 1..200, player1 201..400
	s, e, ok := p.SliceForPlayer(0)
	if !ok || s != 1 || e != 200 {
		t.Fatalf("slice p0 %d..%d ok %v", s, e, ok)
	}
	s, e, _ = p.SliceForPlayer(1)
	if s != 201 || e != 400 {
		t.Fatalf("slice p1 %d..%d", s, e)
	}
	s, e, _ = p.SliceForPlayer(9)
	if s != 1801 || e != 2000 {
		t.Fatalf("slice p9 %d..%d", s, e)
	}
}

// TestP016_PerDefLimitViaPool exercises the per-definition limit gate and
// slice scan [P0-16 §3.2]: limit 2 should allow 2 of the same definition then
// fail, another definition should still succeed, and allocation takes no RNG
// draws.
func TestP016_PerDefLimitViaPool(t *testing.T) {
	p := NewUnitsSliced(5) // 5 per player, usable 50 total
	const defA uint16 = 42
	const defB uint16 = 99
	// Two of defA should succeed
	for i := 0; i < 2; i++ {
		if _, ok := p.AllocForPlayerWithDef(0, defA, true, 2); !ok {
			t.Fatalf("defA alloc %d failed", i)
		}
	}
	// Third of defA should fail per-def limit
	if _, ok := p.AllocForPlayerWithDef(0, defA, true, 2); ok {
		t.Fatal("third defA should fail per-def limit")
	}
	// defB should still succeed (different defId not counted)
	if _, ok := p.AllocForPlayerWithDef(0, defB, true, 2); !ok {
		t.Fatal("defB should succeed despite defA limit")
	}
	// Unlimited sentinel -1 should bypass limit even if limitEnabled true
	if _, ok := p.AllocForPlayerWithDef(1, defA, true, -1); !ok {
		t.Fatal("unlimited -1 should bypass")
	}
	// Disabled bit should bypass even when count >= limit
	// Fill player 2 with defA up to 2, then disabled should allow third
	for i := 0; i < 2; i++ {
		if _, ok := p.AllocForPlayerWithDef(2, defA, true, 2); !ok {
			t.Fatalf("fill p2 %d", i)
		}
	}
	if _, ok := p.AllocForPlayerWithDef(2, defA, false, 2); !ok {
		t.Fatal("disabled limit should allow third")
	}
}

// TestP016_SliceFullVsGlobalSpare verifies slice-local exhaustion: when a
// player's slice is full the allocator fails even though global spare exists
// in other slices [P0-16 §7.3].
func TestP016_SliceFullVsGlobalSpare(t *testing.T) {
	p := NewUnitsSliced(3) // 3 per player, player0 has 1..3
	// Fill player0 completely
	for i := 0; i < 3; i++ {
		if _, ok := p.AllocForPlayerWithDef(0, 1, false, 0); !ok {
			t.Fatalf("fill %d", i)
		}
	}
	if _, ok := p.AllocForPlayerWithDef(0, 1, false, 0); ok {
		t.Fatal("slice full should fail for player 0")
	}
	// Other player still has spare
	if _, ok := p.AllocForPlayerWithDef(1, 1, false, 0); !ok {
		t.Fatal("player1 should still have spare despite p0 full")
	}
	if got := p.Used(); got != 4 {
		t.Fatalf("used %d", got)
	}
}

// TestP016_ForcedSlotOOB covers reconstructor forced-slot verification:
// the candidate must lie in the player's slice and be free [P0-16 §3.3].
func TestP016_ForcedSlotOOB(t *testing.T) {
	p := NewUnitsSliced(5) // player0 1..5, player1 6..10
	// Valid forced slot within slice and free should succeed
	if _, ok := p.AllocForcedWithDef(0, 7, 3, false, 0); !ok {
		t.Fatal("forced 3 in p0 should succeed")
	}
	// Same slot now occupied should fail
	if _, ok := p.AllocForcedWithDef(0, 7, 3, false, 0); ok {
		t.Fatal("occupied forced should fail")
	}
	// OOB: slot belonging to player1 used with player0 should fail
	if _, ok := p.AllocForcedWithDef(0, 7, 6, false, 0); ok {
		t.Fatal("OOB forced 6 for p0 should fail")
	}
	// Slot 0 sentinel never allocated
	if _, ok := p.AllocForcedWithDef(0, 7, 0, false, 0); ok {
		t.Fatal("forced 0 should fail")
	}
	// Far OOB beyond total records
	if _, ok := p.AllocForcedWithDef(0, 7, 9999, false, 0); ok {
		t.Fatal("far OOB should fail")
	}
	// Forced with per-def limit: should also respect limit
	p2 := NewUnitsSliced(5)
	const defA uint16 = 7
	// Fill 2 with limit 2
	for i := 0; i < 2; i++ {
		if _, ok := p2.AllocForPlayerWithDef(0, defA, true, 2); !ok {
			t.Fatalf("fill %d", i)
		}
	}
	// Forced alloc of same def should fail due to limit
	if _, ok := p2.AllocForcedWithDef(0, defA, 3, true, 2); ok {
		t.Fatal("forced should fail due to per-def limit")
	}
}

// TestP016_SlotIndexRetained verifies that the stored slot index survives
// Free [P0-16 §3.4].
// TestSlicedPoolOneAllocationPath locks the retail unit-pool allocation
// contract on the sliced pool [P0-16 §3.1][P0-16 §3.2][01 §6.1]: slot 0 is
// the null sentinel and is never allocated; allocation scans the owning
// player's slice for the lowest free slot and reuses freed slots
// immediately; per-player slices are isolated (a slice-full failure even
// when other players hold spare slots [P0-16 §7.3]); and allocation always
// carries the caller's definition identity — no default identity is handed
// out at allocation [01 §6.1].
func TestSlicedPoolOneAllocationPath(t *testing.T) {
	p := NewUnitsSliced(3) // player0 slots 1..3, player1 4..6

	// Slot 0 is the null sentinel and is never allocated [P0-16 §3.1].
	if h, ok := p.AllocForcedWithDef(0, 7, 0, false, 0); ok || h != 0 {
		t.Fatalf("forced slot 0 allocated: handle %d ok %v", h, ok)
	}
	if p.Alive(0) {
		t.Fatal("slot 0 must be the null sentinel")
	}

	// Lowest-free allocation fills 1,2,3 in order.
	first := Handle(0)
	for want := Handle(1); want <= 3; want++ {
		h, ok := p.AllocForPlayerWithDef(0, 7, false, 0)
		if !ok || h != want {
			t.Fatalf("alloc %d: got %d ok %v, want lowest-free %d", want, h, ok, want)
		}
		if first == 0 {
			first = h
		}
	}
	// Slice full: further allocation for player 0 fails even though player 1
	// has spare slots [P0-16 §7.3].
	if h, ok := p.AllocForPlayerWithDef(0, 7, false, 0); ok {
		t.Fatalf("slice-full allocation succeeded: handle %d", h)
	}
	if h, ok := p.AllocForPlayerWithDef(1, 7, false, 0); !ok || h != 4 {
		t.Fatalf("player 1 spare slot: got %d ok %v, want 4", h, ok)
	}

	// Immediate reuse: freeing 2 makes it the next lowest-free slot again
	// [P0-16 §3.2].
	p.Free(2)
	h, ok := p.AllocForPlayerWithDef(0, 9, false, 0)
	if !ok || h != 2 {
		t.Fatalf("immediate reuse: got %d ok %v, want 2", h, ok)
	}
	// The reused slot carries the new allocation's definition identity, and
	// the stale handle 2 aliases the new occupant with no generation tag
	// [P0-16 §6].
	if id := p.DefID(2); id != 9 {
		t.Fatalf("reused slot identity %d, want 9", id)
	}

	// No default definition identity: a zero identity fails without
	// allocating [01 §6.1] — retail's canonical allocator always carries a
	// real definition identity.
	before := p.Used()
	if h, ok := p.AllocForPlayerWithDef(0, 0, false, 0); ok || h != 0 {
		t.Fatalf("zero definition identity allocated: handle %d ok %v", h, ok)
	}
	if p.Used() != before {
		t.Fatalf("failed allocation changed used count %d -> %d", before, p.Used())
	}

	// Stale handle from before the free/reuse cycle aliases the new occupant.
	if !p.Alive(first) {
		t.Fatal("lowest slot should still be alive")
	}
}

func TestP016_SlotIndexRetained(t *testing.T) {
	p := NewUnitsSliced(5)
	h, _ := p.AllocForPlayerWithDef(0, 1, false, 0)
	if h != 1 {
		t.Fatalf("want 1 got %d", h)
	}
	if idx := p.SlotIndex(h); idx != uint16(h) {
		t.Fatalf("slotIndex %d != %d", idx, h)
	}
	p.Free(h)
	if idx := p.SlotIndex(h); idx != uint16(h) {
		t.Fatalf("after free slotIndex %d != %d stale retain", idx, h)
	}
	// DefID cleared on free
	if id := p.DefID(h); id != 0 {
		t.Fatalf("defID after free %d", id)
	}
	// Alive false after free, but prior index retained for save forcedSlot
	if p.Alive(h) {
		t.Fatal("should be dead after free")
	}
}

// TestP016_FreeAndReallocateLowestFree ensures immediate reuse lowest-free
// per slice [P0-16 §3.2]: freed slot becomes eligible same tick and Alloc
// picks the lowest free.
func TestP016_FreeAndReallocateLowestFree(t *testing.T) {
	p := NewUnitsSliced(5) // p0 1..5
	// Allocate 1,2,3
	h1, _ := p.AllocForPlayerWithDef(0, 1, false, 0) // 1
	h2, _ := p.AllocForPlayerWithDef(0, 1, false, 0) // 2
	_, _ = p.AllocForPlayerWithDef(0, 1, false, 0)   // 3
	p.Free(h2)                                       // free 2
	// Next alloc should reuse lowest free = 2, not 4
	h, _ := p.AllocForPlayerWithDef(0, 1, false, 0)
	if h != h2 {
		t.Fatalf("reuse wanted %d got %d", h2, h)
	}
	// Free 1 as well; next should be 1 (lowest)
	p.Free(h1)
	h, _ = p.AllocForPlayerWithDef(0, 1, false, 0)
	if h != h1 {
		t.Fatalf("reuse 1 wanted %d got %d", h1, h)
	}
	// Stale handle aliases new occupant: after reuse, old handle now aliases new occupant silently
	if !p.Alive(h1) || p.DefID(h1) == 0 {
		t.Fatal("reused handle should be alive")
	}
}

// TestP016_ZeroRNGDraws ensures Alloc paths consume zero RNG draws
// [P0-16 §5] NEGATIVE-BOUNDED.
func TestP016_ZeroRNGDraws(t *testing.T) {
	rng.SeedGlobal(12345, 6789)
	if rng.Global.Sim == nil {
		t.Skip("rng not seeded")
	}
	beforeSim := rng.Global.Sim.Draws()
	beforeCRT := rng.Global.Crt.Draws()
	p := NewUnitsSliced(10)
	for i := 0; i < 5; i++ {
		p.AllocForPlayerWithDef(0, 1, false, 0)
	}
	p.AllocForPlayerWithDef(0, 42, true, 2)
	p.AllocForPlayerWithDef(0, 42, true, 2)
	p.AllocForPlayerWithDef(0, 42, true, 2) // fail
	p.AllocForcedWithDef(0, 42, 9, false, 0)
	p.Free(1)
	if got := rng.Global.Sim.Draws(); got != beforeSim {
		t.Fatalf("sim draws moved %d -> %d", beforeSim, got)
	}
	if got := rng.Global.Crt.Draws(); got != beforeCRT {
		t.Fatalf("crt draws %d -> %d", beforeCRT, got)
	}
}

// TestP016_SameTickReuseVisibility covers immediate reuse matrix: a unit
// freed at slot 2 in player 0's slice is eligible for allocation by a later
// slot in the same tick if slice still ahead [P0-16 §6.3] (S/N per scan pos).
func TestP016_SameTickReuseVisibility(t *testing.T) {
	p := NewUnitsSliced(5) // p0 1..5
	// Simulate tick scan order player 0..9 asc, slots asc.
	// Allocate victim at slot 2 then frees; factory at “later slot” allocates
	// same tick and should reuse same handle if freed before its turn.
	h1, _ := p.AllocForPlayerWithDef(0, 1, false, 0) // 1
	h2, _ := p.AllocForPlayerWithDef(0, 1, false, 0) // 2 victim
	h3, _ := p.AllocForPlayerWithDef(0, 1, false, 0) // 3 factory actor
	// Free victim (slot 2) at slot-end before visiting factory's later slot
	// [P0-16 §6.3].
	p.Free(h2)
	// Next allocation for same player should reuse 2 (lowest free)
	h, _ := p.AllocForPlayerWithDef(0, 1, false, 0)
	if h != h2 {
		t.Fatalf("same-tick reuse expected %d got %d", h2, h)
	}
	_ = h1
	_ = h3
}

// TestUsedMatchesScanThroughAllocAndFree locks the maintained allocated-slot
// count to the scan it replaced. Used() is read once per tick over a pool sized
// by the catalog, so it is maintained rather than scanned; if a future writer
// of alive/defID forgets to keep the count, this is what catches it.
func TestUsedMatchesScanThroughAllocAndFree(t *testing.T) {
	p := NewUnitsSliced(4)
	check := func(step string) {
		t.Helper()
		if got, want := p.Used(), p.countUsed(); got != want {
			t.Fatalf("%s: Used() = %d, scan = %d", step, got, want)
		}
	}
	check("fresh")
	var handles []Handle
	for player := 0; player < 3; player++ {
		for def := uint16(1); def <= 3; def++ {
			h, ok := p.AllocForPlayerWithDef(player, def, false, 0)
			if !ok {
				t.Fatalf("alloc player %d def %d", player, def)
			}
			handles = append(handles, h)
			check("after alloc")
		}
	}
	if p.Used() != 9 {
		t.Fatalf("Used() = %d, want 9", p.Used())
	}
	// A double free must not double-decrement, and a free of the null slot or
	// an out-of-range handle must not move the count at all.
	p.Free(handles[0])
	check("after free")
	p.Free(handles[0])
	check("after double free")
	p.Free(0)
	p.Free(Handle(len(p.alive) + 5))
	check("after invalid frees")
	// A forced-slot reallocation of the freed slot restores the count.
	if _, ok := p.AllocForcedWithDef(0, 2, handles[0], false, 0); !ok {
		t.Fatalf("forced alloc of freed slot %d", handles[0])
	}
	check("after forced alloc")
	for _, h := range handles {
		p.Free(h)
	}
	check("after draining")
	if p.Used() != 0 {
		t.Fatalf("Used() = %d after draining, want 0", p.Used())
	}
}
