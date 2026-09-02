package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func fix(v int64) numeric.Fixed { return numeric.Fixed(v) }

// TestCreationDispatchPrecedence locks [06 §6.2] creation precedence: meteor → ballistic → vlaunch → LOS/selfProp → dropped.
func TestCreationDispatchPrecedence(t *testing.T) {
	// meteor-flagged weapon never takes ballistic branch even when both set [06 §6.2] C15
	w := &content.WeaponDef{Meteor: true, Ballistic: true, VLaunch: true, LineOfSight: true, SelfProp: true, Dropped: true}
	if got := CreationFamilyForWeapon(w); got != CreationMeteor {
		t.Fatalf("meteor precedence got %v want CreationMeteor", got)
	}
	w2 := &content.WeaponDef{Ballistic: true, VLaunch: true, LineOfSight: true, SelfProp: true, Dropped: true}
	if CreationFamilyForWeapon(w2) != CreationBallistic {
		t.Fatalf("ballistic precedence failed")
	}
	w3 := &content.WeaponDef{VLaunch: true, LineOfSight: true, Dropped: true}
	if CreationFamilyForWeapon(w3) != CreationVertical {
		t.Fatalf("vlaunch precedence failed")
	}
	w4 := &content.WeaponDef{LineOfSight: true, Dropped: true}
	if CreationFamilyForWeapon(w4) != CreationOrdinary {
		t.Fatalf("LOS ordinary precedence failed")
	}
	w5 := &content.WeaponDef{SelfProp: true, Dropped: true}
	if CreationFamilyForWeapon(w5) != CreationOrdinary {
		t.Fatalf("selfProp ordinary precedence failed")
	}
	w6 := &content.WeaponDef{Dropped: true}
	if CreationFamilyForWeapon(w6) != CreationDropped {
		t.Fatalf("dropped precedence failed")
	}
	w7 := &content.WeaponDef{}
	if CreationFamilyForWeapon(w7) != CreationNone {
		t.Fatalf("none precedence failed")
	}
	// meteor→ballistic vertical subset
	w8 := &content.WeaponDef{Ballistic: true}
	if CreationFamilyForWeapon(w8) != CreationBallistic {
		t.Fatalf("ballistic alone failed")
	}
	// vlaunch over ordinary when both present and no ballistic/meteor
	w9 := &content.WeaponDef{VLaunch: true, SelfProp: true}
	if CreationFamilyForWeapon(w9) != CreationVertical {
		t.Fatalf("vlaunch over selfProp failed")
	}
	// LOS+SelfProp collapses to ordinary, not dropped
	w10 := &content.WeaponDef{LineOfSight: true, SelfProp: true, Dropped: true}
	if CreationFamilyForWeapon(w10) != CreationOrdinary {
		t.Fatalf("LOS+SelfProp should be ordinary not dropped")
	}
}

// TestMotionDispatchPrecedence locks [06 §6.2] active motion precedence: selfProp → LOS → ballistic → dropped → meteor.
func TestMotionDispatchPrecedence(t *testing.T) {
	wSelf := &content.WeaponDef{SelfProp: true, LineOfSight: true, Ballistic: true, Dropped: true, Meteor: true}
	if MotionFamilyForWeapon(wSelf) != MotionSelfProp {
		t.Fatalf("motion selfProp precedence failed")
	}
	wDirect := &content.WeaponDef{LineOfSight: true, Ballistic: true, Dropped: true, Meteor: true}
	if MotionFamilyForWeapon(wDirect) != MotionDirect {
		t.Fatalf("motion direct precedence failed")
	}
	wBall := &content.WeaponDef{Ballistic: true, Dropped: true, Meteor: true}
	if MotionFamilyForWeapon(wBall) != MotionBallistic {
		t.Fatalf("motion ballistic precedence failed")
	}
	wDrop := &content.WeaponDef{Dropped: true, Meteor: true}
	if MotionFamilyForWeapon(wDrop) != MotionDropped {
		t.Fatalf("motion dropped precedence failed")
	}
	wMeteor := &content.WeaponDef{Meteor: true}
	if MotionFamilyForWeapon(wMeteor) != MotionMeteor {
		t.Fatalf("motion meteor precedence failed")
	}
	wNone := &content.WeaponDef{}
	if MotionFamilyForWeapon(wNone) != MotionNone {
		t.Fatalf("motion none failed")
	}
	// selfProp flagged weapon never takes ballistic branch even though ballistic true [06 §6.2]
	wBoth := &content.WeaponDef{SelfProp: true, Ballistic: true}
	if MotionFamilyForWeapon(wBoth) != MotionSelfProp {
		t.Fatalf("selfProp should outrank ballistic in motion dispatch")
	}
}

