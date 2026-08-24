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

// InitCommon performs the common initializer work per [06 §5.1] [06 §6.1]:
// copies muzzle point into both current/head point and second point, copies
// optional aim point into stored target point, clears beam latch and two-phase
// state, seeds smoke deadline, clears target links, records shooter side/piece.
// Family velocity/expiry are not set here [06 §6.1].
func InitCommon(p *Projectile, now uint32, muzzle, target Vec3, targetUnit pool.Handle, shooterSide uint8, muzzlePiece int16, w *content.WeaponDef) {
	if p == nil {
		return
	}
	p.Pos = muzzle       // [06 §6.1] copies muzzle point into current/head point
	p.StartPos = muzzle  // [06 §6.1] and second tail/waypoint/start point
	p.TargetPos = target // [06 §6.1] optional aim point copied into stored target point
	p.TargetUnit = targetUnit
	p.TargetProjectile = 0 // [06 §6.1] clears projectile-to-projectile link
	p.CreationTick = now   // [06 §5.1] creation tick
	p.BeamLatch = false    // [06 §6.1] clears beam latch and two-phase state
	p.TwoPhase = false
	p.Dead = false
	if w != nil {
		p.SmokeDeadline = now + uint32(w.SmokeDelay) // [06 §5.1] seeds smoke deadline [02 "Weapon record"] smokedelay*30
		p.WeaponID = w.ID
	} else {
		p.SmokeDeadline = now
	}
	p.ShooterSide = shooterSide
	p.MuzzlePiece = muzzlePiece
	p.CacheCellX = 0
	p.CacheCellZ = 0
}

// VelocityFromAngles recomputes velocity components from scalar speed, yaw, pitch
// using the retail circular domain trig helpers per [06 §6.4] [04 §5.1] [06 §6.7].
// Each helper has form (tableValue * magnitude + 0x1000) >>13 with 512-entry
// round(8192*sin) table; products round to nearest [04 §5.1].
// TODO(question): ballistic pitch helper quantizes in 64-unit steps vs generic 128-step table; drift TBD [06 §6.4].
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
// Uses atan2(dx, dz) mapping to 16-bit circular domain trunc(angle*32768/pi) [06 §6.4].
func YawFromDelta(dx, dz numeric.Fixed) numeric.Angle {
	// Convert fixed to float world units for atan2, then trunc toward zero per [01 §8] I3.
	x := float64(dx.Raw()) / 65536.0
	z := float64(dz.Raw()) / 65536.0
	if x == 0 && z == 0 {
		return 0
	}
	a := math.Atan2(x, z)             // yaw 0 => +Z, 90deg => +X per retail Sin/Cos convention [04 §5.1]
	raw := int32(a * 32768 / math.Pi) // [06 §6.4] trunc(angle*32768/pi) I3
	return numeric.Angle(uint16(raw))
}

// PitchFromDelta derives pitch from XYZ delta per [06 §6.3].
func PitchFromDelta(dx, dy, dz numeric.Fixed) numeric.Angle {
	x := float64(dx.Raw()) / 65536.0
	y := float64(dy.Raw()) / 65536.0
	z := float64(dz.Raw()) / 65536.0
	h := math.Hypot(x, z)
	if h == 0 && y == 0 {
		return 0
	}
	a := math.Atan2(y, h)
	raw := int32(a * 32768 / math.Pi) // [06 §6.4] trunc
	return numeric.Angle(uint16(raw))
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
	// Truncate toward zero per [01 §8] I3; both operands positive in ordinary state.
	r := int64(w.Range)
	v := int64(w.WeaponVelocity) // Fixed raw 16.16 per tick
	if v == 0 {
		// Malformed zero velocity after reservation raises divide exception per [06 §6.4] [GAP T5] I11.
		// TODO(question): G2 wrapping and hypot overflow elsewhere; here we return timer fallback placeholder.
		return now + uint32(w.WeaponTimer)
	}
	ticks := (r * 65536) / v // trunc toward zero, Go / truncates
	// Wrap modulo 2^32 via uint32 conversion [06 §6.4] burn-blow deadlines wrap
	return now + uint32(ticks)
}

