package combat

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// CreationFamily is the creation-time family per [06 §6.2] C15.
type CreationFamily int

const (
	CreationNone CreationFamily = iota
	CreationMeteor
	CreationBallistic
	CreationVertical
	CreationOrdinary
	CreationDropped
)

// MotionFamily is the active motion family per [06 §6.2] C15 motion ordering.
type MotionFamily int

const (
	MotionNone MotionFamily = iota
	MotionSelfProp
	MotionDirect
	MotionBallistic
	MotionDropped
	MotionMeteor
)

// AdvanceResult describes the outcome of a motion tick for expiry handling [06 §7.3] C16.
type AdvanceResult int

const (
	AdvanceAlive AdvanceResult = iota
	AdvanceRetire
	AdvanceImpact
	AdvancePhaseTransition
)

// CreationFamilyForWeapon returns the creation family for a weapon per
// [06 §6.2] meteor → ballistic → vertical launch → LOS/self-propelled → dropped (C15).
func CreationFamilyForWeapon(w *content.WeaponDef) CreationFamily {
	if w == nil {
		return CreationNone
	}
	if w.Meteor { // [06 §6.2] 1. meteor creates directly from packet velocity
		return CreationMeteor
	}
	if w.Ballistic { // [06 §6.2] 3. ballistic selects ballistic creator
		return CreationBallistic
	}
	if w.VLaunch { // [06 §6.2] 4. otherwise vertical-launch
		return CreationVertical
	}
	if w.LineOfSight || w.SelfProp { // [06 §6.2] 5. otherwise LOS or self-propelled selects ordinary creator
		return CreationOrdinary
	}
	if w.Dropped { // [06 §6.2] 6. otherwise dropped selects its small inline creator
		return CreationDropped
	}
	return CreationNone // [06 §6.2] 7. otherwise no projectile is created
}

// MotionFamilyForWeapon returns the active motion family per
// [06 §6.2] self-propelled → LOS/direct → ballistic → dropped → meteor (C15).
func MotionFamilyForWeapon(w *content.WeaponDef) MotionFamily {
	if w == nil {
		return MotionNone
	}
	// [06 §6.2] propeller presentation advances first, then family owns tick
	if w.SelfProp { // [06 §6.2] 1. self-propelled
		return MotionSelfProp
	}
	if w.LineOfSight { // [06 §6.2] 2. otherwise line-of-sight/direct
		return MotionDirect
	}
	if w.Ballistic { // [06 §6.2] 3. otherwise ballistic
		return MotionBallistic
	}
	if w.Dropped { // [06 §6.2] 4. otherwise dropped
		return MotionDropped
	}
	if w.Meteor { // [06 §6.2] 5. otherwise meteor
		return MotionMeteor
	}
	return MotionNone // [06 §6.2] 6. otherwise no ordinary motion
}