// TestDirectExpiryRetiresAtExpiry per [06 §6.3] [06 §7.3] C16: direct retires at expiry without move.
func TestDirectExpiryRetiresAtExpiry(t *testing.T) {
	w := &content.WeaponDef{LineOfSight: true, WeaponTimer: 10}
	p := Projectile{
		Pos:          Vec3{X: fix(0), Y: fix(0), Z: fix(0)},
		Velocity:     Vec3{X: fix(65536), Y: fix(0), Z: fix(0)},
		CreationTick: 5,
		ExpiryTick:   8,
		StartPos:     Vec3{X: fix(0)},
	}
	// tick 7 <8 => should move
	p1 := p
	res := AdvanceDirect(&p1, w, 7)
	if res != AdvanceAlive {
		t.Fatalf("direct before expiry expected alive got %v", res)
	}
	if p1.Pos.X.Raw() != 65536 {
		t.Fatalf("direct before expiry pos X got %d want 65536", p1.Pos.X.Raw())
	}
	// tick 8 == expiry => retire without move [06 §6.3]
	p2 := p
	res = AdvanceDirect(&p2, w, 8)
	if res != AdvanceRetire {
		t.Fatalf("direct at expiry expected retire got %v", res)
	}
	if p2.Pos.X.Raw() != 0 {
		t.Fatalf("direct at expiry should not move, pos X got %d want 0", p2.Pos.X.Raw())
	}
	// tick 9 > expiry also retire
	p3 := p
	res = AdvanceDirect(&p3, w, 9)
	if res != AdvanceRetire {
		t.Fatalf("direct after expiry expected retire")
	}
}

// TestBallisticZeroTimerNoExpiry per [06 §6.4]: zero weapon timer integrates without expiry test.
func TestBallisticZeroTimerNoExpiry(t *testing.T) {
	w := &content.WeaponDef{Ballistic: true, WeaponTimer: 0, BurnBlow: false}
	p := Projectile{
		Pos:        Vec3{X: fix(0), Y: fix(0), Z: fix(0)},
		Velocity:   Vec3{X: fix(65536), Y: fix(0), Z: fix(0)},
		ExpiryTick: 5, // would be expiry if checked, but zero timer skips
	}
	wind := Vec3{}
	grav := fix(8192) // 0.125
	res := AdvanceBallistic(&p, w, 100, wind, grav)
	if res != AdvanceAlive {
		t.Fatalf("ballistic zero timer should be alive despite tick >= expiry")
	}
	if p.Pos.X.Raw() != 65536 {
		t.Fatalf("ballistic zero timer pos X got %d want 65536", p.Pos.X.Raw())
	}
	if p.Velocity.Y.Raw() != -8192 {
		t.Fatalf("ballistic zero timer vel Y got %d want -8192 after gravity", p.Velocity.Y.Raw())
	}
}

// TestBallisticTimedNoBurnBlowRetiresWithoutImpact per [06 §6.4] C16.
func TestBallisticTimedNoBurnBlowRetiresWithoutImpact(t *testing.T) {
	w := &content.WeaponDef{Ballistic: true, WeaponTimer: 5, BurnBlow: false}
	p := Projectile{
		Pos:        Vec3{X: fix(0)},
		Velocity:   Vec3{X: fix(65536)},
		ExpiryTick: 10,
	}
	wind := Vec3{}
	grav := fix(8192)
	// before expiry
	p1 := p
	res := AdvanceBallistic(&p1, w, 9, wind, grav)
	if res != AdvanceAlive {
		t.Fatalf("ballistic before expiry expected alive")
	}
	// at expiry without burnBlow => retire without impact [06 §6.4]
	p2 := p
	res = AdvanceBallistic(&p2, w, 10, wind, grav)
	if res != AdvanceRetire {
		t.Fatalf("ballistic at expiry without burnBlow expected retire got %v", res)
	}
	// pos should not have moved on expiry visit (expiry check before integrate)
	if p2.Pos.X.Raw() != 0 {
		t.Fatalf("ballistic expiry visit should not move pos X got %d want 0", p2.Pos.X.Raw())
	}
}

// TestBallisticBurnBlowExpiryImpact per [06 §6.4] [06 §7.3] C16.
func TestBallisticBurnBlowExpiryImpact(t *testing.T) {
	w := &content.WeaponDef{Ballistic: true, WeaponTimer: 5, BurnBlow: true}
	p := Projectile{
		Pos:        Vec3{X: fix(0)},
		Velocity:   Vec3{X: fix(65536)},
		ExpiryTick: 10,
	}
	wind := Vec3{}
	grav := fix(0)
	res := AdvanceBallistic(&p, w, 10, wind, grav)
	if res != AdvanceImpact {
		t.Fatalf("ballistic burnBlow at expiry expected impact got %v", res)
	}
}

