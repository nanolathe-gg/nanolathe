package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// TestTrailPuffAdditiveDeadlineAndExpiryPuff locks the projectile phase's
// trail-style puff sites [06 §13.2][R-STRIP-01 §1 strip 9]: a smoke-trail
// weapon puffs past its next-trail deadline with the deadline advanced
// ADDITIVELY by the smoke delay, and a non-burn-blow timer expiry emits
// exactly one trail-style puff before the silent retirement.
func TestTrailPuffAdditiveDeadlineAndExpiryPuff(t *testing.T) {
	svc := &Service{}
	weapon := &content.WeaponDef{ID: 7, WeaponVelocity: 65536, Range: 32767, LineOfSight: true, SmokeTrail: true, SmokeDelay: 3}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("projectile reservation failed")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = 7
	p.ExpiryTick = 9
	p.SmokeDeadline = 5
	wantPos := Vec3{X: numeric.FixedFromInt(50), Y: numeric.FixedFromInt(0), Z: numeric.FixedFromInt(50)}
	p.Pos = wantPos

	var events []Event
	svc.Events = func(ev Event) { events = append(events, ev) }
	sim := rng.NewSimulation(1)
	for tick := uint32(1); tick <= 12; tick++ {
		svc.TickProjectiles(tick, nil, nil, nil, nil, nil, nil, cat, &sim, nil)
	}

	// Deadline 5 with delay 3: strict deadline puffs on ticks 6 and 9 (6+3,
	// additive advance). This direct record expires silently on tick 9.
	want := map[uint32]int{6: 1}
	got := map[uint32]int{}
	for _, ev := range events {
		if ev.Kind != EventTrailSmoke {
			continue
		}
		got[ev.Tick]++
		if ev.Position != wantPos {
			t.Fatalf("trail puff at tick %d diverged from the projectile position", ev.Tick)
		}
	}
	for tick, n := range want {
		if got[tick] != n {
			t.Fatalf("tick %d: %d trail puffs, want %d", tick, got[tick], n)
		}
	}
	total := 0
	for _, n := range got {
		total += n
	}
	if total != len(want) {
		t.Fatalf("%d trail puffs total, want exactly the deadline and expiry set", total)
	}

	// A BURST PARENT NEVER PUFFS. The projectile phase takes the burst branch
	// and continues before it reaches the trail window, so a parked template
	// with an unlaunched remainder emits nothing however overdue its trail
	// deadline is [06 §13.2][06 §4.3][R-STRIP-01 §1 strip 9]. Its burst
	// deadline is held in the future here so the record stays a parent for the
	// whole run: a clone would carry a cleared remainder and puff normally,
	// which is the other side of the same clause.
	parentSvc := &Service{}
	ph, ok := parentSvc.Reserve()
	if !ok {
		t.Fatal("burst-parent reservation failed")
	}
	parent := &parentSvc.Records[int(ph)-1]
	parent.WeaponID = 7
	parent.ExpiryTick = 100
	parent.SmokeDeadline = 0 // already past every tick below
	parent.BurstRemaining = 1
	parent.BurstDeadline = 1000 // no attempt is due, so the remainder stands
	parent.Pos = wantPos

	var parentEvents []Event
	parentSvc.Events = func(ev Event) { parentEvents = append(parentEvents, ev) }
	parentSim := rng.NewSimulation(1)
	for tick := uint32(1); tick <= 12; tick++ {
		parentSvc.TickProjectiles(tick, nil, nil, nil, nil, nil, nil, cat, &parentSim, nil)
	}
	for _, ev := range parentEvents {
		if ev.Kind == EventTrailSmoke {
			t.Fatalf("a burst parent with an unlaunched remainder puffed at tick %d [06 §13.2]", ev.Tick)
		}
	}
	if parent.SmokeDeadline != 0 {
		t.Fatalf("burst-parent trail deadline advanced to %d; the burst branch returns before the trail window [06 §4.3]", parent.SmokeDeadline)
	}
}