// Per-field reservation audit (WU-19-164), the precondition for dropping the
// zero-fill in Service.Reserve. A reserved record keeps the previous
// occupant's value in every field the reservation does not clear and no
// creator writes [06 §4.1], [06 §6.1], so each field below is either written
// on every creation path, or is deliberately retained because retail retains
// it, or carries its own marker.
//
//	WeaponID          written — "store the definition" [06 §4.1]; a null
//	                  definition stores null, so the nil-weapon arm clears it.
//	Pos, StartPos     written — muzzle point into BOTH points [06 §4.1].
//	TargetPos         CONDITIONAL — copied "only when it is non-null, leaving
//	                  the previous occupant's stored target point in place
//	                  otherwise" [06 §4.1]. Ballistic, dropped and meteor pass
//	                  null [06 §6.1], [06 §6.5]; ordinary and vertical do not.
//	TargetUnit        cleared by the reservation [06 §4.1] and again here
//	                  "before family-specific assignment" [06 §5.1].
//	TargetProjectile  written — "clear the projectile link" [06 §4.1]; the
//	                  vertical creator then stores the interceptor's matched
//	                  link [06 §6.6].
//	Velocity          written by every creator: ordinary [06 §6.3], ballistic
//	                  [06 §6.4], vertical (three zero components, [06 §6.6]),
//	                  dropped [06 §6.4], meteor (packet velocity, [06 §6.5]).
//	Speed             written by ordinary [06 §6.3], vertical [06 §6.6] and
//	                  dropped (zero, [06 §6.4]); the meteor creator writes no
//	                  scalar speed [06 §6.5] and the meteor tick reads none.
//	Yaw, Pitch        ordinary [06 §6.3], ballistic [06 §6.4] and vertical
//	                  [06 §6.6] write both. Dropped writes yaw and "no pitch"
//	                  [06 §6.4]; meteor "initializes no angles" [06 §6.5] and
//	                  its tick ADDS to the retained words on purpose.
//	Shooter           written — the initializer writes the shooter reference,
//	                  or a null one on the null-shooter branch [06 §4.1].
//	ShooterSide       written — the shooter's side byte, or neutral 10
//	                  [06 §4.1], [06 §6.5].
//	MuzzlePiece       written — "resolve and store the firing piece" [06 §4.1].
//	CreationTick      written [06 §4.1]; it doubles as the burst deadline
//	                  [06 §6.1], so BurstDeadline is written with it.
//	BurstRemaining    written — "clear the burst-remaining count" [06 §4.1];
//	                  the ordinary and vertical creators then copy the authored
//	                  count [06 §6.3], [06 §6.6]. It is read before any write
//	                  would otherwise reach it — the motion dispatch admits
//	                  only a record whose remaining burst count is zero
//	                  [06 §6.2] — so clearing it here is load-bearing.
//	ExpiryTick        ordinary [06 §6.3], ballistic [06 §6.4] and vertical
//	                  [06 §6.6] write it; dropped "writes no expiry" [06 §6.4]
//	                  and meteor "initializes no expiry" [06 §6.5], and neither
//	                  family's tick performs an expiry test.
//	SmokeDeadline     written — "seed the smoke deadline to the current tick"
//	                  [06 §4.1] (plus the authored delay, [06 §7.3]).
//	BeamLatch         written — "clear the beam latch" [06 §4.1].
//	TwoPhase          written — "clear the two-phase state bits" [06 §4.1].
//	Dead              cleared by the reservation [06 §4.1]; Slots is the
//	                  authority (I5).
//	PropellerYaw      RETAINED on purpose: no creator and no initializer writes
//	                  it, and the meteor tick's roll accumulator "starts at
//	                  whatever the reused pool slot last held" [06 §6.5].
//	                  Presentation only [06 §6.1].
//	MeteorPitch       RETAINED on purpose, same sentence [06 §6.5].
//	CacheCellX/Z      RETAINED on purpose [R-DMG-01 §13]: the collision
//	                  gate's feature step is the pair's only writer, and a
//	                  reused record keeps its last occupant's pair, so a
//	                  fresh record's first contact with that same feature
//	                  cell is suppressed exactly as retail suppresses it.
//	CachedFloorHeight write-only presentation scratch the gate rewrites on
//	                  every in-map tick [R-DMG-01 §14]; retention is what
//	                  the draw pass sees for a not-yet-gated clone.
//	OldMarker         compaction writes it for every record in the span before
//	                  any copy and reads it only within that same pass
//	                  [06 §5.2]; retention is unobservable.
//
// Two record fields [06 §6.1] enumerates have no Go field to retain: the
// stored planar muzzle-to-aim distance "written by the ordinary creator only"
// (fire.go derives the burst clone's expiry without it), and the renderer's
// roll word, which AdvanceMeteor parks in PropellerYaw. Neither is a retention
// hazard while it is absent, and both belong to their own units.
//
// InitCommon performs that common initializer work per [06 §4.1], [06 §5.1],
// [06 §6.1]. aim is the OPTIONAL aim point: a nil pointer is retail's null aim
// argument and leaves the stored target point alone. Family velocity, angles,
// scalar speed and expiry are not set here [06 §4.1].
func InitCommon(p *Projectile, now uint32, muzzle Vec3, aim *Vec3, targetUnit pool.Handle, shooter pool.Handle, shooterSide uint8, muzzlePiece int16, w *content.WeaponDef) {
	if p == nil {
		return
	}
	p.Pos = muzzle      // [06 §4.1] copies muzzle point into current/head point
	p.StartPos = muzzle // [06 §4.1] and second tail/waypoint/start point
	if aim != nil {
		// [06 §4.1] the aim point reaches the stored target point ONLY when it
		// is non-null; otherwise the previous occupant's point stays [06 §6.1].
		p.TargetPos = *aim
	}
	p.TargetUnit = targetUnit
	p.TargetProjectile = 0 // [06 §4.1] clears projectile-to-projectile link
	p.BurstRemaining = 0   // [06 §4.1] clears the burst-remaining count
	p.CreationTick = now   // [06 §4.1] creation tick
	p.BurstDeadline = now  // [06 §6.1] the creation tick doubles as the burst deadline
	p.BeamLatch = false    // [06 §4.1] clears beam latch
	p.TwoPhase = false     // [06 §4.1] clears the two-phase state bits
	p.Dead = false
	if w != nil {
		p.SmokeDeadline = now + uint32(w.SmokeDelay) // [06 §4.1] seeds smoke deadline [02 "Weapon record"] smokedelay*30
		p.WeaponID = w.ID                            // [06 §4.1] store the definition
	} else {
		p.SmokeDeadline = now
		p.WeaponID = 0 // [06 §4.1] a null definition is stored as null, not retained
	}
	p.Shooter = shooter // [06 §4.1] the shooter reference, or a null one
	p.ShooterSide = shooterSide
	p.MuzzlePiece = muzzlePiece
	// The collision cache's quantized cell pair is NOT cleared: neither the
	// reservation nor this initializer nor any specialized creator writes it
	// [06 §4.1], [R-DMG-01 §13]. See the retention table above.
}

// VelocityFromAngles recomputes velocity components from scalar speed, yaw, pitch
// using the retail circular domain trig helpers per [06 §6.4] [04 §5.1] [06 §6.7].
// Each helper has form (tableValue * magnitude + 4096) >>13 with 512-entry
// round(8192*sin) table; products round to nearest [04 §5.1].
//
// There is no separate ballistic pitch quantization [06 §6.4] (closed
// 2026-09-02): the solver stores its pitch at full 16-bit resolution
// (trunc(theta*32768/pi)) and the ballistic creator, the ordinary creator and
// this per-tick rebuild all read the one 512-entry table through the same
// index, ((angle + 32) >> 7) & 511 [06 §3.3][04 §5.1]. The "64-unit step"
// reading was a documentation error corrected in [06 R-WPN-01 §4]; the only
// real drift was numeric.Sin/Cos omitting the +32 pre-add, fixed with it.
//
// Sign convention (settled by [06 R-WPN-05 §11], RWU-19-41). Retail writes
// every creator's horizontal build as `velocityX = -sin(yaw, H)`,
// `velocityZ = -cos(yaw, H)` [06 §6.3][06 §6.4], and the mover's as
// `-sin(heading)·speed`, `-cos(heading)·speed` [04 R-MOV-01 §4]: a retail
// yaw `a` names the direction `(-sin a, -cos a)`, and its bearing helper is
// written over muzzle-minus-target deltas. This build solves every
// projectile yaw over target-minus-muzzle deltas (YawFromDelta) and builds
// velocity here from +sin/+cos. Both flips cancel — the projectile leaves
// toward the aim point exactly as retail's does — so the two numberings
// differ by exactly half a turn: `p.Yaw == retailYaw + 0x8000` for every
// projectile record, while a unit's heading is retail's own number
// (movement negates, as retail does). This is a consistent, documented
// transform, not a defect: the arithmetic below must stay +sin/+cos as long
// as YawFromDelta is target-minus-muzzle. What must carry the shift is every
// value that leaves the projectile arithmetic — the Aim*/RockUnit arguments
// (aimYawForScript), the fixed-forward drift compare, the published yaw the
// renderer folds (RetailYaw), and the damage packet's direction byte, which
// retail derives from a fresh victim-relative bearing, never from the yaw
// (hitDirectionByte).
func VelocityFromAngles(yaw, pitch numeric.Angle, speed numeric.Fixed) Vec3 {
	// [06 §6.7] recomputes all velocity components from scalar speed, yaw, pitch
	cosPitch := numeric.Cos(pitch) // [04 §5.1] table scaled 8192
	sinPitch := numeric.Sin(pitch)
	sinYaw := numeric.Sin(yaw)
	cosYaw := numeric.Cos(yaw)
	// horizontal magnitude = cos(pitch) * speed [06 §6.4] H = fixedCos(pitch, weaponVelocity)
	horiz := numeric.MulRound(cosPitch, int32(speed.Raw())) // [04 §5.1] (table*mag+4096)>>13
	vy := numeric.MulRound(sinPitch, int32(speed.Raw()))
	vx := numeric.MulRound(sinYaw, horiz)
	vz := numeric.MulRound(cosYaw, horiz)
	return Vec3{
		X: numeric.Fixed(int64(vx)),
		Y: numeric.Fixed(int64(vy)),
		Z: numeric.Fixed(int64(vz)),
	}
}

