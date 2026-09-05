package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestBurstCloneFirstMovesOnTheTickAfterItSpawns locks the projectile phase's
// span capture [06 §4.3] [06 §5.1] [01 §6.2] C11.
//
// The phase captures its active span ONCE, before anything in it runs, and
// that one count bounds both the burst advance and the motion pass. A clone
// the burst advance appends therefore lands beyond the span: it is created on
// tick n and stands still on tick n, and the first tick on which it integrates
// its velocity is n+1. [06 §4.3] states this outright for the sharpest case —
// "an interval of zero can therefore emit a clone during the root's creation
// tick, but the captured phase span still prevents that clone from moving
// until the next tick".
//
// Capturing the count after the burst advance instead put every clone inside
// the span and moved it on its own spawn tick, advancing each pellet's whole
// flight — and so its impact — one tick early. This test fails on that
// ordering: the clone would be one velocity step downrange at the end of
// tick 4.
func TestBurstCloneFirstMovesOnTheTickAfterItSpawns(t *testing.T) {
	svc := &Service{}
	// A line-of-sight weapon takes the direct motion family, whose integrator
	// is a plain per-tick velocity add [06 §6.3] [06 §7.2] — so the clone's X
	// word is a direct readout of how many times it has been stepped.
	weapon := &content.WeaponDef{ID: 11, WeaponVelocity: 65536, Range: 32767, LineOfSight: true, BurstRate: 3}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"burster": weapon}}
	cat.RebuildWeaponIndex()

	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("projectile reservation failed")
	}
	const step = 2
	anchorPos := Vec3{X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(50), Z: numeric.FixedFromInt(100)}
	anchor := &svc.Records[int(h)-1]
	anchor.WeaponID = 11
	anchor.BurstRemaining = 1 // one pellet, then the anchor retires silently [06 §4.3] C8
	anchor.BurstDeadline = 4  // the attempt falls due on tick 4
	anchor.ExpiryTick = 1000
	anchor.Pos = anchorPos
	anchor.StartPos = anchorPos
	anchor.Velocity = Vec3{X: numeric.FixedFromInt(step)}

	sim := rng.NewSimulation(1)

	// Tick 3: not yet due. Nothing is cloned, and the anchor takes the burst
	// branch instead of the motion branch, so it does not move either.
	svc.TickProjectiles(3, nil, nil, nil, nil, nil, nil, cat, &sim, nil)
	if svc.Count() != 1 {
		t.Fatalf("count %d before the burst attempt is due, want the anchor alone", svc.Count())
	}

	// Tick 4: the attempt is due and the clone is appended. The anchor's count
	// reaches zero, so it dies silently and compaction leaves the clone as the
	// only live record.
	svc.TickProjectiles(4, nil, nil, nil, nil, nil, nil, cat, &sim, nil)
	if svc.Count() != 1 {
		t.Fatalf("count %d after the burst attempt, want the clone alone (the anchor retires as pellet 1 launches) [06 §4.3]", svc.Count())
	}
	clone := &svc.Records[0]
	if clone.BurstRemaining != 0 || clone.CreationTick != 4 {
		t.Fatalf("record 0 is not the clone: remaining %d, creation tick %d", clone.BurstRemaining, clone.CreationTick)
	}
	if clone.Pos != anchorPos {
		t.Fatalf("the clone moved on its spawn tick: position %v, want the anchor's muzzle point %v — the phase span is captured before the burst advance, so a clone appended by it waits for the next tick [06 §4.3] [06 §5.1] C11",
			clone.Pos, anchorPos)
	}

	// Tick 5: the clone is inside the span now and takes exactly one step.
	svc.TickProjectiles(5, nil, nil, nil, nil, nil, nil, cat, &sim, nil)
	clone = &svc.Records[0]
	if want := anchorPos.X.Add(numeric.FixedFromInt(step)); clone.Pos.X != want {
		t.Fatalf("the clone's X word is %v after tick 5, want %v — exactly one velocity step, taken on the tick AFTER the spawn [06 §4.3] [06 §7.2]",
			clone.Pos.X, want)
	}
	if clone.Pos.Y != anchorPos.Y || clone.Pos.Z != anchorPos.Z {
		t.Fatalf("the clone drifted off the zero-velocity axes: %v", clone.Pos)
	}
}
