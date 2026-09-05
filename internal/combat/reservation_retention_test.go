package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// recycleSlotOne fills projectile slot 1 with the given record, retires it and
// compacts, so the next Reserve hands slot 1 back with that record still in
// place. Immediate reuse with no generation tag is the pool contract (I5)
// [06 §5.1], [06 §5.2].
func recycleSlotOne(t *testing.T, s *Service, occupant Projectile) {
	t.Helper()
	h, ok := s.Reserve()
	if !ok || h != 1 {
		t.Fatalf("first reservation got handle %d ok=%v, want slot 1", h, ok)
	}
	s.Records[0] = occupant
	s.MarkDead(h)
	s.Compact(nil)
	if s.Count() != 0 {
		t.Fatalf("compaction left count %d, want 0", s.Count())
	}
}

// A reservation clears exactly two fields: the record's dead bit and its
// retained unit target [06 §4.1], [06 §5.1]. Everything else keeps the
// previous occupant's value, because the initializer "does not clear the whole
// reused record" [06 §4.1].
func TestReserveClearsOnlyDeadBitAndRetainedUnitTarget(t *testing.T) {
	var s Service
	occupant := Projectile{
		WeaponID:     77,
		TargetPos:    Vec3{X: numeric.FixedFromInt(11), Y: numeric.FixedFromInt(22), Z: numeric.FixedFromInt(33)},
		TargetUnit:   pool.Handle(9),
		Velocity:     Vec3{X: numeric.FixedFromInt(4)},
		Speed:        numeric.FixedFromInt(5),
		Yaw:          numeric.Angle(1234),
		Pitch:        numeric.Angle(4321),
		PropellerYaw: numeric.Angle(999),
		MeteorPitch:  numeric.Angle(888),
		ExpiryTick:   4242,
		Shooter:      pool.Handle(6),
		ShooterSide:  3,
		Dead:         true,
	}
	recycleSlotOne(t, &s, occupant)

	h, ok := s.Reserve()
	if !ok || h != 1 {
		t.Fatalf("reuse reservation got handle %d ok=%v, want slot 1", h, ok)
	}
	p := &s.Records[0]

	// The two cleared fields.
	if p.Dead {
		t.Fatalf("reservation left the dead bit set [06 §4.1]")
	}
	if s.Slots.IsDead(h) {
		t.Fatalf("authoritative dead flag still set after reservation [06 §5.1] I5")
	}
	if p.TargetUnit != 0 {
		t.Fatalf("retained unit target %d, want cleared [06 §4.1]", p.TargetUnit)
	}

	// Everything else keeps the previous occupant's value.
	retained := occupant
	retained.Dead = false
	retained.TargetUnit = 0
	if *p != retained {
		t.Fatalf("reservation changed a field it must not [06 §4.1]:\n got %+v\nwant %+v", *p, retained)
	}
}

// The retention is load-bearing where the aim point is null: the common
// initializer copies the aim point into the stored target point "only when it
// is non-null, leaving the previous occupant's stored target point in place
// otherwise" [06 §4.1], and the ballistic, dropped and meteor creators all pass
// a null aim point [06 §6.1], [06 §6.5]. The ordinary and vertical creators do
// not, so they overwrite it.
func TestNullAimPointCreatorsKeepThePreviousTargetPoint(t *testing.T) {
	previous := Vec3{X: numeric.FixedFromInt(101), Y: numeric.FixedFromInt(202), Z: numeric.FixedFromInt(303)}
	aim := Vec3{X: numeric.FixedFromInt(400), Y: 0, Z: numeric.FixedFromInt(400)}
	muzzle := Vec3{}
	const now = uint32(500)
	vel := int32(numeric.FixedFromInt(4))

	cases := []struct {
		name     string
		weapon   *content.WeaponDef
		meteor   *Vec3
		wantKept bool // true: stored target point must survive the creation
	}{
		// [06 §6.1] names the three null-aim creators.
		{"ballistic", &content.WeaponDef{ID: 1, Ballistic: true, WeaponVelocity: vel, WeaponTimer: 30}, nil, true},
		{"dropped", &content.WeaponDef{ID: 2, Dropped: true, WeaponVelocity: vel}, nil, true},
		{"meteor", &content.WeaponDef{ID: 3, Meteor: true}, &Vec3{Y: -numeric.FixedFromInt(15)}, true},
		// The other two pass the aim point through [06 §6.3], [06 §6.6].
		{"ordinary", &content.WeaponDef{ID: 4, LineOfSight: true, WeaponVelocity: vel, Range: 100}, nil, false},
		{"vertical", &content.WeaponDef{ID: 5, VLaunch: true, WeaponVelocity: vel, Range: 100}, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Service
			recycleSlotOne(t, &s, Projectile{TargetPos: previous})

			h, ok := s.Reserve()
			if !ok || h != 1 {
				t.Fatalf("reuse reservation got handle %d ok=%v, want slot 1", h, ok)
			}
			p := &s.Records[0]
			if p.TargetPos != previous {
				t.Fatalf("reservation erased the previous stored target point %+v", p.TargetPos)
			}

			InitProjectile(p, tc.weapon, now, muzzle, aim, 0, 0, 0, tc.meteor, 0, 0, 0, 0)

			if tc.wantKept {
				if p.TargetPos != previous {
					t.Fatalf("%s creator passes a null aim point, so the stored target point must stay %+v; got %+v [06 §6.1]", tc.name, previous, p.TargetPos)
				}
				return
			}
			if p.TargetPos != aim {
				t.Fatalf("%s creator passes the aim point, so the stored target point must become %+v; got %+v [06 §4.1]", tc.name, aim, p.TargetPos)
			}
		})
	}
}