// YawFromDelta derives yaw from XZ delta per [06 §6.3] ordinary creator.
// The operands are the signed raw 16.16 words consumed by retail atan2q, and
// the shared helper performs its round-to-nearest-even narrowing [06 §6.4].
func YawFromDelta(dx, dz numeric.Fixed) numeric.Angle {
	return numeric.AngleFromAtan2(dx.Raw(), dz.Raw())
}

// PitchFromDelta derives pitch from an XYZ delta per [06 §3.3][06 §6.3].
//
// The solver [06 §3.3] gives is written over the muzzle point p and the target
// point t as `atan2q(-(int16)((p.Y - t.Y) >> 16), (int16)(dist >> 16))`, and
// the same expression appears in the ordinary creator and in guidance. Our
// callers all pass deltas in target-minus-muzzle order, so the vertical operand
// is built from the negated raw word — `p.Y - t.Y` — before the shift, and only
// then negated. Sign and truncation both matter: the shift is arithmetic, so
// deriving the operand as `-(dy >> 16)` instead rounds a negative delta the
// wrong way as well as inverting it.
//
// Correction: this helper previously computed `-int16(dy >> 16)` directly from
// the target-minus-muzzle delta, which is retail's negation applied to operands
// already in the opposite order. It inverted every direct-fire pitch — a target
// below the muzzle aimed upward — so projectiles climbed away from ground
// targets and never impacted, and the drift gate compared against a
// wrong-signed pitch.
func PitchFromDelta(dx, dy, dz numeric.Fixed) numeric.Angle {
	// Retail first truncates the vertical and planar distance operands to
	// signed whole units, then invokes the same atan2q conversion [06 §3.3].
	vertical := -int64(int16((-dy.Raw()) >> 16))
	distanceRaw := int64(math.Trunc(math.Hypot(float64(dx.Raw()), float64(dz.Raw()))))
	horizontal := int64(int16(distanceRaw >> 16))
	return numeric.AngleFromAtan2(vertical, horizontal)
}

// OrdinaryExpiry computes expiry for ordinary/vertical/selfProp creation per
// [06 §6.3]: when weaponvelocity is zero or noautorange enabled, expiry = now+weapontimer;
// otherwise expiry = now + ( range<<16 / weaponvelocity ) trunc toward zero [01 §8] I3.
func OrdinaryExpiry(now uint32, w *content.WeaponDef) uint32 {
	if w == nil {
		return now
	}
	if w.WeaponVelocity == 0 || w.NoAutoRange { // [06 §6.3]
		return now + uint32(w.WeaponTimer) // [02 "Weapon record"] weapontimer*30
	}
	// [06 §6.3] integer range shifted by fixed-point fraction / weapon velocity
	// Range is integer world units default 32767 [02 "Weapon record"]; shift left 16 to Fixed.
	// Truncate toward zero per [01 §8] I3.
	r := int64(w.Range)
	v := int64(w.WeaponVelocity) // Fixed raw 16.16 per tick
	ticks := (r * 65536) / v     // trunc toward zero; a zero velocity divides by zero exactly as retail raises after reservation [06 §6.4][GAP T5] I11
	// Wrap modulo 2^32 via uint32 conversion [06 §6.4] burn-blow deadlines wrap
	return now + uint32(ticks)
}

// BallisticBurnBlowExpiry computes burn-blow deadline per [06 §6.4]:
// wideDistance = trunc(hypot(targetX-muzzleX, targetZ-muzzleZ))
// H = fixedCos(pitch, weaponVelocity) = (cosTable*vel+4096)>>13
// T = signedDivide(wideDistance, H) trunc toward zero [01 §8]
// expiry = now + T wrapping modulo 2^32.
// Malformed: H==0 raises divide; H<0 truncates toward zero mod 2^32; INT_MIN/-1 raises [GAP T5] I11.
func BallisticBurnBlowExpiry(now uint32, muzzle, target Vec3, pitch numeric.Angle, weaponVelocity int32) uint32 {
	// [GAP T5] X/Z delta components wrap as signed 32-bit before double conversion.
	dx := int32((target.X.Raw() - muzzle.X.Raw()))
	dz := int32((target.Z.Raw() - muzzle.Z.Raw()))
	// [06 §6.4] wideDistance = trunc(hypot(...)) keeps the LOW 32 BITS on
	// overflow [GAP T5]: truncate the double toward zero, then take low 32.
	wide := int32(uint32(uint64(math.Trunc(math.Hypot(float64(dx), float64(dz))))))
	// [06 §6.4] H = fixedCos(pitch, weaponVelocity)
	cosPitch := numeric.Cos(pitch)
	h := numeric.MulRound(cosPitch, weaponVelocity) // int32
	// [GAP T5] I11: an H of zero divides by zero exactly as retail raises
	// after pool reservation — no guard, Go panics like the fault.
	// INT_MIN / -1 raises a signed-divide overflow on x86 where Go wraps;
	// reproduce the raise explicitly rather than the wrap.
	if wide == -2147483648 && h == -1 {
		panic("combat: INT_MIN / -1 signed divide overflow (retail faults)")
	}
	t := wide / h          // trunc toward zero [01 §8]
	return now + uint32(t) // wraps modulo 2^32 [06 §6.4]
}