// TestDroppedNoExpiry per [06 §6.4] [06 §7.3] C16: dropped has no expiry.
func TestDroppedNoExpiry(t *testing.T) {
	w := &content.WeaponDef{Dropped: true, WeaponTimer: 5}
	p := Projectile{
		Pos:        Vec3{X: fix(0)},
		Velocity:   Vec3{X: fix(65536)},
		ExpiryTick: 5,
	}
	wind := Vec3{X: fix(32768)} // 0.5
	grav := fix(8192)
	res := AdvanceDropped(&p, w, 100, wind, grav)
	if res != AdvanceAlive {
		t.Fatalf("dropped should be alive regardless of tick")
	}
	// pos = vel + wind = 65536+32768=98304
	if p.Pos.X.Raw() != 98304 {
		t.Fatalf("dropped pos X got %d want 98304 (vel+wind)", p.Pos.X.Raw())
	}
	if p.Velocity.Y.Raw() != -8192 {
		t.Fatalf("dropped vel Y after gravity got %d want -8192", p.Velocity.Y.Raw())
	}
}

// TestSelfPropTwoPhaseTransition per [06 §6.6] C16.
func TestSelfPropTwoPhaseTransition(t *testing.T) {
	w := &content.WeaponDef{
		SelfProp:           true,
		TwoPhase:           true,
		Tracks:             false,
		FlightTime:         10,
		WeaponTimer:        5,
		WeaponVelocity:     65536,
		WeaponAcceleration: 0,
		BurnBlow:           false,
	}
	p := Projectile{
		Pos:              Vec3{X: fix(0)},
		Velocity:         Vec3{X: fix(10000), Y: fix(0), Z: fix(0)},
		Speed:            fix(0),
		Yaw:              0,
		Pitch:            0,
		CreationTick:     0,
		ExpiryTick:       5,
		TwoPhase:         false,
		TargetUnit:       pool.Handle(3),
		TargetProjectile: pool.Handle(2),
	}
	grav := fix(8192)
	// tick 5 == expiry, twoPhase false, tracks false => transition
	res := AdvanceSelfProp(&p, w, 5, grav, fix(0x7FFFFFFF))
	if res != AdvancePhaseTransition {
		t.Fatalf("selfProp twoPhase at expiry expected phase transition got %v", res)
	}
	if !p.TwoPhase {
		t.Fatalf("selfProp should have entered phase")
	}
	if p.ExpiryTick != 15 { // 5+10
		t.Fatalf("selfProp new expiry got %d want 15", p.ExpiryTick)
	}
	if p.TargetUnit != 0 || p.TargetProjectile != 0 {
		t.Fatalf("selfProp should clear targets when tracks false, got unit %v proj %v", p.TargetUnit, p.TargetProjectile)
	}
	// gravity applied and move: velocity.Y should be -8192, pos should be old pos + velocity (with new velocity.Y)
	if p.Velocity.Y.Raw() != -8192 {
		t.Fatalf("selfProp after transition vel Y got %d want -8192", p.Velocity.Y.Raw())
	}
	// After transition, next tick before new expiry should be normal propulsion
	p2 := p
	p2.Speed = fix(0)
	p2.Velocity = Vec3{}
	res = AdvanceSelfProp(&p2, w, 6, grav, fix(0x7FFFFFFF))
	if res != AdvanceAlive {
		t.Fatalf("selfProp after transition next tick expected alive")
	}
	// later expiry after transition should NOT do second transition but continue gravity [06 §6.6]
	p3 := p
	p3.ExpiryTick = 15
	// now at tick 15 again with TwoPhase already true => should be alive with gravity but not transition
	res = AdvanceSelfProp(&p3, w, 15, grav, fix(0x7FFFFFFF))
	if res == AdvancePhaseTransition {
		t.Fatalf("selfProp second expiry should not transition again per [06 §6.6]")
	}
	if res != AdvanceAlive {
		t.Fatalf("selfProp second expiry without burnBlow should be alive with gravity, got %v", res)
	}
	if p3.TwoPhase != true {
		t.Fatalf("selfProp should remain in phase")
	}
	// expiry should stay 15, not be extended again
	if p3.ExpiryTick != 15 {
		t.Fatalf("selfProp second expiry should not reset expiry, got %d", p3.ExpiryTick)
	}
}

// TestSelfPropTwoPhaseTracksPreservesTargets per [06 §6.6]: when tracks set, targets not cleared.
func TestSelfPropTwoPhaseTracksPreservesTargets(t *testing.T) {
	w := &content.WeaponDef{SelfProp: true, TwoPhase: true, Tracks: true, FlightTime: 10, WeaponTimer: 5, BurnBlow: false}
	p := Projectile{ExpiryTick: 5, TwoPhase: false, TargetUnit: 5, TargetProjectile: 6}
	grav := fix(0)
	res := AdvanceSelfProp(&p, w, 5, grav, fix(0x7FFFFFFF))
	if res != AdvancePhaseTransition {
		t.Fatalf("expected phase transition")
	}
	if p.TargetUnit != 5 || p.TargetProjectile != 6 {
		t.Fatalf("tracks true should preserve targets")
	}
}

