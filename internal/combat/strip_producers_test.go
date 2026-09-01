package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestExplosionEventCarriesStartSmokeFlag locks the event payload the strip-9
// impact-effect-variant producers need [R-STRIP-01 §1 strip 9]: the
// land/water explosion events carry the weapon's start-smoke flag, which
// gates the session-side smoke append.
func TestExplosionEventCarriesStartSmokeFlag(t *testing.T) {
	weapon := &content.WeaponDef{ExplosionGaf: "boom", ExplosionArt: "boomart", StartSmoke: true}
	var events []Event
	svc := &Service{}
	svc.Events = func(ev Event) { events = append(events, ev) }
	p := &Projectile{Pos: Vec3{X: numeric.FixedFromInt(1), Y: numeric.FixedFromInt(2), Z: numeric.FixedFromInt(3)}}
	handleProjectileImpact(svc, 1, p, weapon, nil, nil, nil, nil, nil, 5, Vec3{}, nil, false)

	found := false
	for _, ev := range events {
		if ev.Kind == EventExplosion {
			found = true
			if !ev.Smoke {
				t.Fatal("explosion event dropped the weapon's start-smoke flag")
			}
			if ev.Position != p.Pos {
				t.Fatal("explosion event position diverged from the impact point")
			}
		}
	}
	if !found {
		t.Fatal("no explosion event emitted")
	}

	// Without the flag the payload stays clear — the session must not append.
	events = nil
	weapon.StartSmoke = false
	handleProjectileImpact(svc, 1, p, weapon, nil, nil, nil, nil, nil, 5, Vec3{}, nil, false)
	for _, ev := range events {
		if ev.Kind == EventExplosion && ev.Smoke {
			t.Fatal("smoke flag set on a weapon without startsmoke")
		}
	}
}

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

	// Deadline 5 with delay 3: trail puffs on ticks 5 and 8 (5+3, the
	// additive advance). The record expires on tick 9: one expiry puff
	// there, then silence — no puff on a dead record afterwards.
	want := map[uint32]int{5: 1, 8: 1, 9: 1}
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
}