// BallisticBurnBlowExpiry computes burn-blow deadline per [06 §6.4]:
// wideDistance = trunc(hypot(targetX-muzzleX, targetZ-muzzleZ))
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// T = signedDivide(wideDistance, H) trunc toward zero [01 §8]
// expiry = now + T wrapping modulo 2^32.
// Malformed: H==0 raises divide; H<0 truncates toward zero mod 2^32; INT_MIN/-1 raises [GAP T5] I11.
func BallisticBurnBlowExpiry(now uint32, muzzle, target Vec3, pitch numeric.Angle, weaponVelocity int32) uint32 {
	// [GAP T5] X/Z delta components wrap as signed 32-bit before double conversion.
	dx := int32((target.X.Raw() - muzzle.X.Raw()))
	dz := int32((target.Z.Raw() - muzzle.Z.Raw()))
	// [06 §6.4] wideDistance = trunc(hypot(...)) low 32 bits if overflow
	wide := int32(math.Trunc(math.Hypot(float64(dx), float64(dz)))) // trunc toward zero, matches hypot truncated to i32
	// [06 §6.4] H = fixedCos(pitch, weaponVelocity)
	cosPitch := numeric.Cos(pitch)
	h := numeric.MulRound(cosPitch, weaponVelocity) // int32
	if h == 0 {
		// [06 §6.4] zero horizontal speed raises divide exception after pool reservation I11.
		// TODO(question): retail faults; placeholder returns immediate expiry to force expiry visit.
		return now
	}
	// [GAP T5] signed divide truncates toward zero; Go int32 division does.
	// Handle INT_MIN / -1 overflow: Go does not panic for int32, but retail raises signed-divide overflow.
	// We detect and return now as fault placeholder.
	if wide == -2147483648 && h == -1 {
		// [GAP T5] INT_MIN / -1 raises
		// TODO(question): retail raises exception; placeholder immediate expiry.
		return now
	}
	t := wide / h          // trunc toward zero [01 §8]
	return now + uint32(t) // wraps modulo 2^32 [06 §6.4]
}

