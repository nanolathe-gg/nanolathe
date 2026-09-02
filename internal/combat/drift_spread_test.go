package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestDriftGateZeroToleranceConstants locks the two movement-tier constants and
// the predicate that selects between them [06 R-WPN-03 §2]. Both are read from
// the image: 150 angle units while the shooter is stationary, 2,000 while it is
// moving. Nothing else — not the target's motion, not the weapon — selects.
func TestDriftGateZeroToleranceConstants(t *testing.T) {
	w := &content.WeaponDef{Tolerance: 0, PitchTolerance: 0}
	if y, p := DriftGates(w, true); y != 150 || p != 150 {
		t.Fatalf("stationary zero-tolerance gates = (%d, %d), want (150, 150) [06 R-WPN-03 §2]", y, p)
	}
	if y, p := DriftGates(w, false); y != 2000 || p != 2000 {
		t.Fatalf("moving zero-tolerance gates = (%d, %d), want (2000, 2000) [06 R-WPN-03 §2]", y, p)
	}
	// An authored pitchtolerance is inert while tolerance is zero: the zero
	// test is on tolerance alone [06 R-WPN-03 §2].
	w.PitchTolerance = 7
	if y, p := DriftGates(w, true); y != 150 || p != 150 {
		t.Fatalf("pitchtolerance must not be consulted while tolerance is zero, got (%d, %d)", y, p)
	}
}

// TestDriftGatePitchFallbackOrder locks the only place the two keys interact
// [06 R-WPN-03 §2]: with tolerance nonzero the yaw gate is tolerance and the
// pitch gate is pitchtolerance when that is nonzero, otherwise tolerance.
func TestDriftGatePitchFallbackOrder(t *testing.T) {
	if y, p := DriftGates(&content.WeaponDef{Tolerance: 500, PitchTolerance: 0}, true); y != 500 || p != 500 {
		t.Fatalf("zero pitchtolerance must fall back to tolerance, got (%d, %d)", y, p)
	}
	if y, p := DriftGates(&content.WeaponDef{Tolerance: 500, PitchTolerance: 900}, true); y != 500 || p != 900 {
		t.Fatalf("nonzero pitchtolerance owns the pitch gate, got (%d, %d)", y, p)
	}
	// The movement tier is not consulted at all once tolerance is nonzero.
	if y, p := DriftGates(&content.WeaponDef{Tolerance: 500, PitchTolerance: 900}, false); y != 500 || p != 900 {
		t.Fatalf("an authored tolerance must not take the movement-tier fallback, got (%d, %d)", y, p)
	}
	// Both keys are read zero-extended from a 16-bit store, so a negatively
	// authored value reads back as its two's complement [06 R-WPN-03 §1].
	if y, p := DriftGates(&content.WeaponDef{Tolerance: -1}, true); y != 65535 || p != 65535 {
		t.Fatalf("a negative tolerance reads back as 65535, got (%d, %d)", y, p)
	}
}

// TestDriftGateInclusiveAndShortCircuits locks the comparison strictness and
// the error width [06 R-WPN-03 §2]: the error is the absolute value of a signed
// 16-bit difference, both comparisons are inclusive, and yaw is tested first.
func TestDriftGateInclusiveAndShortCircuits(t *testing.T) {
	w := &content.WeaponDef{Tolerance: 100}
	if !DriftGatePass(w, true, 100, 0, 0, 0) {
		t.Fatal("an error of exactly the gate must pass [06 R-WPN-03 §2]")
	}
	if DriftGatePass(w, true, 101, 0, 0, 0) {
		t.Fatal("an error one past the gate must fail [06 R-WPN-03 §2]")
	}
	// The difference wraps at 16 bits and is then sign-extended, so an angle
	// just below a full circle is a small negative error, not a huge one.
	if !DriftGatePass(w, true, 65436, 0, 0, 0) { // -100
		t.Fatal("the error is |int16(stored-wanted)|, so -100 must pass a gate of 100")
	}
	if got := AngleError(0x8000, 0); got != 32768 {
		t.Fatalf("largest possible error = %d, want 32768 (the wrap of 0x8000)", got)
	}
	// The pitch half is independent and equally inclusive.
	if !DriftGatePass(w, true, 0, 0, 100, 0) {
		t.Fatal("a pitch error of exactly the gate must pass")
	}
	if DriftGatePass(w, true, 0, 0, 101, 0) {
		t.Fatal("a pitch error one past the gate must fail")
	}
}