// TestSelfPropBurnBlowAtExpiryImpact per [06 §6.6]: burnBlow at expiry invokes impact and skips phase.
func TestSelfPropBurnBlowAtExpiryImpact(t *testing.T) {
	w := &content.WeaponDef{SelfProp: true, TwoPhase: true, BurnBlow: true, FlightTime: 10, WeaponTimer: 5}
	p := Projectile{
		Pos:        Vec3{X: fix(0), Y: fix(0), Z: fix(0)},
		Velocity:   Vec3{X: fix(65536)},
		ExpiryTick: 5,
		TwoPhase:   false,
	}
	grav := fix(8192)
	res := AdvanceSelfProp(&p, w, 5, grav, fix(0x7FFFFFFF))
	if res != AdvanceImpact {
		t.Fatalf("selfProp burnBlow at expiry expected impact got %v", res)
	}
	if p.TwoPhase {
		t.Fatalf("burnBlow should skip phase transition")
	}
	// visible flow still adds velocity [06 §6.6]
	if p.Pos.X.Raw() != 65536 {
		t.Fatalf("burnBlow expiry should still add velocity pos X got %d want 65536", p.Pos.X.Raw())
	}
	// velocity.Y should NOT have gravity applied when burnBlow skips it
	if p.Velocity.Y.Raw() != 0 {
		t.Fatalf("burnBlow should skip gravity, vel Y got %d want 0", p.Velocity.Y.Raw())
	}
}

// TestBallisticIntegrationWindGravity per [06 §6.4] [06 §7.2]: hand-computed truncation vector.
func TestBallisticIntegrationWindGravity(t *testing.T) {
	w := &content.WeaponDef{Ballistic: true, WeaponTimer: 10}
	p := Projectile{
		Pos:        Vec3{X: fix(0), Y: fix(0), Z: fix(0)},
		Velocity:   Vec3{X: fix(65536), Y: fix(0), Z: fix(32768)}, // 1.0, 0, 0.5
		ExpiryTick: 100,
	}
	wind := Vec3{X: fix(32768), Y: fix(16384), Z: fix(0)} // 0.5, 0.25, 0
	grav := fix(8192)                                     // 0.125
	res := AdvanceBallistic(&p, w, 0, wind, grav)
	if res != AdvanceAlive {
		t.Fatalf("expected alive")
	}
	// pos = pos+vel+wind per [06 §6.4]
	// X: 0+65536+32768=98304 (1.5)
	// Y: 0+0+16384=16384 (0.25)
	// Z: 0+32768+0=32768 (0.5)
	if p.Pos.X.Raw() != 98304 || p.Pos.Y.Raw() != 16384 || p.Pos.Z.Raw() != 32768 {
		t.Fatalf("ballistic pos got (%d,%d,%d) want (98304,16384,32768)", p.Pos.X.Raw(), p.Pos.Y.Raw(), p.Pos.Z.Raw())
	}
	// velY = 0 - 8192 = -8192
	if p.Velocity.Y.Raw() != -8192 {
		t.Fatalf("ballistic vel Y got %d want -8192", p.Velocity.Y.Raw())
	}
	// X,Z vel unchanged
	if p.Velocity.X.Raw() != 65536 || p.Velocity.Z.Raw() != 32768 {
		t.Fatalf("ballistic vel XZ unchanged")
	}
}