// The initializer's own clears still reach a reused record: a stale burst
// count would keep the new projectile out of the motion dispatch entirely
// ("only a record whose remaining burst count is zero reaches it", [06 §6.2]),
// and a stale shooter would credit the previous occupant's owner [06 §4.1].
func TestCommonInitializerClearsBurstCountAndShooterOnReuse(t *testing.T) {
	var s Service
	recycleSlotOne(t, &s, Projectile{
		BurstRemaining:   3,
		BurstDeadline:    77,
		Shooter:          pool.Handle(6),
		TargetProjectile: pool.Handle(7),
		BeamLatch:        true,
		TwoPhase:         true,
	})

	h, ok := s.Reserve()
	if !ok || h != 1 {
		t.Fatalf("reuse reservation got handle %d ok=%v, want slot 1", h, ok)
	}
	p := &s.Records[0]

	// The meteor creator takes the null-shooter branch [06 §6.5].
	InitMeteor(p, &content.WeaponDef{ID: 3, Meteor: true}, 900, Vec3{}, Vec3{})

	if p.BurstRemaining != 0 {
		t.Fatalf("burst remaining %d after creation, want cleared [06 §4.1]", p.BurstRemaining)
	}
	if p.BurstDeadline != 900 {
		t.Fatalf("burst deadline %d, want the creation tick 900 [06 §6.1]", p.BurstDeadline)
	}
	if p.Shooter != 0 {
		t.Fatalf("shooter %d, want the null reference the no-shooter branch writes [06 §4.1]", p.Shooter)
	}
	if p.ShooterSide != NeutralSide {
		t.Fatalf("shooter side %d, want the neutral byte %d [06 §6.5]", p.ShooterSide, NeutralSide)
	}
	if p.TargetProjectile != 0 || p.BeamLatch || p.TwoPhase {
		t.Fatalf("initializer left link/latch/phase state from the previous occupant [06 §4.1]")
	}
}

