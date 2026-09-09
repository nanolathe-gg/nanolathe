package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// These tests lock the end-to-end weapon chain that a play-test found broken:
// a unit with a live target acquires aim, fires, spends its reload, reloads
// again, and keeps firing until the target dies. Each assertion is a
// relationship between successive states, not a count of anything.

// aimReturnsOne is the smallest Aim* that satisfies the handshake: it pushes a
// nonzero cell and returns it, which is the only thing that grants aim-ready
// [04 §5.3][06 §3.3].
func aimReturnsOne() *cob.Program {
	return progWithAim([]uint32{
		0x10021001, 1, // push 1
		0x10065000, // return
	}, "AimPrimary", 0)
}

// TestPT4_ReloadCountdownRecoversAndTargetDies locks the whole chain end to
// end. Its load-bearing step is the reload countdown: [06 §1.2] and [06 §4.1]
// make the decrement the slot visit's first step, and without it a weapon
// stores its reload once and is locked out of firing forever after its first
// shot. That is exactly what a 5400-tick mission run showed — one volley, then
// no attrition at all for the rest of the game.
func TestPT4_ReloadCountdownRecoversAndTargetDies(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	attachTestCOB(shooter, cob.NewVM(aimReturnsOne()))

	weapon := weaponTurret(101)
	weapon.ReloadTime = 10
	weapon.DamageDefault = 40
	weapon.AreaOfEffect = 8 // direct-target path, no splash [06 §9.3]
	shooter.InstallWeapon(0, weapon)

	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02

	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()

	var svc Service
	bindFixtureControlBytes(&svc) // [06 R-DMG-01 §8] gate 1 needs a player record
	startHealth := target.Health
	if startHealth <= 0 {
		t.Fatalf("fixture target starts with no health")
	}

	var (
		everReady       bool
		everReloading   bool
		reloadRecovered bool
		lastReload      int32
		lastHealth      = startHealth
		healthFell      int
		shots           int
	)
	for tick := uint32(1); tick <= 400 && target.Health > 0; tick++ {
		sum := svc.StepWeaponsForUnit(shooter, tick, w, nil, terrain, nil, cat, nil, nil)
		shots += sum.Fired
		if slot.Aim.Ready {
			everReady = true
		}
		// The relationship the decrement guarantees: a stored reload never
		// stays put. Once nonzero it must come back down [06 §4.1].
		if slot.Reload > 0 {
			everReloading = true
		}
		if lastReload > 0 && slot.Reload < lastReload {
			reloadRecovered = true
		}
		lastReload = slot.Reload
		svc.TickProjectiles(tick, w, terrain, nil, nil, nil, nil, cat, nil, nil)
		if target.Health < lastHealth {
			healthFell++
		}
		lastHealth = target.Health
	}

	if !everReady {
		t.Fatalf("aim-ready was never granted; the Aim* handshake never completed [06 §3.3]")
	}
	if !everReloading {
		t.Fatalf("the weapon never fired, so it never stored a reload [06 §4.2]")
	}
	if !reloadRecovered {
		t.Fatalf("a stored reload was never decremented: the slot visit's first step is missing [06 §1.2][06 §4.1]")
	}
	if shots < 2 {
		t.Fatalf("only %d shot(s) in 400 ticks with reloadtime %d: the weapon fires once and locks", shots, weapon.ReloadTime)
	}
	// Every shot that lands has to move the target's health, and the sequence
	// has to run all the way down to death — one hit and a stall is the defect
	// this test exists to catch.
	if healthFell < 2 {
		t.Fatalf("target health fell only %d time(s) across %d shots", healthFell, shots)
	}
	if target.Health > 0 {
		t.Fatalf("target survived %d shots at %d damage from %d health (now %d)",
			shots, weapon.DamageDefault, startHealth, target.Health)
	}
	if target.Alive && !target.Dying {
		t.Fatalf("target reached %d health without entering the death path", target.Health)
	}
}

