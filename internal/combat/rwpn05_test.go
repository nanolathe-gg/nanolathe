package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestAimYawIsRelativeAndZeroDeadAhead locks [06 R-WPN-05 §4]: the value handed
// to an Aim* script is `(bearing - heading) mod 65536` in retail's convention,
// where a yaw `a` denotes the direction `(-sin a, -cos a)`. So a target dead
// ahead reads exactly zero at every heading — not only at 0x8000, which is all
// the un-shifted absolute yaw this build used to hand over could manage.
func TestAimYawIsRelativeAndZeroDeadAhead(t *testing.T) {
	const reach = 100 << 16 // 100 world units, comfortably beyond rounding

	for _, heading := range []uint16{0, 0x4000, 0x8000, 0xC000} {
		// "Dead ahead" is the heading's own direction [04 R-MOV-01 §4]:
		// (-sin h, -cos h), the same convention retail gives every yaw.
		sin := int64(numeric.Sin(numeric.Angle(heading)))
		cos := int64(numeric.Cos(numeric.Angle(heading)))
		dx := numeric.Fixed(-(sin * reach) >> 13)
		dz := numeric.Fixed(-(cos * reach) >> 13)

		goYaw := uint16(YawFromDelta(dx, dz))
		if got := aimYawForScript(goYaw, heading); got != 0 {
			t.Fatalf("heading %#04x: Aim yaw %#04x, want 0 (solved %#04x)", heading, got, goYaw)
		}
		// The same shift is what makes the fixed-forward drift gate
		// comparable with the heading at all.
		if e := AngleError(retailYawFromGo(goYaw), heading); e != 0 {
			t.Fatalf("heading %#04x: fixed-forward drift error %d, want 0", heading, e)
		}
	}
}

// TestRetailYawShiftIsItsOwnInverse pins the one arithmetic fact the two
// helpers rest on: atan2(-x, -z) = atan2(x, z) + 0x8000 [06 R-WPN-05 §4].
func TestRetailYawShiftIsItsOwnInverse(t *testing.T) {
	for _, d := range []struct{ x, z numeric.Fixed }{
		{1 << 16, 0}, {0, 1 << 16}, {-3 << 16, 7 << 16}, {5 << 16, -2 << 16},
	} {
		fwd := uint16(YawFromDelta(d.x, d.z))
		rev := uint16(YawFromDelta(numeric.Fixed(-d.x.Raw()), numeric.Fixed(-d.z.Raw())))
		if rev != retailYawFromGo(fwd) {
			t.Fatalf("delta (%d,%d): reversed yaw %#04x, want %#04x", d.x.Raw(), d.z.Raw(), rev, retailYawFromGo(fwd))
		}
		if retailYawFromGo(retailYawFromGo(fwd)) != fwd {
			t.Fatalf("shift is not an involution at %#04x", fwd)
		}
	}
}

// spreadFamilyWeapon is a turret weapon with a bound big enough that both draws
// are certain to land off zero, in one of the two creation families.
func spreadFamilyWeapon(id int32, ballistic bool) *content.WeaponDef {
	return &content.WeaponDef{
		ID:             id,
		Range:          1000 * 65536,
		Turret:         true,
		LineOfSight:    !ballistic,
		Ballistic:      ballistic,
		Accuracy:       8000,
		WeaponVelocity: 400 * 65536 / 30,
	}
}

// TestSpreadSteersBallisticOnly locks [06 R-WPN-05 §5]: the retained accuracy
// spread reaches a ballistic trajectory and nothing else. An ordinary
// (`lineofsight`/`selfprop`) shot is re-solved from the pipeline's own aim
// point, so it leaves exactly toward the target however wide the bound; only
// the slot's stored yaw — RockUnit's recoil direction — carries the draw.
func TestSpreadSteersBallisticOnly(t *testing.T) {
	target := Target{Kind: TargetPoint, X: numeric.FixedFromInt(400), Y: 0, Z: numeric.FixedFromInt(40)}

	fireOnce := func(t *testing.T, ballistic bool, health int32) (yaw uint16, storedYaw uint16, vel Vec3) {
		t.Helper()
		var svc Service
		w := spreadFamilyWeapon(9, ballistic)
		slot := &Slot{Weapon: w, Target: target}
		r := rng.NewSimulation(11)
		ports := FirePorts{
			RNG:              &r,
			MuzzlePiece:      func(int) int32 { return -1 },
			Gravity:          numeric.Fixed(8155),
			ShooterHealth:    health,
			ShooterMaxHealth: 100,
		}
		h, ok := TryFire(&svc, slot, 0, target, 0, ports)
		if !ok || h == 0 {
			t.Fatalf("ballistic=%v health=%d: fire refused", ballistic, health)
		}
		p := &svc.Records[int(h)-1]
		return uint16(p.Yaw), slot.DesiredYaw, p.Velocity
	}

	// Full health with accuracy 8000 still leaves a bound of 8000, so both
	// draws are taken; at half health the bound is wider still. Comparing the
	// two shots is the observation: the ordinary one must not move.
	ordFull, ordStoredFull, ordVelFull := fireOnce(t, false, 100)
	ordHurt, ordStoredHurt, ordVelHurt := fireOnce(t, false, 50)
	if ordFull != ordHurt || ordVelFull != ordVelHurt {
		t.Fatalf("an ordinary shot must leave toward the aim point whatever the spread: yaw %#04x/%#04x velocity %v/%v",
			ordFull, ordHurt, ordVelFull, ordVelHurt)
	}
	if ordStoredFull == ordStoredHurt {
		t.Fatal("the slot's stored yaw must still carry the draw — RockUnit's recoil direction reads it [06 R-WPN-05 §5]")
	}

	balFull, _, balVelFull := fireOnce(t, true, 100)
	balHurt, _, balVelHurt := fireOnce(t, true, 50)
	if balFull == balHurt && balVelFull == balVelHurt {
		t.Fatalf("a ballistic shot copies the slot's spread angles and must scatter: yaw %#04x both times", balFull)
	}
}