// InitOrdinary initializes an ordinary/direct projectile per [06 §6.3].
func InitOrdinary(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle) {
	if p == nil || w == nil {
		return
	}
	InitCommon(p, now, muzzle, target, targetUnit, p.ShooterSide, p.MuzzlePiece, w)
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

// InitBallistic initializes a ballistic projectile per [06 §6.4].
// solvedPitch must be the pitch from BallisticSolve [06 §3.3] [06 §6.4].
func InitBallistic(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, solvedPitch numeric.Angle, yaw numeric.Angle) {
	if p == nil || w == nil {
		return
	}
	InitCommon(p, now, muzzle, target, targetUnit, p.ShooterSide, p.MuzzlePiece, w)
	p.Yaw = yaw
	p.Pitch = solvedPitch
	speed := numeric.Fixed(int64(w.WeaponVelocity))
	p.Speed = speed
	p.Velocity = VelocityFromAngles(yaw, solvedPitch, speed) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	InitCommon(p, now, muzzle, target, targetUnit, p.ShooterSide, p.MuzzlePiece, w)
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
func InitDropped(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle) {
	if p == nil || w == nil {
		return
	}
	InitCommon(p, now, muzzle, target, targetUnit, p.ShooterSide, p.MuzzlePiece, w)
	// Dropped has no expiry test per [06 §6.4] [06 §7.3]
	p.ExpiryTick = 0
	// Velocity: treat as falling with gravity; start with zero or weaponVelocity if authored?
	// [06 §6.4] not detailed; use zero for now; motion adds wind/gravity each tick.
	p.Velocity = Vec3{}
	p.Speed = 0
}

// InitMeteor initializes a meteor projectile per [06 §6.5].
func InitMeteor(p *Projectile, w *content.WeaponDef, now uint32, pos, vel Vec3) {
	if p == nil {
		return
	}
	// [06 §6.5] meteor creation copies explicit velocity and bypasses ordinary aim solver; no-shooter owner path
	InitCommon(p, now, pos, pos, 0, 0, 0, w)
	p.Pos = pos
	p.StartPos = pos
	p.Velocity = vel // [06 §6.5] copies explicit velocity
	// [06 §6.5] does not initialize ordinary expiry
	p.ExpiryTick = 0
	p.Speed = 0
}

// InitProjectile dispatches creation and initializes p per [06 §6.2] C15.
// Returns the creation family used; nil weapon returns CreationNone.
func InitProjectile(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, yaw, pitch numeric.Angle, meteorVel *Vec3) CreationFamily {
	fam := CreationFamilyForWeapon(w)
	switch fam {
	case CreationMeteor:
		if meteorVel != nil {
			InitMeteor(p, w, now, muzzle, *meteorVel)
		} else {
			InitMeteor(p, w, now, muzzle, Vec3{})
		}
	case CreationBallistic:
		InitBallistic(p, w, now, muzzle, target, targetUnit, pitch, yaw)
	case CreationVertical:
		InitVertical(p, w, now, muzzle, target, targetUnit)
	case CreationOrdinary:
		InitOrdinary(p, w, now, muzzle, target, targetUnit)
	case CreationDropped:
		InitDropped(p, w, now, muzzle, target, targetUnit)
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
	// [06 §6.4] ballistic with zero timer integrates without expiry test
	if w != nil && w.WeaponTimer != 0 {
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
	// [06 §6.5] Each meteor tick adds velocity to current point, advances two visual orientation accumulators by (high16(velX)<<8) and (high16(velZ)<<8)
	// Visual orientation not used for motion but we tick propeller as placeholder.
	p.PropellerYaw = p.PropellerYaw.Add(1)
	// [06 §7.2] meteor adds velocity; [06 §6.5] does not apply wind/gravity/normal expiry
	p.Pos.X = p.Pos.X.Add(p.Velocity.X)
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	// [06 §6.5] visual accumulators would advance here via high halves <<8; retained in PropellerYaw for test observability
	// High 16 bits of velX/velZ <<8: effectively (Raw>>16 &0xFFFF)<<8 = (Raw>>8)&0xFFFF00 ; wrap modulo 2^16
	// We expose via PropellerYaw progression already; full yaw accumulators remain TODO.

	return AdvanceAlive
}

// AdvanceSelfProp advances a self-propelled projectile per [06 §6.6] [06 §6.7] [06 §7.2] C16.
// Handles acceleration, speed clamp, expiry phase switch, gravity fallback, and two-phase transition.
func AdvanceSelfProp(p *Projectile, w *content.WeaponDef, tick uint32, gravity numeric.Fixed) AdvanceResult {
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
	// Check water eligibility: non-water always eligible; water only when saved pre-motion height < sea level [06 §6.7] [06 §6.9]
	// TODO(question): water medium check requires world height; placeholder always eligible for non-water.
	eligible := true
	if w.WaterWeapon {
		// TODO(question): runtime medium check uses saved pre-motion height strictly below sea level [06 §6.7]; sea level not modeled here.
		eligible = false // placeholder: water weapon skips acceleration/guidance and falls under gravity when not below water [06 §6.7]
		// For WU-09-5 motion tests we assume non-water eligible; water case returns gravity path.
		if !eligible {
			// [06 §6.7] at or above sea level, skips acceleration/guidance, subtracts gravity, forces pitch to zero, then moves/collides
			p.Pitch = 0 // [06 §6.7] forces pitch to zero
			p.Velocity.Y = p.Velocity.Y.Sub(gravity)
			p.Pos.X = p.Pos.X.Add(p.Velocity.X)
			p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
			p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
			return AdvanceAlive
		}
	}
	if eligible {
		accel := numeric.Fixed(int64(w.WeaponAcceleration)) // [02 "Weapon record"] *65536/900
		newSpeed := p.Speed.Add(accel)                      // [06 §6.7] adds acceleration to scalar speed
		limit := numeric.Fixed(int64(w.WeaponVelocity))     // [02] weaponvelocity*65536/30
		// [06 §6.7] clamps overshoot to weapon velocity; acceleration independent of guidance
		if newSpeed.Raw() > limit.Raw() {
			newSpeed = limit // clamp [06 §6.7]
		}
		// TODO: negative speed? Malformed negative acceleration? TODO(question) [06 §7.3] negative timer unknown.
		p.Speed = newSpeed
		// [06 §6.7] after optional guidance, recomputes all velocity components from scalar speed, yaw, pitch
		// TODO(question): guidance pure pursuit not modeled here; yaw/pitch unchanged.
		p.Velocity = VelocityFromAngles(p.Yaw, p.Pitch, p.Speed) // [06 §6.7]
		p.Pos.X = p.Pos.X.Add(p.Velocity.X)                      // [06 §6.7] moves and performs collision [06 §7.2]
		p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
		p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	}
	return AdvanceAlive
}

// Advance dispatches to the appropriate per-family advance per [06 §6.2] motion ordering (C15).
// Propeller presentation advances first [06 §6.2] is handled inside each family; this wrapper selects the family.
func Advance(p *Projectile, w *content.WeaponDef, tick uint32, wind Vec3, gravity numeric.Fixed) AdvanceResult {
	if p == nil || w == nil {
		return AdvanceRetire
	}
	switch MotionFamilyForWeapon(w) { // [06 §6.2] selfProp → LOS → ballistic → dropped → meteor
	case MotionSelfProp:
		return AdvanceSelfProp(p, w, tick, gravity)
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
