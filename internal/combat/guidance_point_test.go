package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// guidanceEnvFor builds the two lookups over a single linked record and a
// single unit, both addressed by handle 1 unless the caller passes 0.
func guidanceEnvFor(link *Projectile, unit *units.Unit) GuidanceEnv {
	return GuidanceEnv{
		Projectile: func(h pool.Handle) *Projectile {
			if h == 1 {
				return link
			}
			return nil
		},
		Unit: func(h pool.Handle) *units.Unit {
			if h == 1 {
				return unit
			}
			return nil
		},
	}
}

// TestGuidanceTargetPointSelectionOrder locks the three sources of the
// guidance target-point helper and their order [06 §6.7]: the projectile link
// first, the retained unit target while its live flag is set second, the
// stored target point last. The stored point is the lost-target fallback
// [06 §6.8], so it must never be consulted while a live reference exists and
// must never be overwritten.
func TestGuidanceTargetPointSelectionOrder(t *testing.T) {
	stored := Vec3{X: fi(700), Y: fi(0), Z: fi(700)}
	link := &Projectile{Pos: Vec3{X: fi(100), Y: fi(10), Z: fi(100)}}
	unit := &units.Unit{X: fi(400), Y: fi(20), Z: fi(400), Alive: true}
	w := &content.WeaponDef{SelfProp: true, Guidance: true}

	// 1. Both references set: the LINK wins, and the unit is not consulted.
	p := Projectile{TargetPos: stored, TargetProjectile: 1, TargetUnit: 1}
	if got := GuidanceTargetPoint(&p, w, guidanceEnvFor(link, unit)); got != link.Pos {
		t.Fatalf("link set: point = %v, want the linked record's current point %v [06 §6.7]", got, link.Pos)
	}

	// 2. No link: the retained unit's world point wins over the stored point.
	p.TargetProjectile = 0
	wantUnit := Vec3{X: unit.X, Y: unit.Y, Z: unit.Z}
	if got := GuidanceTargetPoint(&p, w, guidanceEnvFor(link, unit)); got != wantUnit {
		t.Fatalf("live unit: point = %v, want the unit's world point %v [06 §6.7]", got, wantUnit)
	}

	// 3. The unit's live flag clears: the stored point is the fallback, and
	//    the record does not reacquire anything [06 §6.8].
	unit.Alive = false
	if got := GuidanceTargetPoint(&p, w, guidanceEnvFor(link, unit)); got != stored {
		t.Fatalf("dead unit: point = %v, want the stored target point %v [06 §6.8]", got, stored)
	}
	unit.Alive = true

	// No reference at all is the same fallback.
	p.TargetUnit = 0
	if got := GuidanceTargetPoint(&p, w, guidanceEnvFor(link, unit)); got != stored {
		t.Fatalf("no reference: point = %v, want the stored target point %v [06 §6.8]", got, stored)
	}
	if p.TargetPos != stored {
		t.Fatalf("the helper wrote the stored point (%v): it is the fallback and must survive [06 §6.8]", p.TargetPos)
	}
}

// TestGuidanceLinkIsDereferencedWithoutLivenessCheck locks the absence of any
// validation on the projectile-to-projectile link: no liveness generation
// counter exists, so a link into a record the collision pass already retired
// is read anyway [06 §6.7][06 §5.2].
func TestGuidanceLinkIsDereferencedWithoutLivenessCheck(t *testing.T) {
	link := &Projectile{Pos: Vec3{X: fi(100), Y: fi(10), Z: fi(100)}, Dead: true}
	p := Projectile{TargetPos: Vec3{X: fi(700)}, TargetProjectile: 1}
	if got := GuidanceTargetPoint(&p, &content.WeaponDef{SelfProp: true, Guidance: true}, guidanceEnvFor(link, nil)); got != link.Pos {
		t.Fatalf("retired link: point = %v, want the linked record's point %v anyway [06 §5.2]", got, link.Pos)
	}
}

// TestGuidanceCruiseIgnoresBothRetainedReferences locks [06 §6.8]: the
// `cruise` flag selects the waypoint helper, which ignores the projectile link
// and the retained unit entirely and works from the stored target point.
func TestGuidanceCruiseIgnoresBothRetainedReferences(t *testing.T) {
	stored := Vec3{X: fi(700), Y: fi(0), Z: fi(700)}
	link := &Projectile{Pos: Vec3{X: fi(100)}}
	unit := &units.Unit{X: fi(400), Alive: true}
	p := Projectile{TargetPos: stored, TargetProjectile: 1, TargetUnit: 1}
	w := &content.WeaponDef{SelfProp: true, Guidance: true, Cruise: true}
	if got := GuidanceTargetPoint(&p, w, guidanceEnvFor(link, unit)); got != stored {
		t.Fatalf("cruise: point = %v, want the stored target point %v [06 §6.8]", got, stored)
	}
}

