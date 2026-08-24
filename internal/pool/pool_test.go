package pool

import "testing"

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