// InitOrdinary initializes an ordinary/direct projectile per [06 §6.3].
// It passes a NON-null aim point: the ordinary creator is not one of the three
// null-aim creators [06 §6.1], and the stored target point is the guidance and
// target-loss fallback source for the self-propelled family it also creates
// [06 §6.8].
func InitOrdinary(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle) {
	if p == nil || w == nil {
		return
	}
	InitCommon(p, now, muzzle, &target, targetUnit, p.Shooter, p.ShooterSide, p.MuzzlePiece, w)
	// [06 §6.3] derives yaw and pitch from muzzle to target
	dx := target.X.Sub(muzzle.X)
	dy := target.Y.Sub(muzzle.Y)
	dz := target.Z.Sub(muzzle.Z)
	yaw := YawFromDelta(dx, dz)
	pitch := PitchFromDelta(dx, dy, dz)
	p.Yaw = yaw
	p.Pitch = pitch
	// [06 §6.3] initial scalar speed hierarchy
	var speed numeric.Fixed
	if w.StartVelocity != 0 {
		speed = numeric.Fixed(int64(w.StartVelocity))
	} else if w.WeaponAcceleration != 0 {
		speed = 0
	} else {
		speed = numeric.Fixed(int64(w.WeaponVelocity))
	}
	p.Speed = speed
	p.Velocity = VelocityFromAngles(yaw, pitch, speed) // [06 §6.4] fixed-table helper
	p.ExpiryTick = OrdinaryExpiry(now, w)              // [06 §6.3] [06 §7.3]
	// Burst state not handled here; fire.go owns it [06 §4.3]
}

// BallisticFlightTicks is the ballistic creator's `T0`: the firing slot's
// stored distance word divided by the weapon velocity, an UNSIGNED divide of
// the two raw words yielding whole ticks [06 §6.4].
//
// The distance word is the per-unit constant the slot initializer wrote at
// construction, not a distance to this shot's target — RWU-19-39's correction
// to [06 §6.4]. Because the divide is unsigned, a negative word (the muzzle
// behind the aim-from piece along world Z at initialization) yields a very
// large `T0` rather than a negative one. The arithmetic is Established; it is
// **Unknown** whether stock units ever store a negative word, so this
// reproduces the unsigned divide and does not defend against it (I11).
//
// A zero weapon velocity divides by zero here, which is exactly where retail
// raises the processor divide exception — after the pool record is reserved,
// so the live count is not rolled back [06 §6.4] I11.
func BallisticFlightTicks(slotDistance int32, weaponVelocity int32) uint32 {
	return uint32(slotDistance) / uint32(weaponVelocity)
}

// ballisticLaunchVelocity builds the ballistic creator's launch velocity
// [06 §6.4]:
//
//	velocityY = sin(pitch, weaponvelocity) - T0 * gravity
//	H         = cos(pitch, weaponvelocity)
//	velocityX/Z from the yaw against H
//
// The pre-decrement of the vertical component by one flight-time's worth of
// gravity is part of the launch, not an integrator artefact, and is reproduced
// as written; the product is a 32-bit signed multiply. **Unknown:** the
// pre-decrement's intended geometric meaning, and therefore whether an
// implementation may simplify it [06 §6.4].
func ballisticLaunchVelocity(yaw, pitch numeric.Angle, speed numeric.Fixed, slotDistance int32, gravity numeric.Fixed) Vec3 {
	v := VelocityFromAngles(yaw, pitch, speed)
	t0 := BallisticFlightTicks(slotDistance, int32(speed.Raw()))
	drop := int32(t0) * int32(gravity.Raw()) // 32-bit signed multiply [06 §6.4]
	v.Y = numeric.Fixed(int64(int32(v.Y.Raw()) - drop))
	return v
}

// InitBallistic initializes a ballistic projectile per [06 §6.4].
// solvedPitch must be the pitch from BallisticSolve [06 §3.3] [06 §6.4].
// slotDistance is the firing slot's distance word — the `T0` divisor written
// once at unit construction [06 R-WPN-05 §3] — and gravity is the map's
// per-tick gravity global in 16.16.
func InitBallistic(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, solvedPitch numeric.Angle, yaw numeric.Angle, slotDistance int32, gravity numeric.Fixed) {
	if p == nil || w == nil {
		return
	}
	// NULL aim point [06 §6.1]: the ballistic creator is one of the three that
	// pass one, so the record keeps the previous occupant's stored target
	// point. Nothing on the ballistic motion path reads it — the burn-blow
	// deadline below takes the aim point as an argument [06 §6.4] — so the
	// retained value is carried, not consumed.
	InitCommon(p, now, muzzle, nil, targetUnit, p.Shooter, p.ShooterSide, p.MuzzlePiece, w)
	p.Yaw = yaw
	p.Pitch = solvedPitch
	speed := numeric.Fixed(int64(w.WeaponVelocity))
	p.Speed = speed
	// [06 §6.4]: the shared (table*mag+4096)>>13 build, then the `T0 × gravity`
	// pre-decrement of the vertical component.
	p.Velocity = ballisticLaunchVelocity(yaw, solvedPitch, speed, slotDistance, gravity)
	if w.BurnBlow {
		p.ExpiryTick = BallisticBurnBlowExpiry(now, muzzle, target, solvedPitch, w.WeaponVelocity) // [06 §6.4]
	} else {
		p.ExpiryTick = now + uint32(w.WeaponTimer) // [06 §6.4] non-burn-blow timer based
	}
}