// TestSelfPropSteersAtTheLiveUnitNotTheLaunchPoint is the end-to-end shape of
// the defect this helper fixes: a guided record whose retained unit has moved
// since launch must turn toward the unit, not toward the point the aim solver
// stored at creation [06 §6.7].
func TestSelfPropSteersAtTheLiveUnitNotTheLaunchPoint(t *testing.T) {
	// A turn rate wide enough that the snap branch of [06 §6.7] applies, so
	// the resulting yaw is exactly the wanted angle and the test reads the
	// SELECTED point rather than a fraction of a step toward it.
	w := &content.WeaponDef{SelfProp: true, Guidance: true, TurnRate: 32767, WeaponVelocity: 65536, WeaponAcceleration: 65536}
	stored := Vec3{X: fi(1000), Y: fi(0), Z: fi(0)}
	// The target has moved a quarter-circle away from the stored point.
	unit := &units.Unit{X: fi(0), Y: fi(0), Z: fi(1000), Alive: true}
	p := Projectile{TargetPos: stored, TargetUnit: 1, ExpiryTick: 100}

	if res := AdvanceSelfProp(&p, w, 1, 0, fix(0), guidanceEnvFor(nil, unit)); res != AdvanceAlive {
		t.Fatalf("guided tick = %v, want AdvanceAlive", res)
	}
	wantYaw := YawFromDelta(unit.X, unit.Z) // from the pre-motion origin
	storedYaw := YawFromDelta(stored.X, stored.Z)
	if p.Yaw != wantYaw {
		t.Fatalf("yaw = %d, want %d (toward the live unit); the launch-point yaw is %d [06 §6.7]", p.Yaw, wantYaw, storedYaw)
	}
	if p.TargetPos != stored {
		t.Fatalf("steering overwrote the stored point (%v); it is the lost-target fallback [06 §6.8]", p.TargetPos)
	}

	// When the unit dies the record falls back to that stored point rather
	// than reacquiring [06 §6.8].
	unit.Alive = false
	q := Projectile{TargetPos: stored, TargetUnit: 1, ExpiryTick: 100}
	if res := AdvanceSelfProp(&q, w, 1, 0, fix(0), guidanceEnvFor(nil, unit)); res != AdvanceAlive {
		t.Fatalf("fallback tick = %v, want AdvanceAlive", res)
	}
	if q.Yaw != storedYaw {
		t.Fatalf("lost target: yaw = %d, want the stored-point yaw %d [06 §6.8]", q.Yaw, storedYaw)
	}
}

// TestTwoPhaseGuidingGateIgnoresTheGuidanceFlag locks
// `guiding = twophase ? (twoPhaseStateBits != 0) : guidance` [06 §6.7]: a
// two-phase record does not steer before its first expiry transition even with
// `guidance` set, and does steer afterwards even without it.
func TestTwoPhaseGuidingGateIgnoresTheGuidanceFlag(t *testing.T) {
	unit := &units.Unit{X: fi(0), Y: fi(0), Z: fi(1000), Alive: true}
	env := guidanceEnvFor(nil, unit)
	wantYaw := YawFromDelta(unit.X, unit.Z)

	// Two-phase, state bits still clear, `guidance` set: no steering.
	first := &content.WeaponDef{SelfProp: true, TwoPhase: true, Guidance: true, TurnRate: 32767, WeaponVelocity: 65536}
	p := Projectile{TargetUnit: 1, ExpiryTick: 100, TwoPhase: false}
	if res := AdvanceSelfProp(&p, first, 1, 0, fix(0), env); res != AdvanceAlive {
		t.Fatalf("first-phase tick = %v, want AdvanceAlive", res)
	}
	if p.Yaw != 0 {
		t.Fatalf("first phase steered to yaw %d; two-phase guidance waits for the state bits [06 §6.7][06 §6.6]", p.Yaw)
	}

	// Two-phase, state bits set, `guidance` CLEAR: steering runs.
	second := &content.WeaponDef{SelfProp: true, TwoPhase: true, Guidance: false, TurnRate: 32767, WeaponVelocity: 65536}
	q := Projectile{TargetUnit: 1, ExpiryTick: 100, TwoPhase: true}
	if res := AdvanceSelfProp(&q, second, 1, 0, fix(0), env); res != AdvanceAlive {
		t.Fatalf("second-phase tick = %v, want AdvanceAlive", res)
	}
	if q.Yaw != wantYaw {
		t.Fatalf("second phase yaw = %d, want %d: the state bits, not the guidance flag, gate a two-phase record [06 §6.7]", q.Yaw, wantYaw)
	}
}