// TestCouldNotFireBitSetOnGateFailure locks the producer half of
// [06 R-WPN-05 §6]: a shot-time physical gate that fails with the reload
// already spent raises bit 12 of the shooter's order-event word, and nothing in
// the weapon layer reads it back — a slot whose gate later passes still fires
// with the bit standing.
func TestCouldNotFireBitSetOnGateFailure(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// Range 1 world unit: the target sits well outside it, so the shot-time
	// range test refuses while every earlier step admits.
	weapon := &content.WeaponDef{ID: 21, Range: 1, LineOfSight: true, WeaponVelocity: 100 * 65536 / 30}
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()

	var svc Service
	r := rng.NewSimulation(3)
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 0 {
		t.Fatalf("an out-of-range slot must not fire, fired %d", sum.Fired)
	}
	if shooter.Pending&units.PendingCouldNotFire == 0 {
		t.Fatal("a failed shot-time gate must raise the could-not-fire bit [06 R-WPN-05 §6]")
	}

	// The bit is latched — a second refusal leaves it standing — and it gates
	// nothing: widen the range and the same slot fires with the bit still set.
	weapon.Range = 1000 * 65536
	slot.Reload = 0
	shooter.Move.Heading = retailYawFromGo(uint16(YawFromDelta(target.X.Sub(shooter.X), target.Z.Sub(shooter.Z))))
	if sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 1 {
		t.Fatalf("the could-not-fire bit must gate no firing decision, fired %d", sum.Fired)
	}
	if shooter.Pending&units.PendingCouldNotFire == 0 {
		t.Fatal("the bit is latched until an order consumes it or a new target is bound [06 R-WPN-05 §6]")
	}
}

// TestTurretStoresRelativeYawAndCarriesTheHullTurn locks [06 R-WPN-05 §4]'s
// turret pair. Two facts, at two headings:
//
//   - what the dispatch stores is the RELATIVE yaw — the same value handed to
//     Aim*, zero for a target dead ahead — not the absolute bearing;
//   - the fire-time gate re-solves the relative yaw from the CURRENT heading,
//     so with the target still, a unit that turned by d since the dispatch
//     reads a yaw error of exactly -d. Both halves absolute would cancel the
//     heading and lose that term entirely.
func TestTurretStoresRelativeYawAndCarriesTheHullTurn(t *testing.T) {
	// A tolerance well inside a turn of 4,000 units, so the gate is the
	// observation rather than an accident of the default.
	const tolerance = 1000
	const hullTurn = 4000

	for _, heading := range []uint16{0x2000, 0xB000} {
		w, terrain, shooter, target := newTestWorldAndUnits(t)
		shooter.Move.Heading = heading
		weapon := &content.WeaponDef{
			ID: 31, Range: 1000 * 65536, Turret: true, LineOfSight: true,
			WeaponVelocity: 100 * 65536 / 30, Tolerance: tolerance,
		}
		shooter.InstallWeapon(0, weapon)
		slot := shooter.SlotAt(0)
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
		cat.RebuildWeaponIndex()

		var svc Service
		r := rng.NewSimulation(5)
		// Visit 1 dispatches Aim and stores the pair. With no COB bridge the
		// handshake completes in one visit; nothing fires yet.
		svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil)

		muzzle, ok := muzzleWorldPosResolved(shooter, slot.MuzzlePiece)
		if !ok {
			t.Fatal("fixture muzzle did not resolve")
		}
		wantRel := aimYawForScript(uint16(YawFromDelta(target.X.Sub(muzzle.X), target.Z.Sub(muzzle.Z))), heading)
		if slot.DesiredYaw != wantRel {
			t.Fatalf("heading %#04x: stored yaw %#04x, want the relative %#04x [06 R-WPN-05 §4]",
				heading, slot.DesiredYaw, wantRel)
		}

		// Turn the hull without moving the target. The re-solved relative yaw
		// moves by exactly -hullTurn, which is outside the tolerance, so the
		// gate refuses and clears the Aim latch for a re-dispatch.
		shooter.Move.Heading = heading + hullTurn
		slot.Reload = 0
		slot.Aim.Ready = true
		slot.Aim.IssueBit = true
		if sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 0 {
			t.Fatalf("heading %#04x: a hull turn of %d past a tolerance of %d must refuse the shot [06 R-WPN-05 §4]",
				heading, hullTurn, tolerance)
		}
		if slot.Aim.IssueBit {
			t.Fatalf("heading %#04x: a drift-gate failure clears the Aim latch [06 R-WPN-03 §2]", heading)
		}

		// The same turn inside the tolerance passes, and the executor converts
		// the stored relative yaw back to absolute before the creator.
		shooter.Move.Heading = heading
		slot.Reload = 0
		slot.DesiredYaw = wantRel
		slot.Aim.Ready = true
		slot.Aim.IssueBit = true
		if sum := svc.StepWeaponsForUnit(shooter, 3, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 1 {
			t.Fatalf("heading %#04x: an unturned hull must fire, fired %d", heading, sum.Fired)
		}
		if want := retailYawFromGo(wantRel + heading); slot.DesiredYaw != want {
			t.Fatalf("heading %#04x: after the gate the stored yaw is absolute %#04x, got %#04x [06 R-WPN-05 §4]",
				heading, want, slot.DesiredYaw)
		}
	}
}