// InitVertical initializes a vertical-launch projectile per [06 §6.6].
func InitVertical(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle) {
	if p == nil || w == nil {
		return
	}
	// Non-null aim point: the vertical creator is not one of the three null-aim
	// creators [06 §6.1], and its two-phase guidance reads the stored target
	// point [06 §6.6], [06 §6.8].
	InitCommon(p, now, muzzle, &target, targetUnit, p.Shooter, p.ShooterSide, p.MuzzlePiece, w)
	p.Yaw = 0                      // [06 §6.6] zero yaw
	p.Pitch = numeric.Angle(16384) // [06 §6.6] quarter-turn upward pitch (90deg)
	p.Velocity = Vec3{}            // [06 §6.6] zero velocity components
	// [06 §6.6] scalar-speed hierarchy same as ordinary
	var speed numeric.Fixed
	if w.StartVelocity != 0 {
		speed = numeric.Fixed(int64(w.StartVelocity))
	} else if w.WeaponAcceleration != 0 {
		speed = 0
	} else {
		speed = numeric.Fixed(int64(w.WeaponVelocity))
	}
	p.Speed = speed
	// [06 §6.6] expiry same as ordinary
	p.ExpiryTick = OrdinaryExpiry(now, w)
}

// InitDropped initializes a dropped projectile per [06 §6.4].
//
// dropperHeading and dropperMaxVelocity are the DROPPING UNIT's own state: its
// heading word and its unit definition's `maxvelocity` in 16.16 per tick — not
// any weapon field and not any aim solution [06 §6.4].
func InitDropped(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, dropperHeading numeric.Angle, dropperMaxVelocity numeric.Fixed) {
	if p == nil || w == nil {
		return
	}
	// NULL aim point [06 §6.1]: the dropped creator is one of the three that
	// pass one, so the record keeps the previous occupant's stored target
	// point. Dropped motion reads no target point [06 §6.4].
	InitCommon(p, now, muzzle, nil, targetUnit, p.Shooter, p.ShooterSide, p.MuzzlePiece, w)
	// [06 §6.4] the dropped executor "writes no expiry, no burst count and no
	// pitch": the expiry word therefore keeps the previous occupant's value,
	// and dropped motion is "the same integrate block with no expiry test", so
	// nothing on the family's own path reads it. The `ExpiryTick = 0` that
	// stood here was a write retail does not make.
	p.Speed = 0 // [06 §6.4] sets scalar speed to zero
	// The launch state is the dropping unit's, not an aim solution [06 §6.4]:
	//
	//	yaw       = unitHeading
	//	velocityX = -sin(unitHeading, unitMaxVelocity)
	//	velocityZ = -cos(unitHeading, unitMaxVelocity)
	//	velocityY = 0
	//
	// so a bomb leaves the bay already carrying the bomber's own forward run.
	//
	// The stored yaw is that heading advanced by half a turn, because a
	// projectile yaw in this build carries retail's `+ 0x8000` offset: our
	// velocity build is `+sin`/`+cos` where retail's is `-sin`/`-cos`, while
	// unit headings are kept at retail's own numbering [06 R-WPN-05 §11]. The
	// shift is what makes the two expressions the same vector, so the pair
	// below IS retail's negated build, and the record's yaw stays in the one
	// convention every other creator here writes.
	yaw := dropperHeading.Add(0x8000)
	p.Yaw = yaw
	// The horizontal magnitude is the definition's maximum velocity outright:
	// there is no cos(pitch) term, since the dropped creator writes no pitch
	// and no vertical component [06 §6.4].
	mag := int32(dropperMaxVelocity.Raw())
	p.Velocity = Vec3{
		X: numeric.Fixed(int64(numeric.MulRound(numeric.Sin(yaw), mag))), // [04 §5.1] (table*mag+4096)>>13
		Y: 0,                                                             // [06 §6.4] vertical velocity zero
		Z: numeric.Fixed(int64(numeric.MulRound(numeric.Cos(yaw), mag))),
	}
	// Pitch is deliberately NOT written: [06 §6.4] says the dropped executor
	// writes none, so it keeps the previous occupant's word [06 §6.1].
}

// InitMeteor initializes a meteor projectile per [06 §6.5].
func InitMeteor(p *Projectile, w *content.WeaponDef, now uint32, pos, vel Vec3) {
	if p == nil {
		return
	}
	// [06 §6.5] meteor creation copies an explicit velocity and bypasses the
	// aim solver: the common initializer runs with a NULL aim point and a NULL
	// shooter, so the record keeps the previous occupant's stored target point
	// and takes the neutral side byte 10 [06 §4.1], [06 §6.5].
	InitCommon(p, now, pos, nil, 0, 0, NeutralSide, 0, w)
	p.Velocity = vel // [06 §6.5] copies explicit velocity
	// [06 §6.5] "initializes no expiry, no angles, and no scalar speed": the
	// expiry word, the scalar speed and both orientation words keep the
	// previous occupant's values. The meteor tick applies no expiry test, and
	// it ADDS its two orientation steps to the retained roll and pitch words,
	// which is why they must not be cleared here. The `ExpiryTick = 0` and
	// `Speed = 0` writes that stood here were writes retail does not make.
}

// InitProjectile dispatches creation and initializes p per [06 §6.2] C15.
// Returns the creation family used; nil weapon returns CreationNone.
// slotDistance and gravity are the ballistic creator's two extra operands, and
// dropperHeading/dropperMaxVelocity are the dropped creator's two; each pair is
// ignored by every other family [06 §6.4].
func InitProjectile(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, yaw, pitch numeric.Angle, meteorVel *Vec3, slotDistance int32, gravity numeric.Fixed, dropperHeading numeric.Angle, dropperMaxVelocity numeric.Fixed) CreationFamily {
	fam := CreationFamilyForWeapon(w)
	switch fam {
	case CreationMeteor:
		if meteorVel != nil {
			InitMeteor(p, w, now, muzzle, *meteorVel)
		} else {
			InitMeteor(p, w, now, muzzle, Vec3{})
		}
	case CreationBallistic:
		InitBallistic(p, w, now, muzzle, target, targetUnit, pitch, yaw, slotDistance, gravity)
	case CreationVertical:
		InitVertical(p, w, now, muzzle, target, targetUnit)
	case CreationOrdinary:
		InitOrdinary(p, w, now, muzzle, target, targetUnit)
	case CreationDropped:
		InitDropped(p, w, now, muzzle, target, targetUnit, dropperHeading, dropperMaxVelocity)
	default:
	}
	return fam
}