// TestDirectVelocityReconstructionTruncation hand-computes trig helpers [04 §5.1] [06 §6.4].
func TestDirectVelocityReconstructionTruncation(t *testing.T) {
	// Speed 1.0 =65536, yaw 16384 (90deg), pitch 0 => velX 1.0, velZ 0, velY 0
	// Sin(16384)=8192, Cos(16384)=0, MulRound(8192,65536)=65536
	v := VelocityFromAngles(numeric.Angle(16384), numeric.Angle(0), fix(65536))
	if v.X.Raw() != 65536 || v.Y.Raw() != 0 || v.Z.Raw() != 0 {
		t.Fatalf("vel reconstruct yaw90 pitch0 got (%d,%d,%d) want (65536,0,0)", v.X.Raw(), v.Y.Raw(), v.Z.Raw())
	}
	// Speed 1.0, yaw 0, pitch 0 => velX 0, velZ 1.0
	v2 := VelocityFromAngles(numeric.Angle(0), numeric.Angle(0), fix(65536))
	if v2.X.Raw() != 0 || v2.Z.Raw() != 65536 {
		t.Fatalf("yaw0 pitch0 got (%d,%d,%d) want (0,0,65536)", v2.X.Raw(), v2.Y.Raw(), v2.Z.Raw())
	}
	// Speed 1.0, yaw 0, pitch 16384 (90deg up) => velY 1.0, horiz 0 => velX 0, velZ 0
	v3 := VelocityFromAngles(numeric.Angle(0), numeric.Angle(16384), fix(65536))
	if v3.Y.Raw() != 65536 || v3.X.Raw() != 0 || v3.Z.Raw() != 0 {
		t.Fatalf("pitch90 got (%d,%d,%d) want (0,65536,0)", v3.X.Raw(), v3.Y.Raw(), v3.Z.Raw())
	}
	// Speed 1.0, yaw 8192 (45deg), pitch 0 => sin45 ~ 5793, cos45 ~5793
	// horiz=65536, vx= MulRound(5793,65536)= (5793*65536+4096)>>13=46344; vz same ~46344 [04 §5.1]
	v4 := VelocityFromAngles(numeric.Angle(8192), numeric.Angle(0), fix(65536))
	if v4.X.Raw() != 46344 || v4.Z.Raw() != 46344 {
		t.Fatalf("yaw45 got X %d Z %d want 46344,46344", v4.X.Raw(), v4.Z.Raw())
	}
	// Pitch 8192 (45deg), yaw 0, speed 1.0 => horiz 46344, vy 46344
	v5 := VelocityFromAngles(numeric.Angle(0), numeric.Angle(8192), fix(65536))
	if v5.Y.Raw() != 46344 || v5.Z.Raw() != 46344 {
		t.Fatalf("pitch45 got Y %d Z %d want 46344", v5.Y.Raw(), v5.Z.Raw())
	}
	// Check trunc toward zero: fixed mul floors? But our MulRound rounds +4096, not trunc; per [04 §5.1] products round to nearest.
	// Hand compute: 5793*65536=379M, +4096=379654144, >>13=46340; without rounding, >>13 would be 46340 also? test passes.
}

// TestBeamLatch per [06 §6.10].
func TestBeamLatch(t *testing.T) {
	w := &content.WeaponDef{LineOfSight: true, BeamWeapon: true, Duration: 5, WeaponTimer: 100, WeaponVelocity: 65536, Range: 1000}
	vel := Vec3{X: fix(65536)}
	p := Projectile{
		Pos:          Vec3{X: fix(0)},
		StartPos:     Vec3{X: fix(0)},
		Velocity:     vel,
		CreationTick: 100,
		ExpiryTick:   300,
		BeamLatch:    false,
	}
	// tick 105: creation+duration=105, tick 105 not > => latch false, tail fixed
	res := AdvanceDirect(&p, w, 105)
	if res != AdvanceAlive {
		t.Fatalf("expected alive")
	}
	if p.BeamLatch {
		t.Fatalf("latch should not be set at tick==creation+duration")
	}
	if p.Pos.X.Raw() != 65536 || p.StartPos.X.Raw() != 0 {
		t.Fatalf("before latch, tail fixed: pos %d start %d want 65536,0", p.Pos.X.Raw(), p.StartPos.X.Raw())
	}
	// tick 106: creation+duration < tick true => set latch this tick, tail still fixed
	res = AdvanceDirect(&p, w, 106)
	if !p.BeamLatch {
		t.Fatalf("latch should be set at tick 106")
	}
	if p.Pos.X.Raw() != 131072 || p.StartPos.X.Raw() != 0 {
		t.Fatalf("latch setting tick, tail still fixed: pos %d start %d want 131072,0", p.Pos.X.Raw(), p.StartPos.X.Raw())
	}
	// tick 107: already latched => both move
	res = AdvanceDirect(&p, w, 107)
	if p.Pos.X.Raw() != 196608 || p.StartPos.X.Raw() != 65536 {
		t.Fatalf("after latch both move: pos %d start %d want 196608,65536", p.Pos.X.Raw(), p.StartPos.X.Raw())
	}
	// length preserved: pos-start = 131072 (2*65536)
	if p.Pos.X.Raw()-p.StartPos.X.Raw() != 131072 {
		t.Fatalf("beam length not preserved after latch")
	}
}