// A stockpile launch into a slot whose previous occupant left a non-zero burst
// count and a live velocity still flies. The reservation keeps that occupant's
// fields [06 §4.1], so the launcher has to run the creation dispatch rather
// than fill a subset of the record by hand: a retained burst count alone would
// keep the missile out of the motion dispatch entirely, which admits only a
// record whose remaining burst count is zero [06 §6.2].
func TestStockpileLaunchIntoADirtySlotStillFlies(t *testing.T) {
	var s Service

	// The previous occupant: mid-burst, moving fast along -X, owned by someone
	// else, aimed somewhere else, and long expired.
	recycleSlotOne(t, &s, Projectile{
		WeaponID:         42,
		BurstRemaining:   3,
		BurstDeadline:    11,
		Velocity:         Vec3{X: -numeric.FixedFromInt(40), Y: -numeric.FixedFromInt(9)},
		Speed:            numeric.FixedFromInt(40),
		Yaw:              numeric.Angle(31000),
		Pitch:            numeric.Angle(60000),
		ExpiryTick:       12,
		Shooter:          pool.Handle(7),
		ShooterSide:      4,
		TargetProjectile: pool.Handle(5),
		BeamLatch:        true,
	})

	// A stock launcher's flag set: `stockpile` gates the fire test, `vlaunch`
	// picks the creator [06 §6.2], `selfprop`/`lineofsight` pick the motion
	// [06 §6.2]. All eight stockpile weapons in the retail corpus author this
	// combination (I14).
	const now = uint32(900)
	w := &content.WeaponDef{
		ID:                 7,
		Stockpile:          true,
		VLaunch:            true,
		SelfProp:           true,
		LineOfSight:        true,
		WeaponVelocity:     int32(numeric.FixedFromInt(6)),
		WeaponAcceleration: int32(numeric.FixedFromInt(1)),
		Range:              1200,
	}
	slot := &Slot{Weapon: w, Ammo: 1, MuzzlePiece: 3}
	target := Target{Kind: TargetPoint, X: numeric.FixedFromInt(500), Z: numeric.FixedFromInt(500)}
	// The silo itself, threaded the way every shot threads it [06 §4.1]: the
	// launch is credited to the launching unit, not to whoever the recycled
	// record used to belong to.
	silo := &units.Unit{Handle: 9, Owner: 1, Def: &content.UnitDef{}}

	h, ok := launchStockpileRound(&s, slot, 0, target, now, FirePorts{ShooterSide: 1, Shooter: silo})
	if !ok || h != 1 {
		t.Fatalf("launch got handle %d ok=%v, want the recycled slot 1", h, ok)
	}
	p := &s.Records[0]

	// The fields the previous occupant would have poisoned.
	if p.BurstRemaining != 0 {
		t.Fatalf("burst remaining %d: the missile never reaches motion dispatch [06 §6.2]", p.BurstRemaining)
	}
	if p.Velocity != (Vec3{}) || p.Speed != 0 {
		t.Fatalf("vertical launch starts at rest [06 §6.6]; got velocity %+v speed %v", p.Velocity, p.Speed)
	}
	if p.Pitch != numeric.Angle(16384) || p.Yaw != 0 {
		t.Fatalf("vertical launch angles are yaw 0 pitch 0x4000 [06 §6.6]; got yaw %d pitch %d", p.Yaw, p.Pitch)
	}
	if p.WeaponID != w.ID {
		t.Fatalf("weapon id %d, want the launched weapon %d [06 §4.1]", p.WeaponID, w.ID)
	}
	if p.Shooter != silo.Handle || p.ShooterSide != 1 {
		t.Fatalf("launch shooter %d side %d, want the launching silo %d side 1 — not the previous occupant's [06 §4.1]",
			p.Shooter, p.ShooterSide, silo.Handle)
	}
	if p.TargetProjectile != 0 || p.BeamLatch {
		t.Fatalf("launch inherited link/latch state from the previous occupant [06 §4.1]")
	}
	if p.ExpiryTick <= now {
		t.Fatalf("expiry %d is not in the future of %d — the retained deadline survived [06 §6.3]", p.ExpiryTick, now)
	}
	if p.TargetPos != (Vec3{X: target.X, Y: target.Y, Z: target.Z}) {
		t.Fatalf("aim point %+v, want the ordered point [06 §6.6]", p.TargetPos)
	}
	if slot.Ammo != 0 {
		t.Fatalf("slot byte %d, want the launch's decrement [06 §11.1]", slot.Ammo)
	}

	// And it flies: the self-propelled tick accelerates from rest and rebuilds
	// velocity from the stored angles [06 §6.7], so a straight-up launch gains
	// height on its first live tick.
	if fam := MotionFamilyForWeapon(w); fam != MotionSelfProp {
		t.Fatalf("motion family %v, want self-propelled [06 §6.2]", fam)
	}
	before := p.Pos
	if res := Advance(p, w, now+1, Vec3{}, 0, 0); res != AdvanceAlive {
		t.Fatalf("first live tick returned %v, want alive", res)
	}
	if p.Pos.Y.Raw() <= before.Y.Raw() {
		t.Fatalf("missile did not climb: Y %v then %v", before.Y, p.Pos.Y)
	}
	if p.Pos.X.Raw() != before.X.Raw() || p.Pos.Z.Raw() != before.Z.Raw() {
		t.Fatalf("missile drifted on the previous occupant's horizontal velocity: %+v then %+v", before, p.Pos)
	}
}