// AdvanceDirect advances a direct projectile one tick per [06 §6.3] [06 §7.2] [06 §7.3] C16.
// Retires at expiry without moving or impact [06 §6.3]; beam latch handled per [06 §6.10].
func AdvanceDirect(p *Projectile, w *content.WeaponDef, tick uint32) AdvanceResult {
	if p == nil {
		return AdvanceRetire
	}
	// [06 §6.3] live direct record moves and collides only while tick < expiry
	if tick >= p.ExpiryTick { // [06 §7.3] expiry is not universal but direct retires at expiry [06 §6.3]
		return AdvanceRetire // [06 §6.3] does not move, collide, or deliver expiry damage
	}
	// Propeller visual advances first [06 §6.2]
	p.PropellerYaw = p.PropellerYaw.Add(1)
	// [06 §6.10] beam latch handling
	isBeam := w != nil && w.BeamWeapon
	wasLatched := p.BeamLatch
	if isBeam && !wasLatched {
		// [06 §6.10] latch is set only when creation+duration < tick
		dur := uint32(0)
		if w != nil {
			dur = uint32(w.Duration)
		}
		if tick > p.CreationTick+dur { // [06 §6.10] strictly less
			p.BeamLatch = true // tick that sets it still leaves tail fixed [06 §6.10]
		}
	}
	// [06 §7.2] direct adds velocity
	// Before latch, tail stays fixed [06 §6.10]; after latch, head and tail move same velocity preserving length
	headWasMoved := false
	// Move head
	p.Pos.X = p.Pos.X.Add(p.Velocity.X) // [06 §7.2] integer fixed-point [I2]
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	headWasMoved = true
	_ = headWasMoved
	if isBeam && wasLatched {
		// [06 §6.10] starting on next live tick, already-latched beam moves head and tail by same velocity
		p.StartPos.X = p.StartPos.X.Add(p.Velocity.X)
		p.StartPos.Y = p.StartPos.Y.Add(p.Velocity.Y)
		p.StartPos.Z = p.StartPos.Z.Add(p.Velocity.Z)
	}
	// Collision would run here [06 §7.1] 4. run collision test; elided for motion unit.
	return AdvanceAlive
}

// AdvanceBallistic advances a ballistic/dropped projectile per [06 §6.4] [06 §7.2] C16.
func AdvanceBallistic(p *Projectile, w *content.WeaponDef, tick uint32, wind Vec3, gravity numeric.Fixed) AdvanceResult {
	if p == nil {
		return AdvanceRetire
	}
	// [06 §6.4] ballistic with zero timer integrates without expiry test —
	// EXCEPT burn-blow, whose lifetime is the computed ballistic deadline
	// stored at creation [06 §6.4], so a zero authored timer still expires.
	if w != nil && (w.WeaponTimer != 0 || w.BurnBlow) {
		if tick >= p.ExpiryTick { // [06 §6.4] integrates only while tick < expiry when timer !=0
			if w.BurnBlow {
				// [06 §6.4] burn-blow calls central impact [06 §7.3]
				// Visible control flow still adds velocity and calls collision afterward [06 §6.6] even though dead bit set.
				// For motion unit, we signal impact and still perform move for that visit to preserve visible flow.
				// But spec for ballistic non-selfProp burn-blow at expiry should impact; we still add velocity? The spec says "burn-blow calls central impact" at expiry, but does it still move before impact?
				// Per [06 §6.4] integrating tick adds velocity then wind then gravity then collision; expiry visit is not integrating.
				// So we retire with impact without move.
				return AdvanceImpact
			}
			// [06 §6.4] without burn-blow, emits expiry puff and retires without impact damage [06 §13.2]
			return AdvanceRetire
		}
	}
	p.PropellerYaw = p.PropellerYaw.Add(1) // [06 §6.2] propeller first
	// [06 §6.4] integrating tick adds velocity to position, adds wind, subtracts gravity, then collision
	p.Pos.X = p.Pos.X.Add(p.Velocity.X) // [06 §7.2] integer fixed-point
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	p.Pos.X = p.Pos.X.Add(wind.X) // [06 §6.4] adds all three global wind values directly to position
	p.Pos.Y = p.Pos.Y.Add(wind.Y)
	p.Pos.Z = p.Pos.Z.Add(wind.Z)
	p.Velocity.Y = p.Velocity.Y.Sub(gravity) // [06 §6.4] subtracts gravity from vertical velocity [06 §7.2]
	return AdvanceAlive
}

// AdvanceDropped advances a dropped projectile per [06 §6.4] [06 §7.2] C16.
// No expiry test [06 §6.4] [06 §7.3].
func AdvanceDropped(p *Projectile, w *content.WeaponDef, tick uint32, wind Vec3, gravity numeric.Fixed) AdvanceResult {
	if p == nil {
		return AdvanceRetire
	}
	_ = w
	_ = tick
	p.PropellerYaw = p.PropellerYaw.Add(1) // [06 §6.2]
	p.Pos.X = p.Pos.X.Add(p.Velocity.X)    // [06 §6.4] adds velocity
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	p.Pos.X = p.Pos.X.Add(wind.X) // [06 §6.4] adds wind directly to position
	p.Pos.Y = p.Pos.Y.Add(wind.Y)
	p.Pos.Z = p.Pos.Z.Add(wind.Z)
	p.Velocity.Y = p.Velocity.Y.Sub(gravity) // [06 §6.4] subtracts gravity
	return AdvanceAlive
}