// TestAccuracySpreadBound locks the spread's computed width and the divisor's
// strict `div > 1` condition [06 R-WPN-03 §4].
func TestAccuracySpreadBound(t *testing.T) {
	// bound = accuracy + 2048*(1 - health/maxHealth), truncated.
	if got := AccuracySpreadBound(0, 100, 100, 0); got != 0 {
		t.Fatalf("full health, accuracy 0: bound = %d, want 0 (fires on its solved angles)", got)
	}
	if got := AccuracySpreadBound(0, 50, 100, 0); got != 1024 {
		t.Fatalf("half health, accuracy 0: bound = %d, want 1024", got)
	}
	if got := AccuracySpreadBound(300, 50, 100, 0); got != 1324 {
		t.Fatalf("half health, accuracy 300: bound = %d, want 1324", got)
	}
	// The kill divisor is `kills / 12` and is applied only when it exceeds
	// one, i.e. from 24 credited kills upward. Eleven, twelve and twenty-three
	// kills must all leave the bound alone.
	for _, kills := range []int32{0, 11, 12, 23} {
		if got := AccuracySpreadBound(0, 50, 100, kills); got != 1024 {
			t.Fatalf("kills %d: bound = %d, want 1024 (div <= 1 is not applied) [06 R-WPN-03 §4]", kills, got)
		}
	}
	if got := AccuracySpreadBound(0, 50, 100, 24); got != 512 {
		t.Fatalf("24 kills halve the bound, got %d want 512", got)
	}
	if got := AccuracySpreadBound(0, 50, 100, 36); got != 341 {
		t.Fatalf("36 kills divide the bound by three (truncating), got %d want 341", got)
	}
}

// turretSpreadWeapon is a turret weapon that always solves and always admits,
// so the only thing gating its shot is what a case sets up.
func turretSpreadWeapon(id int32, accuracy int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID:             id,
		Range:          1000 * 65536,
		Turret:         true,
		LineOfSight:    true,
		WeaponVelocity: 100 * 65536 / 30,
		Accuracy:       accuracy,
		Tolerance:      wideDriftTolerance,
	}
}

// armedTurretSlot walks a turret slot through its Aim handshake so the next
// StepWeaponsForUnit call reaches the fire-time gate.
func armedTurretSlot(t *testing.T, u *units.Unit, target *units.Unit, weapon *content.WeaponDef) *units.Slot {
	t.Helper()
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	slot.Reload = 0
	slot.Aim = cob.AimSlot{IssueBit: true, Ready: true}
	slot.Flags |= 0x01
	return slot
}

// TestTurretSpreadDrawCount locks the draw count of a turret shot with and
// without a spread [06 §4.4] [06 R-WPN-03 §4] (I4). The draws are behavior:
// two when the computed bound is at least two, none when it is zero, and none
// when it is exactly one — the shared generator returns zero for a bound below
// two without advancing the stream.
func TestTurretSpreadDrawCount(t *testing.T) {
	cases := []struct {
		name      string
		accuracy  int32
		health    int32
		wantDraws uint64
	}{
		{"full health, accuracy zero, bound 0", 0, 100, 0},
		{"full health, accuracy one, bound 1", 1, 100, 0},
		{"full health, accuracy two, bound 2", 2, 100, 2},
		{"half health, accuracy zero, bound 1024", 0, 50, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			shooter.Health = tc.health
			shooter.MaxHealth = 100
			weapon := turretSpreadWeapon(1, tc.accuracy)
			armedTurretSlot(t, shooter, target, weapon)
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
			cat.RebuildWeaponIndex()
			r := rng.NewSimulation(11)
			var svc Service
			before := r.Draws()
			sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil)
			if sum.Fired != 1 {
				t.Fatalf("fired %d, want 1", sum.Fired)
			}
			if got := r.Draws() - before; got != tc.wantDraws {
				t.Fatalf("draws = %d, want %d [06 R-WPN-03 §4] I4", got, tc.wantDraws)
			}
		})
	}
}