// TestPT4_DirectPitchAimsAtLowerTarget locks the sign of the direct pitch
// solver against [06 §3.3], whose vertical operand is -(muzzle.Y - target.Y).
// Inverted, every shot at a target below the muzzle — the ordinary case, since
// a muzzle sits above a ground unit's origin — climbed away from it and never
// impacted.
func TestPT4_DirectPitchAimsAtLowerTarget(t *testing.T) {
	up := PitchFromDelta(numeric.FixedFromInt(40), numeric.FixedFromInt(10), numeric.FixedFromInt(0))
	down := PitchFromDelta(numeric.FixedFromInt(40), numeric.FixedFromInt(-10), numeric.FixedFromInt(0))
	// The angle domain is unsigned; read it back as the signed offset retail
	// compares [06 §3.3].
	if int16(up) <= 0 {
		t.Fatalf("target above the muzzle must aim up, got signed pitch %d", int16(up))
	}
	if int16(down) >= 0 {
		t.Fatalf("target below the muzzle must aim down, got signed pitch %d", int16(down))
	}

	// The same sign has to survive into the projectile's velocity, which is
	// what actually carries a shot to a lower target [06 §6.3][06 §6.7].
	weapon := weaponTurret(102)
	var p Projectile
	InitOrdinary(&p, weapon, 0,
		Vec3{X: numeric.FixedFromInt(0), Y: numeric.FixedFromInt(30), Z: numeric.FixedFromInt(0)},
		Vec3{X: numeric.FixedFromInt(0), Y: numeric.FixedFromInt(10), Z: numeric.FixedFromInt(60)},
		0)
	if p.Velocity.Y.Raw() >= 0 {
		t.Fatalf("a shot at a target 20 units below climbs: vy raw %d", p.Velocity.Y.Raw())
	}
}

// TestPT4_BurstAnchorRefreshesFromShooterMuzzle locks the burst anchor's
// position refresh [06 §4.3]: the anchor is parked at its own shooter's muzzle
// and re-runs the piece-to-world conversion there. Refreshing it to a zero
// vector instead teleported every anchor — and each pellet copied from it — to
// the world origin, so burst weapons could never hit anything.
func TestPT4_BurstAnchorRefreshesFromShooterMuzzle(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	attachTestCOB(shooter, cob.NewVM(aimReturnsOne()))

	weapon := weaponTurret(103)
	weapon.ReloadTime = 30
	weapon.DamageDefault = 5
	weapon.AreaOfEffect = 8
	weapon.Burst = 3
	weapon.BurstRate = 6 // strictly greater than four, so every attempt refreshes
	shooter.InstallWeapon(0, weapon)

	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02

	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()

	var svc Service
	sawAnchor := false
	for tick := uint32(1); tick <= 40; tick++ {
		svc.StepWeaponsForUnit(shooter, tick, w, nil, terrain, nil, cat, nil, nil)
		svc.TickProjectiles(tick, w, terrain, nil, nil, nil, nil, cat, nil, nil)
		for i := 0; i < svc.Count(); i++ {
			p := &svc.Records[i]
			if p.BurstRemaining <= 0 {
				continue
			}
			sawAnchor = true
			// The anchor sits at its shooter, never at the world origin.
			if p.Pos.X.Raw() == 0 && p.Pos.Y.Raw() == 0 && p.Pos.Z.Raw() == 0 {
				t.Fatalf("tick %d: burst anchor refreshed to the world origin", tick)
			}
			want, ok := muzzleWorldPosResolved(shooter, int32(p.MuzzlePiece))
			if !ok {
				t.Fatalf("tick %d: shooter muzzle no longer resolves", tick)
			}
			if p.Pos != want {
				t.Fatalf("tick %d: anchor at %v, shooter muzzle at %v", tick, p.Pos, want)
			}
		}
	}
	if !sawAnchor {
		t.Fatalf("burst %d never produced an anchor to observe", weapon.Burst)
	}
}