// AdvanceMeteor advances a meteor projectile per [06 §6.5] [06 §7.2].
// Does not apply wind, gravity, or normal expiry [06 §6.5].
func AdvanceMeteor(p *Projectile, w *content.WeaponDef, tick uint32) AdvanceResult {
	if p == nil {
		return AdvanceRetire
	}
	_ = w
	_ = tick
	// [06 §6.5] Each meteor tick adds velocity to the current point and
	// advances two visual orientation accumulators by (high16(velX)<<8) into
	// the record's ROLL word and (high16(velZ)<<8) into its PITCH word,
	// re-derived from the velocity components each tick (no stored rate);
	// they feed only presentation rotation, never motion. The roll word has
	// no dedicated field here and is parked in PropellerYaw, which is the
	// render block's first word for a non-propeller model [06 R-WFX-01 §4];
	// no presentation path reads either yet.
	rollStep, pitchStep := MeteorAngularSteps(p.Velocity.X, p.Velocity.Z)
	p.PropellerYaw = numeric.Angle(uint16(int32(p.PropellerYaw) + int32(int16(rollStep))))
	p.MeteorPitch = numeric.Angle(uint16(int32(p.MeteorPitch) + int32(int16(pitchStep))))
	// The state byte is deliberately NOT written here. [06 §6.5] gives the
	// meteor tick as exactly three things — add velocity to the current point,
	// advance the two orientation accumulators, run ordinary current-point
	// collision — and a state-byte write is not one of them. A line here used to
	// clear bits 0 and 1 on every tick under a comment claiming it KEPT them;
	// those two bits are the beam latch and the DEAD flag [06 §6.1], so the
	// clear would have un-set the dead flag of a meteor that collision had
	// already retired. Orientation is presentation and latches nothing.
	// [06 §7.2] meteor adds velocity; [06 §6.5] does not apply wind/gravity/normal expiry
	p.Pos.X = p.Pos.X.Add(p.Velocity.X)
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)

	return AdvanceAlive
}

// CruiseThreshold is the fixed 0x400 threshold in helper comparison domain
// [P1-07][P1-08 §2.6] [06 §6.8] — when converted distance >0x400 (1024) cruise
// copies target X/Z into second point and forces height to 700, else height is
// max(terrain,sea). Strict >.
const CruiseThreshold = 0x400 // [P1-08 §2.6] [06 §6.8]

// SelectCruisePoint implements the cruise waypoint helper per [P1-08 §2.6]
// [06 §6.8] C15: cruise flag selects waypoint, not creation. Helper is
// presentation/steering only.
func SelectCruisePoint(stored, current Vec3, terrainHeight, seaLevel numeric.Fixed, distanceFixed int32) Vec3 {
	if distanceFixed > CruiseThreshold { // strict > [P1-08 §2.6]
		return Vec3{X: stored.X, Y: numeric.Fixed(int64(700) << 16), Z: stored.Z} // 700<<16 forced [P1-08 §2.6]
	}
	// at or below threshold: stored with height max(terrain,sea) [P1-08 §2.6][06 §6.8]
	y := terrainHeight
	if seaLevel.Raw() > y.Raw() {
		y = seaLevel
	}
	return Vec3{X: stored.X, Y: y, Z: stored.Z}
}

// SmokeAdditiveDeadline advances the smoke deadline additively [P1-08 §4]
// [06 §7.3] — deadline += delay preserving accumulated debt; delay 0 emits
// every eligible tick after first. Smoke is scheduled only for alive records
// before expiry and past next-trail deadline.
func SmokeAdditiveDeadline(currentDeadline uint32, delay int32) uint32 {
	return currentDeadline + uint32(delay) // additive [P1-08][06 §7.3]
}

// Case7 Jitter helpers use CRT stream, not sim [P1-07][P1-08 §2.10] [06 §6.10].
// Each segment draws rand()*11/0x8000-5 per axis (3 axes per segment per pass,
// 2 passes) via CRT *214013+2531011 [P1-08 §5]. This does not affect lockstep.

// StateBits helpers for the projectile record's state byte [P1-08 §2.8]
const (
	StateDead      = 0x02 // state byte &2 dead [P1-08 §2.8][P1-07]
	StateBeamLatch = 0x01 // state byte &1 beam latch [P1-08 §2.8]
	StateTwoPhase  = 0x30 // state byte &0x30 two-phase state (bits 0x10|0x20) [P1-08 §2.8]
)

// steerToward implements [06 §6.7] guidance: pure pursuit of the pursuit
// point, desired yaw and pitch in the signed 16-bit circle, yaw processed
// before pitch. On each axis it snaps only when |err| is STRICTLY less than
// the unsigned turn rate; otherwise (equality included) it steps by exactly
// one turn rate. With burn-blow an error greater than 27,000 fails; a pitch
// failure may occur after yaw was already updated. The caller invokes central
// impact on failure and continues through visible motion.
func steerToward(p *Projectile, w *content.WeaponDef, point Vec3) bool {
	turn := uint32(w.TurnRate) // unsigned turn rate [06 §6.7]
	// Yaw first.
	desiredYaw := YawFromDelta(point.X.Sub(p.Pos.X), point.Z.Sub(p.Pos.Z))
	errYaw := int16(desiredYaw - p.Yaw)
	absYaw := uint32(absU16(uint16(errYaw)))
	if absYaw < turn {
		p.Yaw = desiredYaw // snap strictly inside the rate
	} else if errYaw >= 0 {
		p.Yaw = numeric.Angle(uint16(int32(p.Yaw) + int32(turn)))
	} else {
		p.Yaw = numeric.Angle(uint16(int32(p.Yaw) - int32(turn)))
	}
	if w.BurnBlow && absYaw > 27000 {
		return true // steering failure [06 §6.7]
	}
	// Pitch second; a failure here may follow an applied yaw update.
	desiredPitch := PitchFromDelta(point.X.Sub(p.Pos.X), point.Y.Sub(p.Pos.Y), point.Z.Sub(p.Pos.Z))
	errPitch := int16(desiredPitch - p.Pitch)
	absPitch := uint32(absU16(uint16(errPitch)))
	if absPitch < turn {
		p.Pitch = desiredPitch
	} else if errPitch >= 0 {
		p.Pitch = numeric.Angle(uint16(int32(p.Pitch) + int32(turn)))
	} else {
		p.Pitch = numeric.Angle(uint16(int32(p.Pitch) - int32(turn)))
	}
	if w.BurnBlow && absPitch > 27000 {
		return true
	}
	return false
}

func absU16(v uint16) uint16 {
	if v&0x8000 != 0 {
		return uint16(-int16(v))
	}
	return v
}

