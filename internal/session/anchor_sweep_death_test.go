package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestBurstAnchorSweepRunsAtTheDeathFinalizer locks the wiring of [06 §4.3]'s
// unit-death burst-anchor sweep into the composed session's death boundary.
// [06 §12.1] runs it as the last of the central death handler's fixed teardown
// helpers — after the statistics hook, the order/queue release, the audio
// release and the occupancy unstamp, and before the carrier detach and the
// cargo cascade.
//
// The observable is the projectile pool after the victim's finalizer: its
// scheduler is gone, so the pellets it had not yet emitted never launch, while
// a pellet already in flight and another unit's scheduler are untouched.
func TestBurstAnchorSweepRunsAtTheDeathFinalizer(t *testing.T) {
	s := newLoopTestSession(t, 3)
	all := s.Units.IterSliced()
	if len(all) < 3 {
		t.Fatalf("fixture produced %d units, need three", len(all))
	}
	victim, bystander, killer := all[0], all[1], all[2]
	for _, u := range []*units.Unit{victim, bystander, killer} {
		u.Health, u.MaxHealth = 100, 100
	}

	// Three pool records: the victim's live scheduler, one of its pellets
	// already in flight, and the bystander's scheduler.
	seed := func(shooter pool.Handle, remaining int32, tag int32) {
		h, ok := s.Combat.Reserve()
		if !ok {
			t.Fatalf("pool reserve for tag %d failed", tag)
		}
		p := &s.Combat.Records[int(h)-1]
		p.Shooter = shooter
		p.BurstRemaining = remaining
		p.WeaponID = tag
	}
	seed(victim.Handle, 3, 1)
	seed(victim.Handle, 0, 2)
	seed(bystander.Handle, 3, 3)

	s.Units.DestroyBy(victim.Handle, units.DeathKilled, killer.Handle)
	s.stepUnitPhase(1)

	if s.Units.Unit(victim.Handle) != nil {
		t.Fatal("victim was not finalized at its phase-2 slot")
	}
	if got := s.Combat.Count(); got != 2 {
		t.Fatalf("pool count %d after the sweep, want 2 — one anchor killed and compacted away [06 §4.3]", got)
	}
	// Survivors keep their relative order: the in-flight pellet, then the
	// bystander's scheduler [06 §5.2].
	if got := [2]int32{s.Combat.Records[0].WeaponID, s.Combat.Records[1].WeaponID}; got != [2]int32{2, 3} {
		t.Fatalf("survivors %v, want [2 3] — the sweep is not a general removal of the victim's projectiles [06 §5.2]", got)
	}
	if s.Combat.Records[1].BurstRemaining != 3 {
		t.Fatal("another unit's scheduler must be left alone")
	}
}