// TestNonTurretShotDrawsNothing locks the correction in [06 §4.4]: the spread
// belongs to the executor the `turret` flag selects, so a weapon without it has
// perfectly accurate fire regardless of `accuracy` and consumes no randomness.
func TestNonTurretShotDrawsNothing(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	shooter.Health = 50
	shooter.MaxHealth = 100
	weapon := &content.WeaponDef{
		ID: 2, Range: 1000 * 65536, LineOfSight: true, Accuracy: 4096,
		Tolerance: wideDriftTolerance,
	}
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	r := rng.NewSimulation(11)
	var svc Service
	before := r.Draws()
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil)
	if sum.Fired != 1 {
		t.Fatalf("fired %d, want 1", sum.Fired)
	}
	if got := r.Draws() - before; got != 0 {
		t.Fatalf("a non-turret shot consumed %d draws, want 0 [06 §4.4]", got)
	}
}

// TestTurretDriftGateRefusesAndLeavesTheSlotAiming locks what a refused shot
// leaves behind [06 R-WPN-03 §2]: no projectile, the Aim-issued latch cleared
// so the next visit re-solves and re-dispatches Aim, the reload timer and the
// aim-ready word untouched, and no RNG draw.
func TestTurretDriftGateRefusesAndLeavesTheSlotAiming(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	shooter.Health = 50
	shooter.MaxHealth = 100
	weapon := turretSpreadWeapon(3, 0)
	weapon.Tolerance = 100 // a gate the drift below cannot pass
	slot := armedTurretSlot(t, shooter, target, weapon)
	slot.Reload = 4
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	var svc Service
	r := rng.NewSimulation(11)

	// The stored angles were written when Aim was dispatched. Move the target
	// far around the shooter so the pair re-solved from current geometry is
	// well outside the gate.
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil)
	target.X = numeric.FixedFromInt(-40)
	target.Z = numeric.FixedFromInt(-40)
	slot.Reload = 0
	before := r.Draws()

	sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &r, nil)
	if sum.Fired != 0 || svc.Count() != 0 {
		t.Fatalf("a refused shot must create no projectile: fired=%d count=%d", sum.Fired, svc.Count())
	}
	if r.Draws() != before {
		t.Fatalf("a refused shot must not touch the RNG, %d draws taken [06 R-WPN-03 §2]", r.Draws()-before)
	}
	if slot.Reload != 0 {
		t.Fatalf("a refused shot must not touch the reload timer, got %d", slot.Reload)
	}
	if !slot.Aim.Ready {
		t.Fatal("a refused shot must leave the aim-ready word alone [06 R-WPN-03 §2]")
	}
	if slot.Aim.IssueBit {
		t.Fatal("a refused shot clears the Aim-issued latch so the next visit re-dispatches [06 R-WPN-03 §2]")
	}
}

// TestFixedForwardGateUsesTheUnitHeading locks the line-of-sight/self-propelled
// caller of the same gate [06 R-WPN-03 §2]: it measures how far off the unit's
// own facing the target sits, so a fixed-forward weapon authoring no tolerance
// fires only when the target is within 150 angle units of straight ahead while
// the shooter is stationary.
func TestFixedForwardGateUsesTheUnitHeading(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{ID: 4, Range: 1000 * 65536, LineOfSight: true}
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()

	// Facing away from the target: refused, and no latch of its own to clear.
	muzzle, ok := muzzleWorldPosResolved(shooter, slot.MuzzlePiece)
	if !ok {
		t.Fatal("fixture muzzle did not resolve")
	}
	// The heading and the bearing must be compared in ONE convention, and the
	// heading's is retail's: a yaw `a` faces `(-sin a, -cos a)` [04 R-MOV-01 §4]
	// [06 R-WPN-05 §4]. This build's solved yaw is half a turn from that, so the
	// facing that points AT the target is the shifted bearing, not the raw one.
	bearing := retailYawFromGo(uint16(YawFromDelta(target.X.Sub(muzzle.X), target.Z.Sub(muzzle.Z))))
	shooter.Move.Heading = bearing + 8192 // a quarter turn off
	shooter.Move.Pitch = 0
	var svc Service
	r := rng.NewSimulation(5)
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 0 {
		t.Fatalf("a fixed-forward weapon must not fire a quarter turn off its target, fired %d", sum.Fired)
	}

	// Turned onto the target to exactly the gate's edge: admitted, inclusive.
	shooter.Move.Heading = bearing - uint16(DriftGateStationary)
	slot.Reload = 0
	if sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 1 {
		t.Fatalf("an error of exactly 150 must pass while stationary, fired %d", sum.Fired)
	}
}