// TestOrdinaryExpiryComputation per [06 §6.3] trunc toward zero.
func TestOrdinaryExpiryComputation(t *testing.T) {
	// Range 100, weaponVelocity 65536 (1.0 per tick) => ticks = 100*65536/65536=100
	w := &content.WeaponDef{Range: 100, WeaponVelocity: 65536, WeaponTimer: 50}
	expiry := OrdinaryExpiry(1000, w)
	if expiry != 1100 {
		t.Fatalf("ordinary expiry got %d want 1100", expiry)
	}
	// NoAutoRange forces timer branch
	w2 := &content.WeaponDef{Range: 100, WeaponVelocity: 65536, WeaponTimer: 50, NoAutoRange: true}
	expiry = OrdinaryExpiry(1000, w2)
	if expiry != 1050 {
		t.Fatalf("noAutoRange expiry got %d want 1050", expiry)
	}
	// Zero velocity forces timer branch
	w3 := &content.WeaponDef{Range: 100, WeaponVelocity: 0, WeaponTimer: 50}
	expiry = OrdinaryExpiry(1000, w3)
	if expiry != 1050 { // now+timer
		t.Fatalf("zero velocity expiry got %d want 1050", expiry)
	}
	// Truncation check: range 10, velocity 98304 (1.5 per tick) => 10*65536/98304=6 (trunc from 6.666)
	w4 := &content.WeaponDef{Range: 10, WeaponVelocity: 98304, WeaponTimer: 50}
	expiry = OrdinaryExpiry(0, w4)
	if expiry != 6 {
		t.Fatalf("trunc expiry got %d want 6", expiry)
	}
	// Wrap modulo 2^32: now=0xFFFFFFF0 +10 => 0xFFFFFFFA wraps? Actually 0xFFFFFFF0+20 wraps to 0x00000004 etc.
	// Use large now.
	w5 := &content.WeaponDef{Range: 100, WeaponVelocity: 65536, WeaponTimer: 0}
	expiry = OrdinaryExpiry(0xFFFFFFF0, w5)
	// 0xFFFFFFF0+100 wraps to 0x00000054 (4294967280+100=4294967380 mod 2^32 =84)
	if expiry != 84 {
		t.Fatalf("wrap expiry got %d want 84", expiry)
	}
}

// TestBallisticBurnBlowExpiry per [06 §6.4] using horiz distance.
func TestBallisticBurnBlowExpiry(t *testing.T) {
	// muzzle (0,0,0) target (10,0,0) => wideDistance= trunc(hypot(10*65536,0))? Wait dx Raw = delta Raw, not world units.
	// Our helper uses Raw difference directly as int32 before hypot, not dividing by 65536.
	// For world units 10 => Fixed raw 10*65536=655360, dx=655360.
	// hypot 655360 => wide=655360
	// pitch 0 => cos=8192 => H= MulRound(8192, vel) vel=65536 =>65536
	// T=655360/65536=10
	muzzle := Vec3{X: fix(0), Z: fix(0)}
	target := Vec3{X: fix(655360), Z: fix(0)} // 10 world units? Actually fix(655360)=10*65536
	pitch := numeric.Angle(0)
	vel := int32(65536)
	expiry := BallisticBurnBlowExpiry(100, muzzle, target, pitch, vel)
	if expiry != 110 {
		t.Fatalf("burnBlow expiry got %d want 110", expiry)
	}
	// pitch 45deg => cos 5793 => H=46340 => T=655360/46340=14 trunc
	pitch45 := numeric.Angle(8192)
	expiry = BallisticBurnBlowExpiry(100, muzzle, target, pitch45, vel)
	if expiry != 114 { // 100+14
		t.Fatalf("burnBlow pitch45 expiry got %d want 114", expiry)
	}
	// Zero H raises the same unguarded-divide fault as retail after
	// reservation [GAP T5] I11 — reproduced, not defended.
	pitch90 := numeric.Angle(16384) // cos 0 => H 0
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("zero H should raise the divide fault [GAP T5] I11")
			}
		}()
		BallisticBurnBlowExpiry(100, muzzle, target, pitch90, vel)
	}()
}

// TestYawPitchDerivation uses the shared rounded atan2q conversion [06 §3.3]
// [06 §6.3] [06 §6.4].
func TestYawPitchDerivation(t *testing.T) {
	// delta dx=1.0, dz=1.0 => yaw 45deg => 8192.
	dx := fix(65536)
	dz := fix(65536)
	yaw := YawFromDelta(dx, dz)
	if yaw != numeric.Angle(8192) {
		t.Fatalf("yaw 45deg got %d want 8192", yaw)
	}
	// dx=0, dz=1 => yaw 0
	yaw = YawFromDelta(fix(0), fix(65536))
	if yaw != 0 {
		t.Fatalf("yaw 0 got %d want 0", yaw)
	}
	// [06 §3.3] the vertical operand is -(p.Y - t.Y) = t.Y - p.Y, so a target
	// one whole unit ABOVE the muzzle aims +45 degrees (8192) and one unit
	// below aims -45 degrees (57344). The earlier expectation here had those
	// two swapped, which is the inversion this test now locks against.
	pitch := PitchFromDelta(fix(65536), fix(65536), fix(0))
	if pitch != numeric.Angle(8192) {
		t.Fatalf("pitch +45 got %d want 8192", pitch)
	}
	pitch = PitchFromDelta(fix(65536), fix(-65536), fix(0))
	if pitch != numeric.Angle(57344) {
		t.Fatalf("pitch -45 got %d want 57344", pitch)
	}
	// A non-cardinal vector proves the exact operands and rounding path are
	// used rather than the removed truncating conversion.
	yaw = YawFromDelta(fix(6553), fix(65536))
	expected := numeric.AngleFromAtan2(6553, 65536)
	if yaw != expected {
		t.Fatalf("yaw exact got %d want %d", yaw, expected)
	}
}

