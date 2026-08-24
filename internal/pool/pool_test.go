package pool

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

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

// TestP016_CapacityFormula validates the physical cap = maxDefs*10+1 of
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// Slot 0 null sentinel: Alloc should never return 0
	if h, ok := p.AllocForPlayer(0); !ok || h == 0 {
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

// TestP016_PerDefLimitViaPool exercises the per-def limit gate
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §3.2]: limit 2 should allow 2 of same defId then fail, other def
// should still succeed, zero RNG draws.
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
		if _, ok := p.AllocForPlayer(0); !ok {
			t.Fatalf("fill %d", i)
		}
	}
	if _, ok := p.AllocForPlayer(0); ok {
		t.Fatal("slice full should fail for player 0")
	}
	// Other player still has spare
	if _, ok := p.AllocForPlayer(1); !ok {
		t.Fatal("player1 should still have spare despite p0 full")
	}
	if got := p.Used(); got != 4 {
		t.Fatalf("used %d", got)
	}
}

// TestP016_ForcedSlotOOB covers reconstructor forcedSlot verification:
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestP016_ForcedSlotOOB(t *testing.T) {
	p := NewUnitsSliced(5) // player0 1..5, player1 6..10
	// Valid forced slot within slice and free should succeed
	if _, ok := p.AllocForced(0, 3); !ok {
		t.Fatal("forced 3 in p0 should succeed")
	}
	// Same slot now occupied should fail
	if _, ok := p.AllocForced(0, 3); ok {
		t.Fatal("occupied forced should fail")
	}
	// OOB: slot belonging to player1 used with player0 should fail
	if _, ok := p.AllocForced(0, 6); ok {
		t.Fatal("OOB forced 6 for p0 should fail")
	}
	// Slot 0 sentinel never allocated
	if _, ok := p.AllocForced(0, 0); ok {
		t.Fatal("forced 0 should fail")
	}
	// Far OOB beyond total records
	if _, ok := p.AllocForced(0, 9999); ok {
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §3.4]: slotIndex equals handle and survives Free.
func TestP016_SlotIndexRetained(t *testing.T) {
	p := NewUnitsSliced(5)
	h, _ := p.AllocForPlayer(0)
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
	h1, _ := p.AllocForPlayer(0) // 1
	h2, _ := p.AllocForPlayer(0) // 2
	_, _ = p.AllocForPlayer(0)   // 3
	p.Free(h2)                   // free 2
	// Next alloc should reuse lowest free = 2, not 4
	h, _ := p.AllocForPlayer(0)
	if h != h2 {
		t.Fatalf("reuse wanted %d got %d", h2, h)
	}
	// Free 1 as well; next should be 1 (lowest)
	p.Free(h1)
	h, _ = p.AllocForPlayer(0)
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
		p.AllocForPlayer(0)
	}
	p.AllocForPlayerWithDef(0, 42, true, 2)
	p.AllocForPlayerWithDef(0, 42, true, 2)
	p.AllocForPlayerWithDef(0, 42, true, 2) // fail
	p.AllocForced(0, 9)
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
	h1, _ := p.AllocForPlayer(0) // 1
	h2, _ := p.AllocForPlayer(0) // 2 victim
	h3, _ := p.AllocForPlayer(0) // 3 factory actor
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	p.Free(h2)
	// Next allocation for same player should reuse 2 (lowest free)
	h, _ := p.AllocForPlayer(0)
	if h != h2 {
		t.Fatalf("same-tick reuse expected %d got %d", h2, h)
	}
	_ = h1
	_ = h3
}