// TestFixedForwardGateWidensWhileMoving locks the stationary/moving selection
// at the call site, not only in DriftGates: the same off-axis target that a
// stationary shooter refuses is admitted once the shooter's movement tier
// leaves category 0 [06 R-WPN-03 §2] [04 §5.2].
//
// The selector is the CACHED tier the movement integrator writes, not the speed
// word — so this drives MoveTier directly [04 §5.2 "the mover inhibit bit is the
// blocked flag"]. TestAimGateReadsCachedTier below locks that the speed word on
// its own moves nothing here.
func TestFixedForwardGateWidensWhileMoving(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{ID: 5, Range: 1000 * 65536, LineOfSight: true}
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	muzzle, ok := muzzleWorldPosResolved(shooter, slot.MuzzlePiece)
	if !ok {
		t.Fatal("fixture muzzle did not resolve")
	}
	// The heading and the bearing must be compared in ONE convention, and the
	// heading's is retail's: a yaw `a` faces `(-sin a, -cos a)` [04 R-MOV-01 §4]
	// [06 R-WPN-05 §4]. This build's solved yaw is half a turn from that, so the
	// facing that points AT the target is the shifted bearing, not the raw one.
	bearing := retailYawFromGo(uint16(YawFromDelta(target.X.Sub(muzzle.X), target.Z.Sub(muzzle.Z))))
	// Between the two gates: outside 150, inside 2000.
	shooter.Move.Heading = bearing + 1000
	shooter.Move.Pitch = 0

	var svc Service
	r := rng.NewSimulation(5)
	shooter.Move.Speed = 0
	shooter.MoveTier = 0
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 0 {
		t.Fatalf("a stationary shooter must refuse an error of 1000, fired %d", sum.Fired)
	}
	shooter.Move.Speed = numeric.FixedFromInt(1)
	shooter.MoveTier = 1
	slot.Reload = 0
	if sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 1 {
		t.Fatalf("a moving shooter must admit an error of 1000, fired %d", sum.Fired)
	}
	// A unit attached to a carrier classifies as category 0 and aims under the
	// tight gate even while its carrier moves it [06 R-WPN-03 §2]; the carrier
	// term reaches the gate through the classifier's cache, so the integrator's
	// next run on a carried unit writes tier 0 (movement's
	// TestMoveTierCacheTerms covers the classification itself).
	shooter.MoveTier = 0
	slot.Reload = 0
	if sum := svc.StepWeaponsForUnit(shooter, 3, w, nil, terrain, nil, cat, &r, nil); sum.Fired != 0 {
		t.Fatalf("a carried shooter aims under the tight gate, fired %d", sum.Fired)
	}
}

// TestAimGateReadsCachedTier locks that the drift gate's stationary/moving
// selector is the tier the movement integrator cached and nothing else
// [06 R-WPN-03 §2][04 §5.2 "the mover inhibit bit is the blocked flag"]. A unit
// rejected on its last cross-cell proposal keeps a tier-0 cache while its speed
// word stays nonzero, and it must aim under the TIGHT gate for as long as that
// cache stands — the stale-by-construction blocked flag of [04 R-COLL-01 §5]
// reaching the gate.
func TestAimGateReadsCachedTier(t *testing.T) {
	_, _, shooter, _ := newTestWorldAndUnits(t)
	shooter.Move.Speed = numeric.FixedFromInt(3)
	shooter.MoveTier = 0
	if !unitStationary(shooter) {
		t.Fatal("a nonzero speed word must not override a cached tier of 0 [04 §5.2]")
	}
	// And the converse: a zero speed word does not make a cached nonzero tier
	// stationary. The cache is the only input.
	shooter.Move.Speed = 0
	shooter.MoveTier = 2
	if unitStationary(shooter) {
		t.Fatal("a zero speed word must not override a cached tier of 2 [04 §5.2]")
	}
	// The carrier field is likewise not re-read here: it is one of the
	// classifier's terms, folded into the cache upstream.
	shooter.Attachment.Carrier = 7
	if unitStationary(shooter) {
		t.Fatal("the gate must read the cache, not re-test the carrier field [06 R-WPN-03 §2]")
	}
	if !unitStationary(nil) {
		t.Fatal("a nil shooter is stationary")
	}
}