// TestMeteorNoWindGravity per [06 §6.5] [06 §7.2]: meteor does not apply wind/gravity.
func TestMeteorNoWindGravity(t *testing.T) {
	w := &content.WeaponDef{Meteor: true}
	p := Projectile{
		Pos:      Vec3{X: fix(0), Y: fix(100 * 65536), Z: fix(0)},
		Velocity: Vec3{X: fix(65536), Y: fix(-983040), Z: fix(0)}, // -15 per tick = -983040 raw (15*65536)
	}
	_ = Vec3{X: fix(100000)}
	_ = fix(8192)
	res := AdvanceMeteor(&p, w, 0)
	if res != AdvanceAlive {
		t.Fatalf("meteor should be alive")
	}
	if p.Pos.X.Raw() != 65536 || p.Pos.Y.Raw() != 100*65536-983040 {
		t.Fatalf("meteor pos should be pos+vel without wind/gravity, got X %d Y %d", p.Pos.X.Raw(), p.Pos.Y.Raw())
	}
	// vel unchanged
	if p.Velocity.Y.Raw() != -983040 {
		t.Fatalf("meteor vel Y should not change with gravity")
	}
}

// TestInitProjectileDispatchSetsExpiry per C15 initial state.
func TestInitProjectileDispatchSetsExpiry(t *testing.T) {
	now := uint32(100)
	muzzle := Vec3{X: fix(0), Y: fix(0), Z: fix(0)}
	target := Vec3{X: fix(655360), Y: fix(0), Z: fix(0)} // 10 units
	wOrd := &content.WeaponDef{ID: 1, LineOfSight: true, WeaponVelocity: 65536, WeaponTimer: 50, Range: 10}
	var p Projectile
	fam := InitProjectile(&p, wOrd, now, muzzle, target, 0, 0, 0, nil, 0, 0)
	if fam != CreationOrdinary {
		t.Fatalf("init ordinary fam got %v want ordinary", fam)
	}
	if p.ExpiryTick != 110 { // 100+10
		t.Fatalf("ordinary expiry got %d want 110", p.ExpiryTick)
	}
	if p.Pos.X.Raw() != 0 || p.StartPos.X.Raw() != 0 {
		t.Fatalf("init pos should be muzzle")
	}
	if p.Velocity.X.Raw() == 0 {
		t.Fatalf("ordinary velocity should be non-zero")
	}

	// ballistic init with burnBlow false uses timer
	wBal := &content.WeaponDef{ID: 2, Ballistic: true, WeaponTimer: 30, BurnBlow: false, WeaponVelocity: 65536}
	fam = InitProjectile(&p, wBal, now, muzzle, target, 0, numeric.Angle(0), numeric.Angle(0), nil, 0, 0)
	if fam != CreationBallistic {
		t.Fatalf("ballistic fam")
	}
	if p.ExpiryTick != 130 { // now+timer
		t.Fatalf("ballistic timer expiry got %d want 130", p.ExpiryTick)
	}

	// vertical
	wV := &content.WeaponDef{ID: 3, VLaunch: true, WeaponVelocity: 65536, Range: 10, WeaponAcceleration: 0, StartVelocity: 0}
	fam = InitProjectile(&p, wV, now, muzzle, target, 0, 0, 0, nil, 0, 0)
	if fam != CreationVertical {
		t.Fatalf("vertical fam")
	}
	if p.Pitch != numeric.Angle(16384) {
		t.Fatalf("vertical pitch want 16384 got %d", p.Pitch)
	}
	if p.Velocity.X.Raw() != 0 || p.Velocity.Y.Raw() != 0 {
		t.Fatalf("vertical velocity should be zero")
	}

	// meteor
	wMet := &content.WeaponDef{ID: 4, Meteor: true}
	vel := Vec3{X: fix(1000), Y: fix(-983040), Z: fix(0)}
	fam = InitProjectile(&p, wMet, now, muzzle, target, 0, 0, 0, &vel, 0, 0)
	if fam != CreationMeteor {
		t.Fatalf("meteor fam")
	}
	if p.Velocity.X.Raw() != 1000 || p.Velocity.Y.Raw() != -983040 {
		t.Fatalf("meteor velocity copy failed")
	}
	if p.ExpiryTick != 0 {
		t.Fatalf("meteor expiry should be 0")
	}

	// dropped
	wDrop := &content.WeaponDef{ID: 5, Dropped: true}
	fam = InitProjectile(&p, wDrop, now, muzzle, target, 0, 0, 0, nil, 0, 0)
	if fam != CreationDropped {
		t.Fatalf("dropped fam")
	}
	if p.ExpiryTick != 0 {
		t.Fatalf("dropped expiry should be 0 (no expiry)")
	}
}