// The same hazard on the live path: the interceptor spawner reserves a record
// that may still hold a previous occupant's burst count and velocity, and the
// session drives it every tick. [06 §6.6] names the vertical-launch creator as
// the one that stores the rescan's matched-projectile link, so the spawner runs
// that creator and then writes the link over the initializer's clear.
func TestInterceptorSpawnIntoADirtySlotStillFlies(t *testing.T) {
	var s Service

	// Two records: slot 1 will hold the threat, slot 2 is the dirty slot the
	// interceptor will be reserved into. Retiring only slot 2 and compacting
	// leaves its bytes standing past the new active count [06 §5.2].
	h1, _ := s.Reserve()
	h2, _ := s.Reserve()
	if h1 != 1 || h2 != 2 {
		t.Fatalf("reservations got %d and %d, want slots 1 and 2", h1, h2)
	}
	s.Records[1] = Projectile{
		BurstRemaining: 2,
		Velocity:       Vec3{X: numeric.FixedFromInt(33)},
		Speed:          numeric.FixedFromInt(33),
		Shooter:        pool.Handle(8),
		ExpiryTick:     3,
	}
	s.MarkDead(h2)
	s.Compact(nil)
	if s.Count() != 1 {
		t.Fatalf("compaction left count %d, want 1", s.Count())
	}

	// The threat: an enemy-side targetable record aimed at the ground the
	// interceptor covers. The scan metric is the candidate's STORED aim point
	// [06 §11.2].
	threatWeapon := &content.WeaponDef{ID: 21, Targetable: true, VLaunch: true}
	s.Records[0] = Projectile{
		WeaponID:    threatWeapon.ID,
		ShooterSide: 1,
		Pos:         Vec3{X: numeric.FixedFromInt(60), Y: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(60)},
		TargetPos:   Vec3{X: numeric.FixedFromInt(50), Z: numeric.FixedFromInt(50)},
	}

	anti := &content.WeaponDef{
		ID:                 22,
		Interceptor:        true,
		Stockpile:          true,
		VLaunch:            true,
		SelfProp:           true,
		LineOfSight:        true,
		Coverage:           500,
		WeaponVelocity:     int32(numeric.FixedFromInt(6)),
		WeaponAcceleration: int32(numeric.FixedFromInt(1)),
		Range:              1200,
	}
	slot := &Slot{Weapon: anti, Ammo: 2}
	interceptorPos := Vec3{X: numeric.FixedFromInt(50), Z: numeric.FixedFromInt(50)}
	const now = uint32(1200)

	// The anti-nuke silo reaches the spawner the way a shooter reaches TryFire
	// [06 §4.1]: the reference beside the side byte, so an interception is
	// credited to the launcher rather than to nobody.
	silo := &units.Unit{Handle: 4, Owner: 0, Def: &content.UnitDef{}}
	newH, cand, ok := AcquireInterceptorTargetForSpawn(&s, interceptorPos, anti.Coverage, anti, slot, interceptorPos, now,
		map[int32]*content.WeaponDef{threatWeapon.ID: threatWeapon, anti.ID: anti},
		FirePorts{ShooterSide: 0, Shooter: silo})
	if !ok {
		t.Fatalf("interceptor spawn failed")
	}
	if newH != 2 || cand != 1 {
		t.Fatalf("spawn got handle %d candidate %d, want the recycled slot 2 and threat 1", newH, cand)
	}
	p := &s.Records[1]

	if p.BurstRemaining != 0 {
		t.Fatalf("burst remaining %d: the interceptor never reaches motion dispatch [06 §6.2]", p.BurstRemaining)
	}
	if p.Velocity != (Vec3{}) || p.Speed != 0 {
		t.Fatalf("vertical launch starts at rest [06 §6.6]; got velocity %+v speed %v", p.Velocity, p.Speed)
	}
	if p.Shooter != silo.Handle {
		t.Fatalf("interceptor shooter %d, want the launching silo %d — not the previous occupant's [06 §4.1]", p.Shooter, silo.Handle)
	}
	if want := now + 600; silo.RevealDeadline != want {
		t.Fatalf("silo reveal deadline %d, want %d — the real-shooter branch stamps it [06 §4.1]", silo.RevealDeadline, want)
	}
	if p.TargetProjectile != cand {
		t.Fatalf("reservation link %d, want the candidate %d [06 §11.2] [06 §6.6]", p.TargetProjectile, cand)
	}
	if p.ExpiryTick <= now {
		t.Fatalf("expiry %d is not in the future of %d — the retained deadline survived [06 §6.3]", p.ExpiryTick, now)
	}
	if slot.Ammo != 1 {
		t.Fatalf("slot byte %d, want the launch's decrement [06 §11.1]", slot.Ammo)
	}

	before := p.Pos
	if res := Advance(p, anti, now+1, Vec3{}, 0, 0); res != AdvanceAlive {
		t.Fatalf("first live tick returned %v, want alive", res)
	}
	if p.Pos.Y.Raw() <= before.Y.Raw() {
		t.Fatalf("interceptor did not climb: Y %v then %v", before.Y, p.Pos.Y)
	}
}