// AdvanceSelfProp advances a self-propelled projectile per [06 §6.6] [06 §6.7] [06 §7.2] C16.
// Handles acceleration, guidance, water-medium gating, speed clamp, expiry phase
// switch, gravity fallback, and two-phase transition.
func AdvanceSelfProp(p *Projectile, w *content.WeaponDef, tick uint32, gravity numeric.Fixed, seaLevel numeric.Fixed) AdvanceResult {
	if p == nil || w == nil {
		return AdvanceRetire
	}
	p.PropellerYaw = p.PropellerYaw.Add(1) // [06 §6.2]
	// [06 §6.6] expiry handling at or after expiry
	if tick >= p.ExpiryTick {
		if w.BurnBlow {
			// [06 §6.6] burn-blow invokes central impact and skips gravity/phase-transition work
			// Visible control flow still adds existing velocity and calls collision afterward [06 §6.6]
			p.Pos.X = p.Pos.X.Add(p.Velocity.X)
			p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
			p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
			return AdvanceImpact // [06 §6.6]
		}
		// [06 §6.6] without burn-blow, gravity is applied and motion continues
		// If two-phase enabled and phase still clear, set expiry to tick+flightTime, enter first phase, clear targets when tracks not set
		transitioned := false
		if w.TwoPhase && !p.TwoPhase { // [06 §6.6] still clear
			p.ExpiryTick = tick + uint32(w.FlightTime) // [06 §6.6] unsigned flight time; wraps modulo 2^32
			p.TwoPhase = true                          // [06 §6.6] enters observed first phase state [06 §6.1]
			if !w.Tracks {
				p.TargetUnit = 0 // [06 §6.6] clears both retained references when tracks not set
				p.TargetProjectile = 0
			}
			transitioned = true
			// [06 §6.6] does not toggle entire phase mask
		}
		// [06 §6.6] after one transition, later expiry without burn-blow continues gravity/collision rather than second transition or auto-retire
		// Apply gravity and move
		p.Velocity.Y = p.Velocity.Y.Sub(gravity) // [06 §6.6]
		p.Pos.X = p.Pos.X.Add(p.Velocity.X)
		p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
		p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
		if transitioned {
			return AdvancePhaseTransition // [06 §6.6]
		}
		return AdvanceAlive
	}
	// [06 §6.7] while live and eligible for propulsion, adds acceleration to scalar speed and clamps overshoot to weapon velocity
	// Water medium check: a water weapon propels only STRICTLY below the sea
	// plane; at or above it propulsion/guidance are disabled and gravity is
	// applied with pitch forced to zero [06 §6.9].
	eligible := !w.WaterWeapon || p.Pos.Y.Raw() < seaLevel.Raw()
	if !eligible {
		p.Pitch = 0 // [06 §6.9] forces pitch to zero above water
		p.Velocity.Y = p.Velocity.Y.Sub(gravity)
		p.Pos.X = p.Pos.X.Add(p.Velocity.X)
		p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
		p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
		return AdvanceAlive
	}
	{
		// [06 §6.7]: the gate and the overshoot clamp are UNSIGNED 32-bit
		// compares of the scalar speed word against weaponvelocity, and the
		// block is skipped entirely once speed has reached it (closed
		// 2026-09-02). A negative scalar speed reads above every positive
		// limit and is never accelerated; a negative weaponacceleration
		// decrements until the sum would cross zero, whereupon the clamp snaps
		// it up to weaponvelocity. No stock weapon authors either sign.
		speed := uint32(int32(p.Speed.Raw()))
		limit := uint32(w.WeaponVelocity) // [02] weaponvelocity*65536/30
		if speed < limit {
			speed += uint32(w.WeaponAcceleration) // [02 "Weapon record"] *65536/900, wrapping add
			if speed > limit {
				speed = limit // unsigned overshoot clamp [06 §6.7]
			}
			p.Speed = numeric.Fixed(int64(int32(speed)))
		}
	}
	// [06 §6.7][06 §6.8] guidance pursues the stored target point; a lost unit
	// target falls back to that stored point, so the pursuit point is always
	// TargetPos until the driver refreshes it from a live linked record
	// [06 §11.2].
	if w.Guidance {
		if failed := steerToward(p, w, p.TargetPos); failed {
			// Burn-blow steering failure invokes central impact; the visible
			// velocity/motion code still runs this visit [06 §6.7].
			p.Velocity = VelocityFromAngles(p.Yaw, p.Pitch, p.Speed)
			p.Pos.X = p.Pos.X.Add(p.Velocity.X)
			p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
			p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
			return AdvanceImpact
		}
	}
	// [06 §6.7] after optional guidance, recomputes all velocity components from scalar speed, yaw, pitch
	p.Velocity = VelocityFromAngles(p.Yaw, p.Pitch, p.Speed) // [06 §6.7]
	p.Pos.X = p.Pos.X.Add(p.Velocity.X)                      // [06 §6.7] moves and performs collision [06 §7.2]
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	return AdvanceAlive
}

// Advance dispatches to the appropriate per-family advance per [06 §6.2] motion ordering (C15).
// Propeller presentation advances first [06 §6.2] is handled inside each family; this wrapper selects the family.
func Advance(p *Projectile, w *content.WeaponDef, tick uint32, wind Vec3, gravity numeric.Fixed, seaLevel numeric.Fixed) AdvanceResult {
	if p == nil || w == nil {
		return AdvanceRetire
	}
	switch MotionFamilyForWeapon(w) { // [06 §6.2] selfProp → LOS → ballistic → dropped → meteor
	case MotionSelfProp:
		return AdvanceSelfProp(p, w, tick, gravity, seaLevel)
	case MotionDirect:
		return AdvanceDirect(p, w, tick)
	case MotionBallistic:
		return AdvanceBallistic(p, w, tick, wind, gravity)
	case MotionDropped:
		return AdvanceDropped(p, w, tick, wind, gravity)
	case MotionMeteor:
		return AdvanceMeteor(p, w, tick)
	default:
		return AdvanceRetire
	}
}