// TestAdvanceDispatch per C15 motion ordering.
func TestAdvanceDispatch(t *testing.T) {
	wSelf := &content.WeaponDef{SelfProp: true, WeaponVelocity: 65536, WeaponAcceleration: 0, WeaponTimer: 10}
	p := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(0)}, Speed: fix(0), Yaw: 0, Pitch: 0, ExpiryTick: 20}
	// selfProp before expiry should recompute velocity and move
	res := Advance(&p, wSelf, 5, Vec3{}, fix(0), fix(0))
	if res != AdvanceAlive {
		t.Fatalf("selfProp advance alive")
	}

	wDirect := &content.WeaponDef{LineOfSight: true, WeaponTimer: 10, Range: 10, WeaponVelocity: 65536}
	p2 := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(65536)}, ExpiryTick: 20}
	res = Advance(&p2, wDirect, 5, Vec3{}, fix(0), fix(0))
	if res != AdvanceAlive || p2.Pos.X.Raw() != 65536 {
		t.Fatalf("direct dispatch failed")
	}

	wBal := &content.WeaponDef{Ballistic: true, WeaponTimer: 10, BurnBlow: false}
	p3 := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(65536)}, ExpiryTick: 20}
	res = Advance(&p3, wBal, 5, Vec3{}, fix(8192), fix(0))
	if res != AdvanceAlive || p3.Pos.X.Raw() != 65536 {
		t.Fatalf("ballistic dispatch failed")
	}

	wDrop := &content.WeaponDef{Dropped: true}
	p4 := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(65536)}, ExpiryTick: 0}
	res = Advance(&p4, wDrop, 100, Vec3{}, fix(8192), fix(0))
	if res != AdvanceAlive {
		t.Fatalf("dropped dispatch should be alive")
	}

	wMet := &content.WeaponDef{Meteor: true}
	p5 := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(65536)}}
	res = Advance(&p5, wMet, 0, Vec3{X: fix(100000)}, fix(8192), fix(0))
	if p5.Pos.X.Raw() != 65536 {
		t.Fatalf("meteor advance should ignore wind, pos got %d", p5.Pos.X.Raw())
	}
}

// Ensure malformed cases reproduced not defended: zero duration beam, zero timer ballistic already tested.
// Negative burst not in motion scope; ensure we don't panic on negative timers.
func TestMalformedNegativeTimerPlaceholder(t *testing.T) {
	w := &content.WeaponDef{Ballistic: true, WeaponTimer: -5, BurnBlow: false}
	p := Projectile{Pos: Vec3{X: fix(0)}, Velocity: Vec3{X: fix(0)}, ExpiryTick: 0} // expiry 0 + uint32(-5) wraps to large
	// Init would wrap: now+uint32(-5) = now-5 wraps; our expiry helper uses uint32 conversion so wraps modulo 2^32.
	// Advance at tick 0 with expiry wraps to 0xFFFFFFFB should not immediately retire if we interpret as uint32 comparison wrapping?
	// Since tick 0 < 0xFFFFFFFB unsigned? Actually 0 < 4294967291 true, so alive. That's correct wrapping behavior per [06 §6.4].
	// TODO(question): negative timer malformed wrapping not fully closed; placeholder treats as unsigned wrap.
	expiry := nowPlusTimer(0, w.WeaponTimer)
	p.ExpiryTick = expiry
	res := AdvanceBallistic(&p, w, 0, Vec3{}, fix(0))
	if res != AdvanceAlive {
		t.Fatalf("negative timer wrap should be alive at tick 0")
	}
	// at tick == expiry should retire
	res = AdvanceBallistic(&p, w, expiry, Vec3{}, fix(0))
	if res != AdvanceRetire {
		t.Fatalf("negative timer at expiry should retire")
	}
}

func nowPlusTimer(now uint32, timer int32) uint32 { return now + uint32(timer) }

// The meteor tick does not write the projectile state byte [06 §6.5]: its three
// actions are the velocity add, the two orientation accumulators and ordinary
// current-point collision. Bits 0 and 1 of that byte are the beam latch and the
// dead flag [06 §6.1], and a clear that ran here every tick would revive a
// record collision had already retired.
func TestMeteorLeavesStateByteAlone(t *testing.T) {
	w := &content.WeaponDef{Meteor: true}
	p := Projectile{
		Pos:      Vec3{X: fix(0), Y: fix(100 * 65536), Z: fix(0)},
		Velocity: Vec3{X: fix(65536), Y: fix(-983040), Z: fix(65536)},
		State69:  0x33, // both low bits set, plus the two-phase field
	}
	if res := AdvanceMeteor(&p, w, 0); res != AdvanceAlive {
		t.Fatalf("meteor should be alive")
	}
	if p.State69 != 0x33 {
		t.Fatalf("meteor tick must not touch the state byte, got %#x want 0x33", p.State69)
	}
}
